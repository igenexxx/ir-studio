package mcpserver

import (
	"math"
	"strings"
	"testing"

	"ir-studio/internal/lirc"
)

func TestMCPMeasureFlickerAndRPM(t *testing.T) {
	env := newTestEnv(t, nil)

	// 1. ir_measure_flicker succeeds RX-only without confirm_ir and emits 0 TX bursts.
	flRes := callToolOK(t, env, "ir_measure_flicker", map[string]any{
		"window_ms": 500,
	})
	if len(env.sim.TxHistory()) != 0 {
		t.Fatalf("ir_measure_flicker must keep TX off, got %d TX bursts", len(env.sim.TxHistory()))
	}
	freq, _ := flRes["freq_hz"].(float64)
	if math.Abs(freq-120.0) > 1.0 || flRes["risk_label"] != "HIGH_FLICKER" {
		t.Fatalf("unexpected ir_measure_flicker response: %v", flRes)
	}

	// 2. ir_measure_flicker rejects out-of-range window_ms.
	if msg := callToolErr(t, env, "ir_measure_flicker", map[string]any{
		"window_ms": 50,
	}); !strings.Contains(msg, "window_ms") {
		t.Fatalf("expected window_ms range error, got %q", msg)
	}

	// 3. ir_measure_rpm requires confirm_ir: true.
	if msg := callToolErr(t, env, "ir_measure_rpm", map[string]any{
		"confirm_ir": false,
		"blades":     2,
	}); !strings.Contains(msg, "confirm_ir") {
		t.Fatalf("expected confirm_ir error, got %q", msg)
	}

	// 4. ir_measure_rpm rejects invalid blades and window_ms without transmitting.
	if msg := callToolErr(t, env, "ir_measure_rpm", map[string]any{
		"confirm_ir": true,
		"blades":     13,
	}); !strings.Contains(msg, "blades") {
		t.Fatalf("expected blades range error, got %q", msg)
	}
	if msg := callToolErr(t, env, "ir_measure_rpm", map[string]any{
		"confirm_ir": true,
		"blades":     2,
		"window_ms":  9999,
	}); !strings.Contains(msg, "window_ms") {
		t.Fatalf("expected window_ms range error, got %q", msg)
	}
	if len(env.sim.TxHistory()) != 0 {
		t.Fatalf("rejected ir_measure_rpm calls must not transmit, got %d bursts", len(env.sim.TxHistory()))
	}

	// 5. ir_measure_rpm succeeds in SIM mode (1800 RPM @ 2 blades) and restores 38 kHz / 33%.
	rpmRes := callToolOK(t, env, "ir_measure_rpm", map[string]any{
		"confirm_ir": true,
		"blades":     2,
		"window_ms":  400,
	})
	rpmVal, _ := rpmRes["rpm"].(float64)
	if rpmRes["valid"] != true || math.Abs(rpmVal-1800.0) > 5.0 {
		t.Fatalf("unexpected ir_measure_rpm response: %v", rpmRes)
	}
	cs := env.sim.CarrierState()
	if cs.FreqHz != lirc.DefaultCarrierHz || cs.DutyPct != lirc.DefaultDutyPct {
		t.Fatalf("carrier not restored to 38000 Hz / 33%%: got %+v", cs)
	}
}
