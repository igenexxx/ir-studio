package ui

import (
	"fmt"

	"ir-studio/internal/fb"
	"ir-studio/internal/input"
	"ir-studio/internal/irfile"
	"ir-studio/internal/library"
)

const (
	remoteCols        = 2
	remoteVisibleRows = 4
	remoteTileW       = 150
	remoteTileH       = 21
)

func (a *App) loadActiveRemote() {
	if a.Deps.Store == nil {
		return
	}
	if len(a.Saved.UserEntries) > 0 {
		a.openRemoteEntry(a.Saved.UserEntries[0])
		return
	}
	if len(a.Saved.DBEntries) > 0 {
		a.openRemoteEntry(a.Saved.DBEntries[0])
	}
}

func (a *App) openRemoteEntry(entry library.RemoteEntry) {
	if a.Deps.Store == nil {
		return
	}
	loaded, err := a.Deps.Store.LoadRemote(entry)
	if err != nil {
		a.SetToast("Load err: "+truncate(err.Error(), 24), fb.ColRed)
		return
	}
	loaded.Signals = sortRemoteButtons(loaded.Signals)
	a.Remote.Entry = loaded
	a.Remote.Loaded = true
	a.Remote.Cursor = 0
	a.Remote.Scroll = 0
	a.Remote.ConfirmDel = false
	a.Remote.LearnPending = false
}

func (a *App) handleRemoteKey(act input.KeyAction) bool {
	if a.Remote.AC.Active {
		return a.handleACKey(act)
	}
	if a.Remote.ConfirmDel {
		return a.handleRemoteConfirmDel(act)
	}
	switch act {
	case input.KeyUp:
		a.moveRemoteCursor(-remoteCols)
	case input.KeyDown:
		a.moveRemoteCursor(remoteCols)
	case input.KeyLeft:
		a.moveRemoteCursor(-1)
	case input.KeyRight:
		a.moveRemoteCursor(1)
	case input.KeyEnter, input.KeySoft4:
		a.fireRemoteButton(1)
	case input.KeySpace:
		a.fireRemoteButton(3)
	case input.KeySoft5:
		if a.Set.Settings.ExpertMode {
			a.sweepRemoteButton()
		} else {
			a.startRemoteLearn()
		}
	case input.KeySoft6:
		if a.Set.Settings.ExpertMode {
			a.startRemoteLearn()
		} else {
			a.renameRemoteButton()
		}
	case input.KeyR:
		a.renameRemoteButton()
	case input.Key1:
		a.Remote.AC.Active = true
	case input.KeySoft7, input.KeyDel:
		a.requestRemoteDel()
	case input.KeySoft8:
		a.handleRemoteSoft8()
	case input.KeyEsc:
		if a.cancelCarrierSweep() {
			return false
		}
		if a.Remote.LearnPending {
			a.Remote.LearnPending = false
			a.SetToast("Learn cancelled", fb.ColMuted)
			return false
		}
		if a.Remote.TXActive && a.Deps.TX != nil {
			a.Deps.TX.CancelCurrent()
			a.Remote.TXActive = false
			return false
		}
		a.SwitchTab(TabSaved)
	}
	return false
}

func (a *App) sweepRemoteButton() {
	if !a.Remote.Loaded || a.Remote.Cursor >= len(a.Remote.Entry.Signals) {
		a.SetToast("No signal to sweep", fb.ColAmber)
		return
	}
	a.startCarrierSweep(a.Remote.Entry.Signals[a.Remote.Cursor])
}

func (a *App) handleRemoteConfirmDel(act input.KeyAction) bool {
	if act == input.KeyEnter || act == input.KeySoft7 {
		a.deleteRemoteButton()
	}
	a.Remote.ConfirmDel = false
	return false
}

func (a *App) moveRemoteCursor(delta int) {
	n := len(a.Remote.Entry.Signals)
	if n == 0 {
		a.Remote.Cursor = 0
		return
	}
	next := min(max(a.Remote.Cursor+delta, 0), n-1)
	a.Remote.Cursor = next
	row := next / remoteCols
	if row < a.Remote.Scroll {
		a.Remote.Scroll = row
	}
	if row >= a.Remote.Scroll+remoteVisibleRows {
		a.Remote.Scroll = row - remoteVisibleRows + 1
	}
}

func (a *App) fireRemoteButton(repeat int) {
	if !a.Remote.Loaded || a.Remote.Cursor >= len(a.Remote.Entry.Signals) {
		a.SetToast("No remote button selected", fb.ColAmber)
		return
	}
	sig := a.Remote.Entry.Signals[a.Remote.Cursor]
	a.Remote.LastTXBtn = sig.Name
	a.Remote.TXActive = true
	if err := a.submitTX(sig, repeat); err != nil {
		a.Remote.TXActive = false
		a.SetToast("TX err: "+truncate(err.Error(), 24), fb.ColRed)
		return
	}
	a.SetToast(fmt.Sprintf("TX %s (x%d)", truncate(sig.Name, 14), repeat), fb.ColEmerald)
}

func (a *App) startRemoteLearn() {
	if !a.Remote.Loaded {
		a.SetToast("Open a USER remote first", fb.ColAmber)
		return
	}
	if a.Remote.Entry.ReadOnly {
		a.SetToast("Read-only DB - press 8 PROMO", fb.ColAmber)
		return
	}
	a.Remote.LearnPending = !a.Remote.LearnPending
	if a.Remote.LearnPending {
		a.SetToast("Aim remote & press button...", fb.ColCyan)
	} else {
		a.SetToast("Learn cancelled", fb.ColMuted)
	}
}

func (a *App) renameRemoteButton() {
	if !a.Remote.Loaded || a.Remote.Cursor >= len(a.Remote.Entry.Signals) {
		return
	}
	if a.Remote.Entry.ReadOnly {
		a.SetToast("Read-only DB - press 8 PROMO", fb.ColAmber)
		return
	}
	sig := &a.Remote.Entry.Signals[a.Remote.Cursor]
	sig.Name = fmt.Sprintf("Btn_%d", a.Remote.Cursor+1)
	a.persistActiveRemote("Renamed to " + sig.Name)
}

func (a *App) requestRemoteDel() {
	if !a.Remote.Loaded || a.Remote.Cursor >= len(a.Remote.Entry.Signals) {
		return
	}
	if a.Remote.Entry.ReadOnly {
		a.SetToast("Read-only DB - press 8 PROMO", fb.ColAmber)
		return
	}
	a.Remote.ConfirmDel = true
}

func (a *App) deleteRemoteButton() {
	n := len(a.Remote.Entry.Signals)
	if n == 0 || a.Remote.Cursor >= n || a.Remote.Entry.ReadOnly {
		return
	}
	name := a.Remote.Entry.Signals[a.Remote.Cursor].Name
	a.Remote.Entry.Signals = append(a.Remote.Entry.Signals[:a.Remote.Cursor], a.Remote.Entry.Signals[a.Remote.Cursor+1:]...)
	a.Remote.Entry.ButtonCount = len(a.Remote.Entry.Signals)
	if a.Remote.Cursor >= len(a.Remote.Entry.Signals) && a.Remote.Cursor > 0 {
		a.Remote.Cursor--
	}
	a.persistActiveRemote("Deleted " + name)
}

func (a *App) handleRemoteSoft8() {
	if a.Remote.Loaded && a.Remote.Entry.ReadOnly && a.Deps.Store != nil {
		promoted, err := a.Deps.Store.PromoteToUser(a.Remote.Entry, "")
		if err != nil {
			a.SetToast("Promote err: "+truncate(err.Error(), 22), fb.ColRed)
			return
		}
		a.refreshSaved()
		a.openRemoteEntry(promoted)
		a.SetToast("Promoted to USER: "+truncate(promoted.Name, 16), fb.ColEmerald)
		return
	}
	a.SwitchTab(TabSaved)
}

func (a *App) persistActiveRemote(okMsg string) {
	if a.Deps.Store == nil || a.Remote.Entry.ReadOnly {
		return
	}
	f := &irfile.IRFile{Version: irfile.SupportedVersion, Signals: a.Remote.Entry.Signals}
	saved, err := a.Deps.Store.SaveUserRemote(a.Remote.Entry.RelPath, f)
	if err != nil {
		a.SetToast("Save err: "+truncate(err.Error(), 24), fb.ColRed)
		return
	}
	a.Remote.Entry = saved
	a.refreshSaved()
	a.SetToast(okMsg, fb.ColEmerald)
}

func (a *App) renderRemote(c *fb.Canvas) {
	if a.Remote.AC.Active {
		a.renderAC(c)
		return
	}
	a.drawRemoteHeader(c)
	if !a.Remote.Loaded || len(a.Remote.Entry.Signals) == 0 {
		c.DrawTextCentered(fb.ScreenW/2, 74, "No remote loaded (open in My Remotes)", fb.ColMuted, 1)
	} else {
		a.drawRemoteGrid(c)
		a.drawRemoteFooter(c)
	}
	if a.Remote.ConfirmDel {
		drawConfirmBanner(c, "Delete button? [Enter=YES Esc=NO]")
	} else if a.Remote.LearnPending {
		drawConfirmBanner(c, "AIM REMOTE & PRESS BUTTON TO ADD...")
	}
	s8 := "BACK"
	if a.Remote.Loaded && a.Remote.Entry.ReadOnly {
		s8 = "PROMO"
	}
	if a.Set.Settings.ExpertMode {
		a.drawNavBar(c, [5]string{"SEND", "SWEEP", "ADD +", "DEL", s8})
		return
	}
	a.drawNavBar(c, [5]string{"SEND", "ADD +", "NAME", "DEL", s8})
}

func (a *App) drawRemoteHeader(c *fb.Canvas) {
	title := "No Remote"
	if a.Remote.Loaded {
		title = fmt.Sprintf("%s (%d)", truncate(a.Remote.Entry.Name, 22), len(a.Remote.Entry.Signals))
	}
	x := drawSubHeaderPill(c, 6, title, fb.ColCyan)
	if a.Remote.Loaded {
		if a.Remote.Entry.ReadOnly {
			drawSubHeaderPill(c, x, "DB:RO", fb.ColAmber)
		} else {
			drawSubHeaderPill(c, x, "USER", fb.ColEmerald)
		}
	}
	if a.Remote.LearnPending {
		c.DrawText(254, SubPillY+2, "LEARN..", fb.ColMagenta, 1)
	}
}

func (a *App) drawRemoteGrid(c *fb.Canvas) {
	sigs := a.Remote.Entry.Signals
	startIdx := a.Remote.Scroll * remoteCols
	for row := range remoteVisibleRows {
		for col := range remoteCols {
			idx := startIdx + row*remoteCols + col
			if idx >= len(sigs) {
				return
			}
			a.drawRemoteTile(c, idx, row, col, sigs[idx])
		}
	}
}

func (a *App) drawRemoteTile(c *fb.Canvas, idx, row, col int, sig irfile.IRSignal) {
	x := 6 + col*(remoteTileW+8)
	y := 34 + row*(remoteTileH+2)
	bg, border, fg := fb.ColSurface, fb.ColBorder, fb.ColFg
	if idx == a.Remote.Cursor {
		bg, border, fg = fb.ColSurfaceAlt, fb.ColCyan, fb.ColCyan
		if a.Remote.TXActive && a.Remote.LastTXBtn == sig.Name {
			border = fb.ColEmerald
		}
	}
	c.FillRect(x, y, remoteTileW, remoteTileH, bg)
	c.DrawRect(x, y, remoteTileW, remoteTileH, border)
	c.DrawText(x+5, y+7, truncate(sig.Name, 16), fg, 1)
	proto := sig.Protocol
	if proto == "" {
		proto = "RAW"
	}
	p := truncate(proto, 6)
	c.DrawText(x+remoteTileW-len(p)*6-4, y+7, p, fb.ColMuted, 1)
}

func (a *App) drawRemoteFooter(c *fb.Canvas) {
	if a.Remote.Cursor >= len(a.Remote.Entry.Signals) {
		return
	}
	sig := a.Remote.Entry.Signals[a.Remote.Cursor]
	spec := sig.CarrierSpec()
	info := fmt.Sprintf("%s | %dkHz %d%% | %s",
		truncate(sig.Name, 14), spec.FreqHz/1000, spec.DutyPct, formatSignalMeta(sig))
	c.FillRect(6, 127, 308, 13, fb.ColSurface)
	c.DrawRect(6, 127, 308, 13, fb.ColBorder)
	c.DrawText(10, 130, truncate(info, 49), fb.ColMuted, 1)
}

func formatSignalMeta(sig irfile.IRSignal) string {
	if sig.Kind == irfile.SignalRaw {
		return fmt.Sprintf("RAW %dp", len(sig.RawData))
	}
	return fmt.Sprintf("%s %02X:%02X", sig.Protocol, sig.Address[0], sig.Command[0])
}
