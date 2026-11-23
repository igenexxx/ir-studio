package ui

import (
	"ir-studio/internal/fb"
	"ir-studio/internal/irml"
)

// Canonical CardputerZero bottom softkey geometry (SSOT from DESIGN.md).
const (
	HeaderH   = 18
	SubPillY  = 20
	NavBarY   = 143
	NavKeyY   = 153
	NavTileW  = 50
	NavTileH  = 21
	ToastY    = 22
	ToastH    = 15
	ToastSecs = 36 // ~2.0s at 55ms tick
)

// NavKeyCentersX are the exact X centers of the 5 bottom softkeys aligned with keys 4..8.
var NavKeyCentersX = [5]int{49, 105, 161, 217, 273}

func (a *App) drawHeader(c *fb.Canvas) {
	c.FillRect(0, 0, fb.ScreenW, HeaderH-1, fb.ColSurface)
	c.HLine(0, HeaderH-1, fb.ScreenW, fb.ColBorder)

	a.drawHeaderBreadcrumb(c)
	a.drawHeaderBadges(c)
}

func (a *App) drawHeaderBreadcrumb(c *fb.Canvas) {
	title := a.Chrome.Tab.Title()
	col := fb.ColCyan
	if a.Chrome.Tab == TabSaved && !a.Saved.ScopeUser {
		title = "< DB BROWSER"
		col = fb.ColAmber
	} else if a.Chrome.Tab == TabRemote && a.Remote.Loaded {
		title = "< " + truncate(a.Remote.Entry.Name, 20)
	} else if a.Chrome.Tab == TabHome {
		c.FillRect(4, 3, 58, 11, fb.ColCyan)
		c.DrawText(7, 5, "IR STUDIO", fb.ColBg, 1)
		modeLbl, modeCol := "SIMPLE", fb.ColMuted
		if a.Set.Settings.ExpertMode {
			modeLbl, modeCol = "EXPERT", fb.ColMagenta
		}
		c.DrawText(68, 5, modeLbl, modeCol, 1)
		return
	}
	c.DrawText(6, 5, truncate(title, 28), col, 1)
}

func (a *App) drawHeaderBadges(c *fb.Canvas) {
	if a.Set.Settings.ExpertMode && a.Chrome.Tab != TabHome {
		c.FillRect(206, 3, 28, 11, fb.ColSurfaceAlt)
		c.DrawRect(206, 3, 28, 11, fb.ColMagenta)
		c.DrawTextCentered(220, 5, "EXP", fb.ColMagenta, 1)
	}
	if a.Chrome.Tab == TabRead || a.Chrome.Tab == TabRX {
		a.drawHeaderAmbient(c)
	}
	a.drawHeaderDeviceBadge(c)
}

func (a *App) drawHeaderAmbient(c *fb.Canvas) {
	col := fb.ColEmerald
	txt := "AMB"
	switch a.Chrome.Ambient.Level {
	case irml.AmbientModerate:
		col = fb.ColAmber
		txt = "MOD"
	case irml.AmbientHigh:
		col = fb.ColRed
		txt = "SAT"
	}
	c.FillRect(238, 3, 30, 11, fb.ColSurfaceAlt)
	c.DrawRect(238, 3, 30, 11, col)
	c.DrawTextCentered(253, 5, txt, col, 1)
}

func (a *App) drawHeaderDeviceBadge(c *fb.Canvas) {
	col := fb.ColEmerald
	label := a.Chrome.DeviceLabel
	if a.Chrome.SimMode || label == "" || label == "SIM" {
		col = fb.ColAmber
		label = "SIM"
	}
	c.FillRect(272, 3, 44, 11, fb.ColSurfaceAlt)
	c.DrawRect(272, 3, 44, 11, col)
	c.DrawTextCentered(294, 5, truncate(label, 6), col, 1)
}

// drawSubHeaderPill draws a compact pill at y=20 (never overlapping the y=0..17 header).
func drawSubHeaderPill(c *fb.Canvas, x int, text string, col uint16) int {
	w := len(text)*6 + 8
	c.FillRect(x, SubPillY, w, 11, fb.ColSurfaceAlt)
	c.DrawRect(x, SubPillY, w, 11, col)
	c.DrawText(x+4, SubPillY+2, text, col, 1)
	return x + w + 4
}

func (a *App) drawNavBar(c *fb.Canvas, labels [5]string) {
	c.FillRect(0, NavBarY, fb.ScreenW, fb.ScreenH-NavBarY, fb.ColNavBg)
	c.HLine(0, NavBarY, fb.ScreenW, fb.ColBorder)
	keys := [5]string{"4", "5", "6", "7", "8"}
	topY := NavKeyY - NavTileH/2 + 2
	for i, cx := range NavKeyCentersX {
		bx := cx - NavTileW/2
		c.FillRect(bx, topY, NavTileW, NavTileH, fb.ColSurfaceAlt)
		c.DrawRect(bx, topY, NavTileW, NavTileH, fb.ColBorder)
		c.FillRect(bx+2, topY+2, 9, 9, fb.ColCyan)
		c.DrawText(bx+4, topY+3, keys[i], fb.ColBg, 1)
		c.DrawTextCentered(cx+5, topY+7, truncate(labels[i], 6), fb.ColFg, 1)
	}
}

func (a *App) drawToast(c *fb.Canvas) {
	if a.Chrome.ToastText == "" || a.Chrome.ToastTicks <= 0 {
		return
	}
	msg := truncate(a.Chrome.ToastText, 48)
	w := min(len(msg)*6+14, fb.ScreenW-16)
	x := (fb.ScreenW - w) / 2
	col := a.Chrome.ToastCol
	if col == 0 {
		col = fb.ColCyan
	}
	c.FillRect(x, ToastY, w, ToastH, fb.ColSurface)
	c.DrawRect(x, ToastY, w, ToastH, col)
	c.DrawTextCentered(fb.ScreenW/2, ToastY+4, msg, col, 1)
}

func drawConfirmBanner(c *fb.Canvas, msg string) {
	c.FillRect(8, 124, fb.ScreenW-16, 16, fb.ColSurface)
	c.DrawRect(8, 124, fb.ScreenW-16, 16, fb.ColRed)
	c.DrawTextCentered(fb.ScreenW/2, 129, truncate(msg, 48), fb.ColRed, 1)
}

type helpPage struct {
	Title string
	Lines []string
}

var helpPages = []helpPage{
	{
		Title: "1/4 HOME MENU & NAVIGATION",
		Lines: []string{
			"F / X : Move up / down in menus",
			"Enter : Open item or primary action",
			"Esc   : Step back (Home -> Exit confirm)",
			"4..8  : Bottom bar labeled softkeys",
			"5 EXP : Toggle Simple / Expert Mode",
			"H : Help   Q : Quit immediately",
		},
	},
	{
		Title: "2/4 READ & REPLAY (LINEAR FLOW)",
		Lines: []string{
			"Step 1: Aim remote (5-20cm) & press key",
			"Step 2: Captured! Card shows protocol",
			"  Enter / 4 SEND : Replay immediately!",
			"  5 SAVE : Save & open in Remote Deck",
			"  6 AGAIN: Capture next button",
			"  7 ADDDB: Add full matched DB remote",
		},
	},
	{
		Title: "3/4 MY REMOTES, AC & UNIV OFF",
		Lines: []string{
			"My Remotes: Enter opens your remote",
			"  4 SEND  5 ADD + (learn more buttons)",
			"  Esc / 8 BACK returns to remote list",
			"Smart AC: Z/C Temp F/X Mode R Brand",
			"  4 PWR 5 FAN 6 SWING 7 SEND 8 SAVE",
			"Universal Off: 4 RUN/STOP  6 SAVE",
		},
	},
	{
		Title: "4/4 EXPERT MODE & MCP SERVER",
		Lines: []string{
			"Enable Expert Mode (HOME -> 5 EXP / SET)",
			"Unlocks: IR Lab (Oscilloscope, AC Diff,",
			"  32D k-NN), Macros, and DB Browser.",
			"PulseNet-IR: 24 protocols + PN-CLEAN.",
			"MCP (opt-in SET): ir-studio --mcp",
			"Badges: LIRC(HW) SIM(mock) EXP(Expert)",
		},
	},
}

func (a *App) drawHelpModal(c *fb.Canvas) {
	idx := a.Chrome.HelpPage % len(helpPages)
	if idx < 0 {
		idx += len(helpPages)
	}
	page := helpPages[idx]
	c.FillRect(10, 18, 300, 124, fb.ColSurface)
	c.DrawRect(10, 18, 300, 124, fb.ColCyan)
	c.FillRect(11, 19, 298, 13, fb.ColNavActive)
	c.DrawTextCentered(fb.ScreenW/2, 22, page.Title, fb.ColCyan, 1)
	for i, line := range page.Lines {
		c.DrawText(16, 36+i*12, truncate(line, 47), fb.ColFg, 1)
	}
	c.HLine(12, 128, 296, fb.ColBorder)
	c.DrawTextCentered(fb.ScreenW/2, 132, "[Z/C] Page   [H/Esc] Close", fb.ColAmber, 1)
}

func truncate(s string, maxLen int) string {
	if maxLen <= 0 || len(s) <= maxLen {
		return s
	}
	if maxLen <= 2 {
		return s[:maxLen]
	}
	return s[:maxLen-2] + ".."
}
