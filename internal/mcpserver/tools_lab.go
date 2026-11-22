package mcpserver

import (
	"context"
	"encoding/json"

	"ir-studio/internal/irml"
	"ir-studio/internal/lirc"
)

func labToolDefinitions() []toolDefinition {
	return []toolDefinition{
		{
			Name:        "ir_measure_flicker",
			Description: "RX-only LED/screen PWM flicker meter (TX strictly off): measures dominant Hz, duty %, jitter %, and IEEE 1789 eye-safety risk.",
			InputSchema: objSchema(map[string]any{
				"window_ms": intProp("RX capture window in milliseconds (200..5000, default 1000)", lirc.MinMeasureWindowMs, lirc.MaxMeasureWindowMs),
			}, nil),
		},
		{
			Name:        "ir_measure_rpm",
			Description: "Optical IR RPM tachometer emitting 38 kHz probe bursts and dividing reflected pulse periodicity by blades (1..12) with guaranteed 38 kHz / 33% restore. Requires confirm_ir: true.",
			InputSchema: objSchema(map[string]any{
				"confirm_ir": boolProp("Must be true to emit 38 kHz IR probe bursts"),
				"blades":     intProp("Number of rotor blades or reflective stripes per revolution (1..12, default 2)", irml.MinTachBlades, irml.MaxTachBlades),
				"window_ms":  intProp("Measurement window in milliseconds (200..5000, default 1000)", lirc.MinMeasureWindowMs, lirc.MaxMeasureWindowMs),
			}, []string{"confirm_ir"}),
		},
		{
			Name:        "ir_model_info",
			Description: "Inspect the active on-device Pure-Go INT8 PulseNet-IR .czml model (version, SHA-256, embedded vs override source, 24 fine classes, denoiser availability).",
			InputSchema: objSchema(nil, nil),
		},
	}
}

func (s *Server) toolModelInfo(_ context.Context, _ json.RawMessage) (toolResult, *rpcError) {
	return okJSON(map[string]any{
		"pulsenet_ir":   irml.ActivePulseNetInfo(),
		"models_dir":    irml.DefaultModelsDir(),
		"embedding_dim": irml.PulseNetEmbedDim,
	}), nil
}

func (s *Server) toolMeasureFlicker(ctx context.Context, raw json.RawMessage) (toolResult, *rpcError) {
	var args struct {
		WindowMs int `json:"window_ms"`
	}
	if rpcErr := decodeArgs(raw, &args); rpcErr != nil {
		return toolResult{}, rpcErr
	}
	windowMs := args.WindowMs
	if windowMs == 0 {
		windowMs = lirc.DefaultMeasureWindowMs
	}
	if windowMs < lirc.MinMeasureWindowMs || windowMs > lirc.MaxMeasureWindowMs {
		return errResult("window_ms must be between 200 and 5000"), nil
	}
	report, err := s.backend.MeasureFlicker(ctx, windowMs)
	if err != nil {
		return errResult("flicker measurement failed: " + err.Error()), nil
	}
	return okJSON(map[string]any{
		"freq_hz":    report.FreqHz,
		"duty_pct":   report.DutyPct,
		"jitter_pct": report.JitterPct,
		"periods":    report.Periods,
		"risk":       report.Risk,
		"risk_label": report.RiskLabel,
		"window_ms":  windowMs,
		"mode":       s.backend.HardwareMode(),
	}), nil
}

func (s *Server) toolMeasureRPM(ctx context.Context, raw json.RawMessage) (toolResult, *rpcError) {
	var args struct {
		ConfirmIR bool `json:"confirm_ir"`
		Blades    int  `json:"blades"`
		WindowMs  int  `json:"window_ms"`
	}
	if rpcErr := decodeArgs(raw, &args); rpcErr != nil {
		return toolResult{}, rpcErr
	}
	if !args.ConfirmIR {
		return errResult(errConfirmIRRequired), nil
	}
	blades := args.Blades
	if blades == 0 {
		blades = 2
	}
	if blades < irml.MinTachBlades || blades > irml.MaxTachBlades {
		return errResult("blades must be between 1 and 12"), nil
	}
	windowMs := args.WindowMs
	if windowMs == 0 {
		windowMs = lirc.DefaultMeasureWindowMs
	}
	if windowMs < lirc.MinMeasureWindowMs || windowMs > lirc.MaxMeasureWindowMs {
		return errResult("window_ms must be between 200 and 5000"), nil
	}
	report, err := s.backend.MeasureRPM(ctx, blades, windowMs)
	if err != nil {
		return errResult("RPM measurement failed: " + err.Error()), nil
	}
	return okJSON(map[string]any{
		"rpm":           report.RPM,
		"blade_freq_hz": report.BladeFreqHz,
		"blades":        report.Blades,
		"stability_pct": report.StabilityPct,
		"samples":       report.Samples,
		"valid":         report.Valid,
		"window_ms":     windowMs,
		"mode":          s.backend.HardwareMode(),
	}), nil
}
