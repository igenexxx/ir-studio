package ui

import (
	"context"
	"fmt"

	"ir-studio/internal/config"
	"ir-studio/internal/dbsync"
	"ir-studio/internal/fb"
	"ir-studio/internal/input"
	"ir-studio/internal/library"
)

const settingsRowCount = 6

func (a *App) loadSources() {
	if a.Set.SourcesPath == "" {
		a.Set.Sources = dbsync.DefaultSources()
		return
	}
	srcs, err := dbsync.LoadSources(a.Set.SourcesPath)
	if err != nil || len(srcs) == 0 {
		a.Set.Sources = dbsync.DefaultSources()
		return
	}
	a.Set.Sources = srcs
}

func (a *App) handleSetKey(act input.KeyAction) bool {
	if a.Set.SubViewSources {
		return a.handleSourcesSubViewKey(act)
	}
	switch act {
	case input.KeyUp:
		a.Set.Cursor = (a.Set.Cursor + settingsRowCount - 1) % settingsRowCount
	case input.KeyDown:
		a.Set.Cursor = (a.Set.Cursor + 1) % settingsRowCount
	case input.KeyLeft:
		a.adjustSettingRow(-1)
	case input.KeyRight, input.KeyEnter, input.KeySoft4:
		a.adjustSettingRow(1)
	case input.KeySoft5:
		a.startAsyncSync()
	case input.KeySoft6:
		a.Set.SubViewSources = true
	case input.KeySoft8:
		a.saveSettings()
	}
	return false
}

func (a *App) adjustSettingRow(dir int) {
	switch a.Set.Cursor {
	case 0:
		a.Set.Settings.MCPEnabled = !a.Set.Settings.MCPEnabled
		a.saveSettings()
	case 1:
		idx := (carrierPresetIdx(a.Set.Settings.CarrierHz) + dir + len(config.AllowedCarrierHz)) % len(config.AllowedCarrierHz)
		a.Set.Settings.CarrierHz = config.AllowedCarrierHz[idx]
		a.RX.CarrierIdx = idx
		a.saveSettings()
	case 2:
		if a.Set.Settings.DutyCyclePct == 33 {
			a.Set.Settings.DutyCyclePct = 50
		} else {
			a.Set.Settings.DutyCyclePct = 33
		}
		a.saveSettings()
	case 3:
		n := len(library.ProbeIntervalPresets)
		idx := min(max(intervalPresetIdx(a.Set.Settings.ProbeIntervalMs)+dir, 0), n-1)
		a.Set.Settings.ProbeIntervalMs = int(library.ProbeIntervalPresets[idx].Milliseconds())
		a.Probe.IntervalIdx = idx
		a.saveSettings()
	case 4:
		a.startAsyncSync()
	case 5:
		a.Set.SubViewSources = true
	}
}

func (a *App) saveSettings() {
	a.Set.Settings = config.Sanitize(a.Set.Settings)
	if a.Set.ConfigPath != "" {
		if err := config.SaveAtomic(a.Set.ConfigPath, a.Set.Settings); err != nil {
			a.SetToast("Save err: "+truncate(err.Error(), 24), fb.ColRed)
			return
		}
	}
	a.SetToast("Settings saved", fb.ColEmerald)
}

func (a *App) startAsyncSync() {
	if a.Set.Syncing {
		a.SetToast("Sync already running", fb.ColAmber)
		return
	}
	if a.Deps.Syncer == nil {
		a.SetToast("Syncer unavailable", fb.ColAmber)
		return
	}
	a.Set.Syncing = true
	a.Set.SyncStatus = "Syncing..."
	sources := append([]dbsync.DatabaseSource(nil), a.Set.Sources...)
	go func() {
		results, err := a.Deps.Syncer.SyncAll(context.Background(), sources)
		dl := 0
		for _, r := range results {
			dl += r.Downloaded
		}
		msg := fmt.Sprintf("Synced %d packs (+%d)", len(results), dl)
		a.asyncCh <- asyncEvent{kind: "sync", syncDone: true, syncMsg: msg, syncErr: err}
	}()
}

func (a *App) handleSourcesSubViewKey(act input.KeyAction) bool {
	if a.Set.ConfirmDelPack {
		if act == input.KeyEnter || act == input.KeySoft7 {
			a.deleteFocusedPackDir()
		}
		a.Set.ConfirmDelPack = false
		return false
	}
	switch act {
	case input.KeyEsc:
		a.Set.SubViewSources = false
	case input.KeyUp:
		if len(a.Set.Sources) > 0 {
			a.Set.SrcCursor = max(a.Set.SrcCursor-1, 0)
		}
	case input.KeyDown:
		if len(a.Set.Sources) > 0 {
			a.Set.SrcCursor = min(a.Set.SrcCursor+1, len(a.Set.Sources)-1)
		}
	case input.KeyEnter, input.KeySpace, input.KeySoft4:
		a.toggleFocusedSource()
	case input.KeySoft5:
		a.startAsyncSync()
	case input.KeySoft7, input.KeyDel:
		if len(a.Set.Sources) > 0 {
			a.Set.ConfirmDelPack = true
		}
	case input.KeySoft8:
		a.Set.SubViewSources = false
	}
	return false
}

func (a *App) toggleFocusedSource() {
	if len(a.Set.Sources) == 0 || a.Set.SrcCursor >= len(a.Set.Sources) {
		return
	}
	a.Set.Sources[a.Set.SrcCursor].Enabled = !a.Set.Sources[a.Set.SrcCursor].Enabled
	if a.Set.SourcesPath != "" {
		_ = dbsync.SaveSourcesAtomic(a.Set.SourcesPath, a.Set.Sources)
	}
	a.SetToast("Source toggled", fb.ColCyan)
}

func (a *App) deleteFocusedPackDir() {
	if len(a.Set.Sources) == 0 || a.Set.SrcCursor >= len(a.Set.Sources) || a.Deps.Syncer == nil {
		return
	}
	id := a.Set.Sources[a.Set.SrcCursor].ID
	if err := a.Deps.Syncer.DeletePackDir(id); err != nil {
		a.SetToast("Del err: "+truncate(err.Error(), 24), fb.ColRed)
		return
	}
	a.refreshSaved()
	a.refreshProbePacks()
	a.SetToast("Deleted local pack "+truncate(id, 14), fb.ColAmber)
}

func (a *App) renderSet(c *fb.Canvas) {
	if a.Set.SubViewSources {
		a.renderSourcesSubView(c)
		return
	}
	x := drawSubHeaderPill(c, 6, "SETTINGS", fb.ColCyan)
	if a.Set.Syncing {
		drawSubHeaderPill(c, x, "SYNCING..", fb.ColAmber)
	}
	rows := a.settingsRows()
	for i, r := range rows {
		y := 35 + i*17
		if i == a.Set.Cursor {
			c.FillRect(6, y-1, 308, 15, fb.ColSurfaceAlt)
			c.DrawRect(6, y-1, 308, 15, fb.ColCyan)
		}
		c.DrawText(12, y+3, r.label, fb.ColFg, 1)
		c.DrawText(214, y+3, truncate(r.value, 16), r.col, 1)
	}
	a.drawNavBar(c, [5]string{"TOGL", "SYNC", "SRCS", "RESET", "SAVE"})
}

type settingRow struct {
	label string
	value string
	col   uint16
}

func (a *App) settingsRows() []settingRow {
	mcpVal, mcpCol := "OFF (safe)", fb.ColAmber
	if a.Set.Settings.MCPEnabled {
		mcpVal, mcpCol = "ON (--mcp)", fb.ColEmerald
	}
	syncVal := "Run Now"
	if a.Set.SyncStatus != "" {
		syncVal = a.Set.SyncStatus
	}
	return []settingRow{
		{"MCP Server (SSH)", mcpVal, mcpCol},
		{"TX Carrier Freq", fmt.Sprintf("%d kHz", a.Set.Settings.CarrierHz/1000), fb.ColCyan},
		{"TX Duty Cycle", fmt.Sprintf("%d%%", a.Set.Settings.DutyCyclePct), fb.ColCyan},
		{"Probe Interval", fmt.Sprintf("%.1fs", float64(a.Set.Settings.ProbeIntervalMs)/1000.0), fb.ColFg},
		{"Sync DB Sources", syncVal, fb.ColEmerald},
		{"Manage DB Sources", fmt.Sprintf("%d packs >", len(a.Set.Sources)), fb.ColMagenta},
	}
}

func (a *App) renderSourcesSubView(c *fb.Canvas) {
	drawSubHeaderPill(c, 6, "MANAGE DB SOURCES", fb.ColMagenta)
	for i, src := range a.Set.Sources {
		if i >= 6 {
			break
		}
		y := 36 + i*15
		if i == a.Set.SrcCursor {
			c.FillRect(6, y-1, 308, 13, fb.ColSurfaceAlt)
			c.DrawRect(6, y-1, 308, 13, fb.ColCyan)
		}
		box, bCol := "[ ]", fb.ColMuted
		if src.Enabled {
			box, bCol = "[x]", fb.ColEmerald
		}
		c.DrawText(10, y+2, box, bCol, 1)
		c.DrawText(32, y+2, truncate(src.ID, 18), fb.ColFg, 1)
		c.DrawText(152, y+2, truncate(src.PathPrefix, 24), fb.ColMuted, 1)
	}
	if a.Set.ConfirmDelPack {
		drawConfirmBanner(c, "Delete local pack dir? [Enter=YES]")
	}
	a.drawNavBar(c, [5]string{"TOGL", "SYNC", "", "DELPK", "BACK"})
}
