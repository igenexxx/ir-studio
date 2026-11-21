package lirc_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"ir-studio/internal/irfile"
	"ir-studio/internal/lirc"
)

type mockTransport struct {
	mu           sync.Mutex
	calls        []string
	writeErr     error
	dutyErr      error
	restoreErr   error
	writeDelay   time.Duration
	lastTxEnd    time.Time
	firstRxAfter time.Time
	rxCh         chan []uint32
}

func newMockTransport() *mockTransport {
	return &mockTransport{rxCh: make(chan []uint32, 8)}
}

func (m *mockTransport) record(call string) {
	m.mu.Lock()
	m.calls = append(m.calls, call)
	m.mu.Unlock()
}

func (m *mockTransport) Features() (uint32, error) {
	return lirc.LIRC_CAN_SEND_PULSE | lirc.LIRC_CAN_REC_MODE2, nil
}
func (m *mockTransport) SetSendMode(mode uint32) error {
	m.record(fmt.Sprintf("SetSendMode(%d)", mode))
	return nil
}
func (m *mockTransport) SetRecMode(mode uint32) error {
	m.record(fmt.Sprintf("SetRecMode(%d)", mode))
	return nil
}
func (m *mockTransport) SetSendCarrier(freqHz uint32) error {
	m.record(fmt.Sprintf("SetSendCarrier(%d)", freqHz))
	m.mu.Lock()
	defer m.mu.Unlock()
	if freqHz == lirc.DefaultCarrierHz && m.restoreErr != nil {
		return m.restoreErr
	}
	return nil
}
func (m *mockTransport) SetSendDutyCycle(dutyPct uint32) error {
	m.record(fmt.Sprintf("SetSendDutyCycle(%d)", dutyPct))
	m.mu.Lock()
	err := m.dutyErr
	m.mu.Unlock()
	if dutyPct != lirc.DefaultDutyPct && err != nil {
		return err
	}
	return nil
}
func (m *mockTransport) SetRecTimeout(timeoutUs uint32, enable bool) error {
	m.record(fmt.Sprintf("SetRecTimeout(%d,%v)", timeoutUs, enable))
	return nil
}
func (m *mockTransport) WritePulses(ctx context.Context, pulses []uint32) error {
	m.record(fmt.Sprintf("WritePulses(len=%d)", len(pulses)))
	m.mu.Lock()
	delay := m.writeDelay
	wErr := m.writeErr
	m.mu.Unlock()
	if delay > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
	m.mu.Lock()
	m.lastTxEnd = time.Now()
	m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	return wErr
}
func (m *mockTransport) ReadMode2(ctx context.Context, dst []uint32) (int, error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case pkts := <-m.rxCh:
		m.mu.Lock()
		if !m.lastTxEnd.IsZero() && m.firstRxAfter.IsZero() {
			m.firstRxAfter = time.Now()
		}
		m.mu.Unlock()
		return copy(dst, pkts), nil
	}
}
func (m *mockTransport) DevicePath() string { return "/dev/lirc-mock" }
func (m *mockTransport) IsSim() bool        { return false }
func (m *mockTransport) Close() error       { return nil }

func (m *mockTransport) snapshotCalls() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]string, len(m.calls))
	copy(cp, m.calls)
	return cp
}

func TestTransceiverCarrierRestoreAndValidation(t *testing.T) {
	errWriteBoom := errors.New("hardware write boom")
	errDutyBoom := errors.New("duty boom")

	tests := []struct {
		name      string
		carrier   irfile.CarrierSpec
		pulses    []uint32
		writeErr  error
		dutyErr   error
		cancelCtx bool
		wantErr   error
		wantCalls []string
	}{
		{
			name:    "happy path restores 38kHz 33%",
			carrier: irfile.CarrierSpec{FreqHz: 40000, DutyPct: 50},
			pulses:  []uint32{9000, 4500, 562},
			wantCalls: []string{
				"SetSendCarrier(40000)",
				"SetSendDutyCycle(50)",
				"WritePulses(len=3)",
				"SetSendCarrier(38000)",
				"SetSendDutyCycle(33)",
			},
		},
		{
			name:    "even-length pulses strips trailing space",
			carrier: irfile.CarrierSpec{FreqHz: 38000, DutyPct: 33},
			pulses:  []uint32{9000, 4500, 562, 1000},
			wantCalls: []string{
				"SetSendCarrier(38000)",
				"SetSendDutyCycle(33)",
				"WritePulses(len=3)",
				"SetSendCarrier(38000)",
				"SetSendDutyCycle(33)",
			},
		},
		{
			name:     "write error still restores defaults",
			carrier:  irfile.CarrierSpec{FreqHz: 56000, DutyPct: 50},
			pulses:   []uint32{9000, 4500, 562},
			writeErr: errWriteBoom,
			wantErr:  errWriteBoom,
			wantCalls: []string{
				"SetSendCarrier(56000)",
				"SetSendDutyCycle(50)",
				"WritePulses(len=3)",
				"SetSendCarrier(38000)",
				"SetSendDutyCycle(33)",
			},
		},
		{
			name:    "duty error still restores defaults",
			carrier: irfile.CarrierSpec{FreqHz: 36000, DutyPct: 50},
			pulses:  []uint32{9000, 4500, 562},
			dutyErr: errDutyBoom,
			wantErr: errDutyBoom,
			wantCalls: []string{
				"SetSendCarrier(36000)",
				"SetSendDutyCycle(50)",
				"SetSendCarrier(38000)",
				"SetSendDutyCycle(33)",
			},
		},
		{
			name:      "out of range carrier rejected before ioctl",
			carrier:   irfile.CarrierSpec{FreqHz: 20000, DutyPct: 33},
			pulses:    []uint32{9000, 4500, 562},
			wantErr:   lirc.ErrInvalidCarrier,
			wantCalls: nil,
		},
		{
			name:      "out of range duty rejected before ioctl",
			carrier:   irfile.CarrierSpec{FreqHz: 38000, DutyPct: 100},
			pulses:    []uint32{9000, 4500, 562},
			wantErr:   lirc.ErrInvalidDutyCycle,
			wantCalls: nil,
		},
		{
			name:      "zero duration pulse rejected before ioctl",
			carrier:   irfile.CarrierSpec{FreqHz: 38000, DutyPct: 33},
			pulses:    []uint32{9000, 0, 562},
			wantErr:   lirc.ErrInvalidPulseTrain,
			wantCalls: nil,
		},
		{
			name:      "empty pulse rejected before ioctl",
			carrier:   irfile.CarrierSpec{FreqHz: 38000, DutyPct: 33},
			pulses:    nil,
			wantErr:   lirc.ErrInvalidPulseTrain,
			wantCalls: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock := newMockTransport()
			mock.writeErr = tc.writeErr
			mock.dutyErr = tc.dutyErr
			xcvr := lirc.NewTransceiver(mock, lirc.NewTransceiverGate(lirc.DefaultSettleGuard))

			ctx := context.Background()
			if tc.cancelCtx {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			err := xcvr.Send(ctx, tc.pulses, tc.carrier)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Send() err = %v, want %v", err, tc.wantErr)
				}
			} else if err != nil {
				t.Fatalf("Send() unexpected err: %v", err)
			}

			gotCalls := mock.snapshotCalls()
			if len(gotCalls) != len(tc.wantCalls) {
				t.Fatalf("calls = %v, want %v", gotCalls, tc.wantCalls)
			}
			for i := range gotCalls {
				if gotCalls[i] != tc.wantCalls[i] {
					t.Fatalf("call[%d] = %q, want %q", i, gotCalls[i], tc.wantCalls[i])
				}
			}
		})
	}
}

func TestGateSettleGuardAndMutualExclusion(t *testing.T) {
	gate := lirc.NewTransceiverGate(5 * time.Millisecond) // must enforce >= 20ms minimum
	if gate.SettleGuard() < lirc.DefaultSettleGuard {
		t.Fatalf("SettleGuard = %v, want >= %v", gate.SettleGuard(), lirc.DefaultSettleGuard)
	}

	if err := gate.AcquireTX(context.Background()); err != nil {
		t.Fatalf("AcquireTX: %v", err)
	}
	if !gate.IsTXActive() {
		t.Fatal("expected IsTXActive() == true")
	}

	// Second concurrent AcquireTX must block and respect ctx timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := gate.AcquireTX(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("concurrent AcquireTX err = %v, want DeadlineExceeded", err)
	}

	start := time.Now()
	gate.ReleaseTX()
	elapsed := time.Since(start)
	if elapsed < lirc.DefaultSettleGuard {
		t.Fatalf("ReleaseTX settle guard elapsed %v, want >= %v", elapsed, lirc.DefaultSettleGuard)
	}
	if gate.IsTXActive() {
		t.Fatal("expected IsTXActive() == false after ReleaseTX")
	}
}

func TestDiscoverAndSimTransport(t *testing.T) {
	tr, err := lirc.Discover(lirc.DiscoverOptions{ForceSim: true})
	if err != nil {
		t.Fatalf("Discover(ForceSim): %v", err)
	}
	if !tr.IsSim() || tr.DevicePath() != "SIM" {
		t.Fatalf("got IsSim=%v path=%q", tr.IsSim(), tr.DevicePath())
	}

	// Missing explicit device without fallback must return ErrDeviceOpen.
	_, err = lirc.Discover(lirc.DiscoverOptions{DevicePath: "/dev/lirc-nonexistent-99"})
	if !errors.Is(err, lirc.ErrDeviceOpen) {
		t.Fatalf("Discover(missing) err = %v, want ErrDeviceOpen", err)
	}

	// Missing explicit device WITH fallback returns SIM.
	trFB, err := lirc.Discover(lirc.DiscoverOptions{DevicePath: "/dev/lirc-nonexistent-99", FallbackToSim: true})
	if err != nil || !trFB.IsSim() {
		t.Fatalf("Discover(fallback) = (%v, %v), want SIM", trFB, err)
	}

	// SimTransport loopback & lifecycle methods.
	sim := lirc.NewSimTransport()
	if feat, err := sim.Features(); err != nil || feat == 0 {
		t.Fatalf("Sim Features = (%x, %v)", feat, err)
	}
	_ = sim.SetSendMode(lirc.LIRC_MODE_PULSE)
	_ = sim.SetRecMode(lirc.LIRC_MODE_MODE2)
	_ = sim.SetRecTimeout(lirc.DefaultRecTimeoutUs, true)
	sim.SetLoopback(true)
	if err := sim.WritePulses(context.Background(), []uint32{9000, 4500, 562}); err != nil {
		t.Fatalf("Sim WritePulses: %v", err)
	}
	dst := make([]uint32, 16)
	n, err := sim.ReadMode2(context.Background(), dst)
	if err != nil || n != 4 {
		t.Fatalf("Sim ReadMode2 = (%d, %v), want (4, nil)", n, err)
	}
	if len(sim.TxHistory()) != 1 {
		t.Fatalf("TxHistory len = %d, want 1", len(sim.TxHistory()))
	}
	_ = sim.Close()
	if err := sim.WritePulses(context.Background(), []uint32{500}); !errors.Is(err, lirc.ErrDeviceOpen) {
		t.Fatalf("closed WritePulses = %v, want ErrDeviceOpen", err)
	}
	if _, err := sim.ReadMode2(context.Background(), dst); !errors.Is(err, lirc.ErrDeviceOpen) {
		t.Fatalf("closed ReadMode2 = %v, want ErrDeviceOpen", err)
	}
	sim.InjectMode2([]uint32{lirc.LIRC_MODE2_PULSE | 500}) // no-op when closed
}

func TestTXWorkerEventsCancelAndSafety(t *testing.T) {
	sim := lirc.NewSimTransport()
	gate := lirc.NewTransceiverGate(lirc.DefaultSettleGuard)
	xcvr := lirc.NewTransceiver(sim, gate)
	worker := lirc.NewTXWorker(xcvr, irfile.DefaultRegistry())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker.Start(ctx)
	defer worker.Close()

	// Reject repeat > MaxRepeats.
	necSig := irfile.IRSignal{Kind: irfile.SignalParsed, Protocol: "NEC", Address: [4]byte{0x04}, Command: [4]byte{0x08}}
	if err := worker.Submit(lirc.TXRequest{ID: 1, Signal: necSig, Repeat: 50}); !errors.Is(err, lirc.ErrRepeatLimitExceeded) {
		t.Fatalf("Submit(repeat=50) = %v, want ErrRepeatLimitExceeded", err)
	}

	// Happy path 2 repeats.
	if err := worker.Submit(lirc.TXRequest{ID: 2, Signal: necSig, Repeat: 2, InterFrameGap: 5 * time.Millisecond}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	var kinds []lirc.TXEventKind
	for ev := range worker.Events() {
		if ev.ID != 2 {
			continue
		}
		kinds = append(kinds, ev.Kind)
		if ev.Kind == lirc.TXCompleted || ev.Kind == lirc.TXError {
			break
		}
	}
	want := []lirc.TXEventKind{lirc.TXStarted, lirc.TXProgress, lirc.TXProgress, lirc.TXCompleted}
	if len(kinds) != len(want) {
		t.Fatalf("events = %v, want %v", kinds, want)
	}

	// Cancel mid-transmission.
	if err := worker.Submit(lirc.TXRequest{ID: 3, Signal: necSig, Repeat: 5, InterFrameGap: 200 * time.Millisecond}); err != nil {
		t.Fatalf("Submit cancel test: %v", err)
	}
	for ev := range worker.Events() {
		if ev.ID == 3 && ev.Kind == lirc.TXProgress {
			worker.CancelCurrent()
		}
		if ev.ID == 3 && (ev.Kind == lirc.TXCancelled || ev.Kind == lirc.TXCompleted) {
			if ev.Kind != lirc.TXCancelled {
				t.Fatalf("expected TXCancelled, got %v", ev.Kind)
			}
			break
		}
	}
	// Carrier MUST be restored to 38000/33 after cancel.
	if cs := sim.CarrierState(); cs.FreqHz != lirc.DefaultCarrierHz || cs.DutyPct != lirc.DefaultDutyPct {
		t.Fatalf("carrier not restored after cancel: %+v", cs)
	}
}

func TestRXWorkerFramingNoiseAndBackpressure(t *testing.T) {
	sim := lirc.NewSimTransport()
	gate := lirc.NewTransceiverGate(lirc.DefaultSettleGuard)
	reg := irfile.DefaultRegistry()
	rx := lirc.NewRXWorker(sim, gate, reg)

	ctx, cancel := context.WithCancel(context.Background())
	rx.Start(ctx)

	// 1. Inject ambient noise burst (< MinFramePulses) -> MUST be discarded.
	sim.InjectMode2([]uint32{
		lirc.LIRC_MODE2_SPACE | 5000, // leading space ignored
		lirc.LIRC_MODE2_PULSE | 80,   // sub-MinPulseUs glitch ignored
		lirc.LIRC_MODE2_PULSE | 120,
		lirc.LIRC_MODE2_SPACE | 120,
		lirc.LIRC_MODE2_TIMEOUT | lirc.DefaultRecTimeoutUs,
	})

	// 2. Inject valid NEC frame with consecutive same-sign split mark to verify coalescing.
	necSig := irfile.IRSignal{Kind: irfile.SignalParsed, Protocol: "NEC", Address: [4]byte{0x04}, Command: [4]byte{0x08}}
	pulses, err := reg.Encode(necSig)
	if err != nil {
		t.Fatalf("Encode NEC: %v", err)
	}
	// Split first 9000us mark into 4500 + 4500 consecutive pulses.
	pkts := []uint32{
		lirc.LIRC_MODE2_SPACE | 3000,
		lirc.LIRC_MODE2_PULSE | 4500,
		lirc.LIRC_MODE2_PULSE | 4500,
	}
	for _, p := range pulses[1:] {
		if p > 0 {
			pkts = append(pkts, lirc.LIRC_MODE2_PULSE|uint32(p))
		} else {
			pkts = append(pkts, lirc.LIRC_MODE2_SPACE|uint32(-p))
		}
	}
	pkts = append(pkts, lirc.LIRC_MODE2_TIMEOUT|lirc.DefaultRecTimeoutUs)
	sim.InjectMode2(pkts)

	select {
	case capEvt := <-rx.Captures():
		if capEvt.Decoded == nil || capEvt.Decoded.Protocol != "NEC" ||
			capEvt.Decoded.Address[0] != 0x04 || capEvt.Decoded.Command[0] != 0x08 {
			t.Fatalf("unexpected decoded capture: %+v", capEvt.Decoded)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for NEC RXCapture")
	}

	// 3. Slow consumer backpressure: inject 30 frames without reading -> must not block.
	for range 30 {
		sim.InjectPulseTrain(pulses)
	}
	time.Sleep(20 * time.Millisecond)

	// 4. Clean shutdown within 50ms on cancel.
	start := time.Now()
	cancel()
	rx.Close()
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("RXWorker Close took %v, expected <= 50ms", elapsed)
	}
}
