package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ir-studio/internal/config"
	"ir-studio/internal/dbsync"
	"ir-studio/internal/irfile"
	"ir-studio/internal/irml"
	"ir-studio/internal/library"
	"ir-studio/internal/lirc"
)

type testEnv struct {
	t           *testing.T
	dir         string
	userDir     string
	dbDir       string
	sourcesPath string
	macrosPath  string
	store       *library.Store
	syncer      *dbsync.Syncer
	sim         *lirc.SimTransport
	xcvr        *lirc.Transceiver
	srv         *Server
}

func newTestEnv(t *testing.T, customBackend IRBackend) *testEnv {
	t.Helper()
	dir := t.TempDir()
	userDir := filepath.Join(dir, "remotes")
	dbDir := filepath.Join(dir, "databases")
	sourcesPath := filepath.Join(dir, "sources.json")
	macrosPath := filepath.Join(dir, "macros.json")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatalf("mkdir userDir: %v", err)
	}
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatalf("mkdir dbDir: %v", err)
	}

	store := library.NewStore(userDir, dbDir)
	syncer := dbsync.NewSyncer(dbDir, nil)
	sim := lirc.NewSimTransport()
	gate := lirc.NewTransceiverGate(time.Millisecond)
	xcvr := lirc.NewTransceiver(sim, gate)

	settings := config.DefaultSettings()
	settings.MCPEnabled = true
	cfg := ServeConfig{
		Store:       store,
		Syncer:      syncer,
		Transceiver: xcvr,
		Settings:    settings,
		SourcesPath: sourcesPath,
		MacrosPath:  macrosPath,
	}
	srv := NewServer(customBackend, cfg)
	return &testEnv{
		t:           t,
		dir:         dir,
		userDir:     userDir,
		dbDir:       dbDir,
		sourcesPath: sourcesPath,
		macrosPath:  macrosPath,
		store:       store,
		syncer:      syncer,
		sim:         sim,
		xcvr:        xcvr,
		srv:         srv,
	}
}

func (e *testEnv) seedUserRemote(name string, signals []irfile.IRSignal) {
	e.t.Helper()
	f := &irfile.IRFile{Version: irfile.SupportedVersion, Signals: signals}
	if _, err := e.store.SaveUserRemote(name, f); err != nil {
		e.t.Fatalf("seedUserRemote(%s): %v", name, err)
	}
}

func (e *testEnv) seedPackRemote(pack, relPath string, signals []irfile.IRSignal) string {
	e.t.Helper()
	full := filepath.Join(e.dbDir, pack, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		e.t.Fatalf("mkdir pack: %v", err)
	}
	f := &irfile.IRFile{Version: irfile.SupportedVersion, Signals: signals}
	if err := irfile.SaveFileAtomic(full, f); err != nil {
		e.t.Fatalf("seedPackRemote(%s/%s): %v", pack, relPath, err)
	}
	return full
}

func necPowerSignal() irfile.IRSignal {
	return irfile.IRSignal{
		Name:      "Power",
		Kind:      irfile.SignalParsed,
		Protocol:  "NEC",
		Address:   [4]byte{0x04, 0x00, 0x00, 0x00},
		Command:   [4]byte{0x08, 0x00, 0x00, 0x00},
		Frequency: 38000,
		DutyCycle: 0.33,
	}
}

func ambientNoiseSignal() irfile.IRSignal {
	// Alternating tiny erratic pulses classified as ambient noise.
	return irfile.IRSignal{
		Name:      "Glitch",
		Kind:      irfile.SignalRaw,
		Frequency: 38000,
		DutyCycle: 0.33,
		RawData:   []uint32{80, 90, 75, 110, 65, 95, 70, 120, 60},
	}
}

func callTool(t *testing.T, srv *Server, name string, args map[string]any) (toolResult, map[string]any) {
	t.Helper()
	rawArgs, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	reqBytes, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      name,
			"arguments": json.RawMessage(rawArgs),
		},
	})
	resp := srv.handleRequest(context.Background(), reqBytes)
	if resp == nil {
		t.Fatalf("nil response for %s", name)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected rpc error for %s: %+v", name, resp.Error)
	}
	res, ok := resp.Result.(toolResult)
	if !ok {
		t.Fatalf("unexpected result type %T", resp.Result)
	}
	if len(res.Content) == 0 {
		t.Fatalf("empty content for %s", name)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].Text), &payload); err != nil {
		t.Fatalf("unmarshal tool output for %s: %v (raw=%s)", name, err, res.Content[0].Text)
	}
	return res, payload
}

type stubBackend struct {
	txCalls   atomic.Int32
	learnErr  error
	txErr     error
	lastSig   irfile.IRSignal
	lastRep   int
	mu        sync.Mutex
}

func (b *stubBackend) HardwareMode() string             { return "SIM" }
func (b *stubBackend) DevicePath() string               { return "sim" }
func (b *stubBackend) CarrierSpec() irfile.CarrierSpec  { return lirc.DefaultCarrier() }
func (b *stubBackend) AmbientSnapshot() irml.AmbientSnapshot {
	return irml.AmbientSnapshot{Level: irml.AmbientClean, NoiseDutyPct: 1.5}
}
func (b *stubBackend) LearnSignal(_ context.Context, _ time.Duration, carrierHz uint32) (lirc.RXCapture, error) {
	if b.learnErr != nil {
		return lirc.RXCapture{}, b.learnErr
	}
	sig := necPowerSignal()
	pt, _ := irfile.DefaultRegistry().Encode(sig)
	return lirc.RXCapture{Timestamp: time.Now(), Pulses: pt, Decoded: &sig, CarrierEstHz: carrierHz}, nil
}
func (b *stubBackend) TransmitSignal(_ context.Context, sig irfile.IRSignal, repeat int, _ irfile.CarrierSpec) (time.Duration, error) {
	b.txCalls.Add(1)
	b.mu.Lock()
	b.lastSig = sig
	b.lastRep = repeat
	b.mu.Unlock()
	if b.txErr != nil {
		return 0, b.txErr
	}
	return 2 * time.Millisecond, nil
}
func (b *stubBackend) SweepCarrier(_ context.Context, sig irfile.IRSignal, freqs []uint32, dutyPct uint32, _ time.Duration) (lirc.SweepReport, error) {
	b.txCalls.Add(1)
	if len(freqs) == 0 {
		freqs = lirc.SweepCarrierPresets[:]
	}
	steps := make([]lirc.SweepStepResult, len(freqs))
	for i, hz := range freqs {
		steps[i] = lirc.SweepStepResult{FreqHz: hz, DutyPct: dutyPct, Sent: true}
	}
	return lirc.SweepReport{
		SignalName:      sig.Name,
		Steps:           steps,
		RestoredFreqHz:  lirc.DefaultCarrierHz,
		RestoredDutyPct: lirc.DefaultDutyPct,
	}, nil
}
func (b *stubBackend) TripwireProbe(_ context.Context, cfg lirc.TripwireConfig) (lirc.TripwireReading, error) {
	b.txCalls.Add(1)
	if cfg.Mode == "" {
		cfg.Mode = lirc.TripwireReflective
	}
	if cfg.PingCount == 0 {
		cfg.PingCount = lirc.DefaultTripwirePings
	}
	return lirc.TripwireReading{
		Timestamp:      time.Now(),
		Mode:           cfg.Mode,
		CarrierHz:      lirc.DefaultCarrierHz,
		PingsSent:      cfg.PingCount,
		EchoesReceived: cfg.PingCount,
		ReflectionPct:  100,
		Triggered:      cfg.Mode == lirc.TripwireReflective,
		HardwareMode:   "SIM",
	}, nil
}

func (b *stubBackend) MeasureFlicker(_ context.Context, _ int) (irml.FlickerReport, error) {
	return irml.FlickerReport{
		FreqHz:    120,
		DutyPct:   50,
		JitterPct: 0,
		Periods:   24,
		Risk:      irml.FlickerHigh,
		RiskLabel: irml.FlickerHigh.String(),
	}, nil
}

func (b *stubBackend) MeasureRPM(_ context.Context, blades, _ int) (irml.RPMReport, error) {
	b.txCalls.Add(1)
	if blades <= 0 {
		blades = 2
	}
	return irml.RPMReport{
		RPM:          float32(3600) / float32(blades),
		BladeFreqHz:  60,
		Blades:       blades,
		StabilityPct: 100,
		Samples:      20,
		Valid:        true,
	}, nil
}

func TestServeStdioDisabledGate(t *testing.T) {
	var out bytes.Buffer
	cfg := ServeConfig{Settings: config.DefaultSettings()} // MCPEnabled == false
	err := ServeStdio(context.Background(), strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`+"\n"), &out, cfg)
	if !errors.Is(err, ErrMCPDisabled) {
		t.Fatalf("got err=%v, want ErrMCPDisabled", err)
	}
	if out.Len() != 0 {
		t.Fatalf("expected zero stdout output when disabled, got %q", out.String())
	}
}

func TestServeStdioProtocolLifecycleAndSchemaParity(t *testing.T) {
	env := newTestEnv(t, nil)
	settings := config.DefaultSettings()
	settings.MCPEnabled = true
	cfg := ServeConfig{
		Store:       env.store,
		Syncer:      env.syncer,
		Transceiver: env.xcvr,
		Settings:    settings,
		SourcesPath: env.sourcesPath,
		MacrosPath:  env.macrosPath,
	}

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`,
		`not-valid-json`,
		`{"jsonrpc":"2.0","id":4,"method":"unknown/method"}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"nonexistent_tool","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call"}`,
		"",
	}, "\n") + "\n"

	var out bytes.Buffer
	if err := ServeStdio(context.Background(), strings.NewReader(input), &out, cfg); err != nil {
		t.Fatalf("ServeStdio failed: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 7 {
		t.Fatalf("expected 7 responses (notification ignored), got %d:\n%s", len(lines), out.String())
	}

	// Verify initialize.
	var initResp struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
			ServerInfo      struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &initResp); err != nil {
		t.Fatalf("unmarshal init: %v", err)
	}
	if initResp.Result.ProtocolVersion != protocolVersion || initResp.Result.ServerInfo.Name != "ir-studio" {
		t.Fatalf("unexpected init response: %+v", initResp)
	}

	// Verify tools/list returns all 30 tools with 1:1 schema-to-handler bijection.
	var listResp struct {
		Result struct {
			Tools []toolDefinition `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[2]), &listResp); err != nil {
		t.Fatalf("unmarshal tools/list: %v", err)
	}
	if len(listResp.Result.Tools) != 30 {
		t.Fatalf("expected 30 tools, got %d", len(listResp.Result.Tools))
	}
	seen := make(map[string]bool, len(listResp.Result.Tools))
	for _, td := range listResp.Result.Tools {
		if td.Name == "" || td.Description == "" || td.InputSchema["type"] != "object" {
			t.Fatalf("invalid tool schema: %+v", td)
		}
		if _, ok := env.srv.handlers[td.Name]; !ok {
			t.Fatalf("schema tool %q missing handler", td.Name)
		}
		seen[td.Name] = true
	}
	for name := range env.srv.handlers {
		if !seen[name] {
			t.Fatalf("handler %q missing from schema.go", name)
		}
	}

	// Verify JSON-RPC error codes: -32700, -32601, -32602, -32602.
	wantCodes := []int{errCodeParseError, errCodeMethodNotFound, errCodeInvalidParams, errCodeInvalidParams}
	for i, want := range wantCodes {
		var errResp struct {
			Error *rpcError `json:"error"`
		}
		if err := json.Unmarshal([]byte(lines[3+i]), &errResp); err != nil {
			t.Fatalf("unmarshal err[%d]: %v", i, err)
		}
		if errResp.Error == nil || errResp.Error.Code != want {
			t.Fatalf("line %d: got error=%+v, want code %d", 3+i, errResp.Error, want)
		}
	}
}

func TestStatusAndLearnTools(t *testing.T) {
	env := newTestEnv(t, nil)
	env.seedUserRemote("LG_TV.ir", []irfile.IRSignal{necPowerSignal()})

	res, out := callTool(t, env.srv, "ir_get_status", map[string]any{})
	if res.IsError {
		t.Fatalf("ir_get_status error: %v", out)
	}
	if out["hardware_mode"] != "SIM" || out["user_remotes_count"].(float64) != 1 || out["mcp_enabled"] != true {
		t.Fatalf("unexpected status: %+v", out)
	}

	// Valid learn in SIM mode.
	res, out = callTool(t, env.srv, "ir_learn_signal", map[string]any{"timeout_ms": 1000, "carrier_hz": 38000})
	if res.IsError {
		t.Fatalf("ir_learn_signal error: %v", out)
	}
	if _, ok := out["button"]; !ok {
		t.Fatalf("expected button in learn output: %+v", out)
	}
	if _, ok := out["fingerprint_match"]; !ok {
		t.Fatalf("expected fingerprint_match against seeded LG_TV.ir: %+v", out)
	}

	// Out-of-range timeout & carrier rejected.
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"timeout_too_low", map[string]any{"timeout_ms": 100}},
		{"timeout_too_high", map[string]any{"timeout_ms": 20000}},
		{"carrier_too_low", map[string]any{"carrier_hz": 20000}},
		{"carrier_too_high", map[string]any{"carrier_hz": 80000}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, _ := callTool(t, env.srv, "ir_learn_signal", tc.args)
			if !res.IsError {
				t.Fatalf("expected error for %s", tc.name)
			}
		})
	}
}

func TestTransmitAndMacroSafetyGates(t *testing.T) {
	stub := &stubBackend{}
	env := newTestEnv(t, stub)
	env.seedUserRemote("LG_TV.ir", []irfile.IRSignal{necPowerSignal()})
	env.seedPackRemote("tvs", "Sony.ir", []irfile.IRSignal{necPowerSignal()})

	// Table-driven TX gate rejections (zero hardware calls!).
	cases := []struct {
		name string
		args map[string]any
	}{
		{"missing_confirm_ir", map[string]any{"remote_name": "LG_TV.ir", "button_name": "Power"}},
		{"false_confirm_ir", map[string]any{"confirm_ir": false, "remote_name": "LG_TV.ir", "button_name": "Power"}},
		{"repeat_over_5", map[string]any{"confirm_ir": true, "remote_name": "LG_TV.ir", "button_name": "Power", "repeat": 6}},
		{"repeat_over_10", map[string]any{"confirm_ir": true, "remote_name": "LG_TV.ir", "button_name": "Power", "repeat": 11}},
		{"repeat_negative", map[string]any{"confirm_ir": true, "remote_name": "LG_TV.ir", "button_name": "Power", "repeat": -1}},
		{"bad_carrier", map[string]any{"confirm_ir": true, "remote_name": "LG_TV.ir", "button_name": "Power", "carrier_hz": 90000}},
		{"bad_duty", map[string]any{"confirm_ir": true, "remote_name": "LG_TV.ir", "button_name": "Power", "duty_pct": 10}},
		{"missing_button", map[string]any{"confirm_ir": true, "remote_name": "LG_TV.ir", "button_name": "Nonexistent"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, _ := callTool(t, env.srv, "ir_transmit_button", tc.args)
			if !res.IsError {
				t.Fatalf("expected error for %s", tc.name)
			}
		})
	}
	if stub.txCalls.Load() != 0 {
		t.Fatalf("expected 0 hardware TX calls for rejected requests, got %d", stub.txCalls.Load())
	}

	// Valid TX from UserLibrary and from external pack.
	res, out := callTool(t, env.srv, "ir_transmit_button", map[string]any{
		"confirm_ir":  true,
		"remote_name": "LG_TV.ir",
		"button_name": "Power",
		"repeat":      2,
	})
	if res.IsError || out["transmitted"] != true {
		t.Fatalf("expected successful TX, got %+v", out)
	}
	res, out = callTool(t, env.srv, "ir_transmit_button", map[string]any{
		"confirm_ir":  true,
		"remote_name": "Sony.ir",
		"pack":        "tvs",
		"button_name": "power", // case-insensitive match
	})
	if res.IsError || out["transmitted"] != true {
		t.Fatalf("expected successful pack TX, got %+v", out)
	}

	// Save macros: one valid, one referencing a deleted/missing button.
	mf := &config.MacroFile{
		Version: config.MacroVersion,
		Macros: []config.Macro{
			{
				ID:   "movie",
				Name: "Movie Night",
				Steps: []config.MacroStep{
					{Remote: "LG_TV.ir", Button: "Power", DelayAfterMs: 50, Repeat: 1},
					{Remote: "LG_TV.ir", Button: "Power", DelayAfterMs: 50, Repeat: 1},
				},
			},
			{
				ID:   "broken",
				Name: "Broken Macro",
				Steps: []config.MacroStep{
					{Remote: "LG_TV.ir", Button: "Power", DelayAfterMs: 50, Repeat: 1},
					{Remote: "LG_TV.ir", Button: "MissingBtn", DelayAfterMs: 50, Repeat: 1},
				},
			},
		},
	}
	if err := config.SaveMacrosAtomic(env.macrosPath, mf); err != nil {
		t.Fatalf("SaveMacrosAtomic: %v", err)
	}

	res, out = callTool(t, env.srv, "ir_list_macros", map[string]any{})
	if res.IsError || len(out["macros"].([]any)) != 2 {
		t.Fatalf("unexpected ir_list_macros: %+v", out)
	}

	// confirm_ir: false rejected on ir_run_macro.
	before := stub.txCalls.Load()
	res, _ = callTool(t, env.srv, "ir_run_macro", map[string]any{"macro_name": "Movie Night"})
	if !res.IsError || stub.txCalls.Load() != before {
		t.Fatalf("expected ir_run_macro without confirm_ir to fail with 0 TX")
	}

	// Broken macro pre-validates step 2 and emits ZERO frames even for step 1.
	res, _ = callTool(t, env.srv, "ir_run_macro", map[string]any{"confirm_ir": true, "macro_name": "Broken Macro"})
	if !res.IsError || stub.txCalls.Load() != before {
		t.Fatalf("expected broken macro to emit 0 frames, txCalls delta=%d", stub.txCalls.Load()-before)
	}

	// Valid macro executes both steps.
	res, out = callTool(t, env.srv, "ir_run_macro", map[string]any{"confirm_ir": true, "macro_name": "Movie Night"})
	if res.IsError || out["steps_executed"].(float64) != 2 {
		t.Fatalf("unexpected valid macro output: %+v", out)
	}
}

func TestMLToolsAndPurgeNoiseIsolation(t *testing.T) {
	env := newTestEnv(t, nil)
	env.seedUserRemote("LG_TV.ir", []irfile.IRSignal{necPowerSignal()})
	env.seedUserRemote("NoiseCapture.ir", []irfile.IRSignal{ambientNoiseSignal()})
	packNoisePath := env.seedPackRemote("acs", "Daikin.ir", []irfile.IRSignal{ambientNoiseSignal()})
	env.seedPackRemote("acs", "LG_AC.ir", []irfile.IRSignal{necPowerSignal()})

	pt, err := irfile.DefaultRegistry().Encode(necPowerSignal())
	if err != nil {
		t.Fatalf("encode NEC: %v", err)
	}
	pulsesAny := make([]any, len(pt))
	for i, v := range pt {
		pulsesAny[i] = int(v)
	}

	// ir_analyze_pulses: valid + invalid empty/non-alternating.
	res, out := callTool(t, env.srv, "ir_analyze_pulses", map[string]any{"pulses": pulsesAny})
	if res.IsError || out["decoded_protocol"] == nil {
		t.Fatalf("expected decoded_protocol from ir_analyze_pulses, got %+v", out)
	}
	res, _ = callTool(t, env.srv, "ir_analyze_pulses", map[string]any{"pulses": []any{500, 500}})
	if !res.IsError {
		t.Fatalf("expected error for non-alternating pulses")
	}

	// ir_classify_signal from saved remote.
	res, out = callTool(t, env.srv, "ir_classify_signal", map[string]any{"remote_name": "LG_TV.ir", "button_name": "Power"})
	if res.IsError || out["label"] == "" {
		t.Fatalf("ir_classify_signal failed: %+v", out)
	}

	// ir_match_fingerprint.
	res, out = callTool(t, env.srv, "ir_match_fingerprint", map[string]any{"pulses": pulsesAny, "top_k": 3})
	if res.IsError || len(out["matches"].([]any)) == 0 {
		t.Fatalf("ir_match_fingerprint failed: %+v", out)
	}

	// ir_rank_probe_pack.
	res, out = callTool(t, env.srv, "ir_rank_probe_pack", map[string]any{
		"pack":             "acs",
		"action_filter":    "Power",
		"reference_pulses": pulsesAny,
	})
	if res.IsError || len(out["ranked_candidates"].([]any)) != 1 {
		t.Fatalf("ir_rank_probe_pack failed: %+v", out)
	}

	// ir_purge_noise defaults to dry_run: true (nothing deleted).
	res, out = callTool(t, env.srv, "ir_purge_noise", map[string]any{"min_confidence": 0.5})
	if res.IsError || out["dry_run"] != true || out["deleted_count"].(float64) != 0 {
		t.Fatalf("expected dry_run preview without deletion, got %+v", out)
	}

	// dry_run: false without confirm: true rejected.
	res, _ = callTool(t, env.srv, "ir_purge_noise", map[string]any{"dry_run": false})
	if !res.IsError {
		t.Fatalf("expected dry_run: false without confirm: true to fail")
	}

	// dry_run: false with confirm: true deletes UserLibrary noise ONLY, never touching DatabasePack.
	res, out = callTool(t, env.srv, "ir_purge_noise", map[string]any{"dry_run": false, "confirm": true, "min_confidence": 0.5})
	if res.IsError {
		t.Fatalf("ir_purge_noise failed: %+v", out)
	}
	if _, err := os.Stat(packNoisePath); err != nil {
		t.Fatalf("external read-only DatabasePack file was mutated/deleted: %v", err)
	}
	// Valid TV remote must still exist.
	if _, err := os.Stat(filepath.Join(env.userDir, "LG_TV.ir")); err != nil {
		t.Fatalf("valid user remote LG_TV.ir should not be purged: %v", err)
	}
}

func TestLibraryAndSourcesTools(t *testing.T) {
	env := newTestEnv(t, nil)
	env.seedPackRemote("acs", "Daikin.ir", []irfile.IRSignal{necPowerSignal()})

	// Save parsed + raw remote.
	res, out := callTool(t, env.srv, "ir_save_remote", map[string]any{
		"name": "LivingRoom",
		"buttons": []map[string]any{
			{"name": "Power", "type": "parsed", "protocol": "NEC", "address": "04 00 00 00", "command": "08 00 00 00"},
			{"name": "RawBtn", "type": "raw", "frequency": 38000, "duty_cycle": 0.33, "data": []int{9000, 4500, 560}},
		},
	})
	if res.IsError || out["saved"] != true || !strings.HasSuffix(out["path"].(string), "LivingRoom.ir") {
		t.Fatalf("ir_save_remote failed: %+v", out)
	}

	// Overwrite=true succeeds.
	res, out = callTool(t, env.srv, "ir_save_remote", map[string]any{
		"name":      "LivingRoom",
		"overwrite": true,
		"buttons": []map[string]any{
			{"name": "Power", "type": "parsed", "protocol": "NEC", "address": "04 00 00 00", "command": "08 00 00 00"},
			{"name": "RawBtn", "type": "raw", "frequency": 38000, "duty_cycle": 0.33, "data": []int{9000, 4500, 560}},
		},
	})
	if res.IsError || out["saved"] != true {
		t.Fatalf("ir_save_remote overwrite=true failed: %+v", out)
	}

	// Overwrite=false guard.
	res, _ = callTool(t, env.srv, "ir_save_remote", map[string]any{
		"name":    "LivingRoom",
		"buttons": []map[string]any{{"name": "Power", "type": "parsed", "protocol": "NEC", "address": "04 00 00 00", "command": "08 00 00 00"}},
	})
	if !res.IsError {
		t.Fatalf("expected overwrite=false collision error")
	}

	// List & Read.
	res, out = callTool(t, env.srv, "ir_list_remotes", map[string]any{"filter": "living"})
	if res.IsError || len(out["remotes"].([]any)) != 1 {
		t.Fatalf("ir_list_remotes failed: %+v", out)
	}
	res, out = callTool(t, env.srv, "ir_read_remote", map[string]any{"name": "LivingRoom.ir"})
	if res.IsError || len(out["buttons"].([]any)) != 2 {
		t.Fatalf("ir_read_remote failed: %+v", out)
	}

	// List databases (GEN:TV-B-Gone + GEN:TV-Service + acs) & Promote twice (collision-safe).
	res, out = callTool(t, env.srv, "ir_list_databases", map[string]any{})
	if res.IsError || len(out["packs"].([]any)) != 3 {
		t.Fatalf("ir_list_databases failed: %+v", out)
	}
	res, p1 := callTool(t, env.srv, "ir_promote_remote", map[string]any{"pack": "acs", "remote_name": "Daikin.ir"})
	if res.IsError || p1["promoted"] != true {
		t.Fatalf("first ir_promote_remote failed: %+v", p1)
	}
	res, p2 := callTool(t, env.srv, "ir_promote_remote", map[string]any{"pack": "acs", "remote_name": "Daikin.ir"})
	if res.IsError || p2["saved_as"] == p1["saved_as"] {
		t.Fatalf("expected collision-safe name on second promote, got %v vs %v", p1["saved_as"], p2["saved_as"])
	}

	// Delete remote: confirm=false rejected, confirm=true succeeds.
	res, _ = callTool(t, env.srv, "ir_delete_remote", map[string]any{"name": "LivingRoom.ir"})
	if !res.IsError {
		t.Fatalf("expected confirm=true gate on ir_delete_remote")
	}
	res, out = callTool(t, env.srv, "ir_delete_remote", map[string]any{"name": "LivingRoom.ir", "confirm": true})
	if res.IsError || out["deleted"] != true {
		t.Fatalf("ir_delete_remote failed: %+v", out)
	}

	// Sources: add, list, sync (TLS manifest), remove (partial miss), delete_pack_files.
	manifestSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/manifest.json":
			_, _ = w.Write([]byte(`{"version":1,"files":[{"name":"TestTV.ir","url":"https://` + r.Host + `/TestTV.ir"}]}`))
		case "/TestTV.ir":
			_, _ = w.Write([]byte("Filetype: IR signals file\nVersion: 1\n#\nname: Power\ntype: parsed\nprotocol: NEC\naddress: 04 00 00 00\ncommand: 08 00 00 00\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer manifestSrv.Close()
	env.srv.syncer = dbsync.NewSyncer(env.dbDir, manifestSrv.Client())

	res, out = callTool(t, env.srv, "ir_add_source", map[string]any{
		"id":   "local-tls",
		"kind": "json_manifest",
		"url":  manifestSrv.URL + "/manifest.json",
	})
	if res.IsError || out["added"] != true {
		t.Fatalf("ir_add_source failed: %+v", out)
	}
	res, out = callTool(t, env.srv, "ir_list_sources", map[string]any{})
	if res.IsError || len(out["sources"].([]any)) == 0 {
		t.Fatalf("ir_list_sources failed: %+v", out)
	}
	res, out = callTool(t, env.srv, "ir_sync_sources", map[string]any{"ids": []string{"local-tls"}})
	if res.IsError || len(out["results"].([]any)) != 1 {
		t.Fatalf("ir_sync_sources failed: %+v", out)
	}

	// Delete pack files: confirm=false rejected, confirm=true deletes existing & reports missing.
	res, _ = callTool(t, env.srv, "ir_delete_pack_files", map[string]any{"pack": "local-tls", "files": []string{"TestTV.ir"}})
	if !res.IsError {
		t.Fatalf("expected confirm=true gate on ir_delete_pack_files")
	}
	res, out = callTool(t, env.srv, "ir_delete_pack_files", map[string]any{
		"pack":    "local-tls",
		"files":   []string{"TestTV.ir", "Missing.ir"},
		"confirm": true,
	})
	if res.IsError || len(out["deleted"].([]any)) != 1 || len(out["not_found"].([]any)) != 1 {
		t.Fatalf("ir_delete_pack_files unexpected output: %+v", out)
	}

	// Remove source: confirm=false rejected, confirm=true handles partial miss + delete_files.
	res, _ = callTool(t, env.srv, "ir_remove_source", map[string]any{"ids": []string{"local-tls"}})
	if !res.IsError {
		t.Fatalf("expected confirm=true gate on ir_remove_source")
	}
	res, out = callTool(t, env.srv, "ir_remove_source", map[string]any{
		"ids":          []string{"local-tls", "does-not-exist"},
		"delete_files": true,
		"confirm":      true,
	})
	if res.IsError || len(out["removed_ids"].([]any)) != 1 || len(out["not_found_ids"].([]any)) != 1 {
		t.Fatalf("ir_remove_source unexpected output: %+v", out)
	}
}
