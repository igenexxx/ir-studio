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

func TestHelpModalFivePages(t *testing.T) {
	app, _ := newTestApp(t)
	c, _ := fb.NewCanvas("")

	app.HandleKey(input.KeyH)
	if !app.Chrome.HelpOpen {
		t.Fatalf("expected HelpOpen after KeyH")
	}
	for page := range 5 {
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
	app.HandleKey(input.KeySoft7)
	if app.Saved.ConfirmDel {
		t.Fatalf("RO DB pack must NEVER enter ConfirmDel")
	}
	if len(app.Saved.DBEntries) != 1 {
		t.Fatalf("DBEntries modified: got %d, want 1", len(app.Saved.DBEntries))
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
	app.HandleKey(input.KeyEnter) // toggle MCP ON
	if !app.Set.Settings.MCPEnabled {
		t.Fatalf("expected MCP ON after toggle")
	}
	app.HandleKey(input.KeySoft6) // Manage DB Sources sub-view
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
