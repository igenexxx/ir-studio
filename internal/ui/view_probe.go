package ui

import (
	"context"
	"fmt"

	"ir-studio/internal/fb"
	"ir-studio/internal/input"
	"ir-studio/internal/irml"
	"ir-studio/internal/library"
)

const probeVisibleRows = 6

func (a *App) refreshProbePacks() {
	if a.Deps.Store == nil {
		return
	}
	packs := []string{""} // "" represents USER library
	if dbPacks, err := a.Deps.Store.ListDatabasePacks(); err == nil {
		for _, p := range dbPacks {
			packs = append(packs, p.ID)
		}
	}
	a.Probe.Packs = packs
	if len(packs) > 1 && a.Probe.PackIdx == 0 {
		a.Probe.PackIdx = 1 // prefer first DB pack when available
	}
	if a.Probe.PackIdx >= len(packs) {
		a.Probe.PackIdx = 0
	}
	a.reloadProbeCandidates()
}

func (a *App) reloadProbeCandidates() {
	if a.Deps.Store == nil || len(a.Probe.Packs) == 0 {
		return
	}
	if a.Probe.Session != nil {
		a.Probe.Session.Pause()
	}
	pack := a.Probe.Packs[a.Probe.PackIdx]
	actions := library.DefaultProbeActions()
	act := actions[a.Probe.ActionIdx%len(actions)]
	cands, _, _ := library.FilterPackCandidates(a.Deps.Store, pack, act)
	a.Probe.Candidates = cands
	a.Probe.SmartRanked = false
	a.Probe.Scroll = 0
	interval := library.ProbeIntervalPresets[a.Probe.IntervalIdx%len(library.ProbeIntervalPresets)]
	a.Probe.Session = library.NewProbeSession(cands, interval)
	a.Probe.Snap = a.Probe.Session.Snapshot()
}

func (a *App) handleProbeKey(act input.KeyAction) bool {
	switch act {
	case input.KeySoft4:
		a.toggleProbeRun()
	case input.KeySpace:
		if a.Probe.Snap.State == library.ProbeRunning {
			a.stopProbe()
		} else {
			a.tryProbeCandidate()
		}
	case input.KeyEnter, input.KeySoft5:
		a.tryProbeCandidate()
	case input.KeySoft6:
		a.promoteProbeWinner()
	case input.KeySoft7:
		a.cycleProbeAction()
	case input.KeySoft8:
		a.cycleProbePack()
	case input.KeyUp:
		a.moveProbeCursor(-1)
	case input.KeyDown:
		a.moveProbeCursor(1)
	case input.KeyLeft:
		a.adjustProbeInterval(-1)
	case input.KeyRight:
		a.adjustProbeInterval(1)
	case input.KeyR:
		a.applySmartProbeRank()
	case input.KeyEsc:
		a.stopProbe()
	}
	return false
}

func (a *App) toggleProbeRun() {
	if a.Probe.Session == nil {
		a.SetToast("No probe candidates", fb.ColAmber)
		return
	}
	if a.Probe.Snap.State == library.ProbeRunning {
		a.stopProbe()
		return
	}
	if a.Deps.TX == nil {
		a.SetToast("TX worker unavailable", fb.ColRed)
		return
	}
	err := a.Probe.Session.Start(context.Background(), a.Deps.TX, func(snap library.ProbeSnapshot) {
		s := snap
		select {
		case a.asyncCh <- asyncEvent{kind: "probe", probeSnap: &s}:
		default:
		}
	})
	if err != nil {
		a.SetToast("Probe: "+truncate(err.Error(), 26), fb.ColAmber)
		return
	}
	a.Probe.Snap = a.Probe.Session.Snapshot()
	a.SetToast("Probe running...", fb.ColCyan)
}

func (a *App) stopProbe() {
	if a.Probe.Session != nil && a.Probe.Session.State() == library.ProbeRunning {
		a.Probe.Session.Pause()
		a.Probe.Snap = a.Probe.Session.Snapshot()
		a.SetToast("Probe paused", fb.ColAmber)
	}
}

func (a *App) tryProbeCandidate() {
	if a.Probe.Session == nil || len(a.Probe.Candidates) == 0 {
		a.SetToast("No candidates to try", fb.ColAmber)
		return
	}
	a.stopProbe()
	idx := min(max(a.Probe.Snap.Cursor, 0), len(a.Probe.Candidates)-1)
	cand := a.Probe.Candidates[idx]
	if err := a.submitTX(cand.Signal, 1); err != nil {
		a.SetToast("TX err: "+truncate(err.Error(), 24), fb.ColRed)
		return
	}
	a.Probe.Session.RecordSent(idx)
	a.Probe.Snap = a.Probe.Session.Snapshot()
	a.SetToast("TRY "+truncate(cand.Remote.Name, 20), fb.ColEmerald)
}

func (a *App) promoteProbeWinner() {
	if a.Deps.Store == nil || len(a.Probe.Candidates) == 0 {
		a.SetToast("No candidate selected", fb.ColAmber)
		return
	}
	idx := min(max(a.Probe.Snap.Cursor, 0), len(a.Probe.Candidates)-1)
	cand := a.Probe.Candidates[idx]
	if !cand.Remote.ReadOnly {
		a.SetToast("Already in USER library", fb.ColAmber)
		return
	}
	promoted, err := a.Deps.Store.PromoteToUser(cand.Remote, "")
	if err != nil {
		a.SetToast("Promote err: "+truncate(err.Error(), 22), fb.ColRed)
		return
	}
	a.refreshSaved()
	a.SetToast("Saved to USER: "+truncate(promoted.Name, 18), fb.ColEmerald)
}

func (a *App) cycleProbeAction() {
	actions := library.DefaultProbeActions()
	a.Probe.ActionIdx = (a.Probe.ActionIdx + 1) % len(actions)
	a.reloadProbeCandidates()
	a.SetToast("Action: "+string(actions[a.Probe.ActionIdx]), fb.ColCyan)
}

func (a *App) cycleProbePack() {
	if len(a.Probe.Packs) == 0 {
		return
	}
	a.Probe.PackIdx = (a.Probe.PackIdx + 1) % len(a.Probe.Packs)
	a.reloadProbeCandidates()
	a.SetToast("Pack: "+a.probePackLabel(), fb.ColCyan)
}

func (a *App) moveProbeCursor(delta int) {
	if a.Probe.Session == nil || len(a.Probe.Candidates) == 0 {
		return
	}
	a.stopProbe()
	next := min(max(a.Probe.Snap.Cursor+delta, 0), len(a.Probe.Candidates)-1)
	a.Probe.Session.SetCursor(next)
	a.Probe.Snap = a.Probe.Session.Snapshot()
	if next < a.Probe.Scroll {
		a.Probe.Scroll = next
	}
	if next >= a.Probe.Scroll+probeVisibleRows {
		a.Probe.Scroll = next - probeVisibleRows + 1
	}
}

func (a *App) adjustProbeInterval(delta int) {
	n := len(library.ProbeIntervalPresets)
	a.Probe.IntervalIdx = min(max(a.Probe.IntervalIdx+delta, 0), n-1)
	d := library.ProbeIntervalPresets[a.Probe.IntervalIdx]
	if a.Probe.Session != nil {
		a.Probe.Session.SetInterval(d)
		a.Probe.Snap = a.Probe.Session.Snapshot()
	}
	a.SetToast(fmt.Sprintf("Interval: %.1fs", d.Seconds()), fb.ColCyan)
}

func (a *App) applySmartProbeRank() {
	if a.Probe.Session == nil || len(a.Probe.Candidates) == 0 {
		a.SetToast("No candidates to rank", fb.ColAmber)
		return
	}
	if a.RX.LastCap == nil || len(a.RX.LastCap.Pulses) == 0 {
		a.SetToast("Capture a signal in RX first", fb.ColAmber)
		return
	}
	a.stopProbe()
	emb := irml.ExtractEmbedding(a.RX.LastCap.Pulses, a.RX.LastCap.CarrierEstHz)
	a.Probe.Session.RankByEmbedding(emb, a.RX.LastCap.CarrierEstHz, a.reg)
	a.Probe.Candidates = a.Probe.Session.Candidates()
	a.Probe.Snap = a.Probe.Session.Snapshot()
	a.Probe.SmartRanked = true
	a.Probe.Scroll = 0
	a.SetToast("Smart Ranked by 8D IR match", fb.ColMagenta)
}

func (a *App) probePackLabel() string {
	if len(a.Probe.Packs) == 0 || a.Probe.Packs[a.Probe.PackIdx] == "" {
		return "USER"
	}
	if a.Probe.Packs[a.Probe.PackIdx] == library.VirtualPackTVBGone {
		return "GEN:TVBG"
	}
	return a.Probe.Packs[a.Probe.PackIdx]
}

func (a *App) renderProbe(c *fb.Canvas) {
	a.drawProbeHeader(c)
	a.drawProbeProgress(c)
	a.drawProbeList(c)
	runLbl := "RUN"
	if a.Probe.Snap.State == library.ProbeRunning {
		runLbl = "STOP"
	}
	a.drawNavBar(c, [5]string{runLbl, "TRY", "SAVE", "ACT", "PACK"})
}

func (a *App) drawProbeHeader(c *fb.Canvas) {
	actions := library.DefaultProbeActions()
	act := actions[a.Probe.ActionIdx%len(actions)]
	x := drawSubHeaderPill(c, 6, truncate(a.probePackLabel(), 10), fb.ColCyan)
	x = drawSubHeaderPill(c, x, string(act), fb.ColFg)
	stCol := fb.ColMuted
	switch a.Probe.Snap.State {
	case library.ProbeRunning:
		stCol = fb.ColEmerald
	case library.ProbePaused:
		stCol = fb.ColAmber
	case library.ProbeDone:
		stCol = fb.ColCyan
	}
	x = drawSubHeaderPill(c, x, a.Probe.Snap.State.String(), stCol)
	if a.Probe.SmartRanked {
		drawSubHeaderPill(c, x, "RANK", fb.ColMagenta)
	}
}

func (a *App) drawProbeProgress(c *fb.Canvas) {
	snap := a.Probe.Snap
	d := library.ProbeIntervalPresets[a.Probe.IntervalIdx%len(library.ProbeIntervalPresets)]
	meta := fmt.Sprintf("%03d/%03d  INTV:%.1fs  [R]Rank", snap.SentCount, snap.Total, d.Seconds())
	c.DrawText(6, 35, meta, fb.ColMuted, 1)

	barX, barY, barW, barH := 6, 45, 308, 6
	c.FillRect(barX, barY, barW, barH, fb.ColSurfaceAlt)
	c.DrawRect(barX, barY, barW, barH, fb.ColBorder)
	if snap.Total > 0 {
		fill := (min(snap.SentCount, snap.Total) * (barW - 2)) / snap.Total
		c.FillRect(barX+1, barY+1, fill, barH-2, fb.ColCyan)
	}
}

func (a *App) drawProbeList(c *fb.Canvas) {
	if len(a.Probe.Candidates) == 0 {
		c.DrawTextCentered(fb.ScreenW/2, 86, "No matching signals in pack", fb.ColMuted, 1)
		return
	}
	for row := range probeVisibleRows {
		idx := a.Probe.Scroll + row
		if idx >= len(a.Probe.Candidates) {
			break
		}
		a.drawProbeRow(c, idx, 56+row*14)
	}
}

func (a *App) drawProbeRow(c *fb.Canvas, idx, y int) {
	cand := a.Probe.Candidates[idx]
	focused := idx == a.Probe.Snap.Cursor
	marker := byte(' ')
	if a.Probe.Session != nil {
		marker = a.Probe.Session.MarkerFor(idx)
	}
	if focused {
		c.FillRect(6, y-1, 308, 12, fb.ColSurfaceAlt)
		c.DrawRect(6, y-1, 308, 12, fb.ColCyan)
	}
	mCol := fb.ColMuted
	if marker == '>' {
		mCol = fb.ColEmerald
	} else if marker == '*' {
		mCol = fb.ColAmber
	}
	c.DrawText(10, y+1, string(marker), mCol, 1)
	line := fmt.Sprintf("%s:%s", truncate(cand.Remote.Name, 18), truncate(cand.Signal.Name, 10))
	c.DrawText(20, y+1, line, fb.ColFg, 1)
	if a.Probe.SmartRanked && cand.Score > 0 {
		score := fmt.Sprintf("%2.0f%%", cand.Score*100)
		c.DrawText(286, y+1, score, fb.ColMagenta, 1)
	} else {
		proto := cand.Signal.Protocol
		if proto == "" {
			proto = "RAW"
		}
		c.DrawText(266, y+1, truncate(proto, 7), fb.ColMuted, 1)
	}
}
