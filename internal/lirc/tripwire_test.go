package lirc

import (
	"context"
	"errors"
	"testing"
	"time"

	"ir-studio/internal/irfile"
)

func TestRXWorkerHardwareCarrierMeasurement(t *testing.T) {
	sim := NewSimTransport()
	gate := NewTransceiverGate(time.Millisecond)
	rx := NewRXWorker(sim, gate, irfile.DefaultRegistry())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rx.Start(ctx)
	defer rx.Close()

	// Verify CarrierMeasurer interface works on SimTransport.
	var cm CarrierMeasurer = sim
	if err := cm.SetMeasureCarrier(true); err != nil || !sim.MeasureCarrierEnabled() {
		t.Fatalf("SetMeasureCarrier(true) failed: err=%v enabled=%v", err, sim.MeasureCarrierEnabled())
	}

	// Inject LIRC_MODE2_FREQUENCY (40000 Hz & 40020 Hz -> avg 40010 Hz) interleaved with a valid pulse frame.
	pkts := []uint32{
		LIRC_MODE2_FREQUENCY | 40000,
		LIRC_MODE2_PULSE | 2400,
		LIRC_MODE2_SPACE | 600,
		LIRC_MODE2_FREQUENCY | 40020,
		LIRC_MODE2_PULSE | 1200,
		LIRC_MODE2_SPACE | 600,
		LIRC_MODE2_PULSE | 600,
		LIRC_MODE2_SPACE | 600,
		LIRC_MODE2_PULSE | 600,
		LIRC_MODE2_TIMEOUT | DefaultRecTimeoutUs,
	}
	sim.InjectMode2(pkts)

	select {
	case capEvt := <-rx.Captures():
		if capEvt.CarrierEstHz != 40010 {
			t.Fatalf("CarrierEstHz = %d, want 40010", capEvt.CarrierEstHz)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for RXCapture with measured carrier")
	}

	// Next frame without LIRC_MODE2_FREQUENCY must fall back to DefaultCarrierHz (38000),
	// proving per-frame accumulator reset.
	sim.InjectPulseTrain(irfile.PulseTrain{900, -900, 900, -900, 900, -900, 900})
	select {
	case capEvt := <-rx.Captures():
		if capEvt.CarrierEstHz != DefaultCarrierHz {
			t.Fatalf("fallback CarrierEstHz = %d, want %d", capEvt.CarrierEstHz, DefaultCarrierHz)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for fallback RXCapture")
	}
}

func TestTripwireProbeReflectiveAndBeamBreak(t *testing.T) {
	sim := NewSimTransport()
	gate := NewTransceiverGate(time.Millisecond)
	xcvr := NewTransceiver(sim, gate)

	// 1. Reflective mode with loopback ON -> echoes received, Triggered == true, carrier restored.
	sim.SetLoopback(true)
	reading, err := TripwireProbe(context.Background(), xcvr, TripwireConfig{
		Mode:         TripwireReflective,
		CarrierHz:    40000,
		DutyPct:      50,
		PingCount:    3,
		ListenWindow: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("TripwireProbe reflective failed: %v", err)
	}
	if reading.PingsSent != 3 || reading.EchoesReceived != 3 || !reading.Triggered || reading.ReflectionPct != 100 || reading.HardwareMode != "SIM" {
		t.Fatalf("unexpected reflective reading: %+v", reading)
	}
	if got := sim.CarrierState(); got != DefaultCarrier() {
		t.Fatalf("carrier not restored after probe: got %+v, want %+v", got, DefaultCarrier())
	}

	// 2. Beam-break mode with loopback OFF -> 0 echoes, Triggered == true.
	sim.SetLoopback(false)
	reading, err = TripwireProbe(context.Background(), xcvr, TripwireConfig{
		Mode:         TripwireBeamBreak,
		PingCount:    2,
		ListenWindow: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("TripwireProbe beam_break failed: %v", err)
	}
	if reading.PingsSent != 2 || reading.EchoesReceived != 0 || !reading.Triggered || reading.ReflectionPct != 0 {
		t.Fatalf("unexpected beam_break reading: %+v", reading)
	}
}

func TestTripwireProbeAdversarialAndCancel(t *testing.T) {
	sim := NewSimTransport()
	xcvr := NewTransceiver(sim, NewTransceiverGate(time.Millisecond))

	if _, err := TripwireProbe(context.Background(), nil, TripwireConfig{}); !errors.Is(err, ErrDeviceOpen) {
		t.Fatalf("nil transceiver got %v, want ErrDeviceOpen", err)
	}
	if _, err := TripwireProbe(context.Background(), xcvr, TripwireConfig{Mode: "invalid"}); !errors.Is(err, ErrUnsupportedFeature) {
		t.Fatalf("invalid mode got %v, want ErrUnsupportedFeature", err)
	}
	if _, err := TripwireProbe(context.Background(), xcvr, TripwireConfig{CarrierHz: 12000}); !errors.Is(err, ErrInvalidCarrier) {
		t.Fatalf("out-of-range carrier got %v, want ErrInvalidCarrier", err)
	}
	if _, err := TripwireProbe(context.Background(), xcvr, TripwireConfig{PingCount: 11}); !errors.Is(err, ErrRepeatLimitExceeded) {
		t.Fatalf("excess pings got %v, want ErrRepeatLimitExceeded", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := TripwireProbe(ctx, xcvr, TripwireConfig{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context got %v, want context.Canceled", err)
	}
}
