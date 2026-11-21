package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ir-studio/internal/dbsync"
	"ir-studio/internal/irfile"
	"ir-studio/internal/lirc"
)

func TestPathTraversalAndSymlinkEscapeRejected(t *testing.T) {
	env := newTestEnv(t, nil)
	env.seedUserRemote("Good.ir", []irfile.IRSignal{necPowerSignal()})

	// Create an outside file and a symlink inside UserDir pointing to it.
	outside := filepath.Join(env.dir, "secret.ir")
	if err := os.WriteFile(outside, []byte("Filetype: IR signals file\nVersion: 1\n"), 0o600); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	symlinkPath := filepath.Join(env.userDir, "evil_link.ir")
	if err := os.Symlink(outside, symlinkPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	badPayloads := []string{
		"",
		"   ",
		"../secret.ir",
		"../../etc/passwd",
		"a/../../b.ir",
		"/etc/passwd",
		`..\secret.ir`,
		`a\b.ir`,
		"%2e%2e/secret.ir",
		"%2E%2E",
		"foo\x00.ir",
		"bad\nname.ir",
		"bad\tname.ir",
		" LeadingSpace.ir",
		"evil_link.ir",
	}

	for _, payload := range badPayloads {
		t.Run("read_"+payload, func(t *testing.T) {
			res, _ := callTool(t, env.srv, "ir_read_remote", map[string]any{"name": payload})
			if !res.IsError {
				t.Fatalf("expected ir_read_remote to reject %q", payload)
			}
		})
		t.Run("delete_"+payload, func(t *testing.T) {
			res, _ := callTool(t, env.srv, "ir_delete_remote", map[string]any{"name": payload, "confirm": true})
			if !res.IsError {
				t.Fatalf("expected ir_delete_remote to reject %q", payload)
			}
		})
	}

	// Verify outside file was never touched.
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside file was unexpectedly modified/deleted: %v", err)
	}
}

func TestHTTPSOnlySourceValidation(t *testing.T) {
	env := newTestEnv(t, nil)
	badURLs := []string{
		"http://example.com/manifest.json",
		"file:///etc/passwd",
		"ftp://example.com/remotes.json",
		"javascript:alert(1)",
		"https://",
		"not-a-url",
	}
	for _, u := range badURLs {
		t.Run(u, func(t *testing.T) {
			if err := ValidateHTTPSURL(u); !errors.Is(err, ErrInsecureScheme) {
				t.Fatalf("ValidateHTTPSURL(%q): got %v, want ErrInsecureScheme", u, err)
			}
			res, _ := callTool(t, env.srv, "ir_add_source", map[string]any{
				"id":   "bad-src",
				"kind": "json_manifest",
				"url":  u,
			})
			if !res.IsError {
				t.Fatalf("expected ir_add_source to reject %q", u)
			}
		})
	}
}

func TestSaveRemoteMaxIRFileBytesCap(t *testing.T) {
	env := newTestEnv(t, nil)
	// Build a raw button whose serialized .ir exceeds 1 MiB (1,048,576 bytes).
	huge := make([]int, 270000)
	for i := range huge {
		huge[i] = 560
	}
	res, out := callTool(t, env.srv, "ir_save_remote", map[string]any{
		"name": "Huge.ir",
		"buttons": []map[string]any{
			{"name": "Big", "type": "raw", "frequency": 38000, "duty_cycle": 0.33, "data": huge},
		},
	})
	if !res.IsError || !strings.Contains(out["error"].(string), "1 MiB") {
		t.Fatalf("expected 1 MiB cap error, got res=%+v out=%+v", res, out)
	}
}

func TestConcurrentToolsCallUnderRace(t *testing.T) {
	env := newTestEnv(t, nil)
	env.seedUserRemote("LG_TV.ir", []irfile.IRSignal{necPowerSignal()})

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, _ = callTool(t, env.srv, "ir_get_status", map[string]any{})
			_, _ = callTool(t, env.srv, "ir_learn_signal", map[string]any{"timeout_ms": 500})
			_, _ = callTool(t, env.srv, "ir_transmit_button", map[string]any{
				"confirm_ir":  true,
				"remote_name": "LG_TV.ir",
				"button_name": "Power",
			})
			_, _ = callTool(t, env.srv, "ir_list_remotes", map[string]any{})
			if idx%2 == 0 {
				_, _ = callTool(t, env.srv, "ir_classify_signal", map[string]any{
					"remote_name": "LG_TV.ir",
					"button_name": "Power",
				})
			}
		}(i)
	}
	wg.Wait()

	// Verify carrier/duty cycle restored to default 38 kHz / 33% after concurrent TX.
	if got := env.sim.CarrierState(); got != lirc.DefaultCarrier() {
		t.Fatalf("carrier not restored after concurrent TX: got %+v, want %+v", got, lirc.DefaultCarrier())
	}
}

func TestServeStdioContextCancelAndOversizedLine(t *testing.T) {
	env := newTestEnv(t, nil)

	// Context already cancelled.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	err := env.srv.ServeStdio(ctx, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`+"\n"), &out)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	// Oversized line (> 2 MiB) returns -32700 parse error without crashing.
	out.Reset()
	hugeLine := strings.Repeat("a", stdioMaxBufSize+1024) + "\n"
	err = env.srv.ServeStdio(context.Background(), strings.NewReader(hugeLine), &out)
	if err == nil {
		t.Fatalf("expected scanner error for oversized line")
	}
	var resp struct {
		Error *rpcError `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
		t.Fatalf("expected JSON-RPC parse error frame on oversized line, got %q (%v)", out.String(), err)
	}
	if resp.Error == nil || resp.Error.Code != errCodeParseError {
		t.Fatalf("expected -32700, got %+v", resp.Error)
	}
}

func FuzzValidateSafeName(f *testing.F) {
	seeds := []string{"LG_TV.ir", "../etc/passwd", "a/b", `a\b`, "%2e%2e", "foo\x00.ir", "", "   "}
	for _, s := range seeds {
		f.Add(s)
	}
	root := f.TempDir()
	f.Fuzz(func(t *testing.T, name string) {
		if err := ValidateSafeName(name); err == nil {
			target, err := ResolveSandboxedPath(root, name)
			if err != nil {
				return
			}
			rel, err := filepath.Rel(root, target)
			if err != nil || strings.HasPrefix(rel, "..") {
				t.Fatalf("name %q escaped root %q -> %q", name, root, target)
			}
		}
	})
}

func TestEdgeCasesAndBackendCoverage(t *testing.T) {
	env := newTestEnv(t, nil)
	env.seedUserRemote("LG_TV.ir", []irfile.IRSignal{
		necPowerSignal(),
		{Name: "RawMute", Kind: irfile.SignalRaw, Frequency: 38000, DutyCycle: 0.33, RawData: []uint32{9000, 4500, 560}},
	})
	env.seedPackRemote("tvs", "Samsung.ir", []irfile.IRSignal{necPowerSignal()})

	// HardwareBackend: raw TX + repeat=2 + carrier override.
	res, out := callTool(t, env.srv, "ir_transmit_button", map[string]any{
		"confirm_ir":  true,
		"remote_name": "LG_TV.ir",
		"button_name": "RawMute",
		"repeat":      2,
		"carrier_hz":  40000,
		"duty_pct":    33,
	})
	if res.IsError || out["transmitted"] != true {
		t.Fatalf("raw TX failed: %+v", out)
	}
	if got := env.sim.CarrierState(); got != lirc.DefaultCarrier() {
		t.Fatalf("carrier not restored: got %+v", got)
	}

	// HardwareBackend with RXWorker: capture injected pulse train.
	rxCtx, rxCancel := context.WithCancel(context.Background())
	rx := lirc.NewRXWorker(env.sim, env.xcvr.Gate(), irfile.DefaultRegistry())
	rx.Start(rxCtx)
	defer func() {
		rxCancel()
		rx.Close()
	}()
	be := NewHardwareBackend(ServeConfig{
		Transceiver: env.xcvr,
		RX:          rx,
		Settings:    env.srv.settings,
	}, irfile.DefaultRegistry())
	pt, _ := irfile.DefaultRegistry().Encode(necPowerSignal())
	env.sim.InjectPulseTrain(pt)
	cap, err := be.LearnSignal(context.Background(), 500*time.Millisecond, 38000)
	if err != nil || len(cap.Pulses) == 0 {
		t.Fatalf("RXWorker LearnSignal failed: cap=%+v err=%v", cap, err)
	}
	if be.HardwareMode() != "SIM" || be.DevicePath() == "" {
		t.Fatalf("unexpected mode/path: %s / %s", be.HardwareMode(), be.DevicePath())
	}

	// Save remote validation errors.
	badSaves := []map[string]any{
		{"name": "Empty.ir", "buttons": []map[string]any{}},
		{"name": "NoName.ir", "buttons": []map[string]any{{"name": "", "type": "parsed"}}},
		{"name": "BadHex.ir", "buttons": []map[string]any{{"name": "P", "type": "parsed", "protocol": "NEC", "address": "ZZ", "command": "08 00 00 00"}}},
		{"name": "BadCmd.ir", "buttons": []map[string]any{{"name": "P", "type": "parsed", "protocol": "NEC", "address": "04 00 00 00", "command": "08"}}},
		{"name": "NoProto.ir", "buttons": []map[string]any{{"name": "P", "type": "parsed", "protocol": "", "address": "04 00 00 00", "command": "08 00 00 00"}}},
		{"name": "EmptyRaw.ir", "buttons": []map[string]any{{"name": "R", "type": "raw", "data": []int{}}}},
		{"name": "BadType.ir", "buttons": []map[string]any{{"name": "X", "type": "unknown"}}},
	}
	for _, args := range badSaves {
		res, _ := callTool(t, env.srv, "ir_save_remote", args)
		if !res.IsError {
			t.Fatalf("expected save validation error for %+v", args)
		}
	}

	// ML edge cases: top_k bounds, pack filter, reference_remote, bad min_confidence.
	res, _ = callTool(t, env.srv, "ir_match_fingerprint", map[string]any{"remote_name": "LG_TV.ir", "button_name": "Power", "top_k": 99})
	if !res.IsError {
		t.Fatalf("expected top_k > 10 error")
	}
	res, out = callTool(t, env.srv, "ir_match_fingerprint", map[string]any{"remote_name": "LG_TV.ir", "button_name": "Power", "pack": "tvs"})
	if res.IsError || len(out["matches"].([]any)) == 0 {
		t.Fatalf("expected pack-filtered match: %+v", out)
	}
	res, out = callTool(t, env.srv, "ir_rank_probe_pack", map[string]any{
		"pack":             "tvs",
		"reference_remote": "LG_TV.ir",
		"reference_button": "Power",
	})
	if res.IsError || len(out["ranked_candidates"].([]any)) == 0 {
		t.Fatalf("expected ranked candidates from reference_remote: %+v", out)
	}
	res, _ = callTool(t, env.srv, "ir_purge_noise", map[string]any{"min_confidence": 0.1})
	if !res.IsError {
		t.Fatalf("expected min_confidence < 0.5 error")
	}

	// GitHub tree source add + invalid kind.
	res, out = callTool(t, env.srv, "ir_add_source", map[string]any{
		"id":   "flipper-tvs",
		"kind": "github_tree",
		"url":  "https://github.com/Lucaslhm/Flipper-IRDB/tree/main/TVs",
	})
	if res.IsError || out["added"] != true {
		t.Fatalf("expected github_tree add success: %+v", out)
	}
	res, _ = callTool(t, env.srv, "ir_add_source", map[string]any{
		"id":   "bad-kind",
		"kind": "svn",
		"url":  "https://example.com/repo",
	})
	if !res.IsError {
		t.Fatalf("expected unsupported kind error")
	}

	// Read external pack remote.
	res, out = callTool(t, env.srv, "ir_read_remote", map[string]any{"name": "Samsung.ir", "pack": "tvs"})
	if res.IsError || len(out["buttons"].([]any)) != 1 {
		t.Fatalf("expected external pack read success: %+v", out)
	}

	// Sync all enabled sources (empty ids) + invalid ID in selectSyncSources.
	res, _ = callTool(t, env.srv, "ir_sync_sources", map[string]any{"ids": []string{"../evil"}})
	if !res.IsError {
		t.Fatalf("expected traversal rejection in ir_sync_sources")
	}

	// HardwareBackend nil-transceiver defaults & capturedSignal fallback.
	nilBE := NewHardwareBackend(ServeConfig{}, nil)
	if nilBE.HardwareMode() != "SIM" || nilBE.DevicePath() != "sim" || nilBE.CarrierSpec().FreqHz != irfile.DefaultFreqHz {
		t.Fatalf("unexpected nil backend defaults: mode=%s path=%s carrier=%+v", nilBE.HardwareMode(), nilBE.DevicePath(), nilBE.CarrierSpec())
	}
	if _, err := nilBE.TransmitSignal(context.Background(), necPowerSignal(), 1, irfile.CarrierSpec{FreqHz: 1000, DutyPct: 33}); err == nil {
		t.Fatalf("expected invalid carrier error")
	}
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := nilBE.TransmitSignal(cancelledCtx, necPowerSignal(), 1, lirc.DefaultCarrier()); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled from TransmitSignal, got %v", err)
	}
	if sig := capturedSignal(lirc.RXCapture{Pulses: pt}, irfile.DefaultRegistry(), 38000); sig.Protocol != "NEC" {
		t.Fatalf("expected NEC decode from capturedSignal, got %+v", sig)
	}
	if sig := capturedSignal(lirc.RXCapture{}, irfile.DefaultRegistry(), 38000); sig.Kind != irfile.SignalRaw {
		t.Fatalf("expected raw fallback from empty capturedSignal, got %+v", sig)
	}
	if srv := NewServerFromConfig(ServeConfig{}); srv == nil {
		t.Fatalf("expected non-nil NewServerFromConfig")
	}

	// Additional validation & error paths across tools.
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"ir_promote_remote", map[string]any{"pack": "tvs", "remote_name": "Samsung.ir", "target_name": "../escape.ir"}},
		{"ir_promote_remote", map[string]any{"pack": "tvs", "remote_name": "Missing.ir"}},
		{"ir_delete_pack_files", map[string]any{"pack": "tvs", "files": []string{}, "confirm": true}},
		{"ir_delete_pack_files", map[string]any{"pack": "../bad", "files": []string{"a.ir"}, "confirm": true}},
		{"ir_delete_pack_files", map[string]any{"pack": "tvs", "files": []string{"../escape.ir"}, "confirm": true}},
		{"ir_remove_source", map[string]any{"ids": []string{}, "confirm": true}},
		{"ir_remove_source", map[string]any{"ids": []string{"../evil"}, "confirm": true}},
		{"ir_run_macro", map[string]any{"confirm_ir": true, "macro_name": ""}},
		{"ir_run_macro", map[string]any{"confirm_ir": true, "macro_name": "NoSuchMacro"}},
		{"ir_analyze_pulses", map[string]any{}},
		{"ir_classify_signal", map[string]any{}},
		{"ir_rank_probe_pack", map[string]any{"pack": "../bad"}},
		{"ir_rank_probe_pack", map[string]any{"pack": "tvs"}},
		{"ir_read_remote", map[string]any{"name": "Missing.ir", "pack": "tvs"}},
		{"ir_read_remote", map[string]any{"name": "../bad.ir", "pack": "tvs"}},
		{"ir_read_remote", map[string]any{"name": "Samsung.ir", "pack": "../bad"}},
	} {
		res, _ := callTool(t, env.srv, tc.tool, tc.args)
		if !res.IsError {
			t.Fatalf("expected error for %s(%+v)", tc.tool, tc.args)
		}
	}

	// Invalid JSON arguments trigger -32602 RPC error.
	badReq := []byte(`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"ir_learn_signal","arguments":"not-an-object"}}`)
	if resp := env.srv.handleRequest(context.Background(), badReq); resp.Error == nil || resp.Error.Code != errCodeInvalidParams {
		t.Fatalf("expected -32602 for malformed tool arguments, got %+v", resp)
	}

	// Empty macrosPath returns empty list cleanly.
	env.srv.macrosPath = ""
	res, out = callTool(t, env.srv, "ir_list_macros", map[string]any{})
	if res.IsError || len(out["macros"].([]any)) != 0 {
		t.Fatalf("expected empty macros list: %+v", out)
	}

	// selectSyncSources with empty ids returns only enabled sources.
	sel, err := selectSyncSources([]dbsync.DatabaseSource{
		{ID: "a", Enabled: true},
		{ID: "b", Enabled: false},
	}, nil)
	if err != nil || len(sel) != 1 || sel[0].ID != "a" {
		t.Fatalf("selectSyncSources(nil): got %+v, err=%v", sel, err)
	}
}
