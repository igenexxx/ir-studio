package ui

import (
	"testing"
	"time"

	"ir-studio/internal/config"
	"ir-studio/internal/fb"
	"ir-studio/internal/input"
	"ir-studio/internal/irfile"
	"ir-studio/internal/lirc"
)

func TestLinearReadReplaySaveAndLearnFlow(t *testing.T) {
	app, sim := newTestApp(t)
	c, _ := fb.NewCanvas("")

	// App boots into HOME in Simple Mode (5 items).
	if app.Chrome.Tab != TabHome {
		t.Fatalf("initial tab = %v, want TabHome", app.Chrome.Tab)
	}
	if got := len(app.homeMenuItems()); got != 5 {
		t.Fatalf("Simple Mode homeMenuItems = %d, want 5", got)
	}
	app.Render(c)

	// Enter opens item 0 ("Read & Replay") in listening state.
	app.HandleKey(input.KeyEnter)
	if app.Chrome.Tab != TabRead || app.RX.LastCap != nil || !app.RX.Learning {
		t.Fatalf("expected TabRead listening state, got tab=%v cap=%v learning=%v",
			app.Chrome.Tab, app.RX.LastCap != nil, app.RX.Learning)
	}
	app.Render(c)

	// Inject a real NEC remote press -> freezes capture on Step 2.
	reg := irfile.DefaultRegistry()
	pulses, _ := reg.Encode(irfile.IRSignal{
		Name: "Power", Kind: irfile.SignalParsed, Protocol: "NEC",
		Address: [4]byte{0x10}, Command: [4]byte{0x42},
	})
	dec, _ := reg.Decode(pulses)
	app.InjectCapture(lirc.RXCapture{
		Timestamp: time.Now(), Pulses: pulses, Decoded: &dec, CarrierEstHz: 38000,
	})
	if app.RX.Learning {
		t.Fatalf("expected Learning=false after capture in TabRead to prevent overwrite")
	}
	app.Render(c)

	// Press 4 SEND (or Enter) to immediately replay and test on device.
	app.HandleKey(input.KeySoft4)
	deadline := time.Now().Add(500 * time.Millisecond)
	for len(sim.TxHistory()) == 0 && time.Now().Before(deadline) {
		app.Step()
		time.Sleep(10 * time.Millisecond)
	}
	if len(sim.TxHistory()) == 0 {
		t.Fatalf("expected instant replay TX on 4 SEND in Read & Replay")
	}

	// Press 5 SAVE -> saves remote AND immediately opens it in Remote Deck.
	app.HandleKey(input.KeySoft5)
	if app.Chrome.Tab != TabRemote {
		t.Fatalf("expected auto-open TabRemote after 5 SAVE, got %v", app.Chrome.Tab)
	}
	if !app.Remote.Loaded || len(app.Remote.Entry.Signals) != 1 {
		t.Fatalf("expected newly saved remote with 1 button, got loaded=%v len=%d",
			app.Remote.Loaded, len(app.Remote.Entry.Signals))
	}

	// Simple Mode Remote Deck: 5 LEARN+ arms learning, next IR press appends Btn_2.
	app.HandleKey(input.KeySoft5)
	if !app.Remote.LearnPending {
		t.Fatalf("expected LearnPending=true after Simple Mode 5 LEARN+")
	}
	app.Render(c)
	volPulses, _ := reg.Encode(irfile.IRSignal{
		Name: "Vol", Kind: irfile.SignalParsed, Protocol: "NEC",
		Address: [4]byte{0x10}, Command: [4]byte{0x43},
	})
	app.InjectCapture(lirc.RXCapture{Timestamp: time.Now(), Pulses: volPulses, CarrierEstHz: 38000})
	if app.Remote.LearnPending || len(app.Remote.Entry.Signals) != 2 {
		t.Fatalf("expected Btn_2 appended and LearnPending=false, got pending=%v len=%d",
			app.Remote.LearnPending, len(app.Remote.Entry.Signals))
	}

	// Esc from Remote Deck -> My Remotes; Esc from My Remotes -> HOME.
	app.HandleKey(input.KeyEsc)
	if app.Chrome.Tab != TabSaved {
		t.Fatalf("expected TabSaved on Esc from TabRemote, got %v", app.Chrome.Tab)
	}
	app.HandleKey(input.KeyEsc)
	if app.Chrome.Tab != TabHome {
		t.Fatalf("expected TabHome on Esc from TabSaved, got %v", app.Chrome.Tab)
	}
}

func TestHomeExitConfirmationAndExpertModePersistence(t *testing.T) {
	app, _ := newTestApp(t)
	c, _ := fb.NewCanvas("")

	// Esc on HOME shows confirmation instead of exiting immediately.
	if exit := app.HandleKey(input.KeyEsc); exit {
		t.Fatalf("first Esc on HOME must NOT exit immediately")
	}
	if !app.Home.ConfirmExit {
		t.Fatalf("expected ConfirmExit=true after first Esc on HOME")
	}
	app.Render(c)

	// Any non-confirm key cancels exit confirmation.
	if exit := app.HandleKey(input.KeyDown); exit || app.Home.ConfirmExit {
		t.Fatalf("expected KeyDown to cancel ConfirmExit")
	}

	// Toggle Expert Mode via 5 EXP on HOME -> unlocks 8 menu items & persists to config.json.
	app.HandleKey(input.KeySoft5)
	if !app.Set.Settings.ExpertMode {
		t.Fatalf("expected ExpertMode=true after HOME KeySoft5")
	}
	if got := len(app.homeMenuItems()); got != 8 {
		t.Fatalf("Expert Mode homeMenuItems = %d, want 8", got)
	}
	loaded, err := config.Load(app.Set.ConfigPath)
	if err != nil || !loaded.ExpertMode {
		t.Fatalf("expected ExpertMode=true persisted in config.json, got %+v err=%v", loaded, err)
	}

	// Esc then Enter confirms exit cleanly.
	app.HandleKey(input.KeyEsc)
	if exit := app.HandleKey(input.KeyEnter); !exit {
		t.Fatalf("expected exit=true after Esc -> Enter on HOME")
	}
}

func TestReadNoiseGuardAndBackNavigationAcrossScreens(t *testing.T) {
	app, _ := newTestApp(t)

	// Open Read & Replay and inject noise.
	app.HandleKey(input.KeySoft6)
	if app.Chrome.Tab != TabRead {
		t.Fatalf("expected TabRead after HOME KeySoft6")
	}
	noise := irfile.PulseTrain{100, -100, 120, -90, 80, -110, 95, -105, 110, -85}
	app.InjectCapture(lirc.RXCapture{Timestamp: time.Now(), Pulses: noise, CarrierEstHz: 38000})
	app.RX.Class.IsNoise = true
	app.RX.Class.SaveGuardWarn = true

	// 5 SAVE prompts noise guard; Esc dismisses; 5 SAVE -> Enter force saves.
	app.HandleKey(input.KeySoft5)
	if !app.RX.GuardPending {
		t.Fatalf("expected GuardPending on noise save in TabRead")
	}
	app.HandleKey(input.KeyEsc)
	if app.RX.GuardPending || app.Chrome.Tab != TabRead {
		t.Fatalf("expected Esc to dismiss noise guard without leaving TabRead")
	}
	app.HandleKey(input.KeySoft5)
	app.HandleKey(input.KeyEnter)
	if app.Chrome.Tab != TabRemote {
		t.Fatalf("expected forced save to open TabRemote, got %v", app.Chrome.Tab)
	}

	// Verify Esc returns to HOME from Probe, AC, IR Lab (RX), Macro, Settings.
	for _, tab := range []Tab{TabProbe, TabAC, TabRX, TabMacro, TabSet} {
		app.SwitchTab(tab)
		app.HandleKey(input.KeyEsc)
		if app.Chrome.Tab != TabHome {
			t.Fatalf("Esc from tab %v went to %v, want TabHome", tab, app.Chrome.Tab)
		}
	}
}

func TestReadPromoteDBMatchToUserAndOpenRemote(t *testing.T) {
	app, _ := newTestApp(t)
	c, _ := fb.NewCanvas("")

	// Wait for background fingerprint index to index the seeded DB pack (LG_AKB7509.ir).
	deadline := time.Now().Add(5 * time.Second)
	for app.fpIndexing && time.Now().Before(deadline) {
		app.Step()
		time.Sleep(10 * time.Millisecond)
	}
	if app.fpIndexing {
		t.Fatalf("fingerprint index did not complete in time")
	}

	// Open Read & Replay and inject the exact NEC signal from LG_AKB7509.ir.
	app.HandleKey(input.KeyEnter)
	reg := irfile.DefaultRegistry()
	pulses, _ := reg.Encode(irfile.IRSignal{
		Name: "Power", Kind: irfile.SignalParsed, Protocol: "NEC",
		Address: [4]byte{0x04}, Command: [4]byte{0x08},
	})
	dec, _ := reg.Decode(pulses)
	app.InjectCapture(lirc.RXCapture{
		Timestamp: time.Now(), Pulses: pulses, Decoded: &dec, CarrierEstHz: 38000,
	})
	if !app.hasTopDBMatch() {
		t.Fatalf("expected DB match >= 80%%, got matches=%+v", app.RX.Matches)
	}
	app.Render(c)

	beforeUser := len(app.Saved.UserEntries)
	// Press 7 ADDDB -> copies full DB remote into USER library and opens it in TabRemote!
	app.HandleKey(input.KeySoft7)
	if app.Chrome.Tab != TabRemote {
		t.Fatalf("expected TabRemote after 7 ADDDB, got %v", app.Chrome.Tab)
	}
	if len(app.Saved.UserEntries) != beforeUser+1 {
		t.Fatalf("UserEntries = %d, want %d", len(app.Saved.UserEntries), beforeUser+1)
	}
	if app.Remote.Entry.ReadOnly || len(app.Remote.Entry.Signals) < 2 {
		t.Fatalf("expected editable promoted full DB remote with >=2 buttons, got ro=%v len=%d",
			app.Remote.Entry.ReadOnly, len(app.Remote.Entry.Signals))
	}
}
