package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"

	"ir-studio/internal/irmodem"
	"ir-studio/internal/library"
)

func TestMCPProntoImportExportAndTVService(t *testing.T) {
	env := newTestEnv(t, nil)

	// 1. Export button from GEN:TV-Service virtual pack to Pronto Hex.
	expRes := callToolOK(t, env, "ir_export_pronto", map[string]any{
		"pack":        library.VirtualPackTVService,
		"remote_name": "LG_Service_Discrete.ir",
		"button_name": "IN_START",
	})
	prontoHex, _ := expRes["pronto_hex"].(string)
	if !strings.HasPrefix(prontoHex, "0000 006D ") {
		t.Fatalf("unexpected exported Pronto Hex: %q", prontoHex)
	}

	// 2. Import Pronto Hex back and save into UserLibrary.
	impRes := callToolOK(t, env, "ir_import_pronto", map[string]any{
		"name":       "LG_IN_START",
		"pronto_hex": prontoHex,
		"save_as":    "LG_Service_Copy.ir",
	})
	if impRes["saved_remote"] != "LG_Service_Copy.ir" {
		t.Fatalf("expected saved_remote LG_Service_Copy.ir, got %v", impRes)
	}

	// 3. Path traversal on save_as MUST be rejected.
	errRes := callToolErr(t, env, "ir_import_pronto", map[string]any{
		"name":       "Bad",
		"pronto_hex": prontoHex,
		"save_as":    "../escape.ir",
	})
	if !strings.Contains(errRes, "invalid") {
		t.Fatalf("expected path sandbox error, got %q", errRes)
	}
}

func TestMCPModemSendRecvAndTripwire(t *testing.T) {
	env := newTestEnv(t, nil)

	// 1. ir_modem_send requires confirm_ir: true.
	if msg := callToolErr(t, env, "ir_modem_send", map[string]any{
		"confirm_ir":   false,
		"payload_text": "hello",
	}); !strings.Contains(msg, "confirm_ir") {
		t.Fatalf("expected confirm_ir error, got %q", msg)
	}

	// 2. ir_modem_send with confirm_ir: true succeeds and restores 38 kHz / 33%.
	sendRes := callToolOK(t, env, "ir_modem_send", map[string]any{
		"confirm_ir":   true,
		"payload_text": "PING-38KHZ",
	})
	if sendRes["transmitted"] != true || sendRes["payload_bytes"].(float64) != 10 {
		t.Fatalf("unexpected ir_modem_send response: %v", sendRes)
	}

	// 3. ir_modem_recv decodes pulses directly.
	pulses, err := irmodem.EncodeFrame([]byte("OPTICAL-OK"))
	if err != nil {
		t.Fatalf("EncodeFrame: %v", err)
	}
	recvRes := callToolOK(t, env, "ir_modem_recv", map[string]any{
		"pulses": pulses,
	})
	if recvRes["received"] != true || recvRes["payload_text"] != "OPTICAL-OK" {
		t.Fatalf("unexpected ir_modem_recv response: %v", recvRes)
	}

	// 4. ir_tripwire_probe requires confirm_ir: true.
	if msg := callToolErr(t, env, "ir_tripwire_probe", map[string]any{
		"confirm_ir": false,
	}); !strings.Contains(msg, "confirm_ir") {
		t.Fatalf("expected confirm_ir error for tripwire, got %q", msg)
	}

	// 5. ir_tripwire_probe succeeds in SIM reflective mode with loopback.
	env.sim.SetLoopback(true)
	twRes := callToolOK(t, env, "ir_tripwire_probe", map[string]any{
		"confirm_ir": true,
		"mode":       "reflective",
		"ping_count": 2,
		"listen_ms":  10,
	})
	if twRes["triggered"] != true || twRes["echoes_received"].(float64) != 2 {
		t.Fatalf("unexpected tripwire response: %v", twRes)
	}
}

func callToolOK(t *testing.T, env *testEnv, name string, args map[string]any) map[string]any {
	t.Helper()
	rawArgs, _ := json.Marshal(args)
	resp := env.srv.dispatchTool(t.Context(), json.RawMessage("1"), mustMarshal(t, map[string]any{
		"name":      name,
		"arguments": json.RawMessage(rawArgs),
	}))
	if resp.Error != nil {
		t.Fatalf("%s RPC error: %+v", name, resp.Error)
	}
	res := resp.Result.(toolResult)
	if res.IsError {
		t.Fatalf("%s returned tool error: %s", name, res.Content[0].Text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
		t.Fatalf("%s unmarshal output: %v", name, err)
	}
	return out
}

func callToolErr(t *testing.T, env *testEnv, name string, args map[string]any) string {
	t.Helper()
	rawArgs, _ := json.Marshal(args)
	resp := env.srv.dispatchTool(t.Context(), json.RawMessage("1"), mustMarshal(t, map[string]any{
		"name":      name,
		"arguments": json.RawMessage(rawArgs),
	}))
	if resp.Error != nil {
		return resp.Error.Message
	}
	res := resp.Result.(toolResult)
	if !res.IsError {
		t.Fatalf("%s expected tool error, got success: %s", name, res.Content[0].Text)
	}
	return res.Content[0].Text
}

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
