package ui

import (
	"context"
	"sync/atomic"
	"time"

	"ir-studio/internal/config"
	"ir-studio/internal/dbsync"
	"ir-studio/internal/fb"
	"ir-studio/internal/input"
	"ir-studio/internal/irfile"
	"ir-studio/internal/irml"
	"ir-studio/internal/library"
	"ir-studio/internal/lirc"
)

var uiTXReqCounter atomic.Uint64

// Deps bundles dependency-injected domain services for the UI App coordinator.
type Deps struct {
	Store       *library.Store
	TX          *lirc.TXWorker
	RX          *lirc.RXWorker
	Syncer      *dbsync.Syncer
	MacroRunner *library.MacroRunner
	Settings    config.Settings
	ConfigPath  string
	SourcesPath string
	MacrosPath  string
	SimMode     bool
	DeviceLabel string
}

// App is the 320x170 LCD UI coordinator composing cohesive domain sub-structs.
type App struct {
	Deps    Deps
	Chrome  ChromeState
	RX      RXState
	Remote  RemoteState
	Probe   ProbeState
	Saved   SavedState
	Macro   MacroState
	Set     SettingsState
	reg        *irfile.ProtocolRegistry
	ambient    *irml.AmbientMonitor
	fpIndex    *irml.IRFingerprintIndex
	fpIndexing bool
	fpCancel   context.CancelFunc
	asyncCh    chan asyncEvent
}

// NewApp constructs an App coordinator with injected dependencies.
func NewApp(deps Deps) *App {
	settings := config.Sanitize(deps.Settings)
	app := &App{
		Deps:    deps,
		reg:     irfile.DefaultRegistry(),
		ambient: irml.NewAmbientMonitor(),
		fpIndex: &irml.IRFingerprintIndex{},
		asyncCh: make(chan asyncEvent, 16),
	}
	app.initState(settings)
	app.refreshSaved()
	app.refreshProbePacks()
	app.loadActiveRemote()
	app.loadMacros()
	app.loadSources()
	app.startFingerprintIndexBuild()
	return app
}

func (a *App) initState(settings config.Settings) {
	a.Chrome = ChromeState{Tab: TabRX, SimMode: a.Deps.SimMode, DeviceLabel: a.Deps.DeviceLabel}
	a.RX = RXState{Learning: true, DenoiseOn: true, CarrierIdx: carrierPresetIdx(settings.CarrierHz)}
	a.Remote = RemoteState{RepeatCount: 1, AC: newACControlState()}
	a.Probe = ProbeState{IntervalIdx: intervalPresetIdx(settings.ProbeIntervalMs)}
	a.Saved = SavedState{ScopeUser: true, Selected: make(map[string]bool)}
	a.Macro = MacroState{File: &config.MacroFile{Version: config.MacroVersion}, MacrosPath: a.Deps.MacrosPath}
	a.Set = SettingsState{Settings: settings, ConfigPath: a.Deps.ConfigPath, SourcesPath: a.Deps.SourcesPath}
}

// Close cleanly cancels any background probe, sweep, index, or macro sessions owned by UI.
func (a *App) Close() {
	if a.fpCancel != nil {
		a.fpCancel()
		a.fpCancel = nil
	}
	a.cancelCarrierSweep()
	if a.Probe.Session != nil {
		a.Probe.Session.Pause()
	}
	if a.Deps.MacroRunner != nil {
		a.Deps.MacroRunner.Cancel()
	}
}

// SetToast displays a semantic toast notification for ~2 seconds.
func (a *App) SetToast(msg string, col uint16) {
	a.Chrome.ToastText = msg
	a.Chrome.ToastCol = col
	a.Chrome.ToastTicks = ToastSecs
}

// SwitchTab activates tab t and resets transient confirmation prompts.
func (a *App) SwitchTab(t Tab) {
	a.Chrome.Tab = t % tabCount
	a.RX.GuardPending = false
	a.Remote.ConfirmDel = false
	a.Saved.ConfirmDel = false
	a.Saved.ConfirmPurge = false
	a.Macro.ConfirmDel = false
	a.Set.ConfirmDelPack = false
}

// Step drains non-blocking RX/TX/async events and advances UI timers (~55 ms tick).
func (a *App) Step() {
	a.Chrome.TickCount++
	if a.Chrome.ToastTicks > 0 {
		a.Chrome.ToastTicks--
		if a.Chrome.ToastTicks == 0 {
			a.Chrome.ToastText = ""
		}
	}
	a.drainRX()
	a.drainTX()
	a.drainAsync()
	a.Chrome.Ambient = a.ambient.Snapshot(time.Now().UnixNano())
}

// InjectCapture processes an RXCapture directly (used by live RXWorker, SIM, and shots).
func (a *App) InjectCapture(cap lirc.RXCapture) {
	nowNs := time.Now().UnixNano()
	for _, p := range cap.Pulses {
		a.ambient.RecordPulse(nowNs, p)
	}
	a.Chrome.Ambient = a.ambient.Snapshot(nowNs)
	a.onRXCapture(cap)
}

// HandleKey dispatches a CardputerZero key action. Returns true to exit the app.
func (a *App) HandleKey(act input.KeyAction) bool {
	if act == input.KeyNone {
		return false
	}
	if a.Chrome.HelpOpen {
		return a.handleHelpKey(act)
	}
	if act == input.KeyH {
		a.Chrome.HelpOpen = true
		return false
	}
	if act == input.KeyTab {
		a.SwitchTab((a.Chrome.Tab + 1) % tabCount)
		return false
	}
	if act == input.KeyQ {
		return true
	}
	return a.dispatchTabKey(act)
}

func (a *App) handleHelpKey(act input.KeyAction) bool {
	switch act {
	case input.KeyH, input.KeyEsc, input.KeyEnter:
		a.Chrome.HelpOpen = false
	case input.KeyLeft, input.KeyUp:
		a.Chrome.HelpPage = (a.Chrome.HelpPage + len(helpPages) - 1) % len(helpPages)
	case input.KeyRight, input.KeyDown:
		a.Chrome.HelpPage = (a.Chrome.HelpPage + 1) % len(helpPages)
	}
	return false
}

func (a *App) dispatchTabKey(act input.KeyAction) bool {
	switch a.Chrome.Tab {
	case TabRX:
		return a.handleRXKey(act)
	case TabRemote:
		return a.handleRemoteKey(act)
	case TabProbe:
		return a.handleProbeKey(act)
	case TabSaved:
		return a.handleSavedKey(act)
	case TabMacro:
		return a.handleMacroKey(act)
	case TabSet:
		return a.handleSetKey(act)
	default:
		return false
	}
}

// Render draws the complete 320x170 frame into c.
func (a *App) Render(c *fb.Canvas) {
	c.Clear(fb.ColBg)
	a.drawHeader(c)
	switch a.Chrome.Tab {
	case TabRX:
		a.renderRX(c)
	case TabRemote:
		a.renderRemote(c)
	case TabProbe:
		a.renderProbe(c)
	case TabSaved:
		a.renderSaved(c)
	case TabMacro:
		a.renderMacro(c)
	case TabSet:
		a.renderSet(c)
	}
	if a.Chrome.HelpOpen {
		a.drawHelpModal(c)
	}
	a.drawToast(c)
}

func (a *App) drainRX() {
	if a.Deps.RX == nil {
		return
	}
	for {
		select {
		case cap, ok := <-a.Deps.RX.Captures():
			if !ok {
				return
			}
			a.InjectCapture(cap)
		default:
			return
		}
	}
}

func (a *App) drainTX() {
	if a.Deps.TX == nil {
		return
	}
	for {
		select {
		case ev, ok := <-a.Deps.TX.Events():
			if !ok {
				return
			}
			switch ev.Kind {
			case lirc.TXStarted:
				a.Remote.TXActive = true
			case lirc.TXCompleted, lirc.TXCancelled, lirc.TXError:
				a.Remote.TXActive = false
			}
		default:
			return
		}
	}
}

func (a *App) drainAsync() {
	for {
		select {
		case ev := <-a.asyncCh:
			a.applyAsyncEvent(ev)
		default:
			return
		}
	}
}

func (a *App) applyAsyncEvent(ev asyncEvent) {
	if ev.fpIndex != nil {
		a.fpIndex = ev.fpIndex
		a.fpIndexing = false
	}
	if ev.sweep != nil {
		a.applySweepEvent(ev.sweep)
	}
	if ev.probeSnap != nil {
		a.Probe.Snap = *ev.probeSnap
		if ev.probeSnap.State == library.ProbeDone {
			a.SetToast("Probe sweep complete", fb.ColEmerald)
		}
	}
	if ev.macroProg != nil {
		a.Macro.Progress = *ev.macroProg
		if ev.macroProg.Done {
			a.Macro.Running = false
			if ev.macroProg.Cancelled {
				a.SetToast("Macro cancelled", fb.ColAmber)
			} else if ev.macroProg.Err != nil {
				a.SetToast(truncate(ev.macroProg.Err.Error(), 36), fb.ColRed)
			} else {
				a.SetToast("Macro complete", fb.ColEmerald)
			}
		}
	}
	if ev.syncDone {
		a.Set.Syncing = false
		a.Set.SyncStatus = ev.syncMsg
		if ev.syncErr != nil {
			a.SetToast("Sync error: "+truncate(ev.syncErr.Error(), 24), fb.ColRed)
		} else {
			a.SetToast(ev.syncMsg, fb.ColEmerald)
			a.refreshSaved()
			a.refreshProbePacks()
			a.startFingerprintIndexBuild()
		}
	}
}

func (a *App) submitTX(sig irfile.IRSignal, repeat int) error {
	if a.Deps.TX == nil {
		a.Remote.TXActive = false
		return nil
	}
	if sig.Frequency == 0 {
		sig.Frequency = a.Set.Settings.CarrierHz
	}
	if sig.DutyCycle == 0 {
		sig.DutyCycle = float32(a.Set.Settings.DutyCyclePct) / 100.0
	}
	return a.Deps.TX.Submit(lirc.TXRequest{
		ID:     uiTXReqCounter.Add(1),
		Signal: sig,
		Repeat: max(repeat, 1),
		Ctx:    context.Background(),
	})
}

func carrierPresetIdx(hz uint32) int {
	for i, v := range config.AllowedCarrierHz {
		if v == hz {
			return i
		}
	}
	return 1
}

func intervalPresetIdx(ms int) int {
	best := 3
	for i, d := range library.ProbeIntervalPresets {
		if int(d.Milliseconds()) == ms {
			return i
		}
	}
	return best
}
