# Epic 5: IR-Exclusive Killer Features

## Epic Overview

Infrared has unique physical and protocol characteristics that do not exist in Sub-GHz RF:
- Every appliance is controlled by a **multi-button remote deck** (a single Flipper `.ir` file holds `Power`, `Vol+/-`, `Ch+/-`, `Mute`, `Temp+/-`, `Mode`, `Fan`, etc.).
- Air conditioners transmit **stateful full-frame HVAC packets** (`64–200+` bits) encoding temperature, mode, fan speed, swing, timer, and checksum in every single keypress.
- Home-theater and room setups require **multi-device IR macros** chaining commands across different remotes with precise inter-command delays.
- IR receivers (`/dev/lirc*`) are directly affected by **ambient optical interference** (sunlight, dimmable PWM LED lamps, fluorescent ballasts) and require matching **TX carrier frequencies (`36–56 kHz`)** and **duty cycles (`33%` / `50%`)**.

Epic 5 specifies the four IR-exclusive capabilities that make `ir-studio` a complete, standalone Infrared studio on the CardputerZero (`320×170` RGB565 LCD, 46-key TCA8418 keyboard).

---

## US-05.1: Virtual Remote Deck (`REMOTE` Tab)

**As a** CardputerZero user who has learned a remote or promoted one from `Flipper-IRDB`,  
**I want** an interactive **Virtual Remote Deck** (`REMOTE` tab) that displays all buttons of a `.ir` file with direct firing and in-place Learn/Append/Rename/Delete authoring,  
**So that** I can use CardputerZero as a real daily remote control and maintain custom multi-button `.ir` remotes directly on the device.

### Domain Contract (`internal/ui/view_remote.go`, `internal/library`)

- **Canonical Button Ordering & Icons**:
  - Recognizes and visually highlights standard control groups when present: `Power` / `Power_on` / `Power_off`, `Mute`, `Vol_up` / `Vol+`, `Vol_dn` / `Vol-`, `Ch_next` / `Ch+`, `Ch_prev` / `Ch-`, `Temp_up` / `Temp+`, `Temp_dn` / `Temp-`, `Mode`, `Fan`, followed by all remaining custom buttons in file order.
- **320×170 Viewport Layout (`y = 18..142`)**:
  - Top bar (`y = 19..31`): Active remote filename, button count (`14 btns`), and read-only badge (`[DB:RO]` Amber) vs editable User Library badge (`[USER]` Emerald).
  - Scrollable 2-column button grid (`y = 33..126`, 4 visible rows × 2 columns = 8 visible tiles per page) navigated with `F/X/Z/C` (Up/Down/Left/Right).
  - Focused button metadata bar (`y = 128..140`): Protocol (`NEC 04:08` or `RAW 38kHz 142p`) + ML class badge.
  - Bottom softkeys (`4..8`): `4 LEARN`, `5 SEND`, `6 RENAME`, `7 DEL`, `8 DIFF/PROMOTE`.
- **In-Place Authoring (User Library Only)**:
  - **Learn / Append (`4 LEARN`)**: Captures a new IR button from `/dev/lirc*` (passing through `irml` denoiser & Smart Save Guard), prompts for a button name (pre-filled with next suggested slot like `Power`, `Vol_up`, or `Btn_N`), and appends it to the active `.ir` file.
  - **Rename (`6 RENAME`)**: Opens the lightweight on-screen keyboard/text input bar using CardputerZero's physical 46-key keyboard to rename the focused button.
  - **Delete (`7 DEL`)**: 2-step confirmation to remove the focused button from the `.ir` file (cannot delete the last remaining button without confirming deletion of the empty remote or leaving 0 buttons—explicitly warns if last button).

### Acceptance Criteria (Given / When / Then)

1. **Direct Button Firing & Hold-to-Repeat**:
   - **Given** any `.ir` remote (from User Library or External DB) is open in the `REMOTE` tab and a button (e.g. `Power` or `Vol_up`) is focused,
   - **When** the user presses `5 (SEND)` or `Enter`,
   - **Then** `TXWorker` asynchronously transmits that button's exact carrier, duty cycle, and pulse sequence without blocking the 55 ms UI loop, flashes the focused tile border in Cyan/Emerald during TX, and updates the status indicator.
   - **And** if the user holds `5`/`Enter` on a repeatable protocol (`NEC`, `NECext`, `Samsung32`, `SIRC`), `TXWorker` emits compliant repeat frames until release.
2. **Learn & Append Button into User Remote**:
   - **Given** a User Library remote (`~/ir-studio/remotes/living_room_tv.ir`) is open in `REMOTE`,
   - **When** the user presses `4 (LEARN)`, aims a physical remote at CardputerZero, presses a button, and confirms the button name (e.g. `HDMI_1`),
   - **Then** the new button is appended to `living_room_tv.ir` via atomic temp-file + `fsync` + `rename`, immediately appears in the deck grid, and is ready to transmit.
3. **Rename & Delete Button**:
   - **Given** a User Library remote is open and `Btn_3` is focused,
   - **When** the user presses `6 (RENAME)`, types `Mute`, and presses `Enter`,
   - **Then** `Btn_3` is renamed to `Mute` in memory and persisted atomically to disk.
   - **When** the user presses `7 (DEL)` twice (2-step confirm),
   - **Then** the focused button is removed from the `.ir` file and cursor clamps safely to `min(cursor, len(buttons)-1)`.
4. **Read-Only External DB Protection**:
   - **Given** an External Database remote (`~/ir-studio/databases/TVs/LG_AKB7509.ir`) is opened in `REMOTE`,
   - **When** the user presses `4 (LEARN)`, `6 (RENAME)`, or `7 (DEL)`,
   - **Then** mutation is refused with an Amber toast (`"Read-only DB remote — press 8 PROMOTE to edit copy"`), and `8 (PROMOTE)` copies the entire `.ir` file into `~/ir-studio/remotes/` and switches the deck to the editable copy.

### Edge Cases

- **Duplicate button names**: Renaming or appending a button with a name that already exists in the same `.ir` file either prompts to overwrite or auto-suffixes (`Power_2`), never corrupting the file.
- **Invalid button name characters**: Empty names, newlines, `#`, or leading/trailing whitespace are sanitized/rejected before writing Flipper `.ir` format.
- **Large remotes (100+ buttons)**: Virtualized 2-column viewport renders only the visible 8 tiles (`O(1)` draw cost per frame) with a crisp scrollbar indicator.
- **Concurrent TX cancellation**: Pressing another button while a long AC burst is transmitting cancels the in-flight TX cleanly via `context.Context` and starts the newly requested button.

### Invariants

1. **Atomic Persistence Invariant**: Every Learn/Append/Rename/Delete write to `~/ir-studio/remotes/*.ir` MUST use temp-file + `fsync` + atomic `os.Rename` so power loss never leaves a truncated `.ir` file.
2. **External-DB Read-Only Invariant**: `REMOTE` authoring actions MUST NEVER write to `~/ir-studio/databases/`.
3. **UI Non-Blocking Invariant**: Button TX and Learn RX MUST run on background goroutines; the UI loop MUST remain responsive (`<= 55 ms` frame time).

### Verification Plan

- **Unit tests (`internal/library/remote_edit_test.go`, `internal/ui/view_remote_test.go`)**:
  - Test Append, Rename (including duplicate/invalid name rejection), Delete (including last-button guard), and atomic file round-trip parsing.
  - Test read-only DB rejection and `PromoteRemoteToUser` transition.
  - Test 2-column grid navigation (`F/X/Z/C`), scroll clamping on 1-button and 120-button remotes, and concurrent TX cancellation under `-race`.

---

## US-05.2: AC State-Frame Inspector

**As a** hardware hacker, HVAC technician, or learner reverse-engineering air conditioner remotes,  
**I want** a specialized **AC State-Frame Inspector** that splits multi-subframe HVAC bursts and diffs two captured AC states bit-by-bit (e.g. `22C Cool` vs `24C Cool`),  
**So that** I can immediately see carrier frequency, subframe structure, and which exact bytes/bits encode temperature, mode, fan speed, and checksum.

### Domain Contract (`internal/irml/ac_inspector.go`, `internal/ui/view_ac_diff.go`)

- **Domain Types (`internal/irml/ac_inspector.go`)**:
  - `ACSubFrame`: `Index int`, `HeaderMarkUs int32`, `HeaderSpaceUs int32`, `TrailingGapUs int32`, `BitCount int`, `Bytes []byte` (LSB-first decoded bit payload), `PulseCount int`.
  - `ACFrameReport`: `CarrierHz uint32`, `TotalPulses int`, `TotalBits int`, `ShortUs float64`, `LongUs float64`, `SubFrames []ACSubFrame`, `ChecksumHint string` (e.g. `"Last byte = sum(bytes[0..N-2]) & 0xFF"` when verified!).
  - `ByteDiff`: `SubFrameIdx int`, `ByteIdx int`, `ValA byte`, `ValB byte`, `XORMask byte` (`ValA ^ ValB`, highlighting exact changed bits).
  - `ACDiffReport`: `FrameA ACFrameReport`, `FrameB ACFrameReport`, `ChangedBytes []ByteDiff`, `TotalChangedBits int`, `LengthMismatch bool`.
- **Sub-Frame Splitting Rule**:
  - Splits a denoised/quantized RAW pulse train whenever a negative space gap exceeds `DefaultACSubframeGapUs = 8000 µs` (e.g., Daikin's 3-subframe 25–35 ms gaps, Gree's 20 ms gap, Fujitsu/Mitsubishi repeat subframes).
- **Bit Extraction Rule**:
  - Uses 1D K-Means centroids (`ShortUs`, `LongUs`) from `US-04.1` to decode pulse-distance symbols (`short space = 0`, `long space = 1`) into LSB-first bytes per subframe.

### Acceptance Criteria (Given / When / Then)

1. **Automatic AC Frame Analysis & Sub-Frame Splitting**:
   - **Given** a captured or saved `AC State-Frame` (`>= 64` bits or classified as `ClassACStateFrame`),
   - **When** the user opens the **AC State-Frame Inspector** (from `RX` or `REMOTE` via `8 INSPECT`),
   - **Then** the inspector displays:
     - Carrier frequency (`36–56 kHz`), total pulse count, total bit count, and `Short/Long` timings.
     - Detected sub-frames (e.g. `SF1: 64b (8B) | SF2: 64b (8B) | SF3: 152b (19B)`) with inter-subframe gap durations.
     - Hex dump of each subframe's decoded bytes and an automatic checksum check badge (`CHK:SUM8 OK` in Emerald if `sum(bytes[0..n-2]) & 0xFF == bytes[n-1]`, or `CHK:NIBBLE` / `CHK:UNKNOWN` in Muted).
2. **Bit-by-Bit State Diff (`22C Cool` vs `24C Cool`)**:
   - **Given** the user captures or selects State A (`22C Cool`) and pins it as Baseline (`A`), then captures or selects State B (`24C Cool`),
   - **When** `DiffACFrames(frameA, frameB)` runs,
   - **Then** the inspector highlights only the changed byte positions in Amber/Magenta (e.g. `SF3[06]: 2C -> 30 (XOR 0x1C, bits 2,3,4)` and `SF3[18]: A4 -> A8 (checksum)`), showing both hex and 8-bit binary (`00101100` vs `00110000`) with changed bits underscored/colored.
3. **Graceful Handling of Non-Standard / Length-Mismatched Frames**:
   - **Given** State A has 144 bits and State B has 152 bits (e.g. toggling Timer On adds an extra byte),
   - **When** `DiffACFrames` executes,
   - **Then** `LengthMismatch` is set to `true`, common bytes up to `min(lenA, lenB)` are diffed accurately, and trailing extra bytes are explicitly flagged as `+ADDED` without panicking.

### Edge Cases

- **Non-byte-aligned bit lengths** (e.g., Gree 67-bit frames: 35 bits + 32 bits): Remainder bits (`bitCount % 8 != 0`) are packed cleanly into the final partial byte with the exact `BitCount` displayed (`35b`).
- **Identical captures (`22C Cool` vs `22C Cool`)**: `TotalChangedBits == 0`, UI displays `"Frames identical (0 bits changed)"` in Emerald.
- **Unstructured / noisy RAW capture opened in Inspector**: Displays `"Cannot decode bit symbols — high jitter"` in Amber while still showing raw pulse count and carrier frequency.

### Invariants

1. **Pure Analysis Invariant**: `InspectACFrame` and `DiffACFrames` MUST be pure, deterministic functions in `internal/irml` with zero hardware or UI dependencies.
2. **Checksum Verification Non-Mutation Invariant**: Checksum detection MUST only inspect decoded bytes and NEVER alter the stored RAW pulse data.
3. **Bounds Safety Invariant**: Byte/bit indexing during diffing MUST NEVER panic on mismatched subframe counts or uneven byte lengths.

### Verification Plan

- **Table-driven unit tests (`internal/irml/ac_inspector_test.go`)**:
  - Realistic synthetic fixtures for Daikin (3 subframes, SUM8 checksum), Gree (2 subframes, 67 bits), and Mitsubishi (144 bits).
  - Diff tests: `22C Cool` vs `24C Cool` verifying exact temperature byte + checksum byte XOR masks; identical frames (`0` diffs); length-mismatched subframes; noisy/empty inputs.
- **Fuzz test (`FuzzInspectACFrame`)**: Random `[]int32` pulse trains confirming zero panics and bounded output.

---

## US-05.3: Multi-Device IR Macros / Chains (`MACRO` Tab)

**As a** user who regularly triggers multi-appliance routines (e.g., "Movie Night": turn on TV, wait 800 ms, turn on Soundbar, wait 500 ms, switch TV to `HDMI_2`),  
**I want** to create, manage, and execute **Multi-Device IR Macros** in the `MACRO` tab persisted to `~/.config/ir-studio/macros.json`,  
**So that** a single keypress or MCP tool call fires an entire cross-remote sequence with configurable delays and instant mid-run cancellation.

### Domain Contract (`internal/config/macros.go`, `internal/ui/view_macro.go`)

- **Schema (`~/.config/ir-studio/macros.json`, file mode `0600`, atomic write)**:
  ```json
  {
    "version": 1,
    "macros": [
      {
        "id": "movie_night",
        "name": "Movie Night",
        "steps": [
          { "remote": "TV_LG.ir", "button": "Power", "delay_after_ms": 800 },
          { "remote": "Soundbar_Sonos.ir", "button": "Power", "delay_after_ms": 500 },
          { "remote": "TV_LG.ir", "button": "HDMI_2", "delay_after_ms": 0 }
        ]
      }
    ]
  }
  ```
- **Constants (SSOT)**:
  - `MinStepDelayMs = 50`, `MaxStepDelayMs = 10000`, `DefaultStepDelayMs = 500`
  - `MaxStepsPerMacro = 32`, `MaxMacros = 64`
- **Resolution Rule**:
  - Each step's `remote` resolves strictly inside `~/ir-studio/remotes/<remote>` (path-sandboxed: rejects `..`, `/`, `\`).
- **MCP Integration**:
  - `ir_list_macros` returns all configured macros and step validity status.
  - `ir_run_macro` (requires `confirm_ir: true`) executes a named macro sequentially through the mutex-protected `TXWorker` and returns per-step execution results.

### Acceptance Criteria (Given / When / Then)

1. **Macro Execution & Live Progress**:
   - **Given** a macro `"Movie Night"` with 3 steps (`TV_LG.ir:Power -> 800ms -> Soundbar_Sonos.ir:Power -> 500ms -> TV_LG.ir:HDMI_2`),
   - **When** the user selects `"Movie Night"` in the `MACRO` tab and presses `4 (RUN)` / `Enter`,
   - **Then** `TXWorker` executes each step in order, honoring `delay_after_ms` between steps, while the `320×170` LCD displays a live progress bar and current step status (`"Running 2/3: Soundbar_Sonos Power (wait 500ms)"`), finishing with an Emerald toast `"Macro complete (3/3)"`.
2. **Instant Mid-Run Cancellation**:
   - **Given** a macro is actively running (either transmitting a pulse burst or waiting in a `delay_after_ms` timer),
   - **When** the user presses `4 (STOP)`, `Space`, or `Esc`,
   - **Then** the macro's `context.Context` cancels immediately (`< 50 ms`), any in-flight LIRC TX aborts safely, remaining steps are skipped, and an Amber toast displays `"Macro cancelled at step 2/3"`.
3. **On-Device Macro Authoring (Create / Edit / Delete)**:
   - **Given** the user is in the `MACRO` tab,
   - **When** the user creates a new macro (`5 NEW`), adds steps by picking from User Library remotes & buttons (`6 EDIT` → Add/Reorder/Remove step, adjust `delay_after_ms` with `Z/C` in `100 ms` increments), or deletes a macro (`7 DEL` 2-step confirm),
   - **Then** changes persist atomically to `~/.config/ir-studio/macros.json` (`0600`).
4. **Missing Remote or Button Validation**:
   - **Given** a macro step references a remote file or button that was later deleted or renamed,
   - **When** the user opens `MACRO` or attempts to run the macro,
   - **Then** the broken step is flagged in Red (`[MISSING]`), and running the macro aborts cleanly before transmitting with a clear error (`"Step 2 missing: Soundbar_Sonos.ir / Power"`).

### Edge Cases

- **Empty macro (0 steps)**: Running is refused with `"Macro has no steps"`.
- **Corrupt or missing `macros.json`**: Missing file initializes an empty macro list cleanly; corrupt JSON returns a descriptive error without overwriting the user's file.
- **Delay clamping**: Values outside `[0, 10000] ms` (except `0` on the final step) are rejected on validation/load.
- **Path traversal in `macros.json`**: A hand-edited step with `"remote": "../../etc/passwd"` is rejected by the path-sandbox validator before any file I/O.

### Invariants

1. **Path-Sandbox Invariant**: Macro step `remote` references MUST be bare filenames inside `~/ir-studio/remotes/` (no `/`, `\`, or `..`).
2. **Cancellable Delay Invariant**: Inter-step delays MUST use `select { case <-ctx.Done(): ... case <-timer.C: ... }` — NEVER `time.Sleep`, ensuring immediate cancellation.
3. **Hardware Serialization Invariant**: A running macro holds the `TXWorker` execution slot so UI button taps or MCP TX calls cannot interleave IR pulses mid-macro.

### Verification Plan

- **Unit tests (`internal/config/macros_test.go`, `internal/runner/macro_runner_test.go`)**:
  - Test JSON load/save round-trip, `0600` permissions, atomic write, path-traversal rejection, delay bounds, and missing-step preflight validation.
  - Test `MacroRunner` execution order, timing accuracy, and mid-delay / mid-TX `context.Context` cancellation under `-race`.

---

## US-05.4: Ambient IR Interference & Carrier Monitor (`RX` Tab)

**As a** user trying to learn a remote in a sunny room or under PWM-dimmed LED lighting,  
**I want** a live **Ambient IR Interference Meter** on the `RX` tab plus selectable **TX carrier frequency (`36 / 38 / 40 / 56 kHz`)** and **duty cycle (`33%` / `50%`)**,  
**So that** I know immediately when ambient light is corrupting the IR receiver and can match non-standard carrier frequencies (e.g. Bang & Olufsen / Motorola `56 kHz` or Philips RC5 `36 kHz`) when transmitting.

### Domain Contract (`internal/irml/ambient.go`, `internal/config/config.go`, `internal/lirc`)

- **Ambient IR Monitor (`AmbientMonitor` in `internal/irml/ambient.go`)**:
  - Maintains a fixed-size sliding window (`1.0 s`) over incoming `/dev/lirc*` idle pulse edges without heap allocations.
  - Computes:
    - `EdgesPerSec float32` — background transition rate during idle listening.
    - `NoiseDutyPct float32` — percentage of window spent in active-low IR receiver assertion.
    - `FlickerHz float32` — dominant periodic interference frequency if detected (e.g. `100 Hz` / `120 Hz` mains LED/fluorescent flicker).
    - `Level AmbientLevel`:
      - `AmbientClean` (`"CLEAN"`, Emerald `#10e191`): `< 15 edges/s` and `< 5%` duty.
      - `AmbientModerate` (`"MODERATE"`, Amber `#f59e0b`): `15–150 edges/s` or `5–25%` duty (e.g. PWM LED lamp).
      - `AmbientSaturated` (`"SATURATED"`, Red `#ef4444`): `> 150 edges/s` or `> 25%` duty (direct sunlight / saturated AGC).
- **TX Carrier & Duty Presets (SSOT in `internal/config/config.go`)**:
  - `AllowedCarrierHz = [4]uint32{36000, 38000, 40000, 56000}` (default: `38000 Hz`).
  - `AllowedDutyPct = [2]uint32{33, 50}` (default: `33%`).
  - Persisted in `~/.config/ir-studio/config.json` (`default_carrier_hz`, `default_duty_cycle`).

### Acceptance Criteria (Given / When / Then)

1. **Live Ambient IR Noise-Floor Meter (`RX` Tab)**:
   - **Given** the user is on the `RX` tab listening on `/dev/lirc*` (or `SIM`),
   - **When** ambient infrared light hits the receiver,
   - **Then** a dedicated horizontal meter bar (`y = 20..30` on `RX`) updates smoothly in real time showing `AMB: CLEAN` (Emerald), `AMB: MODERATE 120Hz` (Amber), or `AMB: SATURATED` (Red) alongside `edges/s` and fill bar.
2. **Pre-Learn Interference Warning**:
   - **Given** `AmbientMonitor` reports `AmbientSaturated`,
   - **When** the user initiates Learn (`RX` capture or `REMOTE` → `4 LEARN`),
   - **Then** an Amber/Red banner warns `"High ambient IR! Shield sensor from sun/LEDs"` while still allowing capture attempt so the user is never hard-blocked.
3. **Selectable TX Carrier (`36 / 38 / 40 / 56 kHz`) & Duty Cycle (`33%` / `50%`)**:
   - **Given** the user cycles TX carrier (`C` or `SET → TX Carrier`) or duty cycle (`D` or `SET → TX Duty`),
   - **When** a new preset is chosen (e.g. `56 kHz`, `50%`),
   - **Then** the setting is persisted to `~/.config/ir-studio/config.json`, displayed in the `RX`/`SET` status bar (`56kHz 50%`), and applied via `LIRC_SET_SEND_CARRIER` and `LIRC_SET_SEND_DUTY_CYCLE` before transmitting any RAW signal lacking an explicit carrier or when TX override is active.
4. **Per-Signal Carrier Precedence & Hardware Restoration**:
   - **Given** the user's default TX carrier is `38 kHz`, and they transmit a `type: raw` button whose `.ir` file specifies `frequency: 56000` and `duty_cycle: 0.50`,
   - **When** `TXWorker` transmits that button,
   - **Then** `lirc` temporarily configures `56000 Hz` / `50%` for that transmission and unconditionally restores the configured default (`38000 Hz` / `33%`) upon completion or cancellation.

### Edge Cases

- **SIM mode (no `/dev/lirc*` device)**: `AmbientMonitor` simulates realistic clean/moderate/saturated profiles (toggleable in `SIM` tests) so UI screenshots and automated tests verify all 3 visual states without hardware.
- **Hardware ioctl unsupported (`ENOTTY` / `EINVAL` on some LIRC TX drivers)**: If a kernel driver does not support `LIRC_SET_SEND_DUTY_CYCLE`, `lirc` logs a structured warning, continues TX with driver default duty cycle, and surfaces a non-fatal badge rather than crashing.
- **Invalid config values**: Hand-edited `config.json` with `default_carrier_hz: 999999` or `default_duty_cycle: 99` is clamped/reset to valid SSOT defaults (`38000 Hz`, `33%`) on load.
- **Valid remote press vs ambient noise**: A brief `< 150 ms` IR remote button burst MUST NOT latch `AmbientSaturated` permanently; the 1.0 s sliding window decays back to `CLEAN` within `1.0 s` after the burst ends.

### Invariants

1. **Zero-Allocation Monitor Invariant**: `AmbientMonitor.RecordPulse` and `AmbientMonitor.Snapshot` MUST allocate `0` heap bytes on the hot path.
2. **Carrier Bounds Invariant**: `LIRC_SET_SEND_CARRIER` MUST ONLY be invoked with values in `[30000, 60000] Hz` (and UI presets strictly from `{36000, 38000, 40000, 56000}`).
3. **Duty-Cycle Safety Invariant**: `LIRC_SET_SEND_DUTY_CYCLE` MUST be clamped to `[20, 50]%` to protect the IR LED from thermal overcurrent.

### Verification Plan

- **Unit tests (`internal/irml/ambient_test.go`, `internal/config/config_test.go`)**:
  - Simulate clean idle (`0–5 edges/s`), 100 Hz / 120 Hz PWM LED flicker (`200–240 edges/s` periodic), and continuous sunlight saturation, verifying `AmbientLevel`, `FlickerHz`, and `1.0 s` window decay.
  - Verify config persistence and clamping of out-of-range carrier/duty values.
  - Verify `lirc` mock records exact `LIRC_SET_SEND_CARRIER` / `LIRC_SET_SEND_DUTY_CYCLE` ioctl calls and restoration on exit/cancel.
- **Benchmark (`BenchmarkAmbientMonitorRecordPulse`)**: Assert `0 allocs/op`.
