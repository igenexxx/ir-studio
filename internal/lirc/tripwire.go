package lirc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"ir-studio/internal/irfile"
)

// TripwireMode selects whether reflection triggers proximity detection or loss
// of reflection triggers beam-break detection.
type TripwireMode string

const (
	TripwireReflective TripwireMode = "reflective"
	TripwireBeamBreak  TripwireMode = "beam_break"

	DefaultTripwirePings   = 3
	MaxTripwirePings       = 10
	DefaultTripwirePulseUs = 600
	MinTripwirePulseUs     = 200
	MaxTripwirePulseUs     = 2000
	DefaultTripwireListen  = 35 * time.Millisecond
	MinTripwireListen      = 5 * time.Millisecond
	MaxTripwireListen      = 500 * time.Millisecond
)

// TripwireConfig configures a bounded 38 kHz optical tripwire/proximity probe.
type TripwireConfig struct {
	Mode         TripwireMode
	CarrierHz    uint32
	DutyPct      uint32
	PingCount    int
	PingPulseUs  uint32
	ListenWindow time.Duration
}

// TripwireReading captures the outcome of a single optical tripwire probe.
type TripwireReading struct {
	Timestamp      time.Time    `json:"timestamp"`
	Mode           TripwireMode `json:"mode"`
	CarrierHz      uint32       `json:"carrier_hz"`
	PingsSent      int          `json:"pings_sent"`
	EchoesReceived int          `json:"echoes_received"`
	ReflectionPct  float64      `json:"reflection_pct"`
	Triggered      bool         `json:"triggered"`
	HardwareMode   string       `json:"hardware_mode"`
	DurationMs     int64        `json:"duration_ms"`
}

// TripwireProbe emits bounded 38 kHz ping bursts on TX while listening on RX
// for reflected pulses, synchronized via TransceiverGate with guaranteed carrier
// restoration to 38000 Hz / 33%.
func TripwireProbe(ctx context.Context, xcvr *Transceiver, cfg TripwireConfig) (reading TripwireReading, retErr error) {
	if xcvr == nil || xcvr.Transport() == nil {
		return TripwireReading{}, ErrDeviceOpen
	}
	norm, err := normalizeTripwireConfig(cfg)
	if err != nil {
		return TripwireReading{}, err
	}
	if err := xcvr.Gate().AcquireTX(ctx); err != nil {
		return TripwireReading{}, err
	}
	defer xcvr.Gate().ReleaseTX()

	tr := xcvr.Transport()
	if err := tr.SetSendCarrier(norm.CarrierHz); err != nil {
		return TripwireReading{}, err
	}
	defer func() {
		retErr = errors.Join(retErr, xcvr.restoreDefaults())
	}()
	if err := tr.SetSendDutyCycle(norm.DutyPct); err != nil {
		return TripwireReading{}, err
	}

	start := time.Now()
	pingBurst := []uint32{norm.PingPulseUs, norm.PingPulseUs, norm.PingPulseUs}
	sent, echoes, err := runTripwirePings(ctx, tr, pingBurst, norm.PingCount, norm.ListenWindow)
	if err != nil {
		return TripwireReading{}, err
	}
	return buildTripwireReading(norm, tr, sent, echoes, time.Since(start)), nil
}

func normalizeTripwireConfig(cfg TripwireConfig) (TripwireConfig, error) {
	if cfg.Mode == "" {
		cfg.Mode = TripwireReflective
	}
	if cfg.Mode != TripwireReflective && cfg.Mode != TripwireBeamBreak {
		return cfg, fmt.Errorf("invalid tripwire mode %q: %w", cfg.Mode, ErrUnsupportedFeature)
	}
	if cfg.CarrierHz == 0 {
		cfg.CarrierHz = DefaultCarrierHz
	}
	if cfg.DutyPct == 0 {
		cfg.DutyPct = DefaultDutyPct
	}
	if err := ValidateCarrier(irfile.CarrierSpec{FreqHz: cfg.CarrierHz, DutyPct: cfg.DutyPct}); err != nil {
		return cfg, err
	}
	if cfg.PingCount == 0 {
		cfg.PingCount = DefaultTripwirePings
	}
	if cfg.PingCount < 1 || cfg.PingCount > MaxTripwirePings {
		return cfg, fmt.Errorf("ping count %d out of range [1..%d]: %w", cfg.PingCount, MaxTripwirePings, ErrRepeatLimitExceeded)
	}
	if cfg.PingPulseUs == 0 {
		cfg.PingPulseUs = DefaultTripwirePulseUs
	}
	if cfg.PingPulseUs < MinTripwirePulseUs || cfg.PingPulseUs > MaxTripwirePulseUs {
		return cfg, fmt.Errorf("ping pulse %d us out of range [%d..%d]: %w", cfg.PingPulseUs, MinTripwirePulseUs, MaxTripwirePulseUs, ErrInvalidPulseTrain)
	}
	if cfg.ListenWindow == 0 {
		cfg.ListenWindow = DefaultTripwireListen
	}
	if cfg.ListenWindow < MinTripwireListen || cfg.ListenWindow > MaxTripwireListen {
		return cfg, fmt.Errorf("listen window %v out of range [%v..%v]: %w", cfg.ListenWindow, MinTripwireListen, MaxTripwireListen, ErrInvalidPulseTrain)
	}
	return cfg, nil
}

func runTripwirePings(ctx context.Context, tr Transport, burst []uint32, count int, window time.Duration) (int, int, error) {
	sent, echoes := 0, 0
	buf := make([]uint32, 32)
	for range count {
		if err := ctx.Err(); err != nil {
			return sent, echoes, err
		}
		if err := tr.WritePulses(ctx, burst); err != nil {
			return sent, echoes, err
		}
		sent++
		if listenForEcho(ctx, tr, buf, window) {
			echoes++
		}
	}
	return sent, echoes, nil
}

func listenForEcho(ctx context.Context, tr Transport, buf []uint32, window time.Duration) bool {
	listenCtx, cancel := context.WithTimeout(ctx, window)
	defer cancel()
	for {
		n, err := tr.ReadMode2(listenCtx, buf)
		if err != nil {
			return false
		}
		for _, pkt := range buf[:n] {
			if (pkt&LIRC_MODE2_MASK) == LIRC_MODE2_PULSE && (pkt&LIRC_VALUE_MASK) >= MinPulseUs {
				return true
			}
		}
	}
}

func buildTripwireReading(cfg TripwireConfig, tr Transport, sent, echoes int, elapsed time.Duration) TripwireReading {
	pct := 0.0
	if sent > 0 {
		pct = float64(echoes) / float64(sent) * 100.0
	}
	triggered := echoes > 0
	if cfg.Mode == TripwireBeamBreak {
		triggered = echoes == 0
	}
	hwMode := "LIRC"
	if tr.IsSim() {
		hwMode = "SIM"
	}
	return TripwireReading{
		Timestamp:      time.Now(),
		Mode:           cfg.Mode,
		CarrierHz:      cfg.CarrierHz,
		PingsSent:      sent,
		EchoesReceived: echoes,
		ReflectionPct:  pct,
		Triggered:      triggered,
		HardwareMode:   hwMode,
		DurationMs:     elapsed.Milliseconds(),
	}
}
