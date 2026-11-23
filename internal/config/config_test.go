package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"ir-studio/internal/config"
)

func TestDefaultSettingsAndSanitize(t *testing.T) {
	def := config.DefaultSettings()
	if def.MCPEnabled {
		t.Fatal("DefaultSettings().MCPEnabled MUST be false (default-deny invariant)")
	}
	if def.ExpertMode {
		t.Fatal("DefaultSettings().ExpertMode MUST be false (Simple Mode default)")
	}
	if def.CarrierHz != 38000 || def.DutyCyclePct != 33 || def.ProbeIntervalMs != 1200 {
		t.Fatalf("unexpected defaults: %+v", def)
	}

	bad := config.Settings{
		MCPEnabled:       true,
		CarrierHz:        999999,
		DutyCyclePct:     99,
		RecTimeoutUs:     100,
		ProbeIntervalMs:  999999,
		AmbientThreshold: 1,
	}
	got := config.Sanitize(bad)
	if !got.MCPEnabled {
		t.Fatal("Sanitize should preserve valid MCPEnabled=true")
	}
	if got.CarrierHz != config.DefaultCarrierHz || got.DutyCyclePct != config.DefaultDutyPct ||
		got.RecTimeoutUs != config.DefaultRecTimeoutUs || got.ProbeIntervalMs != config.DefaultProbeIntervalMs ||
		got.AmbientThreshold != config.DefaultAmbientThreshold {
		t.Fatalf("Sanitize did not reset out-of-range values: %+v", got)
	}
}

func TestConfigLoadSaveAtomicAndFailClosed(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ir-studio", "config.json")

	// Missing file -> defaults with nil error.
	s, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load(missing) err = %v", err)
	}
	if s.MCPEnabled {
		t.Fatal("missing config must default MCPEnabled=false")
	}

	// SaveAtomic with 0600 file and 0700 dir permissions.
	s.MCPEnabled = true
	s.ExpertMode = true
	s.CarrierHz = 56000
	s.DutyCyclePct = 50
	s.DevicePath = "/dev/lirc0"
	if err := config.SaveAtomic(cfgPath, s); err != nil {
		t.Fatalf("SaveAtomic: %v", err)
	}

	fi, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatalf("Stat config: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("config.json perm = %o, want 0600", perm)
	}
	di, err := os.Stat(filepath.Dir(cfgPath))
	if err != nil {
		t.Fatalf("Stat dir: %v", err)
	}
	if perm := di.Mode().Perm(); perm != 0o700 {
		t.Fatalf("config dir perm = %o, want 0700", perm)
	}

	loaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !loaded.MCPEnabled || !loaded.ExpertMode || loaded.CarrierHz != 56000 || loaded.DutyCyclePct != 50 || loaded.DevicePath != "/dev/lirc0" {
		t.Fatalf("loaded mismatch: %+v", loaded)
	}

	// Corrupted JSON -> fail closed (MCPEnabled == false) + error.
	if err := os.WriteFile(cfgPath, []byte("{corrupt json"), 0o600); err != nil {
		t.Fatalf("WriteFile corrupt: %v", err)
	}
	badLoaded, err := config.Load(cfgPath)
	if err == nil {
		t.Fatal("expected error on corrupt JSON")
	}
	if badLoaded.MCPEnabled {
		t.Fatal("corrupt config MUST fail closed with MCPEnabled=false")
	}
}

func TestMacrosLoadSaveAndValidation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "macros.json")

	// Missing file -> empty valid MacroFile.
	mf, err := config.LoadMacros(path)
	if err != nil || mf.Version != config.MacroVersion || len(mf.Macros) != 0 {
		t.Fatalf("LoadMacros(missing) = (%+v, %v)", mf, err)
	}

	mf.Macros = []config.Macro{
		{
			ID:   "movie_night",
			Name: "Movie Night",
			Steps: []config.MacroStep{
				{Remote: "TV_LG.ir", Button: "Power", DelayAfterMs: 800},
				{Remote: "Soundbar.ir", Button: "Power", DelayAfterMs: 500},
				{Remote: "TV_LG.ir", Button: "HDMI_2", DelayAfterMs: 0},
			},
		},
	}
	if err := config.SaveMacrosAtomic(path, mf); err != nil {
		t.Fatalf("SaveMacrosAtomic: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("macros.json perm = %v (err=%v), want 0600", fi.Mode().Perm(), err)
	}
	loaded, err := config.LoadMacros(path)
	if err != nil || len(loaded.Macros) != 1 || len(loaded.Macros[0].Steps) != 3 {
		t.Fatalf("LoadMacros round-trip mismatch: %+v, err=%v", loaded, err)
	}

	// Corrupt macros.json returns error.
	if err := os.WriteFile(path, []byte("not-json"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := config.LoadMacros(path); err == nil {
		t.Fatal("expected error on corrupt macros.json")
	}

	// SaveMacrosAtomic error cases: nil, bad version, duplicate ID, too many steps, too many macros.
	if err := config.SaveMacrosAtomic(path, nil); !errors.Is(err, config.ErrInvalidMacro) {
		t.Fatalf("SaveMacrosAtomic(nil) = %v", err)
	}
	if err := config.SaveMacrosAtomic(path, &config.MacroFile{Version: 99}); !errors.Is(err, config.ErrInvalidMacro) {
		t.Fatalf("SaveMacrosAtomic(v99) = %v", err)
	}
	dupMF := &config.MacroFile{
		Version: 0, // should default to 1
		Macros: []config.Macro{
			{ID: "dup", Name: "A"},
			{ID: "dup", Name: "B"},
		},
	}
	if err := config.SaveMacrosAtomic(path, dupMF); !errors.Is(err, config.ErrInvalidMacro) {
		t.Fatalf("SaveMacrosAtomic(dup) = %v", err)
	}
	manySteps := make([]config.MacroStep, config.MaxStepsPerMacro+1)
	for i := range manySteps {
		manySteps[i] = config.MacroStep{Remote: "tv.ir", Button: "Power", DelayAfterMs: 100}
	}
	if err := config.ValidateMacro(config.Macro{ID: "big", Name: "Big", Steps: manySteps}); !errors.Is(err, config.ErrInvalidMacro) {
		t.Fatalf("ValidateMacro(too many steps) = %v", err)
	}
	manyMacros := make([]config.Macro, config.MaxMacros+1)
	for i := range manyMacros {
		manyMacros[i] = config.Macro{ID: filepath.Base(path) + string(rune('a'+i%26)) + string(rune('0'+i/26)), Name: "M"}
	}
	if err := config.SaveMacrosAtomic(path, &config.MacroFile{Version: 1, Macros: manyMacros}); !errors.Is(err, config.ErrInvalidMacro) {
		t.Fatalf("SaveMacrosAtomic(too many macros) = %v", err)
	}
	if err := config.ValidateSafeRemoteName(""); !errors.Is(err, config.ErrInvalidRemotePath) {
		t.Fatalf("ValidateSafeRemoteName(empty) = %v", err)
	}
	if err := config.ValidateSafeRemoteName("tv\x01.ir"); !errors.Is(err, config.ErrInvalidRemotePath) {
		t.Fatalf("ValidateSafeRemoteName(ctrl) = %v", err)
	}
}

func TestMacroValidationAdversarial(t *testing.T) {
	tests := []struct {
		name    string
		macro   config.Macro
		wantErr error
	}{
		{
			name:    "missing id",
			macro:   config.Macro{Name: "X"},
			wantErr: config.ErrInvalidMacro,
		},
		{
			name: "path traversal dotdot",
			macro: config.Macro{
				ID: "m1", Name: "Evil",
				Steps: []config.MacroStep{{Remote: "../../etc/passwd", Button: "Power"}},
			},
			wantErr: config.ErrInvalidRemotePath,
		},
		{
			name: "path slash",
			macro: config.Macro{
				ID: "m1", Name: "Evil",
				Steps: []config.MacroStep{{Remote: "sub/tv.ir", Button: "Power"}},
			},
			wantErr: config.ErrInvalidRemotePath,
		},
		{
			name: "path backslash",
			macro: config.Macro{
				ID: "m1", Name: "Evil",
				Steps: []config.MacroStep{{Remote: `sub\tv.ir`, Button: "Power"}},
			},
			wantErr: config.ErrInvalidRemotePath,
		},
		{
			name: "path NUL byte",
			macro: config.Macro{
				ID: "m1", Name: "Evil",
				Steps: []config.MacroStep{{Remote: "tv\x00.ir", Button: "Power"}},
			},
			wantErr: config.ErrInvalidRemotePath,
		},
		{
			name: "delay below min on non-final step",
			macro: config.Macro{
				ID: "m1", Name: "Fast",
				Steps: []config.MacroStep{
					{Remote: "tv.ir", Button: "Power", DelayAfterMs: 10},
					{Remote: "tv.ir", Button: "HDMI1", DelayAfterMs: 0},
				},
			},
			wantErr: config.ErrInvalidMacro,
		},
		{
			name: "delay above max",
			macro: config.Macro{
				ID: "m1", Name: "Slow",
				Steps: []config.MacroStep{{Remote: "tv.ir", Button: "Power", DelayAfterMs: 20000}},
			},
			wantErr: config.ErrInvalidMacro,
		},
		{
			name: "empty button",
			macro: config.Macro{
				ID: "m1", Name: "NoBtn",
				Steps: []config.MacroStep{{Remote: "tv.ir", Button: ""}},
			},
			wantErr: config.ErrInvalidMacro,
		},
		{
			name: "repeat exceeds 10",
			macro: config.Macro{
				ID: "m1", Name: "Repeat",
				Steps: []config.MacroStep{{Remote: "tv.ir", Button: "Power", Repeat: 11}},
			},
			wantErr: config.ErrInvalidMacro,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := config.ValidateMacro(tc.macro)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("ValidateMacro() err = %v, want %v", err, tc.wantErr)
			}
		})
	}

	// ConfigPath & MacrosPath smoke checks.
	if p, err := config.ConfigPath(); err != nil || filepath.Base(p) != "config.json" {
		t.Fatalf("ConfigPath() = (%q, %v)", p, err)
	}
	if p, err := config.MacrosPath(); err != nil || filepath.Base(p) != "macros.json" {
		t.Fatalf("MacrosPath() = (%q, %v)", p, err)
	}
}
