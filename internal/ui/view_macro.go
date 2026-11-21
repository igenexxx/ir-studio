package ui

import (
	"context"
	"fmt"

	"ir-studio/internal/config"
	"ir-studio/internal/fb"
	"ir-studio/internal/input"
	"ir-studio/internal/library"
)

const macroVisibleRows = 6

func (a *App) loadMacros() {
	if a.Macro.MacrosPath == "" {
		return
	}
	mf, err := config.LoadMacros(a.Macro.MacrosPath)
	if err == nil && mf != nil {
		a.Macro.File = mf
	}
}

func (a *App) persistMacros() {
	if a.Macro.MacrosPath == "" || a.Macro.File == nil {
		return
	}
	if err := config.SaveMacrosAtomic(a.Macro.MacrosPath, a.Macro.File); err != nil {
		a.SetToast("Macro save err: "+truncate(err.Error(), 20), fb.ColRed)
	}
}

func (a *App) handleMacroKey(act input.KeyAction) bool {
	if a.Macro.ConfirmDel {
		return a.handleMacroConfirmDel(act)
	}
	switch act {
	case input.KeyUp:
		a.moveMacroCursor(-1)
	case input.KeyDown:
		a.moveMacroCursor(1)
	case input.KeyLeft:
		a.adjustMacroStepDelay(-100)
	case input.KeyRight:
		a.adjustMacroStepDelay(100)
	case input.KeyEnter, input.KeySoft4:
		a.toggleMacroRun()
	case input.KeySpace, input.KeyEsc:
		if a.Macro.Running {
			a.stopMacro()
		} else if a.Macro.EditMode {
			a.Macro.EditMode = false
		}
	case input.KeySoft5:
		a.createNewMacro()
	case input.KeySoft6:
		a.addMacroStep()
	case input.KeySoft7, input.KeyDel:
		if len(a.Macro.File.Macros) > 0 {
			a.Macro.ConfirmDel = true
		}
	case input.KeySoft8:
		a.Macro.EditMode = !a.Macro.EditMode
		a.Macro.StepCursor = 0
	}
	return false
}

func (a *App) handleMacroConfirmDel(act input.KeyAction) bool {
	if act == input.KeyEnter || act == input.KeySoft7 {
		a.deleteMacroOrStep()
	}
	a.Macro.ConfirmDel = false
	return false
}

func (a *App) moveMacroCursor(delta int) {
	if a.Macro.EditMode {
		m := a.currentMacro()
		if m == nil || len(m.Steps) == 0 {
			return
		}
		a.Macro.StepCursor = min(max(a.Macro.StepCursor+delta, 0), len(m.Steps)-1)
		return
	}
	n := len(a.Macro.File.Macros)
	if n == 0 {
		a.Macro.Cursor = 0
		return
	}
	a.Macro.Cursor = min(max(a.Macro.Cursor+delta, 0), n-1)
}

func (a *App) adjustMacroStepDelay(deltaMs int) {
	m := a.currentMacro()
	if m == nil || len(m.Steps) == 0 {
		return
	}
	idx := min(max(a.Macro.StepCursor, 0), len(m.Steps)-1)
	next := m.Steps[idx].DelayAfterMs + deltaMs
	if next < config.MinStepDelayMs {
		next = 0
	}
	next = min(next, config.MaxStepDelayMs)
	m.Steps[idx].DelayAfterMs = next
	a.persistMacros()
	a.SetToast(fmt.Sprintf("Step %d delay: %dms", idx+1, next), fb.ColCyan)
}

func (a *App) toggleMacroRun() {
	if a.Macro.Running {
		a.stopMacro()
		return
	}
	m := a.currentMacro()
	if m == nil {
		a.SetToast("No macro selected", fb.ColAmber)
		return
	}
	if a.Deps.MacroRunner == nil {
		a.SetToast("MacroRunner unavailable", fb.ColRed)
		return
	}
	err := a.Deps.MacroRunner.Run(context.Background(), *m, func(p library.MacroProgress) {
		cp := p
		select {
		case a.asyncCh <- asyncEvent{kind: "macro", macroProg: &cp}:
		default:
		}
	})
	if err != nil {
		a.SetToast("Macro: "+truncate(err.Error(), 28), fb.ColRed)
		return
	}
	a.Macro.Running = true
	a.Macro.Progress = library.MacroProgress{MacroID: m.ID, StepTotal: len(m.Steps)}
	a.SetToast("Running "+truncate(m.Name, 20), fb.ColCyan)
}

func (a *App) stopMacro() {
	if a.Deps.MacroRunner != nil {
		a.Deps.MacroRunner.Cancel()
	}
	a.Macro.Running = false
	a.SetToast("Macro stopped", fb.ColAmber)
}

func (a *App) createNewMacro() {
	if len(a.Macro.File.Macros) >= config.MaxMacros {
		a.SetToast("Max macros reached", fb.ColAmber)
		return
	}
	n := len(a.Macro.File.Macros) + 1
	m := config.Macro{
		ID:   fmt.Sprintf("macro_%d", n),
		Name: fmt.Sprintf("Macro %d", n),
	}
	a.Macro.File.Macros = append(a.Macro.File.Macros, m)
	a.Macro.Cursor = len(a.Macro.File.Macros) - 1
	a.persistMacros()
	a.SetToast("Created "+m.Name, fb.ColEmerald)
}

func (a *App) addMacroStep() {
	m := a.currentMacro()
	if m == nil {
		a.createNewMacro()
		m = a.currentMacro()
	}
	if len(m.Steps) >= config.MaxStepsPerMacro {
		a.SetToast("Max steps reached", fb.ColAmber)
		return
	}
	remoteName := "remote.ir"
	btnName := "Power"
	if a.Remote.Loaded && !a.Remote.Entry.ReadOnly {
		remoteName = a.Remote.Entry.RelPath
		if a.Remote.Cursor < len(a.Remote.Entry.Signals) {
			btnName = a.Remote.Entry.Signals[a.Remote.Cursor].Name
		}
	} else if len(a.Saved.UserEntries) > 0 {
		remoteName = a.Saved.UserEntries[0].RelPath
	}
	m.Steps = append(m.Steps, config.MacroStep{
		Remote:       remoteName,
		Button:       btnName,
		DelayAfterMs: config.DefaultStepDelayMs,
		Repeat:       1,
	})
	a.Macro.StepCursor = len(m.Steps) - 1
	a.persistMacros()
	a.SetToast("Added step "+btnName, fb.ColEmerald)
}

func (a *App) deleteMacroOrStep() {
	m := a.currentMacro()
	if m == nil {
		return
	}
	if a.Macro.EditMode && len(m.Steps) > 0 {
		idx := min(max(a.Macro.StepCursor, 0), len(m.Steps)-1)
		m.Steps = append(m.Steps[:idx], m.Steps[idx+1:]...)
		if a.Macro.StepCursor >= len(m.Steps) && a.Macro.StepCursor > 0 {
			a.Macro.StepCursor--
		}
		a.persistMacros()
		a.SetToast("Removed macro step", fb.ColAmber)
		return
	}
	idx := a.Macro.Cursor
	a.Macro.File.Macros = append(a.Macro.File.Macros[:idx], a.Macro.File.Macros[idx+1:]...)
	if a.Macro.Cursor >= len(a.Macro.File.Macros) && a.Macro.Cursor > 0 {
		a.Macro.Cursor--
	}
	a.persistMacros()
	a.SetToast("Deleted macro", fb.ColAmber)
}

func (a *App) currentMacro() *config.Macro {
	if a.Macro.File == nil || len(a.Macro.File.Macros) == 0 {
		return nil
	}
	idx := min(max(a.Macro.Cursor, 0), len(a.Macro.File.Macros)-1)
	return &a.Macro.File.Macros[idx]
}

func (a *App) renderMacro(c *fb.Canvas) {
	m := a.currentMacro()
	a.drawMacroHeader(c, m)
	if m == nil {
		c.DrawTextCentered(fb.ScreenW/2, 74, "No macros (press 5 NEW)", fb.ColMuted, 1)
	} else {
		a.drawMacroSteps(c, m)
		a.drawMacroProgressBar(c, m)
	}
	if a.Macro.ConfirmDel {
		drawConfirmBanner(c, "Delete macro/step? [Enter=YES Esc=NO]")
	}
	runLbl := "RUN"
	if a.Macro.Running {
		runLbl = "STOP"
	}
	editLbl := "EDIT"
	if a.Macro.EditMode {
		editLbl = "DONE"
	}
	a.drawNavBar(c, [5]string{runLbl, "NEW", "ADD", "DEL", editLbl})
}

func (a *App) drawMacroHeader(c *fb.Canvas, m *config.Macro) {
	title := "MACROS (0)"
	if m != nil {
		title = fmt.Sprintf("%s (%d steps)", truncate(m.Name, 18), len(m.Steps))
	}
	x := drawSubHeaderPill(c, 6, title, fb.ColCyan)
	if a.Macro.Running {
		drawSubHeaderPill(c, x, "RUNNING", fb.ColEmerald)
	} else if a.Macro.EditMode {
		drawSubHeaderPill(c, x, "EDIT", fb.ColAmber)
	}
}

func (a *App) drawMacroSteps(c *fb.Canvas, m *config.Macro) {
	if len(m.Steps) == 0 {
		c.DrawTextCentered(fb.ScreenW/2, 74, "Empty macro (press 6 ADD)", fb.ColMuted, 1)
		return
	}
	for i := range min(len(m.Steps), macroVisibleRows) {
		step := m.Steps[i]
		y := 36 + i*14
		focused := a.Macro.EditMode && i == a.Macro.StepCursor
		active := a.Macro.Running && i == a.Macro.Progress.StepIndex
		if focused || active {
			col := fb.ColCyan
			if active {
				col = fb.ColEmerald
			}
			c.FillRect(6, y-1, 308, 12, fb.ColSurfaceAlt)
			c.DrawRect(6, y-1, 308, 12, col)
		}
		row := fmt.Sprintf("%02d %-14s %-10s %4dms",
			i+1, truncate(step.Remote, 14), truncate(step.Button, 10), step.DelayAfterMs)
		c.DrawText(10, y+1, truncate(row, 49), fb.ColFg, 1)
	}
}

func (a *App) drawMacroProgressBar(c *fb.Canvas, m *config.Macro) {
	c.FillRect(6, 126, 308, 13, fb.ColSurface)
	c.DrawRect(6, 126, 308, 13, fb.ColBorder)
	if a.Macro.Running && a.Macro.Progress.StepTotal > 0 {
		cur := a.Macro.Progress.StepIndex + 1
		fill := (min(cur, a.Macro.Progress.StepTotal) * 306) / a.Macro.Progress.StepTotal
		c.FillRect(7, 127, fill, 11, fb.ColNavActive)
		msg := fmt.Sprintf("Step %d/%d: %s", cur, a.Macro.Progress.StepTotal, truncate(a.Macro.Progress.Button, 18))
		c.DrawText(10, 129, msg, fb.ColEmerald, 1)
		return
	}
	c.DrawText(10, 129, fmt.Sprintf("Macros: %d | Z/C: adjust delay ms", len(a.Macro.File.Macros)), fb.ColMuted, 1)
}
