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

	a.drawHeaderTabPills(c)
	a.drawHeaderAmbient(c)
	a.drawHeaderDeviceBadge(c)
}

func (a *App) drawHeaderTabPills(c *fb.Canvas) {
	x := 3
	for t := range tabCount {
		label := t.PillLabel()
		w := len(label)*6 + 6
		if t == a.Chrome.Tab {
			c.FillRect(x, 3, w, 11, fb.ColNavActive)
			c.DrawRect(x, 3, w, 11, fb.ColCyan)
			c.DrawText(x+3, 5, label, fb.ColCyan, 1)
		} else {
			c.DrawText(x+3, 5, label, fb.ColMuted, 1)
		}
		x += w + 2
	}
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
		Title: "1/5 NAVIGATION & GLOBAL KEYS",
		Lines: []string{
			"Tab / 9 / 0 : Cycle tabs (RX..SET)",
			"F / X / Z / C : Up / Down / Left / Right",
			"4..8 : Bottom bar contextual actions",
			"Enter : Primary action / Confirm",
			"Space / 1 : Multi-select or STOP probe",
			"R : Smart Probe Rank (32D IR match)",
			"H : Toggle Help   Esc/Q : Cancel/Exit",
		},
	},
	{
		Title: "2/5 RX, SWEEP & AC DIFF",
		Lines: []string{
			"Live /dev/lirc* mark/space oscilloscope",
			"AMB bar: CLEAN / MOD / SAT (sun/LED)",
			"4 HOLD/LEARN : Toggle live capture",
			"5 CLEAN/SWEEP: Denoise or 36-56kHz sweep",
			"R : Carrier sweep active RX capture",
			"6 DIFF : AC bit XOR  7 SAVE  8 CARR",
			"Esc : Cancel active sweep / close diff",
		},
	},
	{
		Title: "3/5 TV-B-GONE PROBE & RANK",
		Lines: []string{
			"GEN:TVBG: 100 offline TV/Proj/Bar codes",
			"4 RUN/STOP | Space : Instant halt",
			"F / X : Rewind > (last) / * (12 trail)",
			"Z / C : Interval 0.3s .. 5.0s",
			"5 TRY : Single test fire focused code",
			"6 SAVE: Promote winning code to USER",
			"7 ACT : Filter   8 PACK | R : Rank",
		},
	},
	{
		Title: "4/5 REMOTE, AC CTRL & SAVED",
		Lines: []string{
			"REMOTE: 4 SEND 5 SWEEP 6 LEARN+ 7 DEL",
			"  1 / 8 AC: Smart AC Controller (6 brd)",
			"  AC: Z/C Temp 16-30C  F/X Mode R Brand",
			"  AC: 4 PWR 5 FAN 6 SWING 7 SEND 8 SAVE",
			"SAVED : Split USER (RW) vs DB (RO)",
			"  1/Space=[x] multi-select, 7 DEL batch",
			"MACRO : 4 RUN 5 NEW 6 ADD 7 DEL 8 EDIT",
		},
	},
	{
		Title: "5/5 DB SYNC, MCP & BADGES",
		Lines: []string{
			"SET: Opt-in MCP Server (default OFF)",
			"  Carrier 36-56kHz, Duty 33/50%, Sync",
			"  Manage DB Sources: toggle/del packs",
			"CLI: ir-studio --sync | --probe | --sim",
			"MCP: ssh <dev> ir-studio --mcp",
			"Badges: LIRC(OK) SIM(mock) AMB(noise)",
			"ML: PulseNet-IR 24-cls, PN-CLEAN, 32D",
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
