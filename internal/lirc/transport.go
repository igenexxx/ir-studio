package lirc

import (
	"context"
	"sync"

	"ir-studio/internal/irfile"
)

// Transport is the dependency-injected ACL over Linux /dev/lirc* ioctls or SIM.
type Transport interface {
	Features() (uint32, error)
	SetSendMode(mode uint32) error
	SetRecMode(mode uint32) error
	SetSendCarrier(freqHz uint32) error
	SetSendDutyCycle(dutyPct uint32) error
	SetRecTimeout(timeoutUs uint32, enableReports bool) error
	WritePulses(ctx context.Context, pulses []uint32) error
	ReadMode2(ctx context.Context, dst []uint32) (int, error)
	DevicePath() string
	IsSim() bool
	Close() error
}

// DiscoverOptions controls LIRC device discovery and SIM fallback behavior.
type DiscoverOptions struct {
	ForceSim      bool
	DevicePath    string
	FallbackToSim bool
	RecTimeoutUs  uint32
}

// Discover opens a Linux /dev/lirc* Transport or falls back to SimTransport.
func Discover(opts DiscoverOptions) (Transport, error) {
	if opts.RecTimeoutUs == 0 {
		opts.RecTimeoutUs = DefaultRecTimeoutUs
	}
	if opts.ForceSim {
		return NewSimTransport(), nil
	}
	return discoverPlatform(opts)
}

// SimTransport is a deterministic, thread-safe in-memory LIRC transceiver.
type SimTransport struct {
	mu          sync.Mutex
	carrierHz   uint32
	dutyPct     uint32
	sendMode    uint32
	recMode     uint32
	recTimeout  uint32
	loopback    bool
	closed      bool
	rxQueue     []uint32
	rxNotify    chan struct{}
	txHistory   [][]uint32
}

// NewSimTransport constructs a SimTransport initialized to default 38 kHz / 33%.
func NewSimTransport() *SimTransport {
	return &SimTransport{
		carrierHz:  DefaultCarrierHz,
		dutyPct:    DefaultDutyPct,
		sendMode:   LIRC_MODE_PULSE,
		recMode:    LIRC_MODE_MODE2,
		recTimeout: DefaultRecTimeoutUs,
		rxNotify:   make(chan struct{}, 1),
	}
}

func (s *SimTransport) Features() (uint32, error) {
	return LIRC_CAN_SEND_PULSE | LIRC_CAN_REC_MODE2, nil
}

func (s *SimTransport) SetSendMode(mode uint32) error {
	s.mu.Lock()
	s.sendMode = mode
	s.mu.Unlock()
	return nil
}

func (s *SimTransport) SetRecMode(mode uint32) error {
	s.mu.Lock()
	s.recMode = mode
	s.mu.Unlock()
	return nil
}

func (s *SimTransport) SetSendCarrier(freqHz uint32) error {
	s.mu.Lock()
	s.carrierHz = freqHz
	s.mu.Unlock()
	return nil
}

func (s *SimTransport) SetSendDutyCycle(dutyPct uint32) error {
	s.mu.Lock()
	s.dutyPct = dutyPct
	s.mu.Unlock()
	return nil
}

func (s *SimTransport) SetRecTimeout(timeoutUs uint32, _ bool) error {
	s.mu.Lock()
	s.recTimeout = timeoutUs
	s.mu.Unlock()
	return nil
}

// SetLoopback enables or disables looping transmitted pulses back into RX mode2.
func (s *SimTransport) SetLoopback(enabled bool) {
	s.mu.Lock()
	s.loopback = enabled
	s.mu.Unlock()
}

// InjectMode2 enqueues synthetic mode2 packets for ReadMode2 consumers.
func (s *SimTransport) InjectMode2(packets []uint32) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.rxQueue = append(s.rxQueue, packets...)
	s.mu.Unlock()
	s.notifyRX()
}

// InjectPulseTrain converts a signed PulseTrain into mode2 packets + timeout
// and enqueues them for ReadMode2.
func (s *SimTransport) InjectPulseTrain(pulses irfile.PulseTrain) {
	pkts := make([]uint32, 0, len(pulses)+1)
	for _, p := range pulses {
		if p > 0 {
			pkts = append(pkts, LIRC_MODE2_PULSE|(uint32(p)&LIRC_VALUE_MASK))
		} else if p < 0 {
			pkts = append(pkts, LIRC_MODE2_SPACE|(uint32(-p)&LIRC_VALUE_MASK))
		}
	}
	pkts = append(pkts, LIRC_MODE2_TIMEOUT|(DefaultRecTimeoutUs&LIRC_VALUE_MASK))
	s.InjectMode2(pkts)
}

func (s *SimTransport) WritePulses(ctx context.Context, pulses []uint32) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cp := make([]uint32, len(pulses))
	copy(cp, pulses)

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrDeviceOpen
	}
	s.txHistory = append(s.txHistory, cp)
	loopback := s.loopback
	s.mu.Unlock()

	if loopback {
		s.enqueueLoopback(cp)
	}
	return nil
}

func (s *SimTransport) enqueueLoopback(pulses []uint32) {
	pkts := make([]uint32, 0, len(pulses)+1)
	for i, d := range pulses {
		tag := LIRC_MODE2_PULSE
		if i%2 == 1 {
			tag = LIRC_MODE2_SPACE
		}
		pkts = append(pkts, tag|(d&LIRC_VALUE_MASK))
	}
	pkts = append(pkts, LIRC_MODE2_TIMEOUT|(DefaultRecTimeoutUs&LIRC_VALUE_MASK))
	s.InjectMode2(pkts)
}

func (s *SimTransport) ReadMode2(ctx context.Context, dst []uint32) (int, error) {
	if len(dst) == 0 {
		return 0, nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		s.mu.Lock()
		if len(s.rxQueue) > 0 {
			n := copy(dst, s.rxQueue)
			s.rxQueue = s.rxQueue[n:]
			s.mu.Unlock()
			return n, nil
		}
		closed := s.closed
		s.mu.Unlock()
		if closed {
			return 0, ErrDeviceOpen
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-s.rxNotify:
		}
	}
}

// TxHistory returns a snapshot copy of all pulse trains written to SimTransport.
func (s *SimTransport) TxHistory() [][]uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][]uint32, len(s.txHistory))
	for i, h := range s.txHistory {
		cp := make([]uint32, len(h))
		copy(cp, h)
		out[i] = cp
	}
	return out
}

// CarrierState returns the current simulated carrier frequency and duty cycle.
func (s *SimTransport) CarrierState() irfile.CarrierSpec {
	s.mu.Lock()
	defer s.mu.Unlock()
	return irfile.CarrierSpec{FreqHz: s.carrierHz, DutyPct: s.dutyPct}
}

func (s *SimTransport) DevicePath() string { return "SIM" }
func (s *SimTransport) IsSim() bool        { return true }

func (s *SimTransport) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.notifyRX()
	return nil
}

func (s *SimTransport) notifyRX() {
	select {
	case s.rxNotify <- struct{}{}:
	default:
	}
}
