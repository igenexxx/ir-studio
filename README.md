# IR Studio

Native 38 kHz Infrared transceiver studio, multi-protocol encoder/decoder, Flipper Zero `.ir` library manager, Universal Power-Off `PROBE`, multi-device `MACRO` runner, and on-device pure-Go IR ML engine for the **M5Stack CardputerZero** (`/dev/lirc*`, `320×170` RGB565 `/dev/fb0`, 46-key TCA8418 `evdev` keyboard).

Built in pure Go (`CGO_ENABLED=0`, zero external runtime dependencies, zero `lircd`/`ir-ctl` subprocesses) with automatic **`SIM`** fallback when no `/dev/lirc*` hardware is attached.

> [!CAUTION]
> **Legal & Responsible Use Disclaimer:** This software is provided strictly for **educational, research, diagnostics, and authorized control** of infrared appliances and hardware that you own or have explicit permission to operate. Unauthorized disruption, tampering, or interference with third-party equipment, public displays, or commercial/industrial systems is strictly prohibited and may be punishable by law. Provided **"AS IS"** without warranty of any kind; use entirely at your own risk.

---

## Features

- **Live IR Capture, Oscilloscope, Carrier Measurement/Sweeper & ML Denoiser (`RX`):** Streams `/dev/lirc*` `LIRC_MODE_MODE2` pulses off the UI thread, enables hardware RX carrier frequency measurement (`LIRC_SET_MEASURE_CARRIER_MODE` / `LIRC_MODE2_FREQUENCY`) when supported, decodes `NEC`, `NECext`, `Samsung32`, `RC5`, `RC6`, `SIRC`, and `Kaseikyo`, monitors live **Ambient IR Interference** (`CLEAN` / `MODERATE` / `SATURATED`), rescues jittery captures with the **1D K-Means IR Pulse Denoiser (`ML-CLEAN`)**, sweeps held captures across `36 / 38 / 40 / 56 kHz` (`5 SWEEP` / `R`), and matches single buttons against `Flipper-IRDB` via **8D IR Fingerprint k-NN**.
- **Virtual Remote Deck & Smart AC Controller (`REMOTE` + `AC`):** 2-column scrollable button pad for any `.ir` file with hold-to-repeat transmission, one-tap **Carrier Frequency Sweeper (`5 SWEEP`)**, lossless **Pronto Hex (`0000 006D ...`)** import/export (`ParseProntoHex` / `FormatProntoHex`), in-place `LEARN+` (`6`), `RENAME` (`R`), and `DEL` (`7`) authoring for `USER` remotes, one-key `PROMO` from read-only database packs, plus an interactive **Smart AC State-Frame Controller (`1` or `8 AC`)** with checksummed parametric encoders for `Gree`, `Midea`, `Daikin`, `LG_AC`, `Samsung_AC`, and `Mitsubishi`.
- **Universal Power-Off, Built-in `GEN:TV-B-Gone` & `GEN:TV-Service` Packs (`PROBE`):** Ships with two compiled-in read-only virtual packs ready immediately before `--sync`: **`GEN:TV-B-Gone`** (top-100 offline universal `Power` codes across TVs, Projectors, and Soundbars) and **`GEN:TV-Service`** (discrete `Power_ON`/`Power_OFF`/`HDMI_1..4` and vendor service commands such as LG `IN_START`/`EZ_ADJUST`, Samsung service codes, Sony/Panasonic/Philips discrete remotes), plus synced read-only `.ir` packs (`~/ir-studio/databases/<pack>/`), Action Filters (`Power`, `Cool`, `Mute`), **Smart Probe Ranking (`R`)**, instant `STOP` (`4` / `Space`), 12-slot `>` / `*` rewind markers, single-shot `5 TRY`, and one-key `6 SAVE` (`PromoteToUser`).
- **38 kHz IR Optical Data Modem & Optical Tripwire (`internal/irmodem`, `internal/lirc`):** Pure-Go framed optical modem (`<= 64 B` payload + CRC-8-CCITT, tolerating `±120 µs` jitter) and bounded 38 kHz reflective/beam-break **Optical Tripwire** probe synchronized via `TransceiverGate` with `SIM` fallback.
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
| **`RX`** | `HOLD` / `LEARN` | `CLEAN` / `SWEEP` | `DIFF` (AC) | `SAVE` | `CARR` (36–56k) |
| **`REMOTE`** | `SEND` | `SWEEP` (36–56k) | `LEARN+` | `DEL` | `AC` / `PROMO` |
| **`REMOTE → AC`** | `PWR` | `FAN` | `SWING` | `SEND` | `SAVE` (`AC_*.ir`) |
| **`PROBE`** | `RUN` / `STOP` | `TRY` | `SAVE` | `ACT` (Filter) | `PACK` (`GEN:TVBG`) |
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

### Exposed MCP Tools (28 Tools)

| Group | Tool Names | Safety Gates |
| :--- | :--- | :--- |
| **Status & RX** | `ir_get_status`, `ir_learn_signal` | Serialized via `lircMu`; `context.Context` timeout (`500–15000 ms`); hardware `LIRC_MODE2_FREQUENCY` reporting when supported |
| **TX, AC, Sweep & Macros** | `ir_transmit_button`, `ir_send_ac_state`, `ir_sweep_carrier`, `ir_run_macro`, `ir_list_macros` | TX/AC/Sweep tools require **`confirm_ir: true`**; repeat cap `<= 5`; macro cap `<= 30 s`; guaranteed `38 kHz / 33%` carrier restore |
| **Pronto Hex, Modem & Tripwire** | `ir_import_pronto`, `ir_export_pronto`, `ir_modem_send`, `ir_modem_recv`, `ir_tripwire_probe` | `ir_modem_send` & `ir_tripwire_probe` require **`confirm_ir: true`**; `ir_import_pronto` `save_as` path-sandboxed; modem payload `<= 64 B` + CRC-8 |
| **On-Device ML** | `ir_analyze_pulses`, `ir_classify_signal`, `ir_match_fingerprint`, `ir_rank_probe_pack`, `ir_purge_noise` | `ir_purge_noise` defaults to **`dry_run: true`**; deletion requires `confirm: true` (User Library only) |
| **Library & Packs** | `ir_list_remotes`, `ir_read_remote`, `ir_save_remote`, `ir_delete_remote`, `ir_list_databases`, `ir_promote_remote` | Path-sandboxed (`1 MiB` cap); includes compiled-in read-only `GEN:TV-B-Gone` & `GEN:TV-Service` (`virtual: true`); `ir_delete_remote` requires **`confirm: true`** |
| **DB Sources** | `ir_list_sources`, `ir_add_source`, `ir_remove_source`, `ir_sync_sources`, `ir_delete_pack_files` | HTTPS-only URLs; `ir_remove_source` & `ir_delete_pack_files` require **`confirm: true`** (virtual packs rejected) |

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
