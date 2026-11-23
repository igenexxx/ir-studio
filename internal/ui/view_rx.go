package ui

import (
	"fmt"
	"slices"

	"ir-studio/internal/config"
	"ir-studio/internal/fb"
	"ir-studio/internal/input"
	"ir-studio/internal/irfile"
	"ir-studio/internal/irml"
	"ir-studio/internal/lirc"
)

func (a *App) onRXCapture(cap lirc.RXCapture) {
	if !a.RX.Learning && !a.Remote.LearnPending {
		return
	}
	if a.Remote.LearnPending && a.Chrome.Tab == TabRemote {
		a.appendLearnedButton(cap)
		return
	}
	cp := cap
	cp.Pulses = slices.Clone(cap.Pulses)
	a.RX.LastCap = &cp
	a.RX.GuardPending = false
	a.RX.DenoiseKind = ""

	cleaned, clusters := irml.DenoisePulses(nil, cp.Pulses, irml.DefaultGlitchMaxUs)
	a.RX.Cleaned = cleaned
	a.RX.Clusters = clusters
	a.RX.Class = irml.ClassifySignal(a.activeRXPulses(), cp.CarrierEstHz)

	if a.RX.DenoiseOn && cp.Decoded == nil {
		if sig, ok := a.reg.Decode(cleaned); ok {
			cp.Decoded = &sig
			a.RX.DenoiseKind = "ML-CLEAN"
		} else if nnClean, _, nnOK := irml.DenoisePulsesNeural(make([]int32, 0, len(cp.Pulses)), cp.Pulses, cp.CarrierEstHz); nnOK {
			if sig, ok := a.reg.Decode(nnClean); ok {
				a.RX.Cleaned = nnClean
				cp.Decoded = &sig
				a.RX.DenoiseKind = "PN-CLEAN"
			}
		}
	}
	emb := irml.ExtractEmbedding(a.activeRXPulses(), cp.CarrierEstHz)
	a.RX.Matches = a.fpIndex.Match(emb, cp.CarrierEstHz, make([]irml.MatchResult, 0, 3))
	a.RX.MatchCursor = 0
	a.updateACDiff(a.activeRXPulses())
	if a.Chrome.Tab == TabRead {
		a.RX.Learning = false
	}
}

func (a *App) appendLearnedButton(cap lirc.RXCapture) {
	a.Remote.LearnPending = false
	if !a.Remote.Loaded || a.Remote.Entry.ReadOnly {
		return
	}
	spec := irfile.CarrierSpec{FreqHz: a.Set.Settings.CarrierHz, DutyPct: a.Set.Settings.DutyCyclePct}
	sig, err := a.reg.DecodeOrRaw(cap.Pulses, spec)
	if err != nil {
		a.SetToast("Learn err: "+truncate(err.Error(), 24), fb.ColRed)
		return
	}
	sig.Name = fmt.Sprintf("Btn_%d", len(a.Remote.Entry.Signals)+1)
	a.Remote.Entry.Signals = append(a.Remote.Entry.Signals, sig)
	a.Remote.Entry.ButtonCount = len(a.Remote.Entry.Signals)
	a.persistActiveRemote("Learned " + sig.Name)
}

func (a *App) activeRXPulses() irfile.PulseTrain {
	if a.RX.LastCap == nil {
		return nil
	}
	if a.RX.DenoiseOn && len(a.RX.Cleaned) > 0 {
		return a.RX.Cleaned
	}
	return a.RX.LastCap.Pulses
}

func (a *App) handleRXKey(act input.KeyAction) bool {
	if a.RX.GuardPending {
		if act == input.KeyEnter || act == input.KeySoft7 {
			a.RX.GuardPending = false
			a.saveRXCapture(true)
			return false
		}
		a.RX.GuardPending = false
		return false
	}
	switch act {
	case input.KeySoft4:
		a.RX.Learning = !a.RX.Learning
		a.SetToast(boolLabel(a.RX.Learning, "RX listening", "RX held"), fb.ColCyan)
	case input.KeySoft5:
		if !a.RX.Learning && a.RX.LastCap != nil {
			a.sweepActiveRX()
		} else {
			a.toggleRXDenoise()
		}
	case input.KeyR:
		a.sweepActiveRX()
	case input.KeySoft6:
		a.toggleACDiffMode()
	case input.KeySoft7:
		a.saveRXCapture(false)
	case input.KeySoft8:
		a.cycleRXCarrier()
	case input.KeyUp:
		if len(a.RX.Matches) > 0 {
			a.RX.MatchCursor = max(a.RX.MatchCursor-1, 0)
		}
	case input.KeyDown:
		if len(a.RX.Matches) > 0 {
			a.RX.MatchCursor = min(a.RX.MatchCursor+1, len(a.RX.Matches)-1)
		}
	case input.KeyEnter:
		a.promoteSelectedMatch()
	case input.KeyEsc:
		if a.cancelCarrierSweep() {
			return false
		}
		if a.RX.AC.Active {
			a.RX.AC.Active = false
			a.RX.AC.HasBase = false
			return false
		}
		a.goHome()
	}
	return false
}

func (a *App) sweepActiveRX() {
	pulses := a.activeRXPulses()
	if len(pulses) == 0 {
		a.SetToast("No signal to sweep", fb.ColAmber)
		return
	}
	spec := irfile.CarrierSpec{FreqHz: a.Set.Settings.CarrierHz, DutyPct: a.Set.Settings.DutyCyclePct}
	sig, err := a.reg.DecodeOrRaw(pulses, spec)
	if err != nil {
		a.SetToast("Sweep err: "+truncate(err.Error(), 24), fb.ColRed)
		return
	}
	if sig.Name == "" {
		sig.Name = "RX_Capture"
	}
	a.startCarrierSweep(sig)
}

func (a *App) toggleRXDenoise() {
	a.RX.DenoiseOn = !a.RX.DenoiseOn
	if a.RX.LastCap != nil {
		a.RX.Class = irml.ClassifySignal(a.activeRXPulses(), a.RX.LastCap.CarrierEstHz)
	}
	a.SetToast(boolLabel(a.RX.DenoiseOn, "1D K-Means ON", "1D K-Means OFF"), fb.ColMagenta)
}

func (a *App) toggleACDiffMode() {
	a.RX.AC.Active = !a.RX.AC.Active
	if a.RX.AC.Active {
		a.RX.AC.HasBase = false
		if pulses := a.activeRXPulses(); len(pulses) > 0 {
			a.updateACDiff(pulses)
		}
		a.SetToast("AC Diff: Baseline pinned", fb.ColMagenta)
	} else {
		a.SetToast("AC Diff closed", fb.ColMuted)
	}
}

func (a *App) cycleRXCarrier() {
	a.RX.CarrierIdx = (a.RX.CarrierIdx + 1) % len(config.AllowedCarrierHz)
	hz := config.AllowedCarrierHz[a.RX.CarrierIdx]
	a.Set.Settings.CarrierHz = hz
	a.SetToast(fmt.Sprintf("TX Carrier: %d kHz", hz/1000), fb.ColCyan)
}

func (a *App) saveRXCapture(force bool) {
	if a.Deps.Store == nil || a.RX.LastCap == nil {
		a.SetToast("No RX capture to save", fb.ColAmber)
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
	name := fmt.Sprintf("capture_%d", a.Chrome.TickCount+1)
	if sig.Protocol != "" && sig.Protocol != "RAW" {
		name = fmt.Sprintf("%s_%02X_%02X", sig.Protocol, sig.Address[0], sig.Command[0])
	}
	entry, err := a.Deps.Store.PromoteSignalToUser(sig, name)
	if err != nil {
		a.SetToast("Save err: "+truncate(err.Error(), 24), fb.ColRed)
		return
	}
	a.refreshSaved()
	a.SetToast("Saved "+truncate(entry.Name, 22), fb.ColEmerald)
}

func (a *App) renderRX(c *fb.Canvas) {
	a.drawRXSubHeader(c)
	if a.RX.AC.Active {
		a.drawACDiffView(c)
	} else {
		a.drawRXScope(c)
		a.drawRXDecodeAndMatches(c)
	}
	if a.RX.GuardPending {
		drawConfirmBanner(c, "ML: Noise! Save anyway? [Enter=YES]")
	}
	s4 := "LEARN"
	s5 := "CLEAN"
	if a.RX.Learning {
		s4 = "HOLD"
	} else if a.RX.LastCap != nil {
		s5 = "SWEEP"
	}
	a.drawNavBar(c, [5]string{s4, s5, "DIFF", "SAVE", "CARR"})
}

func (a *App) drawRXSubHeader(c *fb.Canvas) {
	carr := fmt.Sprintf("%dkHz %d%%", a.Set.Settings.CarrierHz/1000, a.Set.Settings.DutyCyclePct)
	x := drawSubHeaderPill(c, 6, carr, fb.ColCyan)
	if a.RX.DenoiseOn {
		x = drawSubHeaderPill(c, x, "ML-CLEAN", fb.ColMagenta)
	}
	clsTxt := a.RX.Class.Class.String()
	clsCol := fb.ColEmerald
	if a.RX.Class.IsNoise {
		clsCol = fb.ColRed
	} else if a.RX.Class.Class == irml.ClassACStateFrame {
		clsCol = fb.ColMagenta
	}
	if a.RX.LastCap != nil {
		drawSubHeaderPill(c, x, truncate(clsTxt, 14), clsCol)
	} else {
		drawSubHeaderPill(c, x, "WAITING IR", fb.ColMuted)
	}
}

func (a *App) drawRXScope(c *fb.Canvas) {
	c.FillRect(6, 34, 308, 42, fb.ColSurface)
	c.DrawRect(6, 34, 308, 42, fb.ColBorder)
	pulses := a.activeRXPulses()
	if len(pulses) == 0 {
		c.DrawTextCentered(fb.ScreenW/2, 51, "Aim IR remote at /dev/lirc0", fb.ColMuted, 1)
		return
	}
	maxDur := int32(1000)
	n := min(len(pulses), 60)
	for i := range n {
		v := pulses[i]
		if v < 0 {
			v = -v
		}
		if v > maxDur {
			maxDur = v
		}
	}
	barW := max(300/max(n, 1), 2)
	for i := range n {
		v := pulses[i]
		isMark := v > 0
		if v < 0 {
			v = -v
		}
		h := max(int((int64(v)*34)/int64(maxDur)), 2)
		col := fb.ColMuted
		if isMark {
			col = fb.ColCyan
			if a.RX.DenoiseOn {
				col = fb.ColMagenta
			}
		}
		bx := 10 + i*barW
		if bx+barW-1 < 312 {
			c.FillRect(bx, 74-h, max(barW-1, 1), h, col)
		}
	}
}

func (a *App) drawRXDecodeAndMatches(c *fb.Canvas) {
	c.FillRect(6, 79, 308, 22, fb.ColSurface)
	c.DrawRect(6, 79, 308, 22, fb.ColBorder)
	if a.RX.LastCap == nil {
		c.DrawText(10, 86, "No frame decoded yet", fb.ColMuted, 1)
		return
	}
	header, col := "RAW", fb.ColAmber
	if a.RX.LastCap.Decoded != nil {
		sig := a.RX.LastCap.Decoded
		header = fmt.Sprintf("%s A:%02X%02X C:%02X%02X", sig.Protocol, sig.Address[0], sig.Address[1], sig.Command[0], sig.Command[1])
		col = fb.ColEmerald
	}
	c.DrawText(10, 82, truncate(header, 32), col, 1)
	stats := fmt.Sprintf("S:%.0f L:%.0fus %dp", a.RX.Clusters.ShortUs, a.RX.Clusters.LongUs, len(a.activeRXPulses()))
	c.DrawText(10, 91, truncate(stats, 48), fb.ColMuted, 1)
	a.drawRXMatchList(c)
}

func boolLabel(v bool, t, f string) string {
	if v {
		return t
	}
	return f
}
