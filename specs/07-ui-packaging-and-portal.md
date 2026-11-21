# Epic 7: UI, Packaging & Learning Portal (`07-ui-packaging-and-portal.md`)

## Epic Overview

Epic 7 delivers the complete end-user experience for `ir-studio` across three tightly aligned surfaces:
1. **On-Device 320×170 RGB565 Framebuffer UI** (`internal/ui/`): Ergonomic 46-key TCA8418 keyboard navigation, 6 domain tabs (`RX`, `REMOTE`, `PROBE`, `SAVED`, `MACRO`, `SET`), semantic dark-mode design tokens matching `DESIGN.md`, and a 5-page built-in Help modal.
2. **Automated Visual Verification & Debian Packaging** (`cmd/shots/`, `app-builder.json`, `scripts/build_deb.sh`): Deterministic headless screenshot generation (`320×170` and `640×340` PNGs + `256×256` icon) and non-root ARM64 `.deb` APPLaunch packaging with `udev` rules for `/dev/lirc*`.
3. **Interactive Learning Portal & Docs** (`ir-studio/README.md`, `learning-portal/`): Mandatory English guides (`ir-guide`, `ir-domain`, `ir-ml`), a zero-build browser Canvas IR Waveform & 1D K-Means lab widget (`widgets/ir-lab.js`), domain glossary entries, and a practical guide to discovering and syncing `Flipper-IRDB` packs.

---

## US-07.1: 320×170 RGB565 Framebuffer UX & 46-Key TCA8418 Ergonomics

**As a** CardputerZero user operating `ir-studio` handheld on a 320×170 LCD and 46-key matrix keyboard,  
**I want** a crisp, responsive, zero-flicker dark-mode UI with consistent physical-key-aligned softkeys, one-hand navigation, multi-select, Smart Rank, and an in-app Help modal,  
**So that** I can capture, probe, organize, and replay IR remotes effortlessly in the field without memorizing obscure key combos.

### Screen Grid & Semantic Design Tokens (SSOT from `DESIGN.md`)

- **Canvas**: `320×170` RGB565 double-buffered framebuffer (`/dev/fb0`), `5×7` bitmap font (`6*scale` px char width, `9*scale` px line height).
- **Layout Regions**:
  - **Header (`y = 0..17`)**: App badge `IR` (`x = 4..44`), active Tab pill (`x = 48..104`), contextual status (`x = 108..194`, e.g. `38kHz 33%` or `AMB:OK`), ML/Filter pill (`x = 198..268`, e.g. `ML-CLEAN` / `RANK`), Hardware/SIM badge (`x = 272..316`: emerald `LIRC` or amber `SIM`).
  - **Workspace (`y = 18..142`, height `125 px`)**: `6 px` horizontal margins (`x = 6..314`, usable width `308 px`).
  - **Bottom Nav Bar (`y = 143..169`)**: 5 keycap-labeled tiles (`50×21 px`, `y = 153` center) centered directly above physical keys `4..8` at `x = [49, 105, 161, 217, 273]`.
  - **Toast Overlay (`y = 22..37`)**: Centered semantic-bordered pill auto-dismissing after `2 s`.
- **Semantic Colors**:
  - Background `#0b1117`, Surface `#121a23`, Surface Alt `#182230`, Border `#1e293b`, Foreground `#e2e8f0`, Muted `#94a3b8`.
  - Cyan `#00d9ff` (active cursor, selected tab/button, primary waveform), Emerald `#10e191` (`LIRC` ready, decoded protocol, `ENABLED`, successful TX), Amber `#f59e0b` (`SIM`, `DISABLED`, warnings, ambient IR interference), Magenta `#e879f9` (ML outputs: K-Means denoised overlay, MLP label, fingerprint match `%`, Smart Rank), Red `#ef4444` (destructive confirm prompts, `STOP`, errors).

### 6-Tab Ergonomics & Keymap (`46-Key TCA8418`)

| Global / Contextual Key | Behavior |
| :--- | :--- |
| `Tab` / `9` / `0` | Cycle active tab (`RX` → `REMOTE` → `PROBE` → `SAVED` → `MACRO` → `SET`) |
| `F` / `X` / `Z` / `C` (and Arrow keys) | Up / Down / Left / Right navigation across lists, remote button grids, pulse zoom/pan, and settings |
| `4`, `5`, `6`, `7`, `8` | Context-sensitive bottom softkeys visually aligned at `x = [49, 105, 161, 217, 273]` |
| `Enter` | Primary action (Fire selected button, Start/Stop Probe, Open remote, Run macro, Toggle setting) |
| `Space` / `1` / `D` | Multi-select `[x]` toggle in `SAVED`, `PROBE` pack view, and `SET → Sources` (also `Space` = instant `STOP` while `PROBE` is actively running) |
| `R` | Trigger **Smart Probe Ranking** in `PROBE` tab (ranks pack descending by 8D IR cosine similarity to last captured signal) |
| `H` | Open/close the **5-page built-in Help modal** (`Z`/`C` or `Left`/`Right` flips pages 1/5..5/5, `H`/`Esc` closes) |
| `Esc` / `Backspace` | Cancel active TX/RX/Probe/Macro, dismiss modal/confirm prompt, or exit app cleanly from top level |

#### Per-Tab Bottom Softkeys (`4..8`)

| Tab | `4` (`x=49`) | `5` (`x=105`) | `6` (`x=161`) | `7` (`x=217`) | `8` (`x=273`) |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **`RX`** | `LEARN` / `STOP` | `CLEAN` (ML) | `DIFF` (AC) | `SAVE` | `CARR` (36–56k) |
| **`REMOTE`** | `SEND` | `LEARN+` | `RENAME` | `DEL` | `NEXT` (File) |
| **`PROBE`** | `RUN`/`STOP` | `TRY` | `SAVE` | `ACT` (Filter) | `PACK` |
| **`SAVED`** | `OPEN` | `SEND` | `PROMO`/`PURGE` | `DEL` | `SRC` (User/DB) |
| **`MACRO`** | `RUN`/`STOP` | `NEW` | `ADD` | `DEL` | `EDIT` |
| **`SET`** | `TOGGLE` | `SYNC` | `ADD` (Src) | `DEL` (Src) | `SAVE` |

### 5-Page Built-In Help Modal (`drawHelpModal` / `helpPages`)
1. **Page 1/5 — Navigation & Global Keys**: `Tab`/`9`/`0` tabs, `F/X/Z/C` arrows, `4..8` bottom softkeys, `H` help, `Esc` cancel/exit.
2. **Page 2/5 — RX, Ambient Monitor & AC Diff**: Learning signals, ambient IR noise bar, `5 CLEAN` K-Means denoiser, `6 DIFF` AC state-frame bit diff, `8 CARR` 36–56 kHz selector.
3. **Page 3/5 — Universal PROBE & Smart Rank**: Action filters (`Power`/`Cool`/`Mute`), `4 RUN`/`STOP`, 12-slot rewind trail (`>`/`*`), `5 TRY`, `6 SAVE` full-remote promote, `R` Smart Rank.
4. **Page 4/5 — Virtual REMOTE, SAVED & MACRO**: Multi-button remote deck, `1`/`D`/`Space` `[x]` multi-select & 2-step batch delete, `PURGE` noise filter, chaining multi-device macros.
5. **Page 5/5 — DB Sync, MCP Server & Badges**: `SET` sources sync (`Flipper-IRDB`), `MCP Server` opt-in toggle (`--mcp` over SSH), header badge legend (`LIRC`, `SIM`, `ML-CLEAN`, `RANK`).

### Acceptance Criteria (Given / When / Then)

1. **Pixel-Exact Softkey Alignment**:
   - **Given** any of the 6 tabs is rendered to the `320×170` RGB565 buffer,
   - **When** the bottom nav bar is drawn,
   - **Then** all 5 tiles (`50×21 px`) MUST be centered at `x = [49, 105, 161, 217, 273]` and `y = 153`, with zero text clipping or overlap against the `y = 18..142` workspace.
2. **Non-Blocking 55 ms Render Loop**:
   - **Given** a long-running LIRC learn, Universal Probe sweep, macro execution, or HTTPS DB sync is active,
   - **When** the user presses `Esc` or `4 STOP`,
   - **Then** the UI remains responsive at `~18 FPS` (`<= 55 ms` frame budget) and delivers immediate `context.Context` cancellation to the worker goroutine via buffered channels.
3. **Multi-Select & 2-Step Batch Confirm**:
   - **Given** the user is in `SAVED` (User Library) or `SET → Sources` and marks 3 items with `Space`/`1`/`D`,
   - **When** the user presses `7 DEL`,
   - **Then** an inline confirmation prompt appears (`Delete 3 items? [Enter=YES / Esc=NO]`), deleting all 3 only on `Enter` and cancelling cleanly on `Esc`.
4. **5-Page Help Modal**:
   - **Given** the user presses `H` from any tab,
   - **When** `Z`/`C` (or `Left`/`Right`) are pressed,
   - **Then** the modal cycles through pages `1/5` to `5/5`, documents every active keybinding and badge in English, and restores the underlying tab cleanly on `H` or `Esc`.
5. **Struct & File Modularity**:
   - **Given** the `internal/ui/` package implementation,
   - **When** inspected,
   - **Then** state is decomposed into embedded/composed domain sub-structs (`RXState`, `RemoteState`, `ProbeState`, `SavedState`, `MacroState`, `SettingsState`), and UI rendering is split by tab (`app.go`, `chrome.go`, `help.go`, `view_rx.go`, `view_remote.go`, `view_probe.go`, `view_saved.go`, `view_macro.go`, `view_set.go`, `fb.go`, `input.go`), each `<= 350` lines.

### Edge Cases
- **Missing `/dev/fb0` or `/dev/input/event*`**: Support headless rendering in `cmd/shots` and unit tests via injected `Framebuffer` / `InputReader` interfaces without touching OS devices.
- **Rapid key repeat / bounce on TCA8418**: Debounce destructive `Enter` confirmations and prevent held `Space` from toggling `[x]` rapidly while stopping a running Probe (`STOP` consumes the keypress).
- **Long remote or button names**: Deterministically truncate with `..` to fit the `5×7` bitmap column width so text never bleeds across borders.

### Invariants
- **No Hardware I/O on UI Thread Invariant**: `internal/ui` MUST never invoke `/dev/lirc*` ioctls or `net/http` calls synchronously inside the render/event loop.
- **English-Only UI Invariant**: Every label, toast, badge, and Help page MUST be in English.
- **Canonical Softkey Geometry Invariant**: `navKeyCentersX = [5]int{49, 105, 161, 217, 273}` and `navKeyY = 153` are defined once in `chrome.go`.

### Verification Plan
- Headless UI state & key-dispatch unit tests in `internal/ui/app_test.go` covering tab switching, `F/X/Z/C`, `1`/`D`/`Space` multi-select, `R` Smart Rank, `H` 5-page Help navigation, and 2-step delete confirmation.
- Golden pixel-bounds assertion test verifying softkey rectangles center at `x = [49, 105, 161, 217, 273]` and no draw calls panic or clip out of `320×170`.

---

## US-07.2: Automated `cmd/shots` Screenshot Generator & `.deb` APPLaunch Packaging

**As a** maintainer publishing `ir-studio` to CardputerZero APPLaunch and the Learning Portal,  
**I want** an automated headless `cmd/shots` screenshot/icon generator and a reproducible ARM64 `.deb` build script with non-root `udev` rules,  
**So that** documentation visuals never drift from the live UI and users can install and run `ir-studio` without root privileges.

### Acceptance Criteria (Given / When / Then)

1. **Automated `cmd/shots` Generator**:
   - **Given** a developer runs `go run ./cmd/shots` inside `ir-studio/`,
   - **When** the command completes,
   - **Then** it deterministically renders (using `SIM` fixtures, zero physical hardware required):
     - Native `320×170` PNGs and `2×` nearest-neighbor `640×340` PNGs for all 6 tabs (`rx.png`, `remote.png`, `probe.png`, `saved.png`, `macro.png`, `set.png`) plus `help.png` (and `*_2x.png` variants) into `ir-studio/assets/screenshots/`.
     - A crisp `256×256` RGBA application icon (`ir-studio/assets/images/ir-studio-icon.png`).
     - Automatic sync (or explicit flag `--sync-portal`) copying the `320×170` PNGs to `learning-portal/assets/ir-studio/`.
2. **`app-builder.json` Store Manifest**:
   - **Given** `ir-studio/app-builder.json`,
   - **When** validated as JSON,
   - **Then** it defines `app_name: "IR Studio"`, `package_name: "ir-studio"`, `bin_name: "ir-studio"`, `version`, `store.icon: "assets/images/ir-studio-icon.png"`, all 6 tab + help screenshots, `categories: ["Radio & Comms", "Hardware & IoT"]`, `permissions.additional_hardware: true`, `permissions.network: true` (for opt-in HTTPS DB sync), `license: "MIT"`, and a 4-character `share_code` (e.g., `"IRST"`).
3. **Reproducible ARM64 `.deb` Build (`scripts/build_deb.sh`)**:
   - **Given** a developer runs `./scripts/build_deb.sh` on Linux/WSL,
   - **When** the script executes,
   - **Then** it runs `go test -race ./...`, cross-compiles `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w"`, stages the Debian tree, and builds `dist/ir-studio_<ver>-<rev>_arm64.deb` via `dpkg-deb --root-owner-group` containing:
     - `/usr/share/APPLaunch/bin/ir-studio` (`0755`)
     - `/usr/share/APPLaunch/share/images/ir-studio-icon.png` (`0644`)
     - `/usr/share/APPLaunch/applications/ir-studio.desktop` (`Exec=/usr/share/APPLaunch/bin/ir-studio`, `Sysplause=true`, `Terminal=false`)
     - `/etc/udev/rules.d/99-ir-studio-lirc.rules` (`SUBSYSTEM=="lirc", KERNEL=="lirc[0-9]*", MODE="0660", GROUP="lirc"`)
     - `/usr/lib/tmpfiles.d/ir-studio.conf` (ensuring non-root runtime permissions if needed without root daemon privileges)
     - `DEBIAN/control` (`Architecture: arm64`, accurate `Installed-Size`) and `DEBIAN/postinst` (reloading udev rules gracefully if `udevadm` is present and printing group reminder for `lirc`, `video`, `input` without failing in container builds).

### Edge Cases
- **Running `build_deb.sh` inside containers without `udevadm` or systemd**: `DEBIAN/postinst` MUST guard `udevadm control --reload-rules` with `if command -v udevadm >/dev/null 2>&1; then ... || true; fi` so offline/chroot installs never fail.
- **Missing icon or screenshot before packaging**: `scripts/build_deb.sh` verifies `assets/images/ir-studio-icon.png` exists (or invokes `go run ./cmd/shots` automatically if missing).
- **Non-deterministic timestamps in `cmd/shots`**: Pin a fixed synthetic clock/seed in `cmd/shots` so regenerated PNGs produce clean, minimal git diffs only when UI visuals actually change.

### Invariants
- **Exact Dimensions Invariant**: Every tab/help screenshot MUST be exactly `320×170` (native) and `640×340` (`2×` crisp nearest-neighbor, never blurry bilinear scaling); the icon MUST be `256×256`.
- **Non-Root Packaging Invariant**: Package ownership is `--root-owner-group` with `0755` binary and `0644` assets/rules; zero setuid bits (`4755` forbidden).

### Verification Plan
- Run `go run ./cmd/shots` and verify via a Go test (`cmd/shots/main_test.go`) that all 7 native `320×170` PNGs, 7 `640×340` PNGs, and the `256×256` icon decode cleanly with exact dimensions and non-blank pixel variance.
- Validate `ir-studio/app-builder.json` with `jq` / `node -e "JSON.parse(...)"`.
- Run `./scripts/build_deb.sh` and inspect the resulting archive with `dpkg-deb -c` and `dpkg-deb -I` to verify paths, permissions, udev rule, and control metadata.

---

## US-07.3: Mandatory Learning Portal Documentation, Interactive IR Lab & Glossary

**As a** CardputerZero user learning Infrared physics, protocols, and on-device ML,  
**I want** comprehensive English documentation in `ir-studio/README.md` and the zero-build `learning-portal/` — including an interactive browser IR waveform & K-Means lab, Flipper-IRDB pack guide, and glossary entries,  
**So that** I can master both the hardware app and the underlying IR signal processing concepts in one place.

### Required Documentation Deliverables

1. **App README (`ir-studio/README.md`)**:
   - Concise English overview, hardware requirements (`/dev/lirc*`, 38 kHz TX/RX, `SIM` fallback), quickstart, 6-tab control table, MCP `--mcp` SSH usage & 21-tool summary, `.deb` build steps, and relative link to `../learning-portal/`.
2. **Manifest Registration (`learning-portal/manifest.json`)**:
   - Add a dedicated `"ir-studio"` section (`"title": "IR Studio (38 kHz Infrared)"`) with three items:
     - `ir-guide` → `content/ir-studio/guide.md` ("App Guide & Controls")
     - `ir-domain` → `content/ir-studio/ir-domain.md` ("Infrared, LIRC & Protocol Theory")
     - `ir-ml` → `content/ir-studio/ir-ml.md` ("ML: Pulse Denoiser, Classifier & Fingerprint", `badge: "ML"`)
3. **Practical App Guide (`learning-portal/content/ir-studio/guide.md`)**:
   - Step-by-step walkthrough for all 6 tabs (`RX`, `REMOTE`, `PROBE`, `SAVED`, `MACRO`, `SET`) with `320×170` pixelated screenshots from `assets/ir-studio/`.
   - **Dedicated Section: "Finding Relevant Flipper-IRDB Packs"**:
     - Explains the directory structure of `Lucaslhm/Flipper-IRDB` (`ACs`, `TVs`, `Fans`, `LEDs`, `Projectors`, `Audio`, `Cameras`).
     - Shows how to add targeted subfolders (rather than syncing tens of thousands of unrelated files) via `SET → ADD` or MCP `ir_add_source` (`kind: "github_tree"`, `subpath: "ACs"`).
     - Demonstrates combining Action-Filtered `PROBE` (`Power`/`Cool`/`Off`) + Smart Probe Ranking (`R`) + 12-slot rewind (`F/X` + `5 TRY`) + one-key `6 SAVE` full-remote promotion.
   - Full MCP Server SSH setup (`SET → MCP Server: ENABLED`, `ssh <device> /usr/share/APPLaunch/bin/ir-studio --mcp`) and tool reference table.
4. **Domain Deep-Dive (`learning-portal/content/ir-studio/ir-domain.md`)**:
   - 940 nm IR physics, 36–56 kHz carrier modulation & 33%/50% duty cycle trade-offs (LED peak current vs. receiver bandpass).
   - Pulse-distance encoding (`NEC`, `NECext`, `Samsung32`, `Kaseikyo`), pulse-width encoding (`SIRC`), and bi-phase Manchester encoding (`RC5`, `RC6`).
   - Stateful HVAC/AC multi-byte frames vs. stateless TV commands, and how the AC State-Frame Diff reveals temperature/mode/checksum bytes.
   - Linux `/dev/lirc*` subsystem (`LIRC_MODE_PULSE`, ioctls) and ambient IR interference (sunlight DC saturation vs. PWM LED lamp harmonics).
5. **On-Device ML Deep-Dive (`learning-portal/content/ir-studio/ir-ml.md`)**:
   - 1D K-Means pulse denoising (`ShortUs`/`LongUs`/`SyncUs` centroids, `< 80 µs` glitch absorption preserving mark/space alternation).
   - Pure-Go MLP signal/noise classifier (`FeatureVector` → protocol family or ambient noise) and `PURGE` workflow.
   - 8D L2-normalized IR timing `Embedding`, cosine similarity matching (1-button broken-remote recovery), and Smart Probe Ranking (`R`).
   - Embeds the interactive browser lab via ```` ```interactive:ir-lab ````.
6. **Interactive Browser Lab Widget (`learning-portal/widgets/ir-lab.js` + `learning-portal/widgets.js`)**:
   - Zero-build native ES module following the `learning-portal` widget registry pattern (`widgets.js` re-exports and registers `ir-lab`).
   - Interactive HTML5 `<canvas>` IR Waveform & 1D K-Means Pulse Clustering simulator:
     - Protocol preset selector (`NEC 38 kHz`, `Samsung32`, `RC5 36 kHz`, `Daikin AC RAW`).
     - Live sliders for **Timing Jitter (`0–25%`)** and **Ambient EMI Glitches (`0–8` spikes)**.
     - Real-time side-by-side / overlaid plot of raw noisy mark/space waveform vs. K-Means denoised waveform, showing computed centroids (`Short`, `Long`, `Sync`), glitches absorbed, decoded hex frame, and 8D fingerprint similarity score.
7. **Domain Glossary (`learning-portal/content/glossary.md`)**:
   - Add concise English definitions (in alphabetical/structured order matching existing entries) for: `Ambient IR Interference`, `Bi-Phase / Manchester Encoding (RC5/RC6)`, `Carrier Frequency (36–56 kHz)`, `Duty Cycle (IR)`, `Flipper .ir Format`, `IR Fingerprint (8D Embedding)`, `IR Macro`, `LIRC (/dev/lirc*)`, `Mark / Space`, `Pulse-Distance Encoding`, `Smart Probe Ranking`, and `Stateful AC Frame`.

### Acceptance Criteria (Given / When / Then)

1. **Zero-Build Static Portal Integrity**:
   - **Given** all new Markdown pages, `manifest.json`, and `widgets/ir-lab.js` are in place,
   - **When** validated with `node --check learning-portal/app.js learning-portal/widgets.js learning-portal/widgets/ir-lab.js` and JSON parsing on `learning-portal/manifest.json`,
   - **Then** all checks exit `0` with zero syntax errors, zero external npm/bundler dependencies, and every `file` path in `manifest.json` resolves to an existing `.md` file.
2. **Interactive Lab Widget Registration**:
   - **Given** a user opens `#ir-ml` in the Learning Portal,
   - **When** the Markdown renderer encounters ```` ```interactive:ir-lab ````,
   - **Then** `widgets/ir-lab.js` mounts cleanly, renders the interactive waveform canvas using the portal's dark-mode semantic tokens, and updates centroids/decoded output live as sliders or presets change.
3. **Screenshot & Asset Parity**:
   - **Given** `learning-portal/content/ir-studio/guide.md` embeds `assets/ir-studio/*.png`,
   - **When** rendered in a browser,
   - **Then** every referenced PNG exists in `learning-portal/assets/ir-studio/` and displays with crisp `image-rendering: pixelated` framing.
4. **Glossary Completeness**:
   - **Given** any technical term introduced across Epics 1–7,
   - **When** searched in the Learning Portal search bar or `content/glossary.md`,
   - **Then** a clear, accurate English definition is present.

### Edge Cases
- **Mobile viewport (`< 768 px`)**: The `ir-lab` canvas and control sliders MUST scale responsively (`width: 100%`, DPR-aware canvas backing store) without horizontal overflow.
- **High-DPI displays**: `ir-lab.js` multiplies canvas dimensions by `window.devicePixelRatio` while keeping CSS layout responsive so waveform edges remain razor-sharp.
- **Zero-glitch / max-jitter extremes**: The K-Means clustering step in `ir-lab.js` MUST guard against empty clusters or `NaN` centroids when jitter is `0%` or `25%`.

### Invariants
- **Zero-Build GitHub Pages Invariant**: `learning-portal/` MUST remain pure HTML5 + CSS + native ES Modules + Markdown deployable directly via `.github/workflows/pages.yml` with no build step.
- **English-Only Documentation Invariant**: Every heading, paragraph, widget label, tooltip, and glossary entry MUST be written in English.
- **Anti-God-File Widget Invariant**: `learning-portal/widgets/ir-lab.js` MUST be an isolated ES module (`<= 350` lines) imported by the `learning-portal/widgets.js` barrel.

### Verification Plan
- Run `node --check learning-portal/app.js learning-portal/widgets.js learning-portal/widgets/ir-lab.js`.
- Run a JSON & link verification check confirming every `manifest.json` entry and every `assets/ir-studio/*.png` image link in `guide.md` exists on disk.
- Verify `ir-studio/README.md` links cleanly to `../learning-portal/` and lists all 21 MCP tools matching `internal/mcpserver/schema.go`.
