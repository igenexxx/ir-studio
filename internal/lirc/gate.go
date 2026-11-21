package lirc

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"ir-studio/internal/irfile"
)

// TransceiverGate enforces TX/RX mutual exclusion and the >= 20 ms post-TX
// optical settle guard so co-located IR receivers never see TX ring-down.
type TransceiverGate struct {
	sem         chan struct{}
	txActive    atomic.Bool
	settleGuard time.Duration
}

// NewTransceiverGate creates a gate with at least DefaultSettleGuard (20 ms).
func NewTransceiverGate(settleGuard time.Duration) *TransceiverGate {
	if settleGuard < DefaultSettleGuard {
		settleGuard = DefaultSettleGuard
	}
	return &TransceiverGate{
		sem:         make(chan struct{}, 1),
		settleGuard: settleGuard,
	}
}

// AcquireTX waits until exclusive TX ownership is acquired or ctx is cancelled.
func (g *TransceiverGate) AcquireTX(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case g.sem <- struct{}{}:
		g.txActive.Store(true)
		return nil
	}
}

// ReleaseTX holds RX muted for the optical settle guard duration before
// releasing exclusive ownership back to RXWorker.
func (g *TransceiverGate) ReleaseTX() {
	timer := time.NewTimer(g.settleGuard)
	<-timer.C
	g.txActive.Store(false)
	<-g.sem
}

// IsTXActive reports whether a TX burst or its post-TX settle guard is active.
func (g *TransceiverGate) IsTXActive() bool {
	return g.txActive.Load()
}

// SettleGuard returns the configured post-TX optical settle guard duration.
func (g *TransceiverGate) SettleGuard() time.Duration {
	return g.settleGuard
}

// Transceiver wraps a Transport and TransceiverGate, enforcing carrier/duty
// validation and unconditional deferred restoration to defaults (38 kHz, 33%).
type Transceiver struct {
	transport      Transport
	gate           *TransceiverGate
	defaultCarrier irfile.CarrierSpec
}

// NewTransceiver constructs a Transceiver with default carrier restoration.
func NewTransceiver(tr Transport, gate *TransceiverGate) *Transceiver {
	if gate == nil {
		gate = NewTransceiverGate(DefaultSettleGuard)
	}
	return &Transceiver{
		transport:      tr,
		gate:           gate,
		defaultCarrier: DefaultCarrier(),
	}
}

// Transport returns the underlying LIRC transport.
func (t *Transceiver) Transport() Transport { return t.transport }

// Gate returns the shared TX/RX mutual-exclusion gate.
func (t *Transceiver) Gate() *TransceiverGate { return t.gate }

// Send validates carrier and pulse invariants, acquires exclusive TX ownership,
// configures carrier/duty, transmits pulses, and unconditionally restores
// default carrier (38000 Hz) and duty cycle (33%) on exit, error, or cancel.
func (t *Transceiver) Send(ctx context.Context, pulses []uint32, carrier irfile.CarrierSpec) (retErr error) {
	if carrier.IsZero() {
		carrier = t.defaultCarrier
	}
	if err := ValidateCarrier(carrier); err != nil {
		return err
	}
	validPulses, err := ValidateTxPulses(pulses)
	if err != nil {
		return err
	}
	if err := t.gate.AcquireTX(ctx); err != nil {
		return err
	}
	defer t.gate.ReleaseTX()

	if err := t.transport.SetSendCarrier(carrier.FreqHz); err != nil {
		return err
	}
	defer func() {
		retErr = errors.Join(retErr, t.restoreDefaults())
	}()

	if err := t.transport.SetSendDutyCycle(carrier.DutyPct); err != nil {
		return err
	}
	return t.transport.WritePulses(ctx, validPulses)
}

func (t *Transceiver) restoreDefaults() error {
	errCarrier := t.transport.SetSendCarrier(t.defaultCarrier.FreqHz)
	errDuty := t.transport.SetSendDutyCycle(t.defaultCarrier.DutyPct)
	return errors.Join(errCarrier, errDuty)
}
