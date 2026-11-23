package ui

import (
	"fmt"

	"ir-studio/internal/fb"
	"ir-studio/internal/input"
)

const homeVisibleRows = 5

type homeMenuItem struct {
	Group    string
	Tag      string
	TagCol   uint16
	Title    string
	Subtitle string
	Target   Tab
	DBScope  bool
}

func (a *App) homeMenuItems() []homeMenuItem {
	remSub := fmt.Sprintf("%d saved remotes", len(a.Saved.UserEntries))
	if len(a.Saved.UserEntries) == 0 {
		remSub = "Send & manage your remotes"
	}
	items := []homeMenuItem{
		{Group: "QUICK", Tag: "RX", TagCol: fb.ColCyan, Title: "Read & Replay", Subtitle: "Capture, test & save", Target: TabRead},
		{Group: "QUICK", Tag: "MY", TagCol: fb.ColEmerald, Title: "My Remotes", Subtitle: remSub, Target: TabSaved},
		{Group: "TOOLS", Tag: "PW", TagCol: fb.ColAmber, Title: "Universal Off", Subtitle: "TV-B-Gone power sweep", Target: TabProbe},
		{Group: "TOOLS", Tag: "AC", TagCol: fb.ColCyan, Title: "Smart AC", Subtitle: "Control 6 AC brands", Target: TabAC},
		{Group: "SETUP", Tag: "ST", TagCol: fb.ColMuted, Title: "Settings", Subtitle: "Expert, MCP & DB sync", Target: TabSet},
	}
	if a.Set.Settings.ExpertMode {
		items = append(items,
			homeMenuItem{Group: "EXPERT", Tag: "LB", TagCol: fb.ColMagenta, Title: "IR Lab (Scope)", Subtitle: "Oscilloscope, Diff, k-NN", Target: TabRX},
			homeMenuItem{Group: "EXPERT", Tag: "MC", TagCol: fb.ColMagenta, Title: "Macros", Subtitle: "Multi-device chains", Target: TabMacro},
			homeMenuItem{Group: "EXPERT", Tag: "DB", TagCol: fb.ColAmber, Title: "DB Browser", Subtitle: "Flipper-IRDB packs", Target: TabSaved, DBScope: true},
		)
	}
	return items
}

func (a *App) clampHomeCursor(n int) {
	if n <= 0 {
		a.Home.Cursor = 0
		a.Home.Scroll = 0
		return
	}
	a.Home.Cursor = min(max(a.Home.Cursor, 0), n-1)
	if a.Home.Cursor < a.Home.Scroll {
		a.Home.Scroll = a.Home.Cursor
	}
	if a.Home.Cursor >= a.Home.Scroll+homeVisibleRows {
		a.Home.Scroll = a.Home.Cursor - homeVisibleRows + 1
	}
}

func (a *App) handleHomeKey(act input.KeyAction) bool {
	if a.Home.ConfirmExit {
		if act == input.KeyEnter || act == input.KeyEsc || act == input.KeySoft8 {
			return true
		}
		a.Home.ConfirmExit = false
		return false
	}
	items := a.homeMenuItems()
	switch act {
	case input.KeyUp:
		a.Home.Cursor = (a.Home.Cursor + len(items) - 1) % len(items)
		a.clampHomeCursor(len(items))
	case input.KeyDown:
		a.Home.Cursor = (a.Home.Cursor + 1) % len(items)
		a.clampHomeCursor(len(items))
	case input.KeyEnter, input.KeySoft4:
		a.openHomeItem(items[min(a.Home.Cursor, len(items)-1)])
	case input.KeySoft5:
		a.toggleExpertMode()
	case input.KeySoft6:
		a.enterReadAndReplay()
	case input.KeySoft7:
		a.quickOpenLastRemote()
	case input.KeyEsc, input.KeySoft8:
		a.Home.ConfirmExit = true
	}
	return false
}

func (a *App) openHomeItem(it homeMenuItem) {
	switch it.Target {
	case TabRead:
		a.enterReadAndReplay()
	case TabSaved:
		a.Saved.ScopeUser = !it.DBScope
		clear(a.Saved.Selected)
		a.Saved.Cursor = 0
		a.Saved.Scroll = 0
		a.clampSavedCursor()
		a.SwitchTab(TabSaved)
	case TabRX:
		a.RX.Learning = true
		a.SwitchTab(TabRX)
	default:
		a.SwitchTab(it.Target)
	}
}

func (a *App) quickOpenLastRemote() {
	if len(a.Saved.UserEntries) == 0 {
		a.SetToast("No saved remotes - capture one!", fb.ColAmber)
		a.enterReadAndReplay()
		return
	}
	if !a.Remote.Loaded || a.Remote.Entry.ReadOnly {
		a.openRemoteEntry(a.Saved.UserEntries[0])
	}
	a.SwitchTab(TabRemote)
}

func (a *App) toggleExpertMode() {
	a.Set.Settings.ExpertMode = !a.Set.Settings.ExpertMode
	a.clampHomeCursor(len(a.homeMenuItems()))
	a.saveSettingsQuiet()
	if a.Set.Settings.ExpertMode {
		a.SetToast("Expert Mode ON (+3 tools)", fb.ColMagenta)
	} else {
		a.SetToast("Simple Mode ON", fb.ColCyan)
	}
}

func (a *App) renderHome(c *fb.Canvas) {
	items := a.homeMenuItems()
	a.clampHomeCursor(len(items))

	c.DrawText(8, 21, "SELECT ACTION (F/X Move, Enter Open)", fb.ColMuted, 1)
	if len(items) > homeVisibleRows {
		ind := fmt.Sprintf("%d/%d", a.Home.Cursor+1, len(items))
		c.DrawText(290, 21, ind, fb.ColMuted, 1)
	}

	for row := range homeVisibleRows {
		idx := a.Home.Scroll + row
		if idx >= len(items) {
			break
		}
		a.drawHomeRow(c, idx, 32+row*22, items[idx])
	}

	if a.Home.ConfirmExit {
		drawConfirmBanner(c, "Exit IR Studio? [Enter/Esc=YES]")
	}
	expLbl := "EXP-"
	if a.Set.Settings.ExpertMode {
		expLbl = "EXP+"
	}
	a.drawNavBar(c, [5]string{"OPEN", expLbl, "READ", "SEND", "QUIT"})
}

func (a *App) drawHomeRow(c *fb.Canvas, idx, y int, it homeMenuItem) {
	focused := idx == a.Home.Cursor
	bg, border, titleCol := fb.ColSurface, fb.ColBorder, fb.ColFg
	if focused {
		bg, border, titleCol = fb.ColSurfaceAlt, fb.ColCyan, fb.ColCyan
	}
	c.FillRect(6, y, 308, 20, bg)
	c.DrawRect(6, y, 308, 20, border)
	if focused {
		c.FillRect(6, y, 3, 20, fb.ColCyan)
	}
	c.FillRect(12, y+4, 18, 12, fb.ColBg)
	c.DrawRect(12, y+4, 18, 12, it.TagCol)
	c.DrawTextCentered(21, y+6, it.Tag, it.TagCol, 1)

	c.DrawText(36, y+6, truncate(it.Title, 16), titleCol, 1)
	sub := truncate(it.Subtitle, 26)
	c.DrawText(142, y+6, sub, fb.ColMuted, 1)
}
