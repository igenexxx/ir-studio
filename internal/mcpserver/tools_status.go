package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"ir-studio/internal/config"
	"ir-studio/internal/irfile"
	"ir-studio/internal/irml"
	"ir-studio/internal/lirc"
)

const (
	defaultLearnTimeoutMs = 5000
	minLearnTimeoutMs     = 500
	maxLearnTimeoutMs     = 15000
)

type signalDTO struct {
	Name      string   `json:"name"`
	Type      string   `json:"type"`
	Protocol  string   `json:"protocol,omitempty"`
	Address   string   `json:"address,omitempty"`
	Command   string   `json:"command,omitempty"`
	Frequency uint32   `json:"frequency,omitempty"`
	DutyCycle float32  `json:"duty_cycle,omitempty"`
	Data      []uint32 `json:"data,omitempty"`
}

func (s *Server) toolGetStatus(_ context.Context, _ json.RawMessage) (toolResult, *rpcError) {
	carrier := s.backend.CarrierSpec()
	ambient := s.backend.AmbientSnapshot()

	remotes, _ := s.store.ListUserRemotes()
	packs, _ := s.store.ListDatabasePacks()
	macrosCount := 0
	if s.macrosPath != "" {
		if mf, err := config.LoadMacros(s.macrosPath); err == nil && mf != nil {
			macrosCount = len(mf.Macros)
		}
	}

	return okJSON(map[string]any{
		"hardware_mode":        s.backend.HardwareMode(),
		"device_path":          s.backend.DevicePath(),
		"tx_carrier_hz":        carrier.FreqHz,
		"tx_duty_pct":          carrier.DutyPct,
		"ambient_noise_pct":    ambient.NoiseDutyPct,
		"ambient_status":       ambient.Level.String(),
		"user_remotes_count":   len(remotes),
		"database_packs_count": len(packs),
		"macros_count":         macrosCount,
		"mcp_enabled":          s.settings.MCPEnabled,
		"probe_interval_ms":    s.settings.ProbeIntervalMs,
	}), nil
}

func (s *Server) toolLearnSignal(ctx context.Context, raw json.RawMessage) (toolResult, *rpcError) {
	var args struct {
		TimeoutMs int    `json:"timeout_ms"`
		CarrierHz uint32 `json:"carrier_hz"`
	}
	if rpcErr := decodeArgs(raw, &args); rpcErr != nil {
		return toolResult{}, rpcErr
	}
	timeoutMs := args.TimeoutMs
	if timeoutMs == 0 {
		timeoutMs = defaultLearnTimeoutMs
	}
	if timeoutMs < minLearnTimeoutMs || timeoutMs > maxLearnTimeoutMs {
		return errResult(fmt.Sprintf("timeout_ms must be between %d and %d", minLearnTimeoutMs, maxLearnTimeoutMs)), nil
	}
	carrierHz := args.CarrierHz
	if carrierHz == 0 {
		carrierHz = irfile.DefaultFreqHz
	}
	if carrierHz < 36000 || carrierHz > 56000 {
		return errResult("carrier_hz must be between 36000 and 56000"), nil
	}

	cap, err := s.backend.LearnSignal(ctx, time.Duration(timeoutMs)*time.Millisecond, carrierHz)
	if err != nil {
		return errResult("learn signal failed: " + err.Error()), nil
	}

	sig := capturedSignal(cap, s.registry, carrierHz)
	cls := irml.ClassifySignal(cap.Pulses, carrierHz)
	ambient := s.backend.AmbientSnapshot()
	topMatch := s.bestFingerprintMatch(cap.Pulses, carrierHz)

	out := map[string]any{
		"button":            signalToDTO(sig),
		"ambient_noise_pct": ambient.NoiseDutyPct,
		"ml_classification": classificationDTO(cls),
	}
	if topMatch != nil {
		out["fingerprint_match"] = topMatch
	}
	return okJSON(out), nil
}

func capturedSignal(cap lirc.RXCapture, reg *irfile.ProtocolRegistry, carrierHz uint32) irfile.IRSignal {
	if cap.Decoded != nil {
		return *cap.Decoded
	}
	carrier := irfile.CarrierSpec{FreqHz: carrierHz, DutyPct: irfile.DefaultDutyPct}
	sig, err := reg.DecodeOrRaw(cap.Pulses, carrier)
	if err != nil {
		return irfile.IRSignal{
			Name:      "Captured",
			Kind:      irfile.SignalRaw,
			Frequency: carrierHz,
			DutyCycle: irfile.DefaultDutyCycle,
		}
	}
	return sig
}

func signalToDTO(sig irfile.IRSignal) signalDTO {
	dto := signalDTO{
		Name:      sig.Name,
		Type:      string(sig.Kind),
		Frequency: sig.Frequency,
		DutyCycle: sig.DutyCycle,
	}
	if sig.Kind == irfile.SignalParsed {
		dto.Protocol = sig.Protocol
		dto.Address = formatHex4(sig.Address)
		dto.Command = formatHex4(sig.Command)
	} else {
		dto.Data = sig.RawData
	}
	return dto
}

func dtoToSignal(dto signalDTO) (irfile.IRSignal, error) {
	if strings.TrimSpace(dto.Name) == "" {
		return irfile.IRSignal{}, fmt.Errorf("button name is required")
	}
	kind := irfile.SignalKind(strings.ToLower(strings.TrimSpace(dto.Type)))
	switch kind {
	case irfile.SignalParsed:
		addr, err := parseHex4(dto.Address)
		if err != nil {
			return irfile.IRSignal{}, fmt.Errorf("invalid address for %q: %w", dto.Name, err)
		}
		cmd, err := parseHex4(dto.Command)
		if err != nil {
			return irfile.IRSignal{}, fmt.Errorf("invalid command for %q: %w", dto.Name, err)
		}
		if strings.TrimSpace(dto.Protocol) == "" {
			return irfile.IRSignal{}, fmt.Errorf("protocol is required for parsed button %q", dto.Name)
		}
		return irfile.IRSignal{
			Name:     dto.Name,
			Kind:     irfile.SignalParsed,
			Protocol: dto.Protocol,
			Address:  addr,
			Command:  cmd,
		}, nil
	case irfile.SignalRaw:
		if len(dto.Data) == 0 {
			return irfile.IRSignal{}, fmt.Errorf("raw button %q requires non-empty data", dto.Name)
		}
		freq := dto.Frequency
		if freq == 0 {
			freq = irfile.DefaultFreqHz
		}
		duty := dto.DutyCycle
		if duty == 0 {
			duty = irfile.DefaultDutyCycle
		}
		return irfile.IRSignal{
			Name:      dto.Name,
			Kind:      irfile.SignalRaw,
			Frequency: freq,
			DutyCycle: duty,
			RawData:   dto.Data,
		}, nil
	default:
		return irfile.IRSignal{}, fmt.Errorf("button %q has unsupported type %q (expected parsed or raw)", dto.Name, dto.Type)
	}
}

func formatHex4(b [4]byte) string {
	return fmt.Sprintf("%02X %02X %02X %02X", b[0], b[1], b[2], b[3])
}

func parseHex4(s string) ([4]byte, error) {
	var out [4]byte
	fields := strings.Fields(s)
	if len(fields) != 4 {
		return out, fmt.Errorf("expected 4 hex bytes, got %d", len(fields))
	}
	for i, f := range fields {
		v, err := strconv.ParseUint(f, 16, 8)
		if err != nil {
			return out, err
		}
		out[i] = byte(v)
	}
	return out, nil
}
