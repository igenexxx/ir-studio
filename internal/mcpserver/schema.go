package mcpserver

// toolDefinitions is the Single Source of Truth (SSOT) for all 28 MCP tools.
func toolDefinitions() []toolDefinition {
	return []toolDefinition{
		{
			Name:        "ir_get_status",
			Description: "Return hardware mode (LIRC/SIM), carrier settings, ambient IR noise, library/macro counts, and MCP status.",
			InputSchema: objSchema(nil, nil),
		},
		{
			Name:        "ir_learn_signal",
			Description: "Capture one IR frame within timeout_ms, decode protocol, classify with on-device ML, and match fingerprints.",
			InputSchema: objSchema(map[string]any{
				"timeout_ms": intProp("Capture window in milliseconds", 500, 15000),
				"carrier_hz": intProp("Expected carrier frequency in Hz (default 38000)", 36000, 56000),
			}, nil),
		},
		{
			Name:        "ir_transmit_button",
			Description: "Transmit a button from a UserLibrary remote or read-only DatabasePack. Requires confirm_ir: true.",
			InputSchema: objSchema(map[string]any{
				"confirm_ir":  boolProp("Must be true to emit IR signals"),
				"remote_name": strProp("Remote filename (e.g. LG_TV.ir)"),
				"button_name": strProp("Button name inside the remote"),
				"pack":        strProp("Optional external DatabasePack ID; omit for UserLibrary"),
				"repeat":      intProp("Repeat count (1..5, default 1)", 1, maxTransmitRepeat),
				"carrier_hz":  intProp("Optional carrier frequency override (36000..56000 Hz)", 36000, 56000),
				"duty_pct":    intProp("Optional duty cycle override (25..50%)", 25, 50),
			}, []string{"confirm_ir", "remote_name", "button_name"}),
		},
		{
			Name:        "ir_run_macro",
			Description: "Execute a saved multi-step IR macro by name or ID. Requires confirm_ir: true.",
			InputSchema: objSchema(map[string]any{
				"confirm_ir": boolProp("Must be true to emit IR signals"),
				"macro_name": strProp("Macro name or ID from macros.json"),
			}, []string{"confirm_ir", "macro_name"}),
		},
		{
			Name:        "ir_list_macros",
			Description: "List all saved IR macros and their steps from macros.json.",
			InputSchema: objSchema(nil, nil),
		},
		{
			Name:        "ir_analyze_pulses",
			Description: "Run 1D K-Means pulse clustering, glitch denoising, and protocol decoding on raw pulses or a saved button.",
			InputSchema: objSchema(map[string]any{
				"pulses":      arrIntProp("Alternating signed +mark/-space microsecond pulse durations"),
				"remote_name": strProp("Optional remote filename instead of pulses"),
				"button_name": strProp("Optional button name when remote_name is set"),
				"pack":        strProp("Optional external DatabasePack ID"),
			}, nil),
		},
		{
			Name:        "ir_classify_signal",
			Description: "Classify an IR pulse train or saved button using the on-device MLP classifier.",
			InputSchema: objSchema(map[string]any{
				"pulses":      arrIntProp("Alternating signed +mark/-space microsecond pulse durations"),
				"remote_name": strProp("Optional remote filename instead of pulses"),
				"button_name": strProp("Optional button name when remote_name is set"),
				"pack":        strProp("Optional external DatabasePack ID"),
				"carrier_hz":  intProp("Optional carrier frequency in Hz (default 38000)", 10000, 100000),
			}, nil),
		},
		{
			Name:        "ir_match_fingerprint",
			Description: "Compute an L2-normalized 8D timing embedding and rank top-K matching buttons across libraries.",
			InputSchema: objSchema(map[string]any{
				"pulses":      arrIntProp("Alternating signed +mark/-space microsecond pulse durations"),
				"remote_name": strProp("Optional remote filename instead of pulses"),
				"button_name": strProp("Optional button name when remote_name is set"),
				"carrier_hz":  intProp("Carrier frequency in Hz (default 38000)", 10000, 100000),
				"top_k":       intProp("Maximum matches to return (1..10, default 5)", 1, 10),
				"pack":        strProp("Optional DatabasePack filter"),
			}, nil),
		},
		{
			Name:        "ir_rank_probe_pack",
			Description: "Rank buttons in a DatabasePack by timing similarity to a reference signal for Smart Probe.",
			InputSchema: objSchema(map[string]any{
				"pack":             strProp("External DatabasePack ID"),
				"action_filter":    strProp("Optional button action filter (e.g. Power, Mute)"),
				"reference_pulses": arrIntProp("Reference alternating +mark/-space pulse train"),
				"reference_remote": strProp("Optional reference remote filename"),
				"reference_button": strProp("Optional reference button name"),
			}, []string{"pack"}),
		},
		{
			Name:        "ir_purge_noise",
			Description: "Scan UserLibrary ONLY for ambient-noise captures. Defaults to dry_run: true; requires confirm: true when dry_run: false.",
			InputSchema: objSchema(map[string]any{
				"dry_run":        boolProp("Preview candidates without deleting (default true)"),
				"confirm":        boolProp("Must be true when dry_run is false"),
				"min_confidence": numProp("Minimum noise confidence threshold (0.5..0.99, default 0.75)", 0.5, 0.99),
			}, nil),
		},
		{
			Name:        "ir_list_remotes",
			Description: "List saved .ir remotes in UserLibrary with optional substring filter.",
			InputSchema: objSchema(map[string]any{
				"filter": strProp("Optional case-insensitive substring filter"),
			}, nil),
		},
		{
			Name:        "ir_read_remote",
			Description: "Read and parse a Flipper .ir remote from UserLibrary or a read-only DatabasePack (1 MiB cap).",
			InputSchema: objSchema(map[string]any{
				"name": strProp("Remote filename or relative path inside pack"),
				"pack": strProp("Optional external DatabasePack ID"),
			}, []string{"name"}),
		},
		{
			Name:        "ir_save_remote",
			Description: "Validate and atomically save a Flipper .ir remote into UserLibrary (1 MiB cap).",
			InputSchema: objSchema(map[string]any{
				"name":      strProp("Target remote name (.ir auto-appended)"),
				"buttons":   arrObjProp("Non-empty array of IR button objects"),
				"overwrite": boolProp("Allow overwriting an existing UserLibrary remote (default false)"),
			}, []string{"name", "buttons"}),
		},
		{
			Name:        "ir_delete_remote",
			Description: "Delete a remote from UserLibrary ONLY. Requires confirm: true.",
			InputSchema: objSchema(map[string]any{
				"name":    strProp("UserLibrary remote filename to delete"),
				"confirm": boolProp("Must be true to delete the file"),
			}, []string{"name", "confirm"}),
		},
		{
			Name:        "ir_list_databases",
			Description: "List installed read-only external DatabasePacks and sync metadata.",
			InputSchema: objSchema(nil, nil),
		},
		{
			Name:        "ir_promote_remote",
			Description: "Copy a read-only DatabasePack remote into UserLibrary with collision-safe naming.",
			InputSchema: objSchema(map[string]any{
				"pack":        strProp("Source DatabasePack ID"),
				"remote_name": strProp("Source remote filename or relative path inside pack"),
				"target_name": strProp("Optional destination name in UserLibrary"),
			}, []string{"pack", "remote_name"}),
		},
		{
			Name:        "ir_list_sources",
			Description: "List configured external .ir database sources from sources.json.",
			InputSchema: objSchema(nil, nil),
		},
		{
			Name:        "ir_add_source",
			Description: "Add or update an HTTPS-only external .ir database source in sources.json.",
			InputSchema: objSchema(map[string]any{
				"id":      strProp("Source slug ID (lowercase alphanumeric, -, _)"),
				"kind":    enumProp("Source type", []string{"github_tree", "json_manifest", "github", "manifest"}),
				"url":     strProp("HTTPS-only repository or manifest URL"),
				"branch":  strProp("Optional Git branch (default main)"),
				"subpath": strProp("Optional repository subdirectory"),
				"prune":   boolProp("Prune deleted remote files on sync (default false)"),
			}, []string{"id", "kind", "url"}),
		},
		{
			Name:        "ir_remove_source",
			Description: "Remove one or more database sources from sources.json (optionally deleting synced pack files). Requires confirm: true.",
			InputSchema: objSchema(map[string]any{
				"ids":          arrStrProp("Non-empty list of source IDs to remove"),
				"delete_files": boolProp("Also delete local synced pack directory (default false)"),
				"confirm":      boolProp("Must be true to remove sources"),
			}, []string{"ids", "confirm"}),
		},
		{
			Name:        "ir_sync_sources",
			Description: "Run incremental HTTPS ETag/SHA delta sync for selected or all enabled database sources.",
			InputSchema: objSchema(map[string]any{
				"ids": arrStrProp("Optional list of source IDs; omit to sync all enabled sources"),
			}, nil),
		},
		{
			Name:        "ir_delete_pack_files",
			Description: "Delete specific .ir files inside a synced DatabasePack directory. Requires confirm: true.",
			InputSchema: objSchema(map[string]any{
				"pack":    strProp("DatabasePack ID"),
				"files":   arrStrProp("Non-empty list of relative .ir filenames inside the pack"),
				"confirm": boolProp("Must be true to delete pack files"),
			}, []string{"pack", "files", "confirm"}),
		},
		{
			Name:        "ir_send_ac_state",
			Description: "Encode and transmit a checksummed parametric AC state frame (Gree, Midea, Daikin, LG_AC, Samsung_AC, Mitsubishi). Requires confirm_ir: true.",
			InputSchema: objSchema(map[string]any{
				"confirm_ir": boolProp("Must be true to emit IR signals"),
				"brand":      enumProp("AC protocol family", []string{"Gree", "Midea", "Daikin", "LG_AC", "Samsung_AC", "Mitsubishi"}),
				"power":      boolProp("AC power state (true=ON, false=OFF)"),
				"mode":       enumProp("HVAC operating mode", []string{"COOL", "HEAT", "DRY", "FAN", "AUTO"}),
				"temp_c":     intProp("Target temperature in Celsius (16..30)", 16, 30),
				"fan":        enumProp("Fan speed (default AUTO)", []string{"AUTO", "LOW", "MED", "HIGH"}),
				"swing":      boolProp("Vertical vane swing enabled (default false)"),
				"save_as":    strProp("Optional UserLibrary .ir filename to persist the encoded AC button"),
			}, []string{"confirm_ir", "brand", "power", "mode", "temp_c"}),
		},
		{
			Name:        "ir_sweep_carrier",
			Description: "Rapidly resend an IR signal across multiple carrier frequencies (default 36/38/40/56 kHz) with guaranteed 38 kHz / 33% restoration. Requires confirm_ir: true.",
			InputSchema: objSchema(map[string]any{
				"confirm_ir":     boolProp("Must be true to emit IR signals"),
				"remote_name":    strProp("Optional remote filename (with button_name)"),
				"button_name":    strProp("Optional button name inside remote_name"),
				"pack":           strProp("Optional DatabasePack ID (including GEN:TV-B-Gone)"),
				"pulses":         arrIntProp("Optional alternating signed +mark/-space microsecond pulse durations"),
				"frequencies_hz": arrIntProp("Optional carrier frequencies in Hz (36000..56000, max 8; default [36000,38000,40000,56000])"),
				"duty_pct":       intProp("Duty cycle percent (25..50, default 33)", 25, 50),
				"gap_ms":         intProp("Inter-step delay in milliseconds (40..1000, default 80)", 40, 1000),
			}, []string{"confirm_ir"}),
		},
		{
			Name:        "ir_import_pronto",
			Description: "Parse a Pronto Hex (0000 ...) string into a Flipper RAW IRSignal and optionally save/upsert it into UserLibrary.",
			InputSchema: objSchema(map[string]any{
				"name":       strProp("Button name for the imported IR signal"),
				"pronto_hex": strProp("Space-separated raw learned Pronto Hex string starting with 0000"),
				"save_as":    strProp("Optional UserLibrary .ir filename to persist the imported button"),
			}, []string{"name", "pronto_hex"}),
		},
		{
			Name:        "ir_export_pronto",
			Description: "Export a saved remote button or raw pulse train as a lossless Pronto Hex (0000 ...) string.",
			InputSchema: objSchema(map[string]any{
				"remote_name": strProp("Optional remote filename (with button_name)"),
				"button_name": strProp("Optional button name inside remote_name"),
				"pack":        strProp("Optional DatabasePack ID (including GEN:TV-B-Gone and GEN:TV-Service)"),
				"pulses":      arrIntProp("Optional alternating signed +mark/-space microsecond pulse durations"),
				"carrier_hz":  intProp("Optional carrier frequency in Hz for raw pulses (default 38000)", 10000, 100000),
			}, nil),
		},
		{
			Name:        "ir_modem_send",
			Description: "Encode a <= 64-byte payload (UTF-8 text or hex) with CRC-8 via internal/irmodem and transmit at 38 kHz. Requires confirm_ir: true.",
			InputSchema: objSchema(map[string]any{
				"confirm_ir":   boolProp("Must be true to emit IR signals"),
				"payload_text": strProp("UTF-8 payload string (<= 64 bytes; mutually exclusive with payload_hex)"),
				"payload_hex":  strProp("Hex-encoded payload bytes (<= 64 bytes; mutually exclusive with payload_text)"),
				"carrier_hz":   intProp("Optional carrier frequency in Hz (36000..56000, default 38000)", 36000, 56000),
			}, []string{"confirm_ir"}),
		},
		{
			Name:        "ir_modem_recv",
			Description: "Capture an IR frame or decode supplied pulses using the 38 kHz optical data modem (CRC-8 verified).",
			InputSchema: objSchema(map[string]any{
				"timeout_ms": intProp("Capture window in milliseconds (500..15000, default 5000)", 500, 15000),
				"pulses":     arrIntProp("Optional alternating signed +mark/-space pulse train to decode offline"),
			}, nil),
		},
		{
			Name:        "ir_tripwire_probe",
			Description: "Emit bounded 38 kHz ping bursts and measure RX optical reflection or beam-break. Requires confirm_ir: true.",
			InputSchema: objSchema(map[string]any{
				"confirm_ir": boolProp("Must be true to emit IR signals"),
				"mode":       enumProp("Detection mode (default reflective)", []string{"reflective", "beam_break"}),
				"ping_count": intProp("Number of 38 kHz pings (1..10, default 3)", 1, 10),
				"carrier_hz": intProp("Carrier frequency in Hz (36000..56000, default 38000)", 36000, 56000),
				"listen_ms":  intProp("Per-ping RX listen window in milliseconds (5..500, default 35)", 5, 500),
			}, []string{"confirm_ir"}),
		},
	}
}

func objSchema(props map[string]any, required []string) map[string]any {
	if props == nil {
		props = map[string]any{}
	}
	schema := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func strProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func boolProp(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}

func intProp(desc string, minVal, maxVal int) map[string]any {
	return map[string]any{"type": "integer", "description": desc, "minimum": minVal, "maximum": maxVal}
}

func numProp(desc string, minVal, maxVal float64) map[string]any {
	return map[string]any{"type": "number", "description": desc, "minimum": minVal, "maximum": maxVal}
}

func enumProp(desc string, values []string) map[string]any {
	return map[string]any{"type": "string", "description": desc, "enum": values}
}

func arrStrProp(desc string) map[string]any {
	return map[string]any{"type": "array", "description": desc, "items": map[string]any{"type": "string"}}
}

func arrIntProp(desc string) map[string]any {
	return map[string]any{"type": "array", "description": desc, "items": map[string]any{"type": "integer"}}
}

func arrObjProp(desc string) map[string]any {
	return map[string]any{"type": "array", "description": desc, "items": map[string]any{"type": "object"}}
}
