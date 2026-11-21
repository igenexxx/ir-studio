# IR Studio

Native 38 kHz Infrared transceiver studio, multi-protocol encoder/decoder, Flipper Zero `.ir` library manager, Universal Power-Off `PROBE`, multi-device `MACRO` runner, and on-device pure-Go IR ML engine for the **M5Stack CardputerZero** (`/dev/lirc*`, `320×170` RGB565 `/dev/fb0`, 46-key TCA8418 `evdev` keyboard).

Built in pure Go (`CGO_ENABLED=0`, zero external runtime dependencies, zero `lircd`/`ir-ctl` subprocesses) with automatic **`SIM`** fallback when no `/dev/lirc*` hardware is attached.

---

## Features

- **Live IR Capture, Oscilloscope & ML Denoiser (`RX`):** Streams `/dev/lirc*` `LIRC_MODE_MODE2` pulses off the UI thread, decodes `NEC`, `NECext`, `Samsung32`, `RC5`, `RC6`, `SIRC`, and `Kaseikyo`, monitors live **Ambient IR Interference** (`CLEAN` / `MODERATE` / `SATURATED`), rescues jittery captures with the **1D K-Means IR Pulse Denoiser (`ML-CLEAN`)**, and matches single buttons against `Flipper-IRDB` via **8D IR Fingerprint k-NN**.
- **Virtual Remote Deck (`REMOTE`):** 2-column scrollable button pad for any `.ir` file with hold-to-repeat transmission, in-place `LEARN+`, `RENAME`, and `DEL` authoring for `USER` remotes, and one-key `PROMOTE` from read-only database packs.
- **Universal Power-Off & Brand Sweep (`PROBE`):** Steps through read-only external `.ir` packs (`~/ir-studio/databases/<pack>/`) with Action Filters (`Power`, `Cool`, `Mute`), **Smart Probe Ranking (`R`)**, instant `STOP` (`4` / `Space`), 12-slot `>` / `*` rewind markers, single-shot `5 TRY`, and one-key `6 SAVE` (`PromoteToUser`) of the full winning remote.
- **Dual-Scope `.ir` Library (`SAVED`):** Strictly isolates your read-write **User Library** (`~/ir-studio/remotes/`) from read-only **Database Packs** (`~/ir-studio/databases/<pack>/`). Supports `[x]` multi-select (`Space`/`1`/`D`), 2-step batch `DEL`, and ML-powered **`PURGE`** for noise-only captures.
- **Multi-Device IR Macros (`MACRO`):** Chains commands across multiple User Library remotes with configurable `50–10000 ms` delays (`~/.config/ir-studio/macros.json`, `0600`) and instant mid-run `context.Context` cancellation.
- **Hardware, DB Sync & Opt-In MCP (`SET`):** Configure `/dev/lirc*` device, TX carrier (`36 / 38 / 40 / 56 kHz`) and duty cycle (`33% / 50%`), manage and incrementally sync HTTPS `Flipper-IRDB` sources (`ETag`/`SHA` delta sync), and toggle the opt-in **MCP Server**.

---

## 6-Tab Controls (46-Key TCA8418)

### Global Keys

| Key | Action |
| :--- | :--- |
| **`Tab`** / **`9`** / **`0`** | Cycle active tab (`RX` → `REMOTE` → `PROBE` → `SAVED` → `MACRO` → `SET`) |
| **`F` / `X` / `Z` / `C`** (or Arrows) | Up / Down / Left / Right navigation across lists, grids, waveform pan, and settings |
| **`4` `5` `6` `7` `8`** | Context-sensitive bottom softkeys aligned at `x = [49, 105, 161, 217, 273]` |
| **`Enter`** | Primary action (Send button, Start/Stop Probe, Open remote, Run macro, Toggle setting) |
| **`Space` / `1` / `D`** | Toggle `[x]` multi-select (`SAVED`, `PROBE`, `SET → Sources`); `Space` also stops active `PROBE` |
| **`R`** | Trigger **Smart Probe Ranking** in `PROBE` tab (sort pack by 8D IR cosine similarity) |
| **`H`** | Toggle 5-page built-in Help modal (`Z`/`C` flips pages `1/5..5/5`) |
| **`Esc`** / **`Backspace`** | Cancel active TX/RX/Probe/Macro, close modal, or exit cleanly (restores 38 kHz / 33% defaults) |

### Per-Tab Bottom Softkeys (`4` – `8`)

| Tab | `4` | `5` | `6` | `7` | `8` |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **`RX`** | `LEARN` / `STOP` | `CLEAN` (ML) | `DIFF` (AC) | `SAVE` | `CARR` (36–56k) |
| **`REMOTE`** | `SEND` | `LEARN+` | `RENAME` | `DEL` | `NEXT` / `PROMO` |
| **`PROBE`** | `RUN` / `STOP` | `TRY` | `SAVE` | `ACT` (Filter) | `PACK` |
| **`SAVED`** | `OPEN` | `SEND` | `PROMO` / `PURGE` | `DEL` | `SRC` (User/DB) |
| **`MACRO`** | `RUN` / `STOP` | `NEW` | `ADD` | `DEL` | `EDIT` |
| **`SET`** | `TOGGLE` | `SYNC` | `ADD` (Src) | `DEL` (Src) | `SAVE` |

---

## CLI Flags

```bash
# Launch interactive 320x170 UI in offline simulator mode (no /dev/lirc* required)
ir-studio --sim

# Headless hardware diagnostic: discover /dev/lirc*, print features & ambient IR status, exit
ir-studio --probe

# Run incremental ETag/SHA delta sync for all enabled Flipper-IRDB sources and exit
ir-studio --sync

# Run pure-Go JSON-RPC 2.0 stdio MCP server (requires mcp_enabled: true in Settings)
ir-studio --mcp
```

---

## Built-In MCP Server (SSH Opt-In)

The pure-Go stdio **MCP Server** is **disabled by default** (`~/.config/ir-studio/config.json` → `"mcp_enabled": false`). Enable **MCP Server** inside the **`SET`** tab, then connect an AI agent directly over SSH:

```bash
ssh <cardputer-host> /usr/share/APPLaunch/bin/ir-studio --mcp
```

### Exposed MCP Tools (21 Tools)

| Group | Tool Names | Safety Gates |
| :--- | :--- | :--- |
| **Status & RX** | `ir_get_status`, `ir_learn_signal` | Serialized via `lircMu`; `context.Context` timeout (`500–15000 ms`) |
| **TX & Macros** | `ir_transmit_button`, `ir_run_macro`, `ir_list_macros` | TX tools require **`confirm_ir: true`**; repeat cap `<= 5`; macro cap `<= 30 s` |
| **On-Device ML** | `ir_analyze_pulses`, `ir_classify_signal`, `ir_match_fingerprint`, `ir_rank_probe_pack`, `ir_purge_noise` | `ir_purge_noise` defaults to **`dry_run: true`**; deletion requires `confirm: true` (User Library only) |
| **Library & Packs** | `ir_list_remotes`, `ir_read_remote`, `ir_save_remote`, `ir_delete_remote`, `ir_list_databases`, `ir_promote_remote` | Path-sandboxed (`1 MiB` cap); `ir_delete_remote` requires **`confirm: true`** (User Library only) |
| **DB Sources** | `ir_list_sources`, `ir_add_source`, `ir_remove_source`, `ir_sync_sources`, `ir_delete_pack_files` | HTTPS-only URLs; `ir_remove_source` & `ir_delete_pack_files` require **`confirm: true`** |

---

## Build, Test & Packaging

```bash
# Run full unit, fuzz-seed, and race-detector test suite
go test -race -cover ./...

# Regenerate 320x170 & 640x340 UI screenshots + 256x256 icon
go run ./cmd/shots

# Cross-compile ARM64 binary (CGO_ENABLED=0) and build APPLaunch .deb package in dist/
./scripts/build_deb.sh
```

Deploy to CardputerZero (non-root execution via Linux `lirc`, `video`, and `input` groups):

```bash
scp dist/ir-studio_*_arm64.deb cardputer@<CARDPUTER_IP>:~
ssh cardputer@<CARDPUTER_IP> "sudo dpkg -i ~/ir-studio_*_arm64.deb"
```

---

## Documentation & Interactive Learning Portal

Full step-by-step guides, 38 kHz Infrared & LIRC theory, on-device ML architecture, Flipper-IRDB pack workflows, and an interactive **IR Protocol & 1D K-Means Waveform Lab** live in the shared Learning Portal:

- [Learning Portal (Local Relative Link)](../learning-portal/index.html)
- [IR Studio App Guide & Controls](../learning-portal/content/ir-studio/guide.md)
- [Infrared, LIRC & Protocol Theory](../learning-portal/content/ir-studio/ir-domain.md)
- [On-Device IR ML: Denoiser, Classifier & Fingerprint](../learning-portal/content/ir-studio/ir-ml.md)
- [Domain Glossary](../learning-portal/content/glossary.md)
