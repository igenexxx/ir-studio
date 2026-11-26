package ui

import (
	"testing"
	"time"

	"ir-studio/internal/fb"
	"ir-studio/internal/input"
	"ir-studio/internal/irfile"
	"ir-studio/internal/irml"
	"ir-studio/internal/library"
	"ir-studio/internal/lirc"
)

func TestContextAwareHelpAllScreensAndBounds(t *testing.T) {
	app, _ := newTestApp(t)
	c, err := fb.NewCanvas("")
	if err != nil {
		t.Fatalf("NewCanvas: %v", err)
	}
	defer c.Close()

	sampleCap := &lirc.RXCapture{
		Timestamp:    time.Now(),
		Pulses:       irfile.PulseTrain{9000, -4500, 560, -560, 560, -1690},
		CarrierEstHz: 38000,
	}

	setupCases := []struct {
		name      string
		setup     func()
		wantTitle string
	}{
		{"home simple", func() {
			app.Set.Settings.ExpertMode = false
			app.goHome()
		}, "HOME MENU"},
		{"home expert", func() {
			app.Set.Settings.ExpertMode = true
			app.goHome()
		}, "HOME MENU"},
		{"read listen simple", func() {
			app.Set.Settings.ExpertMode = false
			app.enterReadAndReplay()
		}, "READ & REPLAY (LISTEN)"},
		{"read listen expert", func() {
			app.Set.Settings.ExpertMode = true
			app.enterReadAndReplay()
		}, "READ & REPLAY (LISTEN)"},
		{"read captured no match", func() {
			app.enterReadAndReplay()
			app.RX.LastCap = sampleCap
			app.RX.Matches = nil
		}, "READ & REPLAY (CAPTURED)"},
		{"read captured db match", func() {
			app.enterReadAndReplay()
			app.RX.LastCap = sampleCap
			app.RX.Matches = []irml.MatchResult{{RemoteID: "LG.ir", Similarity: 0.95}}
		}, "READ & REPLAY (CAPTURED)"},
		{"saved simple", func() {
			app.Set.Settings.ExpertMode = false
			app.Saved.ScopeUser = true
			app.SwitchTab(TabSaved)
		}, "MY REMOTES"},
		{"saved expert user", func() {
			app.Set.Settings.ExpertMode = true
			app.Saved.ScopeUser = true
			app.SwitchTab(TabSaved)
		}, "MY REMOTES (EXPERT)"},
		{"saved expert db", func() {
			app.Set.Settings.ExpertMode = true
			app.Saved.ScopeUser = false
			app.SwitchTab(TabSaved)
		}, "DB BROWSER (READ-ONLY)"},
		{"remote simple", func() {
			app.Set.Settings.ExpertMode = false
			app.Remote.AC.Active = false
			app.Remote.Entry.ReadOnly = false
			app.SwitchTab(TabRemote)
		}, "REMOTE DECK"},
		{"remote expert ro", func() {
			app.Set.Settings.ExpertMode = true
			app.Remote.AC.Active = false
			app.Remote.Loaded = true
			app.Remote.Entry.ReadOnly = true
			app.SwitchTab(TabRemote)
		}, "REMOTE DECK (EXPERT)"},
		{"remote ac overlay", func() {
			app.Remote.AC.Active = true
			app.SwitchTab(TabRemote)
		}, "SMART AC CONTROLLER"},
		{"probe idle simple", func() {
			app.Remote.AC.Active = false
			app.Set.Settings.ExpertMode = false
			app.SwitchTab(TabProbe)
			app.Probe.Snap.State = library.ProbeIdle
		}, "UNIVERSAL OFF"},
		{"probe idle expert", func() {
			app.Set.Settings.ExpertMode = true
			app.SwitchTab(TabProbe)
			app.Probe.Snap.State = library.ProbeIdle
		}, "UNIVERSAL OFF (EXPERT)"},
		{"probe running", func() {
			app.SwitchTab(TabProbe)
			app.Probe.Snap.State = library.ProbeRunning
		}, "UNIVERSAL OFF (RUNNING)"},
		{"ac tab", func() {
			app.Probe.Snap.State = library.ProbeIdle
			app.SwitchTab(TabAC)
		}, "SMART AC CONTROLLER"},
		{"rx live", func() {
			app.RX.AC.Active = false
			app.RX.Learning = true
			app.RX.LastCap = sampleCap
			app.SwitchTab(TabRX)
		}, "IR LAB (OSCILLOSCOPE)"},
		{"rx held", func() {
			app.RX.AC.Active = false
			app.RX.Learning = false
			app.RX.LastCap = sampleCap
			app.SwitchTab(TabRX)
		}, "IR LAB (OSCILLOSCOPE)"},
		{"rx ac diff", func() {
			app.RX.AC.Active = true
			app.SwitchTab(TabRX)
		}, "IR LAB (AC BIT DIFF)"},
		{"macro idle", func() {
			app.RX.AC.Active = false
			app.Macro.Running = false
			app.Macro.EditMode = false
			app.SwitchTab(TabMacro)
		}, "MACROS"},
		{"macro edit", func() {
			app.Macro.Running = false
			app.Macro.EditMode = true
			app.SwitchTab(TabMacro)
		}, "MACROS (EDIT STEPS)"},
		{"macro running", func() {
			app.Macro.Running = true
			app.Macro.EditMode = false
			app.SwitchTab(TabMacro)
		}, "MACROS (RUNNING)"},
		{"settings simple", func() {
			app.Macro.Running = false
			app.Set.SubViewSources = false
			app.Set.Settings.ExpertMode = false
			app.SwitchTab(TabSet)
		}, "SETTINGS (SIMPLE)"},
		{"settings expert", func() {
			app.Set.SubViewSources = false
			app.Set.Settings.ExpertMode = true
			app.SwitchTab(TabSet)
		}, "SETTINGS (EXPERT)"},
		{"settings sources subview", func() {
			app.Set.SubViewSources = true
			app.SwitchTab(TabSet)
		}, "MANAGE DB SOURCES"},
	}

	for _, tc := range setupCases {
		t.Run(tc.name, func(t *testing.T) {
			tc.setup()
			h := app.ActiveScreenHelp()
			if h.ScreenTitle != tc.wantTitle {
				t.Fatalf("ScreenTitle = %q, want %q", h.ScreenTitle, tc.wantTitle)
			}
			if h.Summary[0] == "" {
				t.Fatalf("Summary[0] must not be empty")
			}
			if len(h.Summary[0]) > helpSummaryMaxCh || len(h.Summary[1]) > helpSummaryMaxCh {
				t.Fatalf("summary line exceeds %d chars: %q (%d) / %q (%d)",
					helpSummaryMaxCh, h.Summary[0], len(h.Summary[0]), h.Summary[1], len(h.Summary[1]))
			}
			if len(h.Keys) == 0 {
				t.Fatalf("Keys must not be empty")
			}
			for _, k := range h.Keys {
				if k.Key == "" || len(k.Key) > 7 {
					t.Fatalf("keycap label %q must be 1..7 chars", k.Key)
				}
				if k.Desc == "" || len(k.Desc) > helpDescMaxCh {
					t.Fatalf("key desc %q (%d) must be 1..%d chars", k.Desc, len(k.Desc), helpDescMaxCh)
				}
			}

			// Open Help, render every page without panic, verify paging wrap, and close cleanly.
			app.HandleKey(input.KeyH)
			if !app.Chrome.HelpOpen || app.Chrome.HelpPage != 0 {
				t.Fatalf("expected HelpOpen=true HelpPage=0, got open=%v page=%d", app.Chrome.HelpOpen, app.Chrome.HelpPage)
			}
			pages := h.PageCount()
			for p := range pages {
				if app.Chrome.HelpPage != p {
					t.Fatalf("HelpPage = %d, want %d", app.Chrome.HelpPage, p)
				}
				app.Render(c)
				if pages > 1 {
					app.HandleKey(input.KeyRight)
				}
			}
			if pages > 1 && app.Chrome.HelpPage != 0 {
				t.Fatalf("expected HelpPage wrap back to 0, got %d", app.Chrome.HelpPage)
			}
			if pages > 1 {
				app.HandleKey(input.KeyLeft)
				if app.Chrome.HelpPage != pages-1 {
					t.Fatalf("expected KeyLeft wrap to %d, got %d", pages-1, app.Chrome.HelpPage)
				}
			}
			// Close with Esc.
			app.HandleKey(input.KeyEsc)
			if app.Chrome.HelpOpen {
				t.Fatalf("expected KeyEsc to close Help modal")
			}
			// Re-open and close with H.
			app.HandleKey(input.KeyH)
			if !app.Chrome.HelpOpen {
				t.Fatalf("expected KeyH to open Help modal")
			}
			app.HandleKey(input.KeyH)
			if app.Chrome.HelpOpen {
				t.Fatalf("expected KeyH to close Help modal")
			}
		})
	}
}
