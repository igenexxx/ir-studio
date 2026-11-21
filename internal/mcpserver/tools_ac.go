package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"ir-studio/internal/irfile"
	"ir-studio/internal/lirc"
)

const maxSweepFrequencies = 8

func (s *Server) toolSendACState(ctx context.Context, raw json.RawMessage) (toolResult, *rpcError) {
	var args struct {
		ConfirmIR bool   `json:"confirm_ir"`
		Brand     string `json:"brand"`
		Power     bool   `json:"power"`
		Mode      string `json:"mode"`
		TempC     int    `json:"temp_c"`
		Fan       string `json:"fan"`
		Swing     bool   `json:"swing"`
		SaveAs    string `json:"save_as"`
	}
	if rpcErr := decodeArgs(raw, &args); rpcErr != nil {
		return toolResult{}, rpcErr
	}
	if !args.ConfirmIR {
		return errResult(errConfirmIRRequired), nil
	}
	state, err := buildACState(args.Brand, args.Power, args.Mode, args.TempC, args.Fan, args.Swing)
	if err != nil {
		return errResult(err.Error()), nil
	}
	if args.SaveAs != "" {
		if err := ValidateSafeName(args.SaveAs); err != nil {
			return errResult(err.Error()), nil
		}
	}
	acReg := irfile.DefaultACRegistry()
	enc, ok := acReg.Lookup(state.Brand)
	if !ok {
		return errResult(fmt.Sprintf("unsupported AC brand %q", state.Brand)), nil
	}
	frameBytes, err := enc.EncodeBytes(state)
	if err != nil {
		return errResult(err.Error()), nil
	}
	sig, err := enc.EncodeSignal(state)
	if err != nil {
		return errResult(err.Error()), nil
	}
	carrier := enc.Carrier()
	dur, err := s.backend.TransmitSignal(ctx, sig, 1, carrier)
	if err != nil {
		return errResult("transmit AC state failed: " + err.Error()), nil
	}
	out := map[string]any{
		"transmitted":   true,
		"brand":         string(state.Brand),
		"power":         state.Power,
		"mode":          string(state.Mode),
		"temp_c":        state.TempC,
		"fan":           string(state.Fan),
		"swing":         state.Swing,
		"carrier_hz":    carrier.FreqHz,
		"pulse_count":   len(sig.RawData),
		"checksum_kind": enc.ChecksumKind(),
		"checksum_hex":  acChecksumHex(frameBytes),
		"duration_ms":   dur.Milliseconds(),
		"mode_hw":       s.backend.HardwareMode(),
	}
	if args.SaveAs != "" {
		savedName, saveErr := s.saveACButton(args.SaveAs, sig)
		if saveErr != nil {
			return errResult("save AC remote: " + saveErr.Error()), nil
		}
		out["saved_remote"] = savedName
	}
	return okJSON(out), nil
}

func buildACState(brand string, power bool, mode string, tempC int, fan string, swing bool) (irfile.ACState, error) {
	if strings.TrimSpace(fan) == "" {
		fan = string(irfile.ACFanAuto)
	}
	st := irfile.ACState{
		Brand: irfile.ACBrand(strings.TrimSpace(brand)),
		Power: power,
		Mode:  irfile.ACMode(strings.ToUpper(strings.TrimSpace(mode))),
		TempC: tempC,
		Fan:   irfile.ACFan(strings.ToUpper(strings.TrimSpace(fan))),
		Swing: swing,
	}
	if err := irfile.ValidateACState(st); err != nil {
		return st, err
	}
	return st, nil
}

func acChecksumHex(frame []byte) string {
	if len(frame) == 0 {
		return "00"
	}
	return fmt.Sprintf("0x%02X", frame[len(frame)-1])
}

func (s *Server) saveACButton(saveAs string, sig irfile.IRSignal) (string, error) {
	name := saveAs
	if !strings.HasSuffix(strings.ToLower(name), ".ir") {
		name += ".ir"
	}
	if _, err := ResolveSandboxedPath(s.store.UserDir, name); err != nil {
		return "", err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	signals := []irfile.IRSignal{sig}
	if existing, err := s.store.LoadUserRemote(name); err == nil {
		signals = upsertSignalByName(existing.Signals, sig)
	}
	saved, err := s.store.SaveUserRemote(name, &irfile.IRFile{
		Version: irfile.SupportedVersion,
		Signals: signals,
	})
	if err != nil {
		return "", err
	}
	return saved.RelPath, nil
}

func upsertSignalByName(existing []irfile.IRSignal, sig irfile.IRSignal) []irfile.IRSignal {
	out := make([]irfile.IRSignal, len(existing))
	copy(out, existing)
	for i := range out {
		if strings.EqualFold(out[i].Name, sig.Name) {
			out[i] = sig
			return out
		}
	}
	return append(out, sig)
}

func (s *Server) toolSweepCarrier(ctx context.Context, raw json.RawMessage) (toolResult, *rpcError) {
	var args struct {
		ConfirmIR     bool     `json:"confirm_ir"`
		RemoteName    string   `json:"remote_name"`
		ButtonName    string   `json:"button_name"`
		Pack          string   `json:"pack"`
		Pulses        []int32  `json:"pulses"`
		FrequenciesHz []uint32 `json:"frequencies_hz"`
		DutyPct       uint32   `json:"duty_pct"`
		GapMs         int      `json:"gap_ms"`
	}
	if rpcErr := decodeArgs(raw, &args); rpcErr != nil {
		return toolResult{}, rpcErr
	}
	if !args.ConfirmIR {
		return errResult(errConfirmIRRequired), nil
	}
	freqs, duty, gap, err := validateSweepArgs(args.FrequenciesHz, args.DutyPct, args.GapMs)
	if err != nil {
		return errResult(err.Error()), nil
	}
	sig, err := s.resolveSweepSignal(args.RemoteName, args.ButtonName, args.Pack, args.Pulses)
	if err != nil {
		return errResult(err.Error()), nil
	}
	report, err := s.backend.SweepCarrier(ctx, sig, freqs, duty, gap)
	if err != nil {
		return errResult("carrier sweep failed: " + err.Error()), nil
	}
	return okJSON(map[string]any{
		"signal_name":       report.SignalName,
		"steps":             report.Steps,
		"restored_freq_hz":  report.RestoredFreqHz,
		"restored_duty_pct": report.RestoredDutyPct,
		"cancelled":         report.Cancelled,
		"mode":              s.backend.HardwareMode(),
	}), nil
}

func validateSweepArgs(freqs []uint32, dutyPct uint32, gapMs int) ([]uint32, uint32, time.Duration, error) {
	if len(freqs) > maxSweepFrequencies {
		return nil, 0, 0, fmt.Errorf("frequencies_hz may contain at most %d entries", maxSweepFrequencies)
	}
	if len(freqs) == 0 {
		freqs = lirc.SweepCarrierPresets[:]
	}
	if dutyPct == 0 {
		dutyPct = lirc.DefaultDutyPct
	}
	if dutyPct < 25 || dutyPct > 50 {
		return nil, 0, 0, fmt.Errorf("duty_pct must be between 25 and 50")
	}
	for _, hz := range freqs {
		if hz < lirc.MinCarrierHz || hz > lirc.MaxCarrierHz {
			return nil, 0, 0, fmt.Errorf("frequency %d Hz out of range [%d..%d]", hz, lirc.MinCarrierHz, lirc.MaxCarrierHz)
		}
	}
	gap := lirc.DefaultSweepGap
	if gapMs != 0 {
		if gapMs < int(lirc.MinSweepGap.Milliseconds()) || gapMs > int(lirc.MaxSweepGap.Milliseconds()) {
			return nil, 0, 0, fmt.Errorf("gap_ms must be between %d and %d", lirc.MinSweepGap.Milliseconds(), lirc.MaxSweepGap.Milliseconds())
		}
		gap = time.Duration(gapMs) * time.Millisecond
	}
	return freqs, dutyPct, gap, nil
}

func (s *Server) resolveSweepSignal(remoteName, buttonName, pack string, pulses []int32) (irfile.IRSignal, error) {
	if len(pulses) > 0 {
		pt := irfile.PulseTrain(pulses)
		lircPulses, err := pt.ToLIRCPulses()
		if err != nil {
			return irfile.IRSignal{}, err
		}
		return irfile.IRSignal{
			Name:      "Raw_Sweep",
			Kind:      irfile.SignalRaw,
			Protocol:  "RAW",
			Frequency: irfile.DefaultFreqHz,
			DutyCycle: irfile.DefaultDutyCycle,
			RawData:   lircPulses,
		}, nil
	}
	if strings.TrimSpace(remoteName) == "" || strings.TrimSpace(buttonName) == "" {
		return irfile.IRSignal{}, fmt.Errorf("provide either pulses or remote_name + button_name")
	}
	return s.lookupButton(remoteName, buttonName, pack)
}
