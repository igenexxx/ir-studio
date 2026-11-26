package ui

import (
	"context"
	"os"
	"path/filepath"
	"testing"
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

func newTestApp(t *testing.T) (*App, *lirc.SimTransport) {
	t.Helper()
	tmp := t.TempDir()
	userDir := filepath.Join(tmp, "remotes")
	dbDir := filepath.Join(tmp, "databases")
	store := library.NewStore(userDir, dbDir)

	// Seed a USER remote and a RO DB pack remote.
	necSig := irfile.IRSignal{
		Name:     "Power",
		Kind:     irfile.SignalParsed,
		Protocol: "NEC",
		Address:  [4]byte{0x04, 0x00, 0x00, 0x00},
		Command:  [4]byte{0x08, 0x00, 0x00, 0x00},
	}
	volSig := irfile.IRSignal{
		Name:     "Vol_up",
		Kind:     irfile.SignalParsed,
		Protocol: "NEC",
		Address:  [4]byte{0x04, 0x00, 0x00, 0x00},
		Command:  [4]byte{0x02, 0x00, 0x00, 0x00},
	}
	if _, err := store.SaveUserRemote("Samsung_TV.ir", &irfile.IRFile{
		Version: 1,
		Signals: []irfile.IRSignal{necSig, volSig},
	}); err != nil {
		t.Fatalf("seed user remote: %v", err)
	}
	dbPackDir := filepath.Join(dbDir, "TVs")
	if err := os.MkdirAll(dbPackDir, 0o755); err != nil {
		t.Fatalf("mkdir db pack: %v", err)
	}
	dbRemote := &irfile.IRFile{Version: 1, Signals: []irfile.IRSignal{necSig, volSig}}
	if err := irfile.SaveFileAtomic(filepath.Join(dbPackDir, "LG_AKB7509.ir"), dbRemote); err != nil {
		t.Fatalf("seed db remote: %v", err)
	}

	sim := lirc.NewSimTransport()
	gate := lirc.NewTransceiverGate(20 * time.Millisecond)
	xcvr := lirc.NewTransceiver(sim, gate)
	tx := lirc.NewTXWorker(xcvr, irfile.DefaultRegistry())
	tx.Start(context.Background())
	t.Cleanup(func() {
		tx.Close()
		_ = sim.Close()
	})

	app := NewApp(Deps{
		Store:       store,
		TX:          tx,
		Syncer:      dbsync.NewSyncer(dbDir, nil),
		MacroRunner: library.NewMacroRunner(store, tx),
		Settings:    config.DefaultSettings(),
		ConfigPath:  filepath.Join(tmp, "config.json"),
		SourcesPath: filepath.Join(tmp, "sources.json"),
		MacrosPath:  filepath.Join(tmp, "macros.json"),
		SimMode:     true,
		DeviceLabel: "SIM",
	})
	t.Cleanup(app.Close)
	return app, sim
}

func TestAllSixTabsRenderAndSoftkeyGeometry(t *testing.T) {
	app, _ := newTestApp(t)
	c, err := fb.NewCanvas("")
	if err != nil {
		t.Fatalf("NewCanvas: %v", err)
	}
	defer c.Close()

	// Inject a realistic NEC capture so RX has waveform + decode + k-NN matches.
	reg := irfile.DefaultRegistry()
	pulses, err := reg.Encode(irfile.IRSignal{
		Name: "Power", Kind: irfile.SignalParsed, Protocol: "NEC",
		Address: [4]byte{0x04}, Command: [4]byte{0x08},
	})
	if err != nil {
		t.Fatalf("encode nec: %v", err)
	}
	sig, _ := reg.Decode(pulses)
	app.InjectCapture(lirc.RXCapture{
		Timestamp:    time.Now(),
		Pulses:       pulses,
		Decoded:      &sig,
		CarrierEstHz: 38000,
	})

	for tab := range tabCount {
		app.SwitchTab(tab)
		app.Step()
		app.Render(c)
		// Verify softkey bar tiles are drawn at canonical X centers.
		for _, cx := range NavKeyCentersX {
			if px := c.PixelAt(cx-NavTileW/2+4, NavKeyY-NavTileH/2+4); px == fb.ColBg {
				t.Fatalf("tab %v: missing softkey badge at cx=%d", tab, cx)
			}
		}
	}
}

func TestHelpModalPages(t *testing.T) {
	app, _ := newTestApp(t)
	c, _ := fb.NewCanvas("")

	app.HandleKey(input.KeyH)
	if !app.Chrome.HelpOpen {
		t.Fatalf("expected HelpOpen after KeyH")
	}
	pages := app.ActiveScreenHelp().PageCount()
	for page := range pages {
		if app.Chrome.HelpPage != page {
			t.Fatalf("HelpPage = %d, want %d", app.Chrome.HelpPage, page)
		}
		app.Render(c)
		app.HandleKey(input.KeyRight)
	}
	if app.Chrome.HelpPage != 0 {
		t.Fatalf("expected wrap to page 0, got %d", app.Chrome.HelpPage)
	}
	app.HandleKey(input.KeyEsc)
	if app.Chrome.HelpOpen {
		t.Fatalf("expected HelpOpen=false after KeyEsc")
	}
}

func TestRXSmartSaveGuardAndACDiff(t *testing.T) {
	app, _ := newTestApp(t)
	app.Set.Settings.ExpertMode = true
	app.SwitchTab(TabRX)
	// Inject ambient noise pulses to trigger Smart Save Guard.
	noise := irfile.PulseTrain{100, -100, 120, -90, 80, -110, 95, -105, 110, -85}
	app.InjectCapture(lirc.RXCapture{
		Timestamp:    time.Now(),
		Pulses:       noise,
		CarrierEstHz: 38000,
	})
	app.RX.Class.IsNoise = true
	app.RX.Class.SaveGuardWarn = true

	app.HandleKey(input.KeySoft7)
	if !app.RX.GuardPending {
		t.Fatalf("expected Smart Save Guard prompt on noise capture")
	}
	// Cancel guard with Esc.
	app.HandleKey(input.KeyEsc)
	if app.RX.GuardPending {
		t.Fatalf("expected GuardPending cleared on Esc")
	}

	// Toggle AC Diff mode & carrier cycle.
	app.HandleKey(input.KeySoft6)
	if !app.RX.AC.Active {
		t.Fatalf("expected AC Diff active after KeySoft6")
	}
	prevHz := app.Set.Settings.CarrierHz
	app.HandleKey(input.KeySoft8)
	if app.Set.Settings.CarrierHz == prevHz {
		t.Fatalf("expected CarrierHz to cycle on KeySoft8")
	}
}

func TestSavedMultiSelectAndReadOnlyDBProtection(t *testing.T) {
	app, _ := newTestApp(t)
	app.Set.Settings.ExpertMode = true
	app.SwitchTab(TabSaved)

	// Select first USER remote with Key1.
	app.HandleKey(input.Key1)
	if len(app.Saved.Selected) != 1 {
		t.Fatalf("selected count = %d, want 1", len(app.Saved.Selected))
	}

	// Switch to DB scope (KeySoft8) and verify DEL is refused.
	app.HandleKey(input.KeySoft8)
	if app.Saved.ScopeUser {
		t.Fatalf("expected DB scope after KeySoft8")
	}
	beforeDB := len(app.Saved.DBEntries)
	app.HandleKey(input.KeySoft7)
	if app.Saved.ConfirmDel {
		t.Fatalf("RO DB pack must NEVER enter ConfirmDel")
	}
	if len(app.Saved.DBEntries) != beforeDB || beforeDB == 0 {
		t.Fatalf("DBEntries modified: got %d, want %d", len(app.Saved.DBEntries), beforeDB)
	}

	// Promote DB remote to USER via KeySoft6.
	app.HandleKey(input.KeySoft6)
	if len(app.Saved.UserEntries) < 2 {
		t.Fatalf("expected promoted remote in UserEntries, got %d", len(app.Saved.UserEntries))
	}
}

func TestProbeSmartRankAndRewind(t *testing.T) {
	app, _ := newTestApp(t)
	reg := irfile.DefaultRegistry()
	pulses, _ := reg.Encode(irfile.IRSignal{
		Name: "Power", Kind: irfile.SignalParsed, Protocol: "NEC",
		Address: [4]byte{0x04}, Command: [4]byte{0x08},
	})
	// AmbientMonitor starts CLEAN before any pulse edges arrive.
	if app.Chrome.Ambient.Level != irml.AmbientClean {
		t.Fatalf("initial Ambient.Level = %v, want CLEAN", app.Chrome.Ambient.Level)
	}
	app.InjectCapture(lirc.RXCapture{Timestamp: time.Now(), Pulses: pulses, CarrierEstHz: 38000})

	app.SwitchTab(TabProbe)
	app.HandleKey(input.KeyR)
	if !app.Probe.SmartRanked {
		t.Fatalf("expected SmartRanked=true after KeyR")
	}
	app.HandleKey(input.KeySoft5) // TRY
	if app.Probe.Snap.LastSentIdx != 0 {
		t.Fatalf("LastSentIdx = %d, want 0", app.Probe.Snap.LastSentIdx)
	}
	// Verify '>' marker.
	if m := app.Probe.Session.MarkerFor(0); m != '>' {
		t.Fatalf("MarkerFor(0) = %q, want '>'", m)
	}
}

func TestMacroAndSettingsInteractions(t *testing.T) {
	app, _ := newTestApp(t)
	app.SwitchTab(TabMacro)
	app.HandleKey(input.KeySoft5) // NEW
	app.HandleKey(input.KeySoft6) // ADD step
	if len(app.Macro.File.Macros) != 1 || len(app.Macro.File.Macros[0].Steps) != 1 {
		t.Fatalf("expected 1 macro with 1 step")
	}
	app.HandleKey(input.KeySoft8) // EDIT
	app.HandleKey(input.KeyRight) // +100ms delay
	if app.Macro.File.Macros[0].Steps[0].DelayAfterMs != 600 {
		t.Fatalf("delay = %d, want 600", app.Macro.File.Macros[0].Steps[0].DelayAfterMs)
	}

	app.SwitchTab(TabSet)
	if app.Set.Settings.MCPEnabled {
		t.Fatalf("MCP must default to OFF")
	}
	if app.Set.Settings.ExpertMode {
		t.Fatalf("ExpertMode must default to OFF")
	}
	// Row 0 is Expert Mode, Row 1 is MCP Server (SSH).
	app.HandleKey(input.KeyEnter) // toggle Expert Mode ON
	if !app.Set.Settings.ExpertMode {
		t.Fatalf("expected ExpertMode ON after row 0 toggle")
	}
	app.HandleKey(input.KeyDown)  // move to MCP Server
	app.HandleKey(input.KeyEnter) // toggle MCP ON
	if !app.Set.Settings.MCPEnabled {
		t.Fatalf("expected MCP ON after toggle")
	}
	app.HandleKey(input.KeySoft6) // Manage DB Sources sub-view (Expert Mode)
	if !app.Set.SubViewSources {
		t.Fatalf("expected SubViewSources=true")
	}
	c, _ := fb.NewCanvas("")
	app.Render(c)
	app.HandleKey(input.KeyEsc)
	if app.Set.SubViewSources {
		t.Fatalf("expected SubViewSources=false after Esc")
	}
}

func TestACControllerSubViewAndPersistence(t *testing.T) {
	app, sim := newTestApp(t)
	c, _ := fb.NewCanvas("")

	app.SwitchTab(TabAC)

	// Z/C temperature clamping [16..30].
	for range 20 {
		app.HandleKey(input.KeyRight)
	}
	if app.Remote.AC.TempC != irfile.ACMaxTempC {
		t.Fatalf("TempC max clamp got %d, want %d", app.Remote.AC.TempC, irfile.ACMaxTempC)
	}
	for range 20 {
		app.HandleKey(input.KeyLeft)
	}
	if app.Remote.AC.TempC != irfile.ACMinTempC {
		t.Fatalf("TempC min clamp got %d, want %d", app.Remote.AC.TempC, irfile.ACMinTempC)
	}
	// Dial to 24C, cycle mode, brand, fan, swing, render, send, save.
	for range 8 {
		app.HandleKey(input.KeyRight)
	}
	app.HandleKey(input.KeyDown)  // HEAT
	app.HandleKey(input.KeyR)     // Midea
	app.HandleKey(input.KeySoft5) // FAN LOW
	app.HandleKey(input.KeySoft6) // SWING ON
	app.Render(c)

	app.HandleKey(input.KeySoft7) // SEND
	app.HandleKey(input.KeySoft8) // SAVE -> AC_Midea.ir
	if _, err := app.Deps.Store.LoadUserRemote("AC_Midea.ir"); err != nil {
		t.Fatalf("expected AC_Midea.ir saved in UserLibrary: %v", err)
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for len(sim.TxHistory()) == 0 && time.Now().Before(deadline) {
		app.Step()
		time.Sleep(10 * time.Millisecond)
	}
	if len(sim.TxHistory()) == 0 {
		t.Fatalf("expected AC state frame transmitted")
	}

	app.HandleKey(input.KeyEsc)
	if app.Chrome.Tab != TabHome {
		t.Fatalf("expected TabHome after Esc from TabAC, got %v", app.Chrome.Tab)
	}
}

func TestCarrierSweepAndTVBGoneDefaultProbe(t *testing.T) {
	app, sim := newTestApp(t)

	// Fresh probe defaults to GEN:TV-B-Gone with 100 Power candidates.
	app.SwitchTab(TabProbe)
	if app.probePackLabel() != "GEN:TVBG" || len(app.Probe.Candidates) != library.TVBGoneCodeCount {
		t.Fatalf("expected GEN:TVBG (100), got %q (%d)", app.probePackLabel(), len(app.Probe.Candidates))
	}

	// Trigger non-blocking carrier sweep from Expert REMOTE tab via KeySoft5.
	app.Set.Settings.ExpertMode = true
	app.SwitchTab(TabRemote)
	app.HandleKey(input.KeySoft5)
	if !app.Chrome.Sweep.Active {
		t.Fatalf("expected Sweep.Active=true after Expert REMOTE KeySoft5")
	}
	deadline := time.Now().Add(1500 * time.Millisecond)
	for app.Chrome.Sweep.Active && time.Now().Before(deadline) {
		app.Step()
		time.Sleep(15 * time.Millisecond)
	}
	if app.Chrome.Sweep.Active {
		t.Fatalf("carrier sweep did not finish in time")
	}
	if len(sim.TxHistory()) < 4 {
		t.Fatalf("expected >= 4 sweep bursts, got %d", len(sim.TxHistory()))
	}
	if st := sim.CarrierState(); st.FreqHz != lirc.DefaultCarrierHz || st.DutyPct != lirc.DefaultDutyPct {
		t.Fatalf("carrier not restored after UI sweep: %+v", st)
	}
}
