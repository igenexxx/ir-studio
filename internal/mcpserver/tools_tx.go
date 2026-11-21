package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"ir-studio/internal/config"
	"ir-studio/internal/irfile"
	"ir-studio/internal/library"
)

const errConfirmIRRequired = "confirm_ir: true is required to transmit IR signals"

func (s *Server) toolTransmitButton(ctx context.Context, raw json.RawMessage) (toolResult, *rpcError) {
	var args struct {
		ConfirmIR  bool   `json:"confirm_ir"`
		RemoteName string `json:"remote_name"`
		ButtonName string `json:"button_name"`
		Pack       string `json:"pack"`
		Repeat     int    `json:"repeat"`
		CarrierHz  uint32 `json:"carrier_hz"`
		DutyPct    uint32 `json:"duty_pct"`
	}
	if rpcErr := decodeArgs(raw, &args); rpcErr != nil {
		return toolResult{}, rpcErr
	}
	if !args.ConfirmIR {
		return errResult(errConfirmIRRequired), nil
	}
	repeat, err := normalizeRepeat(args.Repeat)
	if err != nil {
		return errResult(err.Error()), nil
	}
	carrier, err := s.resolveTXCarrier(args.CarrierHz, args.DutyPct)
	if err != nil {
		return errResult(err.Error()), nil
	}
	sig, err := s.lookupButton(args.RemoteName, args.ButtonName, args.Pack)
	if err != nil {
		return errResult(err.Error()), nil
	}
	if args.CarrierHz == 0 && sig.Frequency >= 36000 && sig.Frequency <= 56000 {
		carrier.FreqHz = sig.Frequency
	}

	dur, err := s.backend.TransmitSignal(ctx, sig, repeat, carrier)
	if err != nil {
		return errResult("transmit failed: " + err.Error()), nil
	}
	proto := sig.Protocol
	if proto == "" {
		proto = string(sig.Kind)
	}
	return okJSON(map[string]any{
		"transmitted": true,
		"remote_name": args.RemoteName,
		"button_name": sig.Name,
		"protocol":    proto,
		"carrier_hz":  carrier.FreqHz,
		"duty_pct":    carrier.DutyPct,
		"duration_ms": dur.Milliseconds(),
		"mode":        s.backend.HardwareMode(),
	}), nil
}

func normalizeRepeat(repeat int) (int, error) {
	if repeat == 0 {
		return 1, nil
	}
	if repeat < 1 || repeat > maxTransmitRepeat {
		return 0, fmt.Errorf("repeat must be between 1 and %d", maxTransmitRepeat)
	}
	return repeat, nil
}

func (s *Server) resolveTXCarrier(carrierHz, dutyPct uint32) (irfile.CarrierSpec, error) {
	spec := s.backend.CarrierSpec()
	if carrierHz != 0 {
		if carrierHz < 36000 || carrierHz > 56000 {
			return spec, fmt.Errorf("carrier_hz must be between 36000 and 56000")
		}
		spec.FreqHz = carrierHz
	}
	if dutyPct != 0 {
		if dutyPct < 25 || dutyPct > 50 {
			return spec, fmt.Errorf("duty_pct must be between 25 and 50")
		}
		spec.DutyPct = dutyPct
	}
	return spec, nil
}

type resolvedMacroStep struct {
	step   config.MacroStep
	signal irfile.IRSignal
	repeat int
}

func (s *Server) toolRunMacro(ctx context.Context, raw json.RawMessage) (toolResult, *rpcError) {
	var args struct {
		ConfirmIR bool   `json:"confirm_ir"`
		MacroName string `json:"macro_name"`
	}
	if rpcErr := decodeArgs(raw, &args); rpcErr != nil {
		return toolResult{}, rpcErr
	}
	if !args.ConfirmIR {
		return errResult(errConfirmIRRequired), nil
	}
	macro, err := s.findMacro(args.MacroName)
	if err != nil {
		return errResult(err.Error()), nil
	}
	steps, err := s.prevalidateMacroSteps(macro)
	if err != nil {
		return errResult(err.Error()), nil
	}
	return s.executeMacroSteps(ctx, macro.Name, steps), nil
}

func (s *Server) findMacro(name string) (config.Macro, error) {
	if strings.TrimSpace(name) == "" {
		return config.Macro{}, fmt.Errorf("macro_name is required")
	}
	mf, err := config.LoadMacros(s.macrosPath)
	if err != nil {
		return config.Macro{}, fmt.Errorf("load macros: %w", err)
	}
	for _, m := range mf.Macros {
		if m.Name == name || m.ID == name || strings.EqualFold(m.Name, name) {
			return m, nil
		}
	}
	return config.Macro{}, fmt.Errorf("macro %q not found", name)
}

func (s *Server) prevalidateMacroSteps(m config.Macro) ([]resolvedMacroStep, error) {
	if len(m.Steps) == 0 {
		return nil, fmt.Errorf("macro %q has no steps", m.Name)
	}
	resolved := make([]resolvedMacroStep, 0, len(m.Steps))
	for i, step := range m.Steps {
		rep, err := normalizeRepeat(step.Repeat)
		if err != nil {
			return nil, fmt.Errorf("step %d: %w", i+1, err)
		}
		sig, err := s.lookupButton(step.Remote, step.Button, "")
		if err != nil {
			return nil, fmt.Errorf("step %d (%s/%s): %w", i+1, step.Remote, step.Button, err)
		}
		resolved = append(resolved, resolvedMacroStep{step: step, signal: sig, repeat: rep})
	}
	return resolved, nil
}

func (s *Server) executeMacroSteps(ctx context.Context, macroName string, steps []resolvedMacroStep) toolResult {
	runCtx, cancel := context.WithTimeout(ctx, maxMacroDuration)
	defer cancel()

	start := time.Now()
	carrier := s.backend.CarrierSpec()
	results := make([]map[string]any, 0, len(steps))

	for i, st := range steps {
		dur, err := s.backend.TransmitSignal(runCtx, st.signal, st.repeat, carrier)
		if err != nil {
			return errResult(fmt.Sprintf("macro %q failed at step %d: %v", macroName, i+1, err))
		}
		results = append(results, map[string]any{
			"step":        i + 1,
			"remote":      st.step.Remote,
			"button":      st.signal.Name,
			"repeat":      st.repeat,
			"duration_ms": dur.Milliseconds(),
		})
		if st.step.DelayAfterMs > 0 && i+1 < len(steps) {
			select {
			case <-runCtx.Done():
				return errResult(fmt.Sprintf("macro %q cancelled during delay after step %d", macroName, i+1))
			case <-time.After(time.Duration(st.step.DelayAfterMs) * time.Millisecond):
			}
		}
	}

	return okJSON(map[string]any{
		"macro_name":     macroName,
		"steps_executed": len(results),
		"steps_total":    len(steps),
		"elapsed_ms":     time.Since(start).Milliseconds(),
		"step_results":   results,
	})
}

func (s *Server) toolListMacros(_ context.Context, _ json.RawMessage) (toolResult, *rpcError) {
	if s.macrosPath == "" {
		return okJSON(map[string]any{"macros": []any{}}), nil
	}
	mf, err := config.LoadMacros(s.macrosPath)
	if err != nil {
		return errResult("load macros: " + err.Error()), nil
	}
	items := make([]map[string]any, 0, len(mf.Macros))
	for _, m := range mf.Macros {
		totalDelay := 0
		for _, st := range m.Steps {
			totalDelay += st.DelayAfterMs
		}
		items = append(items, map[string]any{
			"id":             m.ID,
			"name":           m.Name,
			"description":    m.Description,
			"step_count":     len(m.Steps),
			"total_delay_ms": totalDelay,
			"steps":          m.Steps,
		})
	}
	return okJSON(map[string]any{"macros": items}), nil
}

func (s *Server) lookupButton(remoteName, buttonName, pack string) (irfile.IRSignal, error) {
	if strings.TrimSpace(buttonName) == "" {
		return irfile.IRSignal{}, fmt.Errorf("button_name is required")
	}
	entry, err := s.loadRemoteEntry(remoteName, pack)
	if err != nil {
		return irfile.IRSignal{}, err
	}
	for _, sig := range entry.Signals {
		if sig.Name == buttonName {
			return sig, nil
		}
	}
	for _, sig := range entry.Signals {
		if strings.EqualFold(sig.Name, buttonName) {
			return sig, nil
		}
	}
	return irfile.IRSignal{}, fmt.Errorf("button %q not found in remote %q", buttonName, remoteName)
}

func (s *Server) loadRemoteEntry(name, pack string) (library.RemoteEntry, error) {
	if pack == "" {
		if err := ValidateSafeName(name); err != nil {
			return library.RemoteEntry{}, err
		}
		if _, err := ResolveSandboxedPath(s.store.UserDir, name); err != nil {
			return library.RemoteEntry{}, err
		}
		return s.store.LoadUserRemote(name)
	}
	if library.IsVirtualPack(pack) {
		if err := ValidateSafeName(name); err != nil {
			return library.RemoteEntry{}, err
		}
		remotes, err := s.store.ListPackRemotes(pack)
		if err != nil {
			return library.RemoteEntry{}, err
		}
		for _, r := range remotes {
			if r.RelPath == name || r.Name == name {
				return s.store.LoadRemote(r)
			}
		}
		return library.RemoteEntry{}, fmt.Errorf("remote %q not found in pack %q", name, pack)
	}
	if err := ValidateSafeName(pack); err != nil {
		return library.RemoteEntry{}, err
	}
	if err := ValidateSafeRelPath(name); err != nil {
		return library.RemoteEntry{}, err
	}
	packDir, err := ResolveSandboxedPath(s.store.DatabasesDir, pack)
	if err != nil {
		return library.RemoteEntry{}, err
	}
	if _, err := ResolveSandboxedPath(packDir, name); err != nil {
		return library.RemoteEntry{}, err
	}
	remotes, err := s.store.ListPackRemotes(pack)
	if err != nil {
		return library.RemoteEntry{}, err
	}
	for _, r := range remotes {
		if r.RelPath == name || r.Name == name {
			return s.store.LoadRemote(r)
		}
	}
	return library.RemoteEntry{}, fmt.Errorf("remote %q not found in pack %q", name, pack)
}
