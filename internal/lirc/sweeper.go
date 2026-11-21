package lirc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"ir-studio/internal/irfile"
)

// SweepCarrierPresets is the SSOT carrier sequence for finicky IR receivers
// (Philips RC5 36 kHz, standard 38 kHz, Sony SIRC 40 kHz, Cisco/Motorola 56 kHz).
var SweepCarrierPresets = [4]uint32{36000, 38000, 40000, 56000}

const (
	// DefaultSweepGap is the inter-burst delay between swept carrier steps.
	DefaultSweepGap = 80 * time.Millisecond
	// MinSweepGap and MaxSweepGap bound user-supplied sweep delays.
	MinSweepGap = 40 * time.Millisecond
	MaxSweepGap = 1000 * time.Millisecond
)

// SweepStepResult records the outcome of a single carrier frequency step.
type SweepStepResult struct {
	FreqHz     uint32 `json:"freq_hz"`
	DutyPct    uint32 `json:"duty_pct"`
	DurationMs int64  `json:"duration_ms"`
	Sent       bool   `json:"sent"`
	Err        string `json:"err,omitempty"`
}

// SweepReport summarizes a multi-carrier resend sweep and guaranteed restore.
type SweepReport struct {
	SignalName      string            `json:"signal_name"`
	Steps           []SweepStepResult `json:"steps"`
	RestoredFreqHz  uint32            `json:"restored_freq_hz"`
	RestoredDutyPct uint32            `json:"restored_duty_pct"`
	Cancelled       bool              `json:"cancelled"`
}

// SweepCarrier rapidly resends sig across freqs (defaulting to SweepCarrierPresets)
// and unconditionally restores the transceiver to 38000 Hz / 33% on completion,
// error, or context cancellation.
func SweepCarrier(
	ctx context.Context,
	xcvr *Transceiver,
	reg *irfile.ProtocolRegistry,
	sig irfile.IRSignal,
	freqs []uint32,
	dutyPct uint32,
	gap time.Duration,
	onStep func(stepIdx int, freqHz uint32),
) (report SweepReport, retErr error) {
	if xcvr == nil || xcvr.Transport() == nil {
		return report, errors.New("lirc: nil transceiver for carrier sweep")
	}
	if reg == nil {
		reg = irfile.DefaultRegistry()
	}
	targetFreqs, duty, stepGap, err := normalizeSweepParams(freqs, dutyPct, gap)
	if err != nil {
		return report, err
	}
	pulses, err := encodeSweepPulses(reg, sig)
	if err != nil {
		return report, err
	}

	report = SweepReport{
		SignalName:      sig.Name,
		Steps:           make([]SweepStepResult, 0, len(targetFreqs)),
		RestoredFreqHz:  DefaultCarrierHz,
		RestoredDutyPct: DefaultDutyPct,
	}
	defer func() {
		retErr = errors.Join(retErr, xcvr.restoreDefaults())
	}()

	for i, hz := range targetFreqs {
		if ctx.Err() != nil {
			report.Cancelled = true
			return report, nil
		}
		if onStep != nil {
			onStep(i, hz)
		}
		step, sendErr := runSweepStep(ctx, xcvr, pulses, hz, duty)
		report.Steps = append(report.Steps, step)
		if sendErr != nil {
			if errors.Is(sendErr, context.Canceled) || errors.Is(sendErr, context.DeadlineExceeded) {
				report.Cancelled = true
				return report, nil
			}
			return report, sendErr
		}
		if i+1 < len(targetFreqs) {
			if err := sleepWithContext(ctx, stepGap); err != nil {
				report.Cancelled = true
				return report, nil
			}
		}
	}
	return report, nil
}

func normalizeSweepParams(freqs []uint32, dutyPct uint32, gap time.Duration) ([]uint32, uint32, time.Duration, error) {
	if len(freqs) == 0 {
		freqs = SweepCarrierPresets[:]
	}
	if dutyPct == 0 {
		dutyPct = DefaultDutyPct
	}
	for _, hz := range freqs {
		if err := ValidateCarrier(irfile.CarrierSpec{FreqHz: hz, DutyPct: dutyPct}); err != nil {
			return nil, 0, 0, fmt.Errorf("sweep frequency %d Hz: %w", hz, err)
		}
	}
	if gap <= 0 {
		gap = DefaultSweepGap
	}
	gap = min(max(gap, MinSweepGap), MaxSweepGap)
	return freqs, dutyPct, gap, nil
}

func encodeSweepPulses(reg *irfile.ProtocolRegistry, sig irfile.IRSignal) ([]uint32, error) {
	train, err := reg.Encode(sig)
	if err != nil {
		return nil, fmt.Errorf("sweep encode %q: %w", sig.Name, err)
	}
	pulses, err := train.ToLIRCPulses()
	if err != nil {
		return nil, fmt.Errorf("sweep normalize %q: %w", sig.Name, err)
	}
	return pulses, nil
}

func runSweepStep(ctx context.Context, xcvr *Transceiver, pulses []uint32, hz, duty uint32) (SweepStepResult, error) {
	start := time.Now()
	err := xcvr.Send(ctx, pulses, irfile.CarrierSpec{FreqHz: hz, DutyPct: duty})
	durMs := time.Since(start).Milliseconds()
	step := SweepStepResult{
		FreqHz:     hz,
		DutyPct:    duty,
		DurationMs: durMs,
		Sent:       err == nil,
	}
	if err != nil {
		step.Err = err.Error()
	}
	return step, err
}
