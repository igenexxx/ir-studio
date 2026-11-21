package lirc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"ir-studio/internal/irml"
)

const (
	// DefaultMeasureWindowMs is the default capture window for flicker and RPM measurements.
	DefaultMeasureWindowMs = 1000
	// MinMeasureWindowMs and MaxMeasureWindowMs bound measurement windows.
	MinMeasureWindowMs = 200
	MaxMeasureWindowMs = 5000

	maxMeasurePulses = 512
	rpmProbePulseUs  = 600
	rpmProbeGap      = 15 * time.Millisecond
)

// MeasureFlicker performs a strictly RX-only pulse capture on tr for windowMs
// milliseconds (TX LED remains completely off) and returns an IEEE 1789 PWM
// flicker analysis. In SIM mode it returns a deterministic 120 Hz LED PWM train.
func MeasureFlicker(ctx context.Context, tr *Transceiver, windowMs int) (irml.FlickerReport, error) {
	if tr == nil || tr.Transport() == nil {
		return irml.FlickerReport{}, ErrDeviceOpen
	}
	win, err := normalizeMeasureWindow(windowMs)
	if err != nil {
		return irml.FlickerReport{}, err
	}
	if err := ctx.Err(); err != nil {
		return irml.FlickerReport{}, err
	}
	if tr.Transport().IsSim() {
		return irml.AnalyzeFlicker(simFlickerPulses()), nil
	}
	pulses, err := captureMode2Pulses(ctx, tr.Transport(), win)
	if err != nil {
		return irml.FlickerReport{}, err
	}
	return irml.AnalyzeFlicker(pulses), nil
}

// MeasureRPM emits bounded 38 kHz optical probe bursts via TransceiverGate and
// measures reflected pulse periodicity on RX, dividing by blades (1..12).
// Carrier and duty cycle are unconditionally restored to 38000 Hz / 33% on exit.
// In SIM mode it returns a deterministic 1800 RPM (2-blade, 60 Hz) reflection.
func MeasureRPM(ctx context.Context, tr *Transceiver, blades int, windowMs int) (report irml.RPMReport, retErr error) {
	if tr == nil || tr.Transport() == nil {
		return irml.RPMReport{}, ErrDeviceOpen
	}
	if blades < irml.MinTachBlades || blades > irml.MaxTachBlades {
		return irml.RPMReport{}, fmt.Errorf("blades %d out of range [%d..%d]: %w", blades, irml.MinTachBlades, irml.MaxTachBlades, ErrInvalidPulseTrain)
	}
	win, err := normalizeMeasureWindow(windowMs)
	if err != nil {
		return irml.RPMReport{}, err
	}
	if err := ctx.Err(); err != nil {
		return irml.RPMReport{}, err
	}
	if err := tr.Gate().AcquireTX(ctx); err != nil {
		return irml.RPMReport{}, err
	}
	defer tr.Gate().ReleaseTX()

	transport := tr.Transport()
	if err := transport.SetSendCarrier(DefaultCarrierHz); err != nil {
		return irml.RPMReport{}, err
	}
	defer func() {
		retErr = errors.Join(retErr, tr.restoreDefaults())
	}()
	if err := transport.SetSendDutyCycle(DefaultDutyPct); err != nil {
		return irml.RPMReport{}, err
	}

	probeBurst := []uint32{rpmProbePulseUs, rpmProbePulseUs, rpmProbePulseUs}
	if err := transport.WritePulses(ctx, probeBurst); err != nil {
		return irml.RPMReport{}, err
	}
	if transport.IsSim() {
		return irml.AnalyzeRPM(simRPMPulses(), blades), nil
	}
	pulses, err := captureRPMReflections(ctx, transport, probeBurst, win)
	if err != nil {
		return irml.RPMReport{}, err
	}
	return irml.AnalyzeRPM(pulses, blades), nil
}

func normalizeMeasureWindow(windowMs int) (time.Duration, error) {
	if windowMs == 0 {
		windowMs = DefaultMeasureWindowMs
	}
	if windowMs < MinMeasureWindowMs || windowMs > MaxMeasureWindowMs {
		return 0, fmt.Errorf("window_ms %d out of range [%d..%d]: %w", windowMs, MinMeasureWindowMs, MaxMeasureWindowMs, ErrInvalidPulseTrain)
	}
	return time.Duration(windowMs) * time.Millisecond, nil
}

func captureMode2Pulses(ctx context.Context, tr Transport, window time.Duration) ([]int32, error) {
	winCtx, cancel := context.WithTimeout(ctx, window)
	defer cancel()

	pulses := make([]int32, 0, maxMeasurePulses)
	buf := make([]uint32, 64)
	for len(pulses) < maxMeasurePulses {
		n, err := tr.ReadMode2(winCtx, buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			break
		}
		pulses = appendMode2Pulses(pulses, buf[:n], maxMeasurePulses)
	}
	return pulses, nil
}

func captureRPMReflections(ctx context.Context, tr Transport, burst []uint32, window time.Duration) ([]int32, error) {
	winCtx, cancel := context.WithTimeout(ctx, window)
	defer cancel()

	pulses := make([]int32, 0, maxMeasurePulses)
	buf := make([]uint32, 64)
	for len(pulses) < maxMeasurePulses && winCtx.Err() == nil {
		sliceCtx, sliceCancel := context.WithTimeout(winCtx, rpmProbeGap)
		n, err := tr.ReadMode2(sliceCtx, buf)
		sliceCancel()
		if err == nil && n > 0 {
			pulses = appendMode2Pulses(pulses, buf[:n], maxMeasurePulses)
			continue
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if winCtx.Err() != nil {
			break
		}
		if wErr := tr.WritePulses(winCtx, burst); wErr != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			break
		}
	}
	return pulses, nil
}

func appendMode2Pulses(dst []int32, pkts []uint32, limit int) []int32 {
	for _, pkt := range pkts {
		if len(dst) >= limit {
			break
		}
		val := int32(pkt & LIRC_VALUE_MASK)
		if val <= 0 {
			continue
		}
		switch pkt & LIRC_MODE2_MASK {
		case LIRC_MODE2_PULSE:
			dst = append(dst, val)
		case LIRC_MODE2_SPACE:
			dst = append(dst, -val)
		}
	}
	return dst
}

// simFlickerPulses returns a deterministic 120 Hz, 50% duty LED PWM train.
func simFlickerPulses() []int32 {
	const cycles = 24
	out := make([]int32, 0, cycles*2)
	for range cycles {
		out = append(out, 4167, -4166)
	}
	return out
}

// simRPMPulses returns a deterministic 60 Hz blade-pass reflection (1800 RPM @ 2 blades).
func simRPMPulses() []int32 {
	const cycles = 20
	out := make([]int32, 0, cycles*2)
	for range cycles {
		out = append(out, 3000, -13667)
	}
	return out
}
