package ui

import (
	"context"
	"fmt"

	"ir-studio/internal/fb"
	"ir-studio/internal/irfile"
	"ir-studio/internal/irml"
	"ir-studio/internal/library"
)

const maxIndexedSignalsPerRemote = 3

func (a *App) startFingerprintIndexBuild() {
	if a.fpCancel != nil {
		a.fpCancel()
		a.fpCancel = nil
	}
	if a.Deps.Store == nil {
		a.fpIndex = &irml.IRFingerprintIndex{}
		a.fpIndexing = false
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.fpCancel = cancel
	a.fpIndexing = true
	store := a.Deps.Store
	reg := a.reg
	ch := a.asyncCh

	go func() {
		idx := buildFingerprintIndex(ctx, store, reg)
		if ctx.Err() != nil {
			return
		}
		select {
		case ch <- asyncEvent{kind: "fp", fpIndex: idx}:
		case <-ctx.Done():
		}
	}()
}

func buildFingerprintIndex(ctx context.Context, store *library.Store, reg *irfile.ProtocolRegistry) *irml.IRFingerprintIndex {
	idx := &irml.IRFingerprintIndex{}
	packs, err := store.ListDatabasePacks()
	if err != nil {
		return idx
	}
	for _, pack := range packs {
		if ctx.Err() != nil {
			return idx
		}
		entries, err := store.ListPackRemotes(pack.ID)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if ctx.Err() != nil {
				return idx
			}
			indexRemoteEntry(ctx, idx, store, reg, pack.ID, e)
		}
	}
	return idx
}

func indexRemoteEntry(ctx context.Context, idx *irml.IRFingerprintIndex, store *library.Store, reg *irfile.ProtocolRegistry, packID string, e library.RemoteEntry) {
	loaded, err := store.LoadRemote(e)
	if err != nil {
		return
	}
	added := 0
	for _, sig := range loaded.Signals {
		if ctx.Err() != nil || added >= maxIndexedSignalsPerRemote {
			return
		}
		pulses, carrier := signalToPulsesWithReg(reg, sig)
		if len(pulses) == 0 {
			continue
		}
		idx.Add(irml.FingerprintEntry{
			RemoteID:   loaded.RelPath,
			Pack:       packID,
			SignalName: sig.Name,
			CarrierHz:  carrier,
			Embedding:  irml.ExtractEmbedding(pulses, carrier),
		})
		added++
	}
}

func (a *App) signalToPulses(sig irfile.IRSignal) (irfile.PulseTrain, uint32) {
	return signalToPulsesWithReg(a.reg, sig)
}

func signalToPulsesWithReg(reg *irfile.ProtocolRegistry, sig irfile.IRSignal) (irfile.PulseTrain, uint32) {
	carrier := sig.CarrierSpec().FreqHz
	if sig.Kind == irfile.SignalRaw {
		p, err := irfile.FromRawData(sig.RawData)
		if err != nil {
			return nil, 0
		}
		return p, carrier
	}
	p, err := reg.Encode(sig)
	if err != nil {
		return nil, 0
	}
	return p, carrier
}

func (a *App) updateACDiff(pulses irfile.PulseTrain) {
	if !a.RX.AC.Active || len(pulses) == 0 {
		return
	}
	frame := irml.ParseACFrame(pulses)
	if !a.RX.AC.HasBase {
		a.RX.AC.Baseline = frame
		a.RX.AC.HasBase = true
		a.RX.AC.Current = frame
		a.RX.AC.Diff = irml.ACDiffResult{}
		return
	}
	a.RX.AC.Current = frame
	a.RX.AC.Diff = irml.DiffACFrames(a.RX.AC.Baseline, frame)
}

func (a *App) promoteSelectedMatch() {
	if a.Deps.Store == nil || len(a.RX.Matches) == 0 {
		return
	}
	m := a.RX.Matches[min(a.RX.MatchCursor, len(a.RX.Matches)-1)]
	entries, err := a.Deps.Store.ListPackRemotes(m.Pack)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.RelPath == m.RemoteID {
			promoted, pErr := a.Deps.Store.PromoteToUser(e, "")
			if pErr != nil {
				a.SetToast("Promote err: "+truncate(pErr.Error(), 22), fb.ColRed)
				return
			}
			a.refreshSaved()
			a.SetToast("Promoted "+truncate(promoted.Name, 20), fb.ColEmerald)
			return
		}
	}
}

func (a *App) drawRXMatchList(c *fb.Canvas) {
	c.FillRect(6, 103, 308, 37, fb.ColSurface)
	c.DrawRect(6, 103, 308, 37, fb.ColBorder)
	if len(a.RX.Matches) == 0 {
		hint := "k-NN Match: sync DB packs for hints"
		if a.fpIndexing {
			hint = "k-NN Match: indexing DB in background..."
		}
		c.DrawText(10, 117, hint, fb.ColMuted, 1)
		return
	}
	for i, m := range a.RX.Matches {
		if i >= 3 {
			break
		}
		y := 106 + i*11
		fg := fb.ColFg
		if i == a.RX.MatchCursor {
			c.FillRect(7, y-1, 306, 10, fb.ColSurfaceAlt)
			fg = fb.ColCyan
		}
		row := fmt.Sprintf("%d.%-16s %2.0f%% [Ent:Promo]", i+1, truncate(m.RemoteID, 16), m.Similarity*100)
		c.DrawText(10, y, truncate(row, 48), fg, 1)
	}
}

func (a *App) drawACDiffView(c *fb.Canvas) {
	c.FillRect(6, 34, 308, 106, fb.ColSurface)
	c.DrawRect(6, 34, 308, 106, fb.ColMagenta)
	c.DrawText(10, 38, "AC STATE-FRAME DIFF (Base vs Cur)", fb.ColMagenta, 1)
	if !a.RX.AC.Baseline.Valid {
		c.DrawText(10, 56, "Capture an AC frame to inspect/diff", fb.ColMuted, 1)
		return
	}
	chk, chkCol := "CHK:UNK", fb.ColMuted
	if a.RX.AC.Current.ChecksumValid {
		chk, chkCol = "CHK:SUM8 OK", fb.ColEmerald
	}
	meta := fmt.Sprintf("SF:%d Bits:%d %s", len(a.RX.AC.Current.SubFrames), a.RX.AC.Current.TotalBits, chk)
	c.DrawText(10, 50, meta, chkCol, 1)
	if len(a.RX.AC.Diff.Diffs) == 0 {
		c.DrawText(10, 68, "Frames identical (0 bits changed)", fb.ColEmerald, 1)
		return
	}
	for i, d := range a.RX.AC.Diff.Diffs {
		if i >= 5 {
			break
		}
		row := fmt.Sprintf("SF%d[%02d]: %02X -> %02X (XOR %02X)",
			d.SubFrameIndex+1, d.ByteIndex, d.Before, d.After, d.BitMask)
		c.DrawText(10, 64+i*13, row, fb.ColAmber, 1)
	}
}
