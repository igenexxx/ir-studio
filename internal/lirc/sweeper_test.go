package lirc

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"ir-studio/internal/irfile"
)

type recordingTransport struct {
	*SimTransport
	mu          sync.Mutex
	carriers    []uint32
	failAtWrite int
	writeCount  int
}

func (r *recordingTransport) SetSendCarrier(freqHz uint32) error {
	r.mu.Lock()
	r.carriers = append(r.carriers, freqHz)
	r.mu.Unlock()
	return r.SimTransport.SetSendCarrier(freqHz)
}

func (r *recordingTransport) WritePulses(ctx context.Context, pulses []uint32) error {
	r.mu.Lock()
	r.writeCount++
	fail := r.failAtWrite > 0 && r.writeCount == r.failAtWrite
	r.mu.Unlock()
	if fail {
		return errors.New("simulated ioctl write failure")
	}
	return r.SimTransport.WritePulses(ctx, pulses)
}

func sampleNECSignal() irfile.IRSignal {
	return irfile.IRSignal{
		Name:      "Power",
		Kind:      irfile.SignalParsed,
		Protocol:  "NEC",
		Address:   [4]byte{0x04},
		Command:   [4]byte{0x08},
		Frequency: 38000,
		DutyCycle: 0.33,
	}
}

func TestSweepCarrierFullSequenceAndRestore(t *testing.T) {
	rec := &recordingTransport{SimTransport: NewSimTransport()}
	xcvr := NewTransceiver(rec, NewTransceiverGate(DefaultSettleGuard))

	var seenSteps []uint32
	report, err := SweepCarrier(
		context.Background(),
		xcvr,
		irfile.DefaultRegistry(),
		sampleNECSignal(),
		nil,
		0,
		MinSweepGap,
		func(_ int, hz uint32) { seenSteps = append(seenSteps, hz) },
	)
	if err != nil {
		t.Fatalf("SweepCarrier failed: %v", err)
	}
	if report.Cancelled {
		t.Fatalf("unexpected cancellation")
	}
	if !slices.Equal(seenSteps, SweepCarrierPresets[:]) {
		t.Fatalf("steps=%v, want %v", seenSteps, SweepCarrierPresets[:])
	}
	if len(report.Steps) != 4 || len(rec.TxHistory()) != 4 {
		t.Fatalf("expected 4 bursts, got steps=%d tx=%d", len(report.Steps), len(rec.TxHistory()))
	}
	if got := rec.CarrierState(); got.FreqHz != DefaultCarrierHz || got.DutyPct != DefaultDutyPct {
		t.Fatalf("carrier not restored: got %+v, want 38000/33", got)
	}
}

func TestSweepCarrierMidSweepCancelRestoresDefaults(t *testing.T) {
	rec := &recordingTransport{SimTransport: NewSimTransport()}
	xcvr := NewTransceiver(rec, NewTransceiverGate(DefaultSettleGuard))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	report, err := SweepCarrier(
		ctx,
		xcvr,
		irfile.DefaultRegistry(),
		sampleNECSignal(),
		nil,
		33,
		MinSweepGap,
		func(idx int, _ uint32) {
			if idx == 1 {
				cancel()
			}
		},
	)
	if err != nil {
		t.Fatalf("SweepCarrier on cancel returned error: %v", err)
	}
	if !report.Cancelled {
		t.Fatalf("expected report.Cancelled == true")
	}
	if len(rec.TxHistory()) > 2 {
		t.Fatalf("expected <= 2 bursts after cancel at step 2, got %d", len(rec.TxHistory()))
	}
	if got := rec.CarrierState(); got.FreqHz != DefaultCarrierHz || got.DutyPct != DefaultDutyPct {
		t.Fatalf("carrier not restored after cancel: got %+v", got)
	}
}

func TestSweepCarrierErrorAndValidation(t *testing.T) {
	rec := &recordingTransport{SimTransport: NewSimTransport(), failAtWrite: 2}
	xcvr := NewTransceiver(rec, NewTransceiverGate(DefaultSettleGuard))

	report, err := SweepCarrier(context.Background(), xcvr, nil, sampleNECSignal(), nil, 33, MinSweepGap, nil)
	if err == nil {
		t.Fatalf("expected write error on step 2")
	}
	if len(report.Steps) != 2 || report.Steps[1].Sent {
		t.Fatalf("unexpected step report on failure: %+v", report.Steps)
	}
	if got := rec.CarrierState(); got.FreqHz != DefaultCarrierHz || got.DutyPct != DefaultDutyPct {
		t.Fatalf("carrier not restored after write error: got %+v", got)
	}

	if _, err := SweepCarrier(context.Background(), nil, nil, sampleNECSignal(), nil, 33, time.Millisecond, nil); err == nil {
		t.Fatalf("expected error for nil transceiver")
	}
	if _, err := SweepCarrier(context.Background(), xcvr, nil, sampleNECSignal(), []uint32{99000}, 33, MinSweepGap, nil); err == nil {
		t.Fatalf("expected error for out-of-range frequency 99000 Hz")
	}
}
