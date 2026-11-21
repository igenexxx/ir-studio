package lirc

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"ir-studio/internal/irml"
)

func TestMeasureFlickerSimRXOnly(t *testing.T) {
	sim := NewSimTransport()
	xcvr := NewTransceiver(sim, NewTransceiverGate(time.Millisecond))

	report, err := MeasureFlicker(context.Background(), xcvr, 500)
	if err != nil {
		t.Fatalf("MeasureFlicker: %v", err)
	}
	if len(sim.TxHistory()) != 0 {
		t.Fatalf("MeasureFlicker must keep TX strictly off, got %d TX bursts", len(sim.TxHistory()))
	}
	if math.Abs(float64(report.FreqHz-120.0)) > 1.0 {
		t.Fatalf("expected ~120 Hz SIM LED PWM, got %.2f Hz", report.FreqHz)
	}
	if report.Risk != irml.FlickerHigh || report.RiskLabel != "HIGH_FLICKER" {
		t.Fatalf("expected HIGH_FLICKER, got %+v", report)
	}
}

func TestMeasureFlickerValidationAndCancel(t *testing.T) {
	sim := NewSimTransport()
	xcvr := NewTransceiver(sim, NewTransceiverGate(time.Millisecond))

	if _, err := MeasureFlicker(context.Background(), nil, 500); !errors.Is(err, ErrDeviceOpen) {
		t.Fatalf("nil transceiver: got %v, want ErrDeviceOpen", err)
	}
	if _, err := MeasureFlicker(context.Background(), xcvr, 100); err == nil {
		t.Fatal("expected error for window_ms < 200")
	}
	if _, err := MeasureFlicker(context.Background(), xcvr, 6000); err == nil {
		t.Fatal("expected error for window_ms > 5000")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := MeasureFlicker(ctx, xcvr, 500); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled ctx: got %v, want context.Canceled", err)
	}
}

func TestMeasureRPMSimAndCarrierRestore(t *testing.T) {
	sim := NewSimTransport()
	// Pre-configure non-default carrier/duty to verify unconditional restore.
	_ = sim.SetSendCarrier(56000)
	_ = sim.SetSendDutyCycle(50)
	xcvr := NewTransceiver(sim, NewTransceiverGate(time.Millisecond))

	report, err := MeasureRPM(context.Background(), xcvr, 2, 500)
	if err != nil {
		t.Fatalf("MeasureRPM: %v", err)
	}
	if !report.Valid {
		t.Fatalf("expected valid RPM report, got %+v", report)
	}
	if math.Abs(float64(report.RPM-1800.0)) > 5.0 {
		t.Fatalf("expected ~1800 RPM for 2-blade SIM fan, got %.2f RPM", report.RPM)
	}
	if len(sim.TxHistory()) == 0 {
		t.Fatal("expected at least one 38 kHz probe burst on TX")
	}
	cs := sim.CarrierState()
	if cs.FreqHz != DefaultCarrierHz || cs.DutyPct != DefaultDutyPct {
		t.Fatalf("carrier not restored to 38000 Hz / 33%%: got %+v", cs)
	}
}

func TestMeasureRPMValidationAndCancel(t *testing.T) {
	sim := NewSimTransport()
	xcvr := NewTransceiver(sim, NewTransceiverGate(time.Millisecond))

	if _, err := MeasureRPM(context.Background(), nil, 2, 500); !errors.Is(err, ErrDeviceOpen) {
		t.Fatalf("nil transceiver: got %v, want ErrDeviceOpen", err)
	}
	for _, badBlades := range []int{0, -1, 13} {
		if _, err := MeasureRPM(context.Background(), xcvr, badBlades, 500); err == nil {
			t.Fatalf("expected error for blades=%d", badBlades)
		}
	}
	if _, err := MeasureRPM(context.Background(), xcvr, 2, 100); err == nil {
		t.Fatal("expected error for window_ms < 200")
	}
	if _, err := MeasureRPM(context.Background(), xcvr, 2, 6000); err == nil {
		t.Fatal("expected error for window_ms > 5000")
	}
	if len(sim.TxHistory()) != 0 {
		t.Fatalf("invalid calls must not transmit, got %d bursts", len(sim.TxHistory()))
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := MeasureRPM(ctx, xcvr, 2, 500); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled ctx: got %v, want context.Canceled", err)
	}
}

// hwStubTransport simulates a non-SIM LIRC transport to exercise real-hardware RX/TX paths.
type hwStubTransport struct {
	*SimTransport
}

func (h *hwStubTransport) IsSim() bool        { return false }
func (h *hwStubTransport) DevicePath() string { return "/dev/lirc0" }

func TestMeasureFlickerAndRPMHardwarePath(t *testing.T) {
	stub := &hwStubTransport{SimTransport: NewSimTransport()}
	xcvr := NewTransceiver(stub, NewTransceiverGate(time.Millisecond))

	// Inject 2000 Hz PWM packets (250 us mark, 250 us space).
	pkts := make([]uint32, 0, 40)
	for range 20 {
		pkts = append(pkts, LIRC_MODE2_PULSE|250, LIRC_MODE2_SPACE|250)
	}
	stub.InjectMode2(pkts)

	flicker, err := MeasureFlicker(context.Background(), xcvr, 200)
	if err != nil {
		t.Fatalf("hardware MeasureFlicker: %v", err)
	}
	if flicker.Risk != irml.FlickerLowRisk || math.Abs(float64(flicker.FreqHz-2000.0)) > 15.0 {
		t.Fatalf("unexpected hardware flicker report: %+v", flicker)
	}

	// Inject 60 Hz reflection packets for 4-blade rotor => 900 RPM.
	rpmPkts := make([]uint32, 0, 32)
	for range 16 {
		rpmPkts = append(rpmPkts, LIRC_MODE2_PULSE|3000, LIRC_MODE2_SPACE|13667)
	}
	stub.InjectMode2(rpmPkts)

	rpm, err := MeasureRPM(context.Background(), xcvr, 4, 200)
	if err != nil {
		t.Fatalf("hardware MeasureRPM: %v", err)
	}
	if !rpm.Valid || math.Abs(float64(rpm.RPM-900.0)) > 5.0 {
		t.Fatalf("unexpected hardware RPM report: %+v", rpm)
	}
}
