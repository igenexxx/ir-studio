package ui

import (
	"fmt"
	"strings"

	"ir-studio/internal/fb"
	"ir-studio/internal/input"
	"ir-studio/internal/library"
)

const (
	helpPage1MaxKeys = 6
	helpPageNMaxKeys = 8
	helpSummaryMaxCh = 48
	helpDescMaxCh    = 41
)

// HelpKeyHint describes one available key or softkey on the active screen.
type HelpKeyHint struct {
	Key     string // Short keycap label (<= 7 chars), e.g. "4/Ent", "Z/C", "R"
	Desc    string // Concise English explanation (<= 41 chars)
	Softkey bool   // True for 4..8 bottom-bar softkeys (highlighted in cyan)
}

// ScreenHelp holds context-aware documentation for the active screen and sub-state.
type ScreenHelp struct {
	ScreenTitle string
	Summary     [2]string
	Keys        []HelpKeyHint
}

// PageCount returns how many modal pages are needed to display s cleanly on 320x170.
func (s ScreenHelp) PageCount() int {
	if len(s.Keys) <= helpPage1MaxKeys {
		return 1
	}
	rem := len(s.Keys) - helpPage1MaxKeys
	return 1 + (rem+helpPageNMaxKeys-1)/helpPageNMaxKeys
}

// ActiveScreenHelp returns exact context-aware help for the currently visible screen.
func (a *App) ActiveScreenHelp() ScreenHelp {
	switch a.Chrome.Tab {
	case TabHome:
		return helpForHome(a.Set.Settings.ExpertMode)
	case TabRead:
		return helpForRead(a.RX.LastCap != nil, a.hasTopDBMatch(), a.Set.Settings.ExpertMode)
	case TabSaved:
		return helpForSaved(a.Set.Settings.ExpertMode, a.Saved.ScopeUser)
	case TabRemote:
		if a.Remote.AC.Active {
			return helpForAC()
		}
		return helpForRemote(a.Set.Settings.ExpertMode, a.Remote.Loaded && a.Remote.Entry.ReadOnly)
	case TabProbe:
		return helpForProbe(a.Probe.Snap.State == library.ProbeRunning, a.Set.Settings.ExpertMode)
	case TabAC:
		return helpForAC()
	case TabRX:
		if a.RX.AC.Active {
			return helpForACDiff()
		}
		return helpForRX(a.RX.Learning, a.RX.LastCap != nil)
	case TabMacro:
		return helpForMacro(a.Macro.Running, a.Macro.EditMode)
	case TabSet:
		if a.Set.SubViewSources {
			return helpForSources()
		}
		return helpForSettings(a.Set.Settings.ExpertMode)
	default:
		return helpForHome(a.Set.Settings.ExpertMode)
	}
}

func (a *App) handleHelpKey(act input.KeyAction) bool {
	pages := a.ActiveScreenHelp().PageCount()
	switch act {
	case input.KeyLeft, input.KeyUp:
		if pages > 1 {
			a.Chrome.HelpPage = (a.Chrome.HelpPage + pages - 1) % pages
		}
	case input.KeyRight, input.KeyDown, input.KeySpace, input.KeyTab, input.KeySoft5:
		if pages > 1 {
			a.Chrome.HelpPage = (a.Chrome.HelpPage + 1) % pages
		}
	case input.KeyH, input.KeyQ, input.KeyEsc, input.KeyEnter, input.KeySoft4, input.KeySoft8:
		a.Chrome.HelpOpen = false
	}
	return false
}

func (a *App) drawHelpModal(c *fb.Canvas) {
	h := a.ActiveScreenHelp()
	totalPages := h.PageCount()
	page := a.Chrome.HelpPage % totalPages
	if page < 0 {
		page += totalPages
	}

	// Clean full-screen backdrop eliminates half-clipped underlying chrome on 320x170.
	c.Clear(fb.ColBg)

	const (
		mx, my, mw, mh = 4, 4, 312, 162
	)
	c.FillRect(mx, my, mw, mh, fb.ColSurface)
	c.DrawRect(mx, my, mw, mh, fb.ColCyan)
	c.FillRect(mx+1, my+1, mw-2, 14, fb.ColNavActive)

	title := "HELP: " + truncate(h.ScreenTitle, 32)
	c.DrawText(mx+6, my+4, title, fb.ColFg, 1)
	pageLbl := fmt.Sprintf("%d/%d", page+1, totalPages)
	c.DrawText(mx+mw-len(pageLbl)*6-6, my+4, pageLbl, fb.ColAmber, 1)

	if page == 0 {
		drawHelpPageOne(c, h, mx, my)
	} else {
		drawHelpOverflowPage(c, h, page, mx, my)
	}

	c.HLine(mx+2, my+mh-17, mw-4, fb.ColBorder)
	footer := "[H / Enter / Esc] Close Help"
	if totalPages > 1 {
		footer = "[Z/C] Page   [H / Enter / Esc] Close"
	}
	c.DrawTextCentered(fb.ScreenW/2, my+mh-12, footer, fb.ColAmber, 1)
}

func drawHelpPageOne(c *fb.Canvas, h ScreenHelp, mx, my int) {
	// Screen summary card with left cyan accent.
	c.FillRect(mx+5, my+18, 302, 26, fb.ColSurfaceAlt)
	c.FillRect(mx+5, my+18, 2, 26, fb.ColCyan)
	c.DrawText(mx+10, my+21, truncate(h.Summary[0], helpSummaryMaxCh), fb.ColFg, 1)
	if strings.TrimSpace(h.Summary[1]) != "" {
		c.DrawText(mx+10, my+32, truncate(h.Summary[1], helpSummaryMaxCh), fb.ColMuted, 1)
	}

	c.DrawText(mx+6, my+48, "AVAILABLE CONTROLS", fb.ColCyan, 1)
	c.HLine(mx+118, my+51, 188, fb.ColBorder)

	end := min(len(h.Keys), helpPage1MaxKeys)
	for i := range end {
		drawHelpKeyRow(c, mx+5, my+59+i*14, h.Keys[i])
	}
}

func drawHelpOverflowPage(c *fb.Canvas, h ScreenHelp, page, mx, my int) {
	c.DrawText(mx+6, my+19, "MORE CONTROLS", fb.ColCyan, 1)
	c.HLine(mx+88, my+22, 218, fb.ColBorder)

	start := helpPage1MaxKeys + (page-1)*helpPageNMaxKeys
	end := min(len(h.Keys), start+helpPageNMaxKeys)
	for i := start; i < end; i++ {
		drawHelpKeyRow(c, mx+5, my+31+(i-start)*14, h.Keys[i])
	}
}

func drawHelpKeyRow(c *fb.Canvas, x, y int, k HelpKeyHint) {
	const badgeW = 46
	borderCol, keyCol := fb.ColAmber, fb.ColAmber
	if k.Softkey {
		borderCol, keyCol = fb.ColCyan, fb.ColCyan
	}
	c.FillRect(x, y, badgeW, 11, fb.ColBg)
	c.DrawRect(x, y, badgeW, 11, borderCol)
	c.DrawTextCentered(x+badgeW/2, y+2, truncate(k.Key, 7), keyCol, 1)
	c.DrawText(x+badgeW+5, y+2, truncate(k.Desc, helpDescMaxCh), fb.ColFg, 1)
}
