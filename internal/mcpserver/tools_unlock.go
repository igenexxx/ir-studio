package mcpserver

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"ir-studio/internal/irfile"
	"ir-studio/internal/irmodem"
	"ir-studio/internal/lirc"
)

func (s *Server) toolImportPronto(_ context.Context, raw json.RawMessage) (toolResult, *rpcError) {
	var args struct {
		Name      string `json:"name"`
		ProntoHex string `json:"pronto_hex"`
		SaveAs    string `json:"save_as"`
	}
	if rpcErr := decodeArgs(raw, &args); rpcErr != nil {
		return toolResult{}, rpcErr
	}
	if args.SaveAs != "" {
		if err := ValidateSafeName(args.SaveAs); err != nil {
			return errResult(err.Error()), nil
		}
	}
	sig, err := irfile.ParseProntoHex(args.Name, args.ProntoHex)
	if err != nil {
		return errResult(err.Error()), nil
	}
	out := map[string]any{
		"name":         sig.Name,
		"protocol":     sig.Protocol,
		"frequency_hz": sig.Frequency,
		"duty_cycle":   sig.DutyCycle,
		"pulse_count":  len(sig.RawData),
		"raw_data":     sig.RawData,
	}
	if args.SaveAs != "" {
		savedName, saveErr := s.saveACButton(args.SaveAs, sig)
		if saveErr != nil {
			return errResult("save pronto remote: " + saveErr.Error()), nil
		}
		out["saved_remote"] = savedName
	}
	return okJSON(out), nil
}

func (s *Server) toolExportPronto(_ context.Context, raw json.RawMessage) (toolResult, *rpcError) {
	var args struct {
		RemoteName string  `json:"remote_name"`
		ButtonName string  `json:"button_name"`
		Pack       string  `json:"pack"`
		Pulses     []int32 `json:"pulses"`
		CarrierHz  uint32  `json:"carrier_hz"`
	}
	if rpcErr := decodeArgs(raw, &args); rpcErr != nil {
		return toolResult{}, rpcErr
	}
	sig, err := s.resolveExportSignal(args.RemoteName, args.ButtonName, args.Pack, args.Pulses, args.CarrierHz)
	if err != nil {
		return errResult(err.Error()), nil
	}
	pronto, err := irfile.FormatProntoHex(sig)
	if err != nil {
		return errResult(err.Error()), nil
	}
	return okJSON(map[string]any{
		"name":         sig.Name,
		"frequency_hz": sig.CarrierSpec().FreqHz,
		"pronto_hex":   pronto,
		"word_count":   len(strings.Fields(pronto)),
	}), nil
}

func (s *Server) resolveExportSignal(remoteName, buttonName, pack string, pulses []int32, carrierHz uint32) (irfile.IRSignal, error) {
	if len(pulses) > 0 {
		lircPulses, err := irfile.PulseTrain(pulses).ToLIRCPulses()
		if err != nil {
			return irfile.IRSignal{}, err
		}
		if carrierHz == 0 {
			carrierHz = irfile.DefaultFreqHz
		}
		return irfile.IRSignal{
			Name:      "Raw_Export",
			Kind:      irfile.SignalRaw,
			Protocol:  "RAW",
			Frequency: carrierHz,
			DutyCycle: irfile.DefaultDutyCycle,
			RawData:   lircPulses,
		}, nil
	}
	if strings.TrimSpace(remoteName) == "" || strings.TrimSpace(buttonName) == "" {
		return irfile.IRSignal{}, fmt.Errorf("provide either pulses or remote_name + button_name")
	}
	return s.lookupButton(remoteName, buttonName, pack)
}

func (s *Server) toolModemSend(ctx context.Context, raw json.RawMessage) (toolResult, *rpcError) {
	var args struct {
		ConfirmIR   bool   `json:"confirm_ir"`
		PayloadText string `json:"payload_text"`
		PayloadHex  string `json:"payload_hex"`
		CarrierHz   uint32 `json:"carrier_hz"`
	}
	if rpcErr := decodeArgs(raw, &args); rpcErr != nil {
		return toolResult{}, rpcErr
	}
	if !args.ConfirmIR {
		return errResult(errConfirmIRRequired), nil
	}
	payload, err := decodeModemPayloadArgs(args.PayloadText, args.PayloadHex)
	if err != nil {
		return errResult(err.Error()), nil
	}
	pulses, err := irmodem.EncodeFrame(payload)
	if err != nil {
		return errResult(err.Error()), nil
	}
	lircPulses, err := pulses.ToLIRCPulses()
	if err != nil {
		return errResult(err.Error()), nil
	}
	carrierHz := args.CarrierHz
	if carrierHz == 0 {
		carrierHz = irmodem.CarrierHz
	}
	carrier := irfile.CarrierSpec{FreqHz: carrierHz, DutyPct: irmodem.DutyPct}
	sig := irfile.IRSignal{
		Name:      "Modem_Frame",
		Kind:      irfile.SignalRaw,
		Protocol:  "RAW",
		Frequency: carrierHz,
		DutyCycle: irfile.DefaultDutyCycle,
		RawData:   lircPulses,
	}
	dur, err := s.backend.TransmitSignal(ctx, sig, 1, carrier)
	if err != nil {
		return errResult("modem transmit failed: " + err.Error()), nil
	}
	wire := append([]byte{irmodem.FrameSyncByte, byte(len(payload))}, payload...)
	return okJSON(map[string]any{
		"transmitted":   true,
		"payload_bytes": len(payload),
		"payload_hex":   hex.EncodeToString(payload),
		"crc8_hex":      fmt.Sprintf("0x%02X", irmodem.CRC8(wire)),
		"pulse_count":   len(lircPulses),
		"carrier_hz":    carrierHz,
		"duration_ms":   dur.Milliseconds(),
		"mode":          s.backend.HardwareMode(),
	}), nil
}

func decodeModemPayloadArgs(text, hexStr string) ([]byte, error) {
	if text != "" && hexStr != "" {
		return nil, fmt.Errorf("specify either payload_text or payload_hex, not both")
	}
	if hexStr != "" {
		clean := strings.ReplaceAll(strings.TrimSpace(hexStr), " ", "")
		b, err := hex.DecodeString(clean)
		if err != nil {
			return nil, fmt.Errorf("invalid payload_hex: %w", err)
		}
		return b, nil
	}
	return []byte(text), nil
}

func (s *Server) toolModemRecv(ctx context.Context, raw json.RawMessage) (toolResult, *rpcError) {
	var args struct {
		TimeoutMs int     `json:"timeout_ms"`
		Pulses    []int32 `json:"pulses"`
	}
	if rpcErr := decodeArgs(raw, &args); rpcErr != nil {
		return toolResult{}, rpcErr
	}
	var pulses irfile.PulseTrain
	carrierHz := irmodem.CarrierHz
	if len(args.Pulses) > 0 {
		pulses = irfile.PulseTrain(args.Pulses)
	} else {
		timeoutMs := args.TimeoutMs
		if timeoutMs == 0 {
			timeoutMs = 5000
		}
		if timeoutMs < 500 || timeoutMs > 15000 {
			return errResult("timeout_ms must be between 500 and 15000"), nil
		}
		capEvt, err := s.backend.LearnSignal(ctx, time.Duration(timeoutMs)*time.Millisecond, irmodem.CarrierHz)
		if err != nil {
			return errResult("modem receive failed: " + err.Error()), nil
		}
		pulses = capEvt.Pulses
		if capEvt.CarrierEstHz != 0 {
			carrierHz = capEvt.CarrierEstHz
		}
	}
	payload, err := irmodem.DecodeFrame(pulses)
	if err != nil {
		return errResult("modem decode failed: " + err.Error()), nil
	}
	text := ""
	if utf8.Valid(payload) {
		text = string(payload)
	}
	return okJSON(map[string]any{
		"received":      true,
		"payload_bytes": len(payload),
		"payload_hex":   hex.EncodeToString(payload),
		"payload_text":  text,
		"carrier_hz":    carrierHz,
		"pulse_count":   len(pulses),
		"mode":          s.backend.HardwareMode(),
	}), nil
}

func (s *Server) toolTripwireProbe(ctx context.Context, raw json.RawMessage) (toolResult, *rpcError) {
	var args struct {
		ConfirmIR bool   `json:"confirm_ir"`
		Mode      string `json:"mode"`
		PingCount int    `json:"ping_count"`
		CarrierHz uint32 `json:"carrier_hz"`
		ListenMs  int    `json:"listen_ms"`
	}
	if rpcErr := decodeArgs(raw, &args); rpcErr != nil {
		return toolResult{}, rpcErr
	}
	if !args.ConfirmIR {
		return errResult(errConfirmIRRequired), nil
	}
	cfg := lirc.TripwireConfig{
		Mode:      lirc.TripwireMode(strings.TrimSpace(args.Mode)),
		PingCount: args.PingCount,
		CarrierHz: args.CarrierHz,
	}
	if args.ListenMs != 0 {
		cfg.ListenWindow = time.Duration(args.ListenMs) * time.Millisecond
	}
	reading, err := s.backend.TripwireProbe(ctx, cfg)
	if err != nil {
		return errResult("tripwire probe failed: " + err.Error()), nil
	}
	return okJSON(reading), nil
}
