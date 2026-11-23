package ui

import (
	"fmt"
	"strings"

	"ir-studio/internal/fb"
	"ir-studio/internal/input"
	"ir-studio/internal/irfile"
)

func newACControlState() ACControlState {
	def := irfile.DefaultACState()
	return ACControlState{
		TempC: def.TempC,
		Power: def.Power,
		Swing: def.Swing,
		Reg:   irfile.DefaultACRegistry(),
	}
}

func (a *App) currentACState() irfile.ACState {
	reg := a.acRegistry()
	brands := reg.Brands()
	modes := irfile.ACModes()
	fans := irfile.ACFans()
	return irfile.ACState{
		Brand: brands[a.Remote.AC.BrandIdx%len(brands)],
		Power: a.Remote.AC.Power,
		Mode:  modes[a.Remote.AC.ModeIdx%len(modes)],
		TempC: min(max(a.Remote.AC.TempC, irfile.ACMinTempC), irfile.ACMaxTempC),
		Fan:   fans[a.Remote.AC.FanIdx%len(fans)],
		Swing: a.Remote.AC.Swing,
	}
}

func (a *App) acRegistry() *irfile.ACRegistry {
	if a.Remote.AC.Reg == nil {
		a.Remote.AC.Reg = irfile.DefaultACRegistry()
	}
	return a.Remote.AC.Reg
}

func (a *App) handleACKey(act input.KeyAction) bool {
	switch act {
	case input.KeyLeft:
		a.adjustACTemp(-1)
	case input.KeyRight:
		a.adjustACTemp(1)
	case input.KeyUp:
		a.cycleACMode(-1)
	case input.KeyDown:
		a.cycleACMode(1)
	case input.KeyR:
		a.cycleACBrand()
	case input.KeySoft4:
		a.Remote.AC.Power = !a.Remote.AC.Power
		a.SetToast(boolLabel(a.Remote.AC.Power, "AC Power ON", "AC Power OFF"), fb.ColCyan)
	case input.KeySoft5:
		a.cycleACFan()
	case input.KeySoft6:
		a.Remote.AC.Swing = !a.Remote.AC.Swing
		a.SetToast(boolLabel(a.Remote.AC.Swing, "AC Swing ON", "AC Swing OFF"), fb.ColCyan)
	case input.KeySoft7, input.KeyEnter:
		a.sendACState()
	case input.KeySoft8:
		a.saveACState()
	case input.KeyEsc, input.Key1:
		if a.Chrome.Tab == TabAC {
			a.goHome()
		} else {
			a.Remote.AC.Active = false
		}
	}
	return false
}

func (a *App) adjustACTemp(delta int) {
	next := min(max(a.Remote.AC.TempC+delta, irfile.ACMinTempC), irfile.ACMaxTempC)
	a.Remote.AC.TempC = next
}

func (a *App) cycleACMode(delta int) {
	modes := irfile.ACModes()
	n := len(modes)
	a.Remote.AC.ModeIdx = (a.Remote.AC.ModeIdx + delta + n) % n
}

func (a *App) cycleACBrand() {
	brands := a.acRegistry().Brands()
	a.Remote.AC.BrandIdx = (a.Remote.AC.BrandIdx + 1) % len(brands)
	a.SetToast("AC Brand: "+string(brands[a.Remote.AC.BrandIdx]), fb.ColCyan)
}

func (a *App) cycleACFan() {
	fans := irfile.ACFans()
	a.Remote.AC.FanIdx = (a.Remote.AC.FanIdx + 1) % len(fans)
	a.SetToast("AC Fan: "+string(fans[a.Remote.AC.FanIdx]), fb.ColCyan)
}

func (a *App) sendACState() {
	st := a.currentACState()
	sig, err := a.acRegistry().EncodeSignal(st)
	if err != nil {
		a.SetToast("AC err: "+truncate(err.Error(), 24), fb.ColRed)
		return
	}
	a.Remote.LastTXBtn = sig.Name
	a.Remote.TXActive = true
	if err := a.submitTX(sig, 1); err != nil {
		a.Remote.TXActive = false
		a.SetToast("TX err: "+truncate(err.Error(), 24), fb.ColRed)
		return
	}
	a.SetToast(fmt.Sprintf("AC TX %s %s", st.Brand, sig.Name), fb.ColEmerald)
}

func (a *App) saveACState() {
	if a.Deps.Store == nil {
		a.SetToast("Store unavailable", fb.ColRed)
		return
	}
	st := a.currentACState()
	sig, err := a.acRegistry().EncodeSignal(st)
	if err != nil {
		a.SetToast("AC err: "+truncate(err.Error(), 24), fb.ColRed)
		return
	}
	fileName := fmt.Sprintf("AC_%s.ir", strings.TrimSpace(string(st.Brand)))
	signals := []irfile.IRSignal{sig}
	if existing, loadErr := a.Deps.Store.LoadUserRemote(fileName); loadErr == nil {
		signals = upsertACSignal(existing.Signals, sig)
	}
	saved, err := a.Deps.Store.SaveUserRemote(fileName, &irfile.IRFile{
		Version: irfile.SupportedVersion,
		Signals: signals,
	})
	if err != nil {
		a.SetToast("Save err: "+truncate(err.Error(), 24), fb.ColRed)
		return
	}
	a.refreshSaved()
	a.SetToast(fmt.Sprintf("Saved %s:%s", truncate(saved.Name, 12), sig.Name), fb.ColEmerald)
}

func upsertACSignal(existing []irfile.IRSignal, sig irfile.IRSignal) []irfile.IRSignal {
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

func (a *App) renderAC(c *fb.Canvas) {
	st := a.currentACState()
	enc, _ := a.acRegistry().Lookup(st.Brand)
	sig, sigErr := a.acRegistry().EncodeSignal(st)

	a.drawACHeader(c, st, sigErr == nil)
	a.drawACCard(c, st)
	a.drawACFooter(c, enc, sig, sigErr)
	a.drawNavBar(c, [5]string{"PWR", "FAN", "SWING", "SEND", "SAVE"})
}

func (a *App) drawACHeader(c *fb.Canvas, st irfile.ACState, chkOK bool) {
	x := drawSubHeaderPill(c, 6, truncate(string(st.Brand), 11), fb.ColCyan)
	c.DrawText(x, SubPillY+2, "[R]Brd", fb.ColMuted, 1)
	pwrTxt, pwrCol := "OFF", fb.ColRed
	if st.Power {
		pwrTxt, pwrCol = "ON", fb.ColEmerald
	}
	x2 := drawSubHeaderPill(c, 214, pwrTxt, pwrCol)
	chkCol := fb.ColEmerald
	chkTxt := "CHK:OK"
	if !chkOK {
		chkCol = fb.ColRed
		chkTxt = "CHK:ER"
	}
	drawSubHeaderPill(c, x2, chkTxt, chkCol)
}

func (a *App) drawACCard(c *fb.Canvas, st irfile.ACState) {
	c.FillRect(6, 34, 308, 76, fb.ColSurface)
	c.DrawRect(6, 34, 308, 76, fb.ColBorder)

	tempCol := fb.ColCyan
	if !st.Power {
		tempCol = fb.ColMuted
	}
	c.DrawText(22, 54, fmt.Sprintf("%2dC", st.TempC), tempCol, 3)

	modeCol := acModeColor(st.Mode)
	c.FillRect(140, 44, 72, 18, fb.ColSurfaceAlt)
	c.DrawRect(140, 44, 72, 18, modeCol)
	c.DrawTextCentered(176, 49, string(st.Mode), modeCol, 1)

	fanTxt := "FAN:" + string(st.Fan)
	c.FillRect(140, 68, 76, 16, fb.ColSurfaceAlt)
	c.DrawRect(140, 68, 76, 16, fb.ColBorder)
	c.DrawText(146, 72, fanTxt, fb.ColFg, 1)

	swgTxt := "SWG:OFF"
	swgCol := fb.ColMuted
	if st.Swing {
		swgTxt = "SWG:ON"
		swgCol = fb.ColEmerald
	}
	c.FillRect(224, 68, 76, 16, fb.ColSurfaceAlt)
	c.DrawRect(224, 68, 76, 16, swgCol)
	c.DrawText(230, 72, swgTxt, swgCol, 1)

	c.DrawText(140, 92, "Btn: "+irfile.ACSignalName(st), fb.ColMuted, 1)
}

func (a *App) drawACFooter(c *fb.Canvas, enc irfile.ACEncoder, sig irfile.IRSignal, err error) {
	c.FillRect(6, 113, 308, 26, fb.ColSurface)
	c.DrawRect(6, 113, 308, 26, fb.ColBorder)
	c.DrawText(10, 116, "Z/C:Temp  F/X:Mode  R:Brand  Esc:Back", fb.ColMuted, 1)
	if err != nil {
		c.DrawText(10, 127, truncate(err.Error(), 48), fb.ColRed, 1)
		return
	}
	chk := "SUM8"
	if enc != nil {
		chk = enc.ChecksumKind()
	}
	spec := sig.CarrierSpec()
	meta := fmt.Sprintf("%dkHz %d%% | %dp | %s | 8 SAVE -> AC_%s.ir",
		spec.FreqHz/1000, spec.DutyPct, len(sig.RawData), chk, truncate(string(a.currentACState().Brand), 8))
	c.DrawText(10, 127, truncate(meta, 49), fb.ColCyan, 1)
}

func acModeColor(m irfile.ACMode) uint16 {
	switch m {
	case irfile.ACModeCool:
		return fb.ColCyan
	case irfile.ACModeHeat:
		return fb.ColAmber
	case irfile.ACModeDry:
		return fb.ColMagenta
	case irfile.ACModeFan:
		return fb.ColEmerald
	default:
		return fb.ColFg
	}
}
