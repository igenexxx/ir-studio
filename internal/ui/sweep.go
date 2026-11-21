package ui

import (
	"context"
	"fmt"

	"ir-studio/internal/fb"
	"ir-studio/internal/irfile"
	"ir-studio/internal/lirc"
)

func (a *App) startCarrierSweep(sig irfile.IRSignal) {
	if a.Deps.TX == nil || a.Deps.TX.Transceiver() == nil {
		a.SetToast("Transceiver unavailable", fb.ColRed)
		return
	}
	a.cancelCarrierSweep()
	ctx, cancel := context.WithCancel(context.Background())
	a.Chrome.Sweep = SweepState{
		Active: true,
		Total:  len(lirc.SweepCarrierPresets),
		FreqHz: lirc.SweepCarrierPresets[0],
		Cancel: cancel,
	}
	a.SetToast(fmt.Sprintf("SWEEP %dkHz (1/%d)", lirc.SweepCarrierPresets[0]/1000, len(lirc.SweepCarrierPresets)), fb.ColCyan)

	xcvr := a.Deps.TX.Transceiver()
	reg := a.reg
	duty := a.Set.Settings.DutyCyclePct
	go func() {
		defer cancel()
		report, err := lirc.SweepCarrier(ctx, xcvr, reg, sig, nil, duty, lirc.DefaultSweepGap, func(idx int, hz uint32) {
			a.pushSweepEvent(&sweepAsync{
				stepIdx: idx,
				total:   len(lirc.SweepCarrierPresets),
				freqHz:  hz,
			})
		})
		a.pushSweepEvent(&sweepAsync{
			done:      true,
			cancelled: report.Cancelled,
			err:       err,
		})
	}()
}

func (a *App) cancelCarrierSweep() bool {
	if !a.Chrome.Sweep.Active {
		return false
	}
	if a.Chrome.Sweep.Cancel != nil {
		a.Chrome.Sweep.Cancel()
	}
	a.Chrome.Sweep.Active = false
	a.Chrome.Sweep.Cancel = nil
	a.SetToast("Carrier sweep cancelled", fb.ColAmber)
	return true
}

func (a *App) pushSweepEvent(ev *sweepAsync) {
	select {
	case a.asyncCh <- asyncEvent{kind: "sweep", sweep: ev}:
	default:
	}
}

func (a *App) applySweepEvent(ev *sweepAsync) {
	if ev == nil {
		return
	}
	if ev.done {
		a.Chrome.Sweep.Active = false
		a.Chrome.Sweep.Cancel = nil
		switch {
		case ev.cancelled:
			a.SetToast("Sweep cancelled (38kHz restored)", fb.ColAmber)
		case ev.err != nil:
			a.SetToast("Sweep err: "+truncate(ev.err.Error(), 24), fb.ColRed)
		default:
			a.SetToast("Sweep done (36/38/40/56kHz)", fb.ColEmerald)
		}
		return
	}
	a.Chrome.Sweep.StepIdx = ev.stepIdx
	a.Chrome.Sweep.Total = ev.total
	a.Chrome.Sweep.FreqHz = ev.freqHz
	a.SetToast(fmt.Sprintf("SWEEP %dkHz (%d/%d)", ev.freqHz/1000, ev.stepIdx+1, ev.total), fb.ColCyan)
}
