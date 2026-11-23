package ui

import (
	"fmt"

	"ir-studio/internal/fb"
	"ir-studio/internal/input"
	"ir-studio/internal/irfile"
	"ir-studio/internal/irml"
)

func (a *App) enterReadAndReplay() {
	a.flushPendingRX()
	a.RX.LastCap = nil
	a.RX.Cleaned = nil
	a.RX.Matches = nil
	a.RX.DenoiseKind = ""
	a.RX.GuardPending = false
	a.RX.DenoiseOn = true
	a.RX.Learning = true
	a.SwitchTab(TabRead)
}

func (a *App) handleReadKey(act input.KeyAction) bool {
	if a.RX.GuardPending {
		if act == input.KeyEnter || act == input.KeySoft5 {
			a.RX.GuardPending = false
			a.saveReadAndOpenRemote(true)
			return false
		}
		a.RX.GuardPending = false
		return false
	}
	if a.RX.LastCap == nil {
		return a.handleReadListeningKey(act)
	}
	return a.handleReadCapturedKey(act)
}

func (a *App) handleReadListeningKey(act input.KeyAction) bool {
	switch act {
	case input.KeySoft7:
		if a.Set.Settings.ExpertMode {
			a.SwitchTab(TabRX)
		}
	case input.KeyEsc, input.KeySoft8:
		a.goHome()
	}
	return false
}

func (a *App) hasTopDBMatch() bool {
	return len(a.RX.Matches) > 0 && a.RX.Matches[0].Similarity >= 0.80
}

func (a *App) handleReadCapturedKey(act input.KeyAction) bool {
	switch act {
	case input.KeyEnter, input.KeySoft4:
		a.replayCapturedRX()
	case input.KeySoft5:
		a.saveReadAndOpenRemote(false)
	case input.KeySoft6, input.KeySpace:
		a.enterReadAndReplay()
		a.SetToast("Ready - press next button", fb.ColCyan)
	case input.KeySoft7:
		if a.hasTopDBMatch() {
			a.promoteReadMatchAndOpen()
		} else {
			a.sweepActiveRX()
		}
	case input.Key1:
		if a.hasTopDBMatch() {
			a.promoteReadMatchAndOpen()
		}
	case input.KeyR:
		a.sweepActiveRX()
	case input.KeyEsc, input.KeySoft8:
		if a.cancelCarrierSweep() {
			return false
		}
		a.goHome()
	}
	return false
}

func (a *App) promoteReadMatchAndOpen() {
	if a.Deps.Store == nil || !a.hasTopDBMatch() {
		return
	}
	m := a.RX.Matches[0]
	entries, err := a.Deps.Store.ListPackRemotes(m.Pack)
	if err != nil {
		a.SetToast("DB err: "+truncate(err.Error(), 24), fb.ColRed)
		return
	}
	for _, e := range entries {
		if e.RelPath == m.RemoteID {
			promoted, pErr := a.Deps.Store.PromoteToUser(e, "")
			if pErr != nil {
				a.SetToast("Copy err: "+truncate(pErr.Error(), 22), fb.ColRed)
				return
			}
			a.refreshSaved()
			a.openRemoteEntry(promoted)
			a.SwitchTab(TabRemote)
			a.SetToast("Added "+truncate(promoted.Name, 22), fb.ColEmerald)
			return
		}
	}
	a.SetToast("DB remote not found", fb.ColAmber)
}

func (a *App) replayCapturedRX() {
	pulses := a.activeRXPulses()
	if len(pulses) == 0 {
		a.SetToast("No signal captured yet", fb.ColAmber)
		return
	}
	spec := irfile.CarrierSpec{FreqHz: a.Set.Settings.CarrierHz, DutyPct: a.Set.Settings.DutyCyclePct}
	sig, err := a.reg.DecodeOrRaw(pulses, spec)
	if err != nil {
		a.SetToast("Replay err: "+truncate(err.Error(), 22), fb.ColRed)
		return
	}
	if sig.Name == "" {
		sig.Name = "Captured"
	}
	if err := a.submitTX(sig, 1); err != nil {
		a.SetToast("TX err: "+truncate(err.Error(), 24), fb.ColRed)
		return
	}
	a.SetToast("Sent! Did device react?", fb.ColEmerald)
}

func (a *App) saveReadAndOpenRemote(force bool) {
	if a.Deps.Store == nil || a.RX.LastCap == nil {
		a.SetToast("Nothing to save yet", fb.ColAmber)
		return
	}
	if !force && (a.RX.Class.SaveGuardWarn || a.RX.Class.IsNoise) {
		a.RX.GuardPending = true
		return
	}
	spec := irfile.CarrierSpec{FreqHz: a.Set.Settings.CarrierHz, DutyPct: a.Set.Settings.DutyCyclePct}
	sig, err := a.reg.DecodeOrRaw(a.activeRXPulses(), spec)
	if err != nil {
		a.SetToast("Save err: "+truncate(err.Error(), 24), fb.ColRed)
		return
	}
	sig.Name = "Power"
	name := fmt.Sprintf("Remote_%d", a.Chrome.TickCount+1)
	if sig.Protocol != "" && sig.Protocol != "RAW" {
		name = fmt.Sprintf("%s_%02X_%02X", sig.Protocol, sig.Address[0], sig.Command[0])
	} else if a.RX.Class.ProtocolLabel != "" && !a.RX.Class.IsNoise {
		name = fmt.Sprintf("%s_%d", a.RX.Class.ProtocolLabel, a.Chrome.TickCount+1)
	}
	entry, err := a.Deps.Store.PromoteSignalToUser(sig, name)
	if err != nil {
		a.SetToast("Save err: "+truncate(err.Error(), 24), fb.ColRed)
		return
	}
	a.refreshSaved()
	a.openRemoteEntry(entry)
	a.SwitchTab(TabRemote)
	a.SetToast("Saved! 5=LEARN+ to add more", fb.ColEmerald)
}

func (a *App) renderRead(c *fb.Canvas) {
	if a.RX.LastCap == nil {
		a.renderReadListening(c)
		return
	}
	a.renderReadCaptured(c)
}

func (a *App) renderReadListening(c *fb.Canvas) {
	c.FillRect(6, 24, 308, 112, fb.ColSurface)
	c.DrawRect(6, 24, 308, 112, fb.ColCyan)

	c.DrawTextCentered(fb.ScreenW/2, 38, "STEP 1: AIM REMOTE & PRESS", fb.ColCyan, 1)
	c.DrawTextCentered(fb.ScreenW/2, 56, "Point IR remote 5-20 cm at sensor", fb.ColFg, 1)
	c.DrawTextCentered(fb.ScreenW/2, 70, "and press any button once.", fb.ColMuted, 1)

	statusTxt, statusCol := "Listening on /dev/lirc (38 kHz)...", fb.ColEmerald
	if a.Chrome.SimMode {
		statusTxt, statusCol = "Simulator mode (no hardware IR)", fb.ColAmber
	}
	c.DrawTextCentered(fb.ScreenW/2, 96, statusTxt, statusCol, 1)

	if a.Chrome.Ambient.Level != irml.AmbientClean {
		c.DrawTextCentered(fb.ScreenW/2, 114, "Warning: Bright light / LED flicker!", fb.ColAmber, 1)
	}

	s7 := ""
	if a.Set.Settings.ExpertMode {
		s7 = "LAB"
	}
	a.drawNavBar(c, [5]string{"", "", "", s7, "MENU"})
}

func (a *App) renderReadCaptured(c *fb.Canvas) {
	borderCol := fb.ColEmerald
	titleTxt := "STEP 2: SIGNAL CAPTURED!"
	if a.RX.Class.IsNoise {
		borderCol = fb.ColAmber
		titleTxt = "CAPTURED (LOOKS LIKE NOISE)"
	}

	c.FillRect(6, 24, 308, 112, fb.ColSurface)
	c.DrawRect(6, 24, 308, 112, borderCol)

	c.DrawText(14, 32, titleTxt, borderCol, 1)
	if a.RX.DenoiseKind != "" {
		c.DrawText(246, 32, a.RX.DenoiseKind, fb.ColMagenta, 1)
	}

	protoLine, detailLine := a.formatReadSummary()
	c.DrawText(14, 52, truncate(protoLine, 46), fb.ColCyan, 1)
	c.DrawText(14, 66, truncate(detailLine, 48), fb.ColFg, 1)

	if a.hasTopDBMatch() {
		m := a.RX.Matches[0]
		hint := fmt.Sprintf("DB Match: %s (%.0f%%) [7]Add", truncate(m.RemoteID, 19), m.Similarity*100)
		c.DrawText(14, 84, truncate(hint, 48), fb.ColMagenta, 1)
	} else if a.fpIndexing {
		c.DrawText(14, 84, "Searching DB packs in background...", fb.ColCyan, 1)
	} else {
		c.DrawText(14, 84, "Tip: Press 7 SWEEP if device ignores 38kHz", fb.ColMuted, 1)
	}

	c.HLine(12, 102, 296, fb.ColBorder)
	if a.hasTopDBMatch() {
		c.DrawTextCentered(fb.ScreenW/2, 112, "[4] TEST  [5] SAVE 1  [7] ADD FULL DB", fb.ColEmerald, 1)
	} else {
		c.DrawTextCentered(fb.ScreenW/2, 112, "[Enter/4] TEST SEND   [5] SAVE & OPEN", fb.ColEmerald, 1)
	}

	if a.RX.GuardPending {
		drawConfirmBanner(c, "Looks like noise! Save anyway? [Enter=YES]")
	}
	s7 := "SWEEP"
	if a.hasTopDBMatch() {
		s7 = "ADDDB"
	}
	a.drawNavBar(c, [5]string{"SEND", "SAVE", "AGAIN", s7, "MENU"})
}

func (a *App) formatReadSummary() (string, string) {
	cls := a.RX.Class.Class.String()
	if a.RX.LastCap != nil && a.RX.LastCap.Decoded != nil {
		sig := a.RX.LastCap.Decoded
		proto := fmt.Sprintf("Protocol: %s  (%s)", sig.Protocol, cls)
		detail := fmt.Sprintf("Code: Addr %02X %02X  Cmd %02X %02X", sig.Address[0], sig.Address[1], sig.Command[0], sig.Command[1])
		return proto, detail
	}
	label := "RAW IR Signal"
	if a.RX.Class.ProtocolLabel != "" {
		label = fmt.Sprintf("Detected: %s (%.0f%%)", a.RX.Class.ProtocolLabel, a.RX.Class.ProtocolConfidence*100)
	}
	detail := fmt.Sprintf("Category: %s | %d pulses", cls, len(a.activeRXPulses()))
	return label, detail
}
