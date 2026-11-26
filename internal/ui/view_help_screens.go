package ui

func helpForHome(expert bool) ScreenHelp {
	summary := [2]string{
		"Main launcher for 38 kHz IR tools (Simple).",
		"Press 5 EXP to unlock +3 engineering labs.",
	}
	if expert {
		summary = [2]string{
			"Main launcher in Expert Mode (all 8 tools).",
			"Includes IR Lab, Macros & DB Browser.",
		}
	}
	return ScreenHelp{
		ScreenTitle: "HOME MENU",
		Summary:     summary,
		Keys: []HelpKeyHint{
			{"F/X", "Move selection up / down", false},
			{"4/Ent", "Open highlighted tool", true},
			{"5 EXP", "Toggle Simple / Expert Mode (saved)", true},
			{"6 READ", "Quick-open Read & Replay wizard", true},
			{"7 SEND", "Quick-open last saved remote", true},
			{"Esc/8", "Exit prompt (Enter/Esc confirms)", true},
			{"Tab", "Cycle screens (Expert) or Home", false},
			{"Q / H", "Quit IR Studio / Toggle this Help", false},
		},
	}
}

func helpForRead(captured, hasDBMatch, expert bool) ScreenHelp {
	if !captured {
		keys := []HelpKeyHint{}
		if expert {
			keys = append(keys, HelpKeyHint{"7 LAB", "Jump to Expert IR Lab oscilloscope", true})
		}
		keys = append(keys,
			HelpKeyHint{"Esc/8", "Return to Home Menu", true},
			HelpKeyHint{"H", "Show or hide this Help window", false},
			HelpKeyHint{"Q", "Quit IR Studio immediately", false},
		)
		return ScreenHelp{
			ScreenTitle: "READ & REPLAY (LISTEN)",
			Summary: [2]string{
				"Aim IR remote 5-20 cm & press any button.",
				"PulseNet-IR filters ambient room flicker.",
			},
			Keys: keys,
		}
	}
	keys := []HelpKeyHint{
		{"4/Ent", "Replay captured IR signal immediately", true},
		{"5 SAVE", "Save .ir & open in Remote Deck", true},
		{"6/Spc", "Discard & listen for next button", true},
	}
	if hasDBMatch {
		keys = append(keys,
			HelpKeyHint{"7/1", "Add full matched DB remote to My Remotes", true},
			HelpKeyHint{"R", "Sweep TX carrier (36/38/40/56 kHz)", false},
		)
	} else {
		keys = append(keys,
			HelpKeyHint{"7/R", "Sweep TX carrier (36/38/40/56 kHz)", true},
		)
	}
	keys = append(keys, HelpKeyHint{"Esc/8", "Cancel sweep or return to Home Menu", true})
	return ScreenHelp{
		ScreenTitle: "READ & REPLAY (CAPTURED)",
		Summary: [2]string{
			"Signal captured! Test replay, save, or sweep.",
			"Noise Guard warns before saving room flicker.",
		},
		Keys: keys,
	}
}

func helpForSaved(expert, scopeUser bool) ScreenHelp {
	if !expert {
		return ScreenHelp{
			ScreenTitle: "MY REMOTES",
			Summary: [2]string{
				"Personal .ir remotes (~/ir-studio/remotes).",
				"Open remote deck, quick-send, add, or delete.",
			},
			Keys: []HelpKeyHint{
				{"F/X", "Select saved .ir remote", false},
				{"4/Ent", "Open highlighted remote in Remote Deck", true},
				{"5 SEND", "Transmit first button of remote", true},
				{"6 NEW", "Capture a new remote (Read & Replay)", true},
				{"7/Del", "Delete selected remote (2-step)", true},
				{"Esc/8", "Return to Home Menu", true},
			},
		}
	}
	if !scopeUser {
		return ScreenHelp{
			ScreenTitle: "DB BROWSER (READ-ONLY)",
			Summary: [2]string{
				"Browse synced Flipper-IRDB packs (read-only).",
				"Promote any DB remote into My Remotes safely.",
			},
			Keys: []HelpKeyHint{
				{"F/X", "Scroll through remotes in active pack", false},
				{"Z/C", "Switch synced Flipper-IRDB pack", false},
				{"4/Ent", "Open DB remote in Remote Deck", true},
				{"5 SEND", "Quick-send first button of DB remote", true},
				{"6 PRM", "Promote (copy) DB remote to USER lib", true},
				{"8 SRC", "Switch back to My Remotes (USER)", true},
				{"Esc", "Return to Home Menu", false},
			},
		}
	}
	return ScreenHelp{
		ScreenTitle: "MY REMOTES (EXPERT)",
		Summary: [2]string{
			"Manage read-write User .ir library safely.",
			"Supports [x] batch delete & ML noise purge.",
		},
		Keys: []HelpKeyHint{
			{"F/X", "Move cursor through USER remotes", false},
			{"4/Ent", "Open highlighted remote in Remote Deck", true},
			{"5 SEND", "Transmit first button of remote", true},
			{"6 PRG", "Purge ML-detected noise (2-step)", true},
			{"7/Del", "Delete selected or [x] batch (2-step)", true},
			{"8 SRC", "Switch to read-only DB Browser", true},
			{"1/Spc", "Mark/unmark [x] for batch delete", false},
			{"Esc", "Return to Home Menu", false},
		},
	}
}

func helpForRemote(expert, readOnly bool) ScreenHelp {
	k8 := HelpKeyHint{"8 BACK", "Return to My Remotes list", true}
	if readOnly {
		k8 = HelpKeyHint{"8 PRM", "Promote read-only DB remote to USER", true}
	}
	if !expert {
		return ScreenHelp{
			ScreenTitle: "REMOTE DECK",
			Summary: [2]string{
				"2-column virtual remote button pad.",
				"Send, hold-repeat, learn new buttons, or edit.",
			},
			Keys: []HelpKeyHint{
				{"F/X/Z/C", "Move cursor across 2-col button grid", false},
				{"4/Ent", "Transmit highlighted button (1x)", true},
				{"5 ADD+", "Learn & append a new IR button", true},
				{"6/R", "Rename highlighted button (Btn_N)", true},
				{"7/Del", "Delete highlighted button (2-step)", true},
				k8,
				{"Space", "Transmit highlighted button (3x burst)", false},
				{"1", "Open Smart AC Controller overlay", false},
				{"Esc", "Cancel TX/learn or back to My Remotes", false},
			},
		}
	}
	return ScreenHelp{
		ScreenTitle: "REMOTE DECK (EXPERT)",
		Summary: [2]string{
			"Virtual remote deck with carrier sweep.",
			"Press 5 SWEEP if device ignores 38 kHz.",
		},
		Keys: []HelpKeyHint{
			{"F/X/Z/C", "Move cursor across 2-col button grid", false},
			{"4/Ent", "Transmit highlighted button (1x)", true},
			{"5 SWP", "Sweep button across 36/38/40/56 kHz", true},
			{"6 ADD+", "Learn & append a new IR button", true},
			{"7/Del", "Delete highlighted button (2-step)", true},
			k8,
			{"Space", "Transmit highlighted button (3x burst)", false},
			{"R / 1", "Rename button / Open Smart AC overlay", false},
			{"Esc", "Cancel sweep/learn or back to Saved", false},
		},
	}
}

func helpForProbe(running, expert bool) ScreenHelp {
	if running {
		return ScreenHelp{
			ScreenTitle: "UNIVERSAL OFF (RUNNING)",
			Summary: [2]string{
				"Auto-sending IR codes from active pack.",
				"Stop immediately on reaction, then rewind.",
			},
			Keys: []HelpKeyHint{
				{"4/Spc", "STOP sweep immediately & pause", true},
				{"Esc", "STOP sweep immediately", false},
				{"Z/C", "Adjust probe interval (0.25s - 5.0s)", false},
				{"F/X", "Stop & rewind along > / * trail", false},
				{"5 TRY", "Stop & single-fire focused candidate", true},
				{"6 SAVE", "Save winning remote to My Remotes", true},
			},
		}
	}
	if !expert {
		return ScreenHelp{
			ScreenTitle: "UNIVERSAL OFF",
			Summary: [2]string{
				"Sweep GEN:TV-B-Gone (100 codes) or DB packs.",
				"Rewind via >/* markers & save winning remote.",
			},
			Keys: []HelpKeyHint{
				{"4/Ent", "Start / stop Universal Off sweep", true},
				{"5/Spc", "Single-fire highlighted candidate", true},
				{"6 SAVE", "Promote winning DB remote to USER", true},
				{"7 PACK", "Cycle GEN:TVBG / DB packs / USER", true},
				{"F/X", "Step cursor up/down (rewind >/*)", false},
				{"Z/C", "Adjust probe interval (0.25s - 5.0s)", false},
				{"R", "Smart-rank pack by last RX capture", false},
				{"Esc/8", "Return to Home Menu", true},
			},
		}
	}
	return ScreenHelp{
		ScreenTitle: "UNIVERSAL OFF (EXPERT)",
		Summary: [2]string{
			"Sweep GEN:TV-B-Gone, TV-Service, or DB packs.",
			"Filter actions, smart-rank & rewind >/* trail.",
		},
		Keys: []HelpKeyHint{
			{"4 RUN", "Start / stop Universal Off sweep", true},
			{"5/Ent", "Single-fire highlighted candidate", true},
			{"6 SAVE", "Promote winning DB remote to USER", true},
			{"7 ACT", "Cycle action filter (Power/Cool/Mute)", true},
			{"8 PACK", "Cycle GEN:TVBG / DB packs / USER", true},
			{"F/X", "Step cursor up/down (rewind >/*)", false},
			{"Z/C", "Adjust probe interval (0.25s - 5.0s)", false},
			{"R", "Smart-rank pack by last RX capture", false},
			{"Esc", "Return to Home Menu", false},
		},
	}
}

func helpForAC() ScreenHelp {
	return ScreenHelp{
		ScreenTitle: "SMART AC CONTROLLER",
		Summary: [2]string{
			"Checksummed state-frame encoder for 6 brands:",
			"Gree, Midea, Daikin, LG, Samsung, Mitsubishi.",
		},
		Keys: []HelpKeyHint{
			{"Z/C", "Adjust temperature (16C - 30C)", false},
			{"F/X", "Cycle mode (COOL/HEAT/DRY/FAN/AUTO)", false},
			{"R", "Cycle AC brand preset", false},
			{"4 PWR", "Toggle AC power ON / OFF", true},
			{"5 FAN", "Cycle fan speed (AUTO/LOW/MED/HIGH)", true},
			{"6 SWG", "Toggle vane swing ON / OFF", true},
			{"7/Ent", "Encode checksum & transmit AC frame", true},
			{"8 SAVE", "Save button into AC_<Brand>.ir", true},
			{"Esc/1", "Return to Home / Remote Deck", false},
		},
	}
}

func helpForRX(learning, hasCap bool) ScreenHelp {
	k5 := HelpKeyHint{"5 CLN", "Toggle 1D K-Means / PN-CLEAN denoiser", true}
	if !learning && hasCap {
		k5 = HelpKeyHint{"5 SWP", "Sweep held frame (36/38/40/56 kHz)", true}
	}
	return ScreenHelp{
		ScreenTitle: "IR LAB (OSCILLOSCOPE)",
		Summary: [2]string{
			"Live /dev/lirc pulse scope & protocol decode.",
			"1D K-Means / PulseNet-IR clean + 32D k-NN.",
		},
		Keys: []HelpKeyHint{
			{"4 HOLD", "Toggle live RX listening / hold frame", true},
			k5,
			{"6 DIFF", "Open AC State-Frame Bit Diff inspector", true},
			{"7 SAVE", "Save capture to USER (noise guarded)", true},
			{"8 CARR", "Cycle TX carrier (36/38/40/56 kHz)", true},
			{"F/X", "Select 32D SupCon k-NN DB match", false},
			{"Enter", "Promote highlighted k-NN DB remote", false},
			{"R", "Sweep active capture across carriers", false},
			{"Esc", "Cancel sweep or return to Home Menu", false},
		},
	}
}

func helpForACDiff() ScreenHelp {
	return ScreenHelp{
		ScreenTitle: "IR LAB (AC BIT DIFF)",
		Summary: [2]string{
			"Pins baseline AC frame & XOR-diffs next RX.",
			"Shows changed subframe bytes & SUM8 checksum.",
		},
		Keys: []HelpKeyHint{
			{"4 HOLD", "Toggle live RX listening / hold", true},
			{"5 CLN", "Toggle ML denoiser (or SWEEP if held)", true},
			{"6 DIFF", "Exit AC Diff & return to Oscilloscope", true},
			{"7 SAVE", "Save current AC capture to My Remotes", true},
			{"8 CARR", "Cycle TX carrier (36/38/40/56 kHz)", true},
			{"Esc", "Close AC Bit Diff inspector", false},
		},
	}
}

func helpForMacro(running, editMode bool) ScreenHelp {
	if running {
		return ScreenHelp{
			ScreenTitle: "MACROS (RUNNING)",
			Summary: [2]string{
				"Executing multi-device IR macro sequence.",
				"Press 4 STOP, Space, or Esc to abort.",
			},
			Keys: []HelpKeyHint{
				{"4/Ent", "STOP running macro immediately", true},
				{"Spc/Esc", "Cancel macro execution immediately", false},
				{"H", "Show or hide this Help window", false},
			},
		}
	}
	if editMode {
		return ScreenHelp{
			ScreenTitle: "MACROS (EDIT STEPS)",
			Summary: [2]string{
				"Edit individual steps & inter-step delays.",
				"Changes save atomically to macros.json.",
			},
			Keys: []HelpKeyHint{
				{"F/X", "Select macro step up / down", false},
				{"Z/C", "Adjust step delay (-/+ 100 ms)", false},
				{"4/Ent", "Run current macro sequence", true},
				{"6 ADD", "Append step from active remote button", true},
				{"7/Del", "Delete highlighted step (2-step)", true},
				{"8 DONE", "Exit step edit mode (also Esc/Space)", true},
			},
		}
	}
	return ScreenHelp{
		ScreenTitle: "MACROS",
		Summary: [2]string{
			"Chain IR commands across multiple remotes.",
			"Saved in ~/.config/ir-studio/macros.json.",
		},
		Keys: []HelpKeyHint{
			{"F/X", "Select macro up / down", false},
			{"4/Ent", "Run selected macro sequence", true},
			{"5 NEW", "Create a new empty macro", true},
			{"6 ADD", "Add step from active remote button", true},
			{"7/Del", "Delete selected macro (2-step)", true},
			{"8 EDIT", "Enter step editor (adjust delays)", true},
			{"Z/C", "Adjust step delay (-/+ 100 ms)", false},
			{"Esc", "Return to Home Menu", false},
		},
	}
}

func helpForSettings(expert bool) ScreenHelp {
	if !expert {
		return ScreenHelp{
			ScreenTitle: "SETTINGS (SIMPLE)",
			Summary: [2]string{
				"Configure Expert Mode, opt-in SSH MCP Server,",
				"DB Sync, and default TX Carrier frequency.",
			},
			Keys: []HelpKeyHint{
				{"F/X", "Select setting row (4 options)", false},
				{"4/Ent", "Toggle or activate selected setting", true},
				{"Z/C", "Adjust TX Carrier (36/38/40/56 kHz)", false},
				{"5 SYNC", "Run incremental remote DB sync", true},
				{"Esc/8", "Return to Home Menu", true},
			},
		}
	}
	return ScreenHelp{
		ScreenTitle: "SETTINGS (EXPERT)",
		Summary: [2]string{
			"Full carrier, duty cycle, probe interval,",
			"DB sources & opt-in SSH MCP configuration.",
		},
		Keys: []HelpKeyHint{
			{"F/X", "Select setting row (7 options)", false},
			{"4/Ent", "Toggle setting or open DB Sources", true},
			{"Z/C", "Adjust Carrier, Duty, or Probe Interval", false},
			{"5 SYNC", "Run incremental remote DB sync", true},
			{"6 SRCS", "Open Manage DB Sources sub-view", true},
			{"7 SAVE", "Save ~/.config/ir-studio/config.json", true},
			{"Esc/8", "Return to Home Menu", true},
		},
	}
}

func helpForSources() ScreenHelp {
	return ScreenHelp{
		ScreenTitle: "MANAGE DB SOURCES",
		Summary: [2]string{
			"Manage GitHub/HTTP Flipper-IRDB sources.",
			"Toggle packs ON/OFF, sync, or delete dir.",
		},
		Keys: []HelpKeyHint{
			{"F/X", "Move cursor through DB sources", false},
			{"4/Ent", "Toggle selected source ON / OFF", true},
			{"5 SYNC", "Run incremental delta sync now", true},
			{"7/Del", "Delete local pack directory (2-step)", true},
			{"Esc/8", "Back to Settings", true},
		},
	}
}
