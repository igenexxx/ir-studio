package mcpserver

import (
	"os"
	"path/filepath"
	"testing"

	"ir-studio/internal/irfile"
	"ir-studio/internal/library"
)

func TestMCPTVBGoneVirtualPackIntegration(t *testing.T) {
	env := newTestEnv(t, nil)

	// ir_list_databases exposes GEN:TV-B-Gone with virtual: true and 100 remotes.
	res, out := callTool(t, env.srv, "ir_list_databases", map[string]any{})
	if res.IsError {
		t.Fatalf("ir_list_databases error: %v", out)
	}
	packs := out["packs"].([]any)
	if len(packs) == 0 {
		t.Fatalf("expected GEN:TV-B-Gone in packs")
	}
	first := packs[0].(map[string]any)
	if first["id"] != library.VirtualPackTVBGone || first["virtual"] != true || first["remote_count"].(float64) != 100 {
		t.Fatalf("unexpected virtual pack metadata: %+v", first)
	}

	// ir_read_remote, ir_transmit_button, ir_promote_remote, ir_rank_probe_pack.
	res, out = callTool(t, env.srv, "ir_read_remote", map[string]any{
		"pack": library.VirtualPackTVBGone,
		"name": "Samsung_TV.ir",
	})
	if res.IsError || len(out["buttons"].([]any)) != 1 {
		t.Fatalf("ir_read_remote virtual failed: %+v", out)
	}

	res, out = callTool(t, env.srv, "ir_transmit_button", map[string]any{
		"confirm_ir":  true,
		"pack":        library.VirtualPackTVBGone,
		"remote_name": "Samsung_TV.ir",
		"button_name": "Power",
	})
	if res.IsError || out["transmitted"] != true {
		t.Fatalf("ir_transmit_button virtual failed: %+v", out)
	}

	res, out = callTool(t, env.srv, "ir_promote_remote", map[string]any{
		"pack":        library.VirtualPackTVBGone,
		"remote_name": "Samsung_TV.ir",
	})
	if res.IsError || out["promoted"] != true {
		t.Fatalf("ir_promote_remote virtual failed: %+v", out)
	}

	pt, _ := irfile.DefaultRegistry().Encode(necPowerSignal())
	pulsesAny := make([]any, len(pt))
	for i, v := range pt {
		pulsesAny[i] = int(v)
	}
	res, out = callTool(t, env.srv, "ir_rank_probe_pack", map[string]any{
		"pack":             library.VirtualPackTVBGone,
		"action_filter":    "Power",
		"reference_pulses": pulsesAny,
	})
	if res.IsError || len(out["ranked_candidates"].([]any)) != 100 {
		t.Fatalf("ir_rank_probe_pack virtual failed: err=%v count=%d", res.IsError, len(out["ranked_candidates"].([]any)))
	}

	// ir_delete_pack_files MUST refuse virtual pack with ErrReadOnlyPack.
	res, _ = callTool(t, env.srv, "ir_delete_pack_files", map[string]any{
		"pack":    library.VirtualPackTVBGone,
		"files":   []string{"Samsung_TV.ir"},
		"confirm": true,
	})
	if !res.IsError {
		t.Fatalf("expected ir_delete_pack_files to reject virtual pack")
	}
}

func TestMCPSendACStateAndSweepCarrierTools(t *testing.T) {
	env := newTestEnv(t, nil)
	env.seedUserRemote("LG_TV.ir", []irfile.IRSignal{necPowerSignal()})

	// Rejected AC calls (zero pulses emitted).
	badAC := []map[string]any{
		{"confirm_ir": false, "brand": "Daikin", "power": true, "mode": "COOL", "temp_c": 22},
		{"confirm_ir": true, "brand": "Daikin", "power": true, "mode": "COOL", "temp_c": 12},
		{"confirm_ir": true, "brand": "Unknown", "power": true, "mode": "COOL", "temp_c": 22},
		{"confirm_ir": true, "brand": "Daikin", "power": true, "mode": "COOL", "temp_c": 22, "save_as": "../evil.ir"},
	}
	for _, args := range badAC {
		res, _ := callTool(t, env.srv, "ir_send_ac_state", args)
		if !res.IsError {
			t.Fatalf("expected error for bad AC args %+v", args)
		}
	}
	if len(env.sim.TxHistory()) != 0 {
		t.Fatalf("expected 0 TX bursts for rejected AC calls, got %d", len(env.sim.TxHistory()))
	}

	// Valid AC transmit + save_as.
	res, out := callTool(t, env.srv, "ir_send_ac_state", map[string]any{
		"confirm_ir": true,
		"brand":      "Daikin",
		"power":      true,
		"mode":       "COOL",
		"temp_c":     22,
		"fan":        "HIGH",
		"swing":      true,
		"save_as":    "AC_Daikin.ir",
	})
	if res.IsError || out["transmitted"] != true || out["checksum_kind"] != "SUM8" || out["saved_remote"] != "AC_Daikin.ir" {
		t.Fatalf("ir_send_ac_state failed: %+v", out)
	}
	if _, err := os.Stat(filepath.Join(env.userDir, "AC_Daikin.ir")); err != nil {
		t.Fatalf("expected AC_Daikin.ir persisted: %v", err)
	}

	// Rejected sweep calls.
	badSweep := []map[string]any{
		{"confirm_ir": false, "remote_name": "LG_TV.ir", "button_name": "Power"},
		{"confirm_ir": true, "remote_name": "LG_TV.ir", "button_name": "Power", "frequencies_hz": []int{99000}},
		{"confirm_ir": true, "remote_name": "LG_TV.ir", "button_name": "Power", "frequencies_hz": []int{36000, 37000, 38000, 39000, 40000, 41000, 42000, 43000, 44000}},
		{"confirm_ir": true},
	}
	beforeTx := len(env.sim.TxHistory())
	for _, args := range badSweep {
		res, _ := callTool(t, env.srv, "ir_sweep_carrier", args)
		if !res.IsError {
			t.Fatalf("expected error for bad sweep args %+v", args)
		}
	}
	if len(env.sim.TxHistory()) != beforeTx {
		t.Fatalf("rejected sweep emitted pulses")
	}

	// Valid sweep on virtual pack GEN:TV-B-Gone.
	res, out = callTool(t, env.srv, "ir_sweep_carrier", map[string]any{
		"confirm_ir":  true,
		"pack":        library.VirtualPackTVBGone,
		"remote_name": "Samsung_TV.ir",
		"button_name": "Power",
		"gap_ms":      40,
	})
	if res.IsError || out["cancelled"] != false || out["restored_freq_hz"].(float64) != 38000 {
		t.Fatalf("ir_sweep_carrier failed: %+v", out)
	}
	if len(out["steps"].([]any)) != 4 {
		t.Fatalf("expected 4 sweep steps, got %d", len(out["steps"].([]any)))
	}
}
