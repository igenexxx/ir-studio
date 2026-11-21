package ui

import (
	"fmt"

	"ir-studio/internal/fb"
	"ir-studio/internal/input"
	"ir-studio/internal/library"
)

const savedVisibleRows = 7

func (a *App) refreshSaved() {
	if a.Deps.Store == nil {
		return
	}
	if entries, err := a.Deps.Store.ListUserRemotes(); err == nil {
		a.Saved.UserEntries = entries
	}
	if packs, err := a.Deps.Store.ListDatabasePacks(); err == nil {
		a.Saved.DBPacks = packs
		if a.Saved.DBPackIdx >= len(packs) {
			a.Saved.DBPackIdx = 0
		}
		if len(packs) > 0 {
			a.Saved.DBEntries, _ = a.Deps.Store.ListPackRemotes(packs[a.Saved.DBPackIdx].ID)
		} else {
			a.Saved.DBEntries = nil
		}
	}
	a.clampSavedCursor()
}

func (a *App) activeSavedList() []library.RemoteEntry {
	if a.Saved.ScopeUser {
		return a.Saved.UserEntries
	}
	return a.Saved.DBEntries
}

func (a *App) clampSavedCursor() {
	n := len(a.activeSavedList())
	if n == 0 {
		a.Saved.Cursor = 0
		a.Saved.Scroll = 0
		return
	}
	a.Saved.Cursor = min(max(a.Saved.Cursor, 0), n-1)
	if a.Saved.Cursor < a.Saved.Scroll {
		a.Saved.Scroll = a.Saved.Cursor
	}
	if a.Saved.Cursor >= a.Saved.Scroll+savedVisibleRows {
		a.Saved.Scroll = a.Saved.Cursor - savedVisibleRows + 1
	}
}

func (a *App) handleSavedKey(act input.KeyAction) bool {
	if a.Saved.ConfirmDel {
		return a.handleSavedConfirmDel(act)
	}
	if a.Saved.ConfirmPurge {
		return a.handleSavedConfirmPurge(act)
	}
	switch act {
	case input.KeyUp:
		a.Saved.Cursor--
		a.clampSavedCursor()
	case input.KeyDown:
		a.Saved.Cursor++
		a.clampSavedCursor()
	case input.KeyLeft, input.KeyRight:
		a.cycleSavedDBPack(act)
	case input.Key1, input.KeySpace:
		a.toggleSavedSelect()
	case input.KeyEnter, input.KeySoft4:
		a.openFocusedSaved()
	case input.KeySoft5:
		a.sendFocusedSaved()
	case input.KeySoft6:
		a.handleSavedSoft6()
	case input.KeySoft7, input.KeyDel:
		a.requestSavedDelete()
	case input.KeySoft8:
		a.toggleSavedScope()
	}
	return false
}

func (a *App) cycleSavedDBPack(act input.KeyAction) {
	if a.Saved.ScopeUser || len(a.Saved.DBPacks) <= 1 {
		return
	}
	if act == input.KeyRight {
		a.Saved.DBPackIdx = (a.Saved.DBPackIdx + 1) % len(a.Saved.DBPacks)
	} else {
		a.Saved.DBPackIdx = (a.Saved.DBPackIdx + len(a.Saved.DBPacks) - 1) % len(a.Saved.DBPacks)
	}
	a.Saved.DBEntries, _ = a.Deps.Store.ListPackRemotes(a.Saved.DBPacks[a.Saved.DBPackIdx].ID)
	a.Saved.Cursor = 0
	a.Saved.Scroll = 0
}

func (a *App) toggleSavedSelect() {
	list := a.activeSavedList()
	if len(list) == 0 || !a.Saved.ScopeUser {
		if !a.Saved.ScopeUser {
			a.SetToast("DB packs are read-only", fb.ColAmber)
		}
		return
	}
	key := list[a.Saved.Cursor].RelPath
	if a.Saved.Selected[key] {
		delete(a.Saved.Selected, key)
	} else {
		a.Saved.Selected[key] = true
	}
}

func (a *App) openFocusedSaved() {
	list := a.activeSavedList()
	if len(list) == 0 {
		a.SetToast("Library is empty", fb.ColAmber)
		return
	}
	a.openRemoteEntry(list[a.Saved.Cursor])
	a.SwitchTab(TabRemote)
}

func (a *App) sendFocusedSaved() {
	list := a.activeSavedList()
	if len(list) == 0 || a.Deps.Store == nil {
		return
	}
	loaded, err := a.Deps.Store.LoadRemote(list[a.Saved.Cursor])
	if err != nil || len(loaded.Signals) == 0 {
		a.SetToast("No signals in remote", fb.ColAmber)
		return
	}
	sig := loaded.Signals[0]
	if err := a.submitTX(sig, 1); err != nil {
		a.SetToast("TX err: "+truncate(err.Error(), 24), fb.ColRed)
		return
	}
	a.SetToast(fmt.Sprintf("TX %s:%s", truncate(loaded.Name, 12), truncate(sig.Name, 10)), fb.ColEmerald)
}

func (a *App) handleSavedSoft6() {
	if a.Saved.ScopeUser {
		if len(a.Saved.UserEntries) == 0 {
			a.SetToast("No USER remotes to purge", fb.ColAmber)
			return
		}
		a.Saved.ConfirmPurge = true
		return
	}
	list := a.Saved.DBEntries
	if len(list) == 0 || a.Deps.Store == nil {
		return
	}
	promoted, err := a.Deps.Store.PromoteToUser(list[a.Saved.Cursor], "")
	if err != nil {
		a.SetToast("Promote err: "+truncate(err.Error(), 22), fb.ColRed)
		return
	}
	a.refreshSaved()
	a.SetToast("Promoted: "+truncate(promoted.Name, 20), fb.ColEmerald)
}

func (a *App) requestSavedDelete() {
	if !a.Saved.ScopeUser {
		a.SetToast("Read-only DB: delete refused", fb.ColAmber)
		return
	}
	if len(a.Saved.UserEntries) == 0 {
		return
	}
	a.Saved.ConfirmDel = true
}

func (a *App) handleSavedConfirmDel(act input.KeyAction) bool {
	if act == input.KeyEnter || act == input.KeySoft7 {
		a.executeSavedDelete()
	}
	a.Saved.ConfirmDel = false
	return false
}

func (a *App) executeSavedDelete() {
	if a.Deps.Store == nil || !a.Saved.ScopeUser {
		return
	}
	var targets []string
	for k := range a.Saved.Selected {
		targets = append(targets, k)
	}
	if len(targets) == 0 && a.Saved.Cursor < len(a.Saved.UserEntries) {
		targets = []string{a.Saved.UserEntries[a.Saved.Cursor].RelPath}
	}
	deleted, err := a.Deps.Store.BatchDeleteUserRemotes(targets)
	clear(a.Saved.Selected)
	a.refreshSaved()
	if err != nil {
		a.SetToast(fmt.Sprintf("Deleted %d (some errors)", deleted), fb.ColAmber)
		return
	}
	a.SetToast(fmt.Sprintf("Deleted %d remote(s)", deleted), fb.ColEmerald)
}

func (a *App) handleSavedConfirmPurge(act input.KeyAction) bool {
	if act == input.KeyEnter || act == input.KeySoft6 {
		if a.Deps.Store != nil {
			n, err := a.Deps.Store.PurgeNoiseRemotes()
			a.refreshSaved()
			if err != nil {
				a.SetToast("Purge err: "+truncate(err.Error(), 22), fb.ColRed)
			} else {
				a.SetToast(fmt.Sprintf("Purged %d noise remote(s)", n), fb.ColEmerald)
			}
		}
	}
	a.Saved.ConfirmPurge = false
	return false
}

func (a *App) toggleSavedScope() {
	a.Saved.ScopeUser = !a.Saved.ScopeUser
	clear(a.Saved.Selected)
	a.Saved.Cursor = 0
	a.Saved.Scroll = 0
	a.clampSavedCursor()
}

func (a *App) renderSaved(c *fb.Canvas) {
	a.drawSavedHeader(c)
	list := a.activeSavedList()
	if len(list) == 0 {
		msg := "No USER remotes yet"
		if !a.Saved.ScopeUser {
			msg = "No DB packs (sync in SET)"
		}
		c.DrawTextCentered(fb.ScreenW/2, 76, msg, fb.ColMuted, 1)
	} else {
		for row := range savedVisibleRows {
			idx := a.Saved.Scroll + row
			if idx >= len(list) {
				break
			}
			a.drawSavedRow(c, idx, 36+row*14, list[idx])
		}
	}
	if a.Saved.ConfirmDel {
		cnt := max(len(a.Saved.Selected), 1)
		drawConfirmBanner(c, fmt.Sprintf("Delete %d USER item(s)? [Enter=YES]", cnt))
	} else if a.Saved.ConfirmPurge {
		drawConfirmBanner(c, "Purge ML noise remotes? [Enter=YES]")
	}
	s6 := "PURGE"
	if !a.Saved.ScopeUser {
		s6 = "PROMO"
	}
	a.drawNavBar(c, [5]string{"OPEN", "SEND", s6, "DEL", "SRC"})
}

func (a *App) drawSavedHeader(c *fb.Canvas) {
	if a.Saved.ScopeUser {
		x := drawSubHeaderPill(c, 6, fmt.Sprintf("USER (%d)", len(a.Saved.UserEntries)), fb.ColEmerald)
		if len(a.Saved.Selected) > 0 {
			drawSubHeaderPill(c, x, fmt.Sprintf("SEL:%d", len(a.Saved.Selected)), fb.ColCyan)
		}
		c.DrawText(224, SubPillY+2, "[1/Spc]Mark", fb.ColMuted, 1)
		return
	}
	packLabel := "DB:NONE"
	if len(a.Saved.DBPacks) > 0 {
		p := a.Saved.DBPacks[a.Saved.DBPackIdx]
		packLabel = fmt.Sprintf("DB:%s (%d)", truncate(p.ID, 12), len(a.Saved.DBEntries))
	}
	x := drawSubHeaderPill(c, 6, packLabel, fb.ColAmber)
	drawSubHeaderPill(c, x, "RO", fb.ColMuted)
}

func (a *App) drawSavedRow(c *fb.Canvas, idx, y int, entry library.RemoteEntry) {
	focused := idx == a.Saved.Cursor
	if focused {
		c.FillRect(6, y-1, 308, 12, fb.ColSurfaceAlt)
		c.DrawRect(6, y-1, 308, 12, fb.ColCyan)
	}
	prefix := "   "
	if a.Saved.ScopeUser {
		if a.Saved.Selected[entry.RelPath] {
			prefix = "[x]"
		} else {
			prefix = "[ ]"
		}
	} else {
		prefix = "RO "
	}
	pCol := fb.ColMuted
	if prefix == "[x]" {
		pCol = fb.ColCyan
	}
	c.DrawText(10, y+1, prefix, pCol, 1)
	c.DrawText(32, y+1, truncate(entry.Name, 30), fb.ColFg, 1)
	sizeKB := fmt.Sprintf("%dB", entry.SizeBytes)
	c.DrawText(268, y+1, truncate(sizeKB, 7), fb.ColMuted, 1)
}
