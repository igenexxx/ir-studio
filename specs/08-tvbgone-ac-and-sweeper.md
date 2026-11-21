# Epic 8: Built-in Offline TV-B-Gone, Smart AC Controller & Carrier Frequency Sweeper

## Epic Overview

Epic 8 expands `ir-studio` with three high-impact field capabilities that work **100% offline** out of the box on M5Stack CardputerZero (`320×170` RGB565 `/dev/fb0`, 46-key TCA8418 keyboard, `/dev/lirc*`, `CGO_ENABLED=0`) and integrate seamlessly with the existing `internal/{irfile,library,lirc,ui,mcpserver}` packages:

1. **`US-08.1` — Built-in Offline TV-B-Gone Virtual Pack (`GEN:TV-B-Gone`)**: Top-100 universal `Power` codes across TVs, Projectors, and Soundbars compiled directly into the binary (mirroring `GEN:*` virtual packs in `rf-heatmap`), ready immediately before `--sync`.
2. **`US-08.2` — Smart AC State-Frame Controller (`AC` sub-view)**: Pure-Go parametric HVAC state-frame encoders with valid `SUM8` / nibble / complement checksums for `Gree`, `Midea`, `Daikin`, `LG_AC`, `Samsung_AC`, and `Mitsubishi`, with an interactive `320×170` controller and `ir_send_ac_state` MCP tool.
3. **`US-08.3` — Carrier Frequency Sweeper (`36 / 38 / 40 / 56 kHz`)**: Rapid multi-carrier resend helper for finicky receivers (Sony `40 kHz`, Cisco/Motorola `56 kHz`, Philips RC5 `36 kHz`) with unconditional deferred restoration to `38000 Hz / 33%`, accessible via UI softkeys and `ir_sweep_carrier`.

---

## US-08.1: Built-in Offline TV-B-Gone Virtual Pack (`GEN:TV-B-Gone`)

**As a** CardputerZero user on a fresh install or in an air-gapped environment without internet access,
**I want** a compiled-in `GEN:TV-B-Gone` virtual pack containing the top-100 universal `Power` codes across major TV, Projector, and Soundbar brands,
**So that** I can run Universal Power-Off sweeps in `PROBE` immediately before running `--sync` and promote winning codes into `UserLibrary`.

### Domain Contract (`internal/library/tvbgone.go`, `internal/library/tvbgone_codes.go`)

- **SSOT Constants & Types**:
  ```go
  const (
      VirtualPackPrefix  = "GEN:"
      VirtualPackTVBGone = "GEN:TV-B-Gone"
      TVBGoneCodeCount   = 100
  )

  type TVBGoneCategory string

  const (
      CategoryTV        TVBGoneCategory = "TV"
      CategoryProjector TVBGoneCategory = "Projector"
      CategorySoundbar  TVBGoneCategory = "Soundbar"
  )

  type TVBGoneEntry struct {
      Brand    string
      Category TVBGoneCategory
      Signal   irfile.IRSignal // Kind: SignalParsed, Name: "Power"
  }

  func IsVirtualPack(pack string) bool
  func TVBGoneEntries() []TVBGoneEntry   // deterministic priority order, len == 100
  func TVBGoneRemotes() []RemoteEntry    // ReadOnly: true, SourcePack: VirtualPackTVBGone
  ```
- **Brand & Protocol Coverage (`TVBGoneCodeCount == 100`)**:
  - **TVs (~65 codes)**: Samsung (`Samsung32`), LG (`NEC`), Sony (`SIRC`), Panasonic (`Kaseikyo`), Philips (`RC5`/`RC6`), TCL (`NEC`/`NECext`), Hisense (`NEC`), Toshiba (`NEC`), Vizio (`NEC`), Sharp (`Kaseikyo`/`NEC`), Hitachi (`NEC`), Insignia/Roku (`NEC`), Sanyo (`NEC`), JVC (`NEC`), Grundig (`RC5`), Thomson (`RC5`), Loewe (`RC5`), Xiaomi/Skyworth (`NEC`).
  - **Projectors (~20 codes)**: Epson (`NEC`), BenQ (`NEC`), Optoma (`NEC`), ViewSonic (`NEC`), Acer (`NEC`), NEC Display (`NEC`), Panasonic Projector (`Kaseikyo`), Sony Projector (`SIRC`), Christie (`NEC`).
  - **Soundbars & A/V (~15 codes)**: Bose (`NEC`), Sonos (`NEC`), Yamaha (`NEC`/`NECext`), Denon (`Kaseikyo`), Onkyo (`NECext`), Pioneer (`NEC`), JBL (`NEC`), Polk (`NEC`), Klipsch (`NEC`).
- **Store & Probe Integration (`internal/library`)**:
  - `Store.ListPackRemotes(VirtualPackTVBGone)` returns `TVBGoneRemotes()` with zero disk I/O (bypasses `validateBareName` colon check only for `IsVirtualPack(pack)`).
  - `Store.LoadRemote(entry)`: when `IsVirtualPack(entry.SourcePack)`, returns `entry` directly (already populated with `Signals`, `ButtonCount == 1`, `ReadOnly == true`) without touching `DatabasesDir`.
  - `Store.PromoteToUser(entry, dstName)`: when `IsVirtualPack(entry.SourcePack)`, serializes `entry.Signals` into `UserDir` via `SaveUserRemote` + `pickCollisionSafeName` (default filename `<Brand>_<Category>.ir`).
  - `Store.DeleteDatabaseRemote(pack, relPath)`: immediately rejects `IsVirtualPack(pack)` with `ErrReadOnlyPack`.
  - `FilterPackCandidates(store, VirtualPackTVBGone, action)` works seamlessly via `ListPackRemotes` + `LoadRemote`.
- **UI Integration (`internal/ui/view_probe.go`, `320×170` LCD)**:
  - `refreshProbePacks()` inserts `library.VirtualPackTVBGone` immediately after `""` (`USER`), making `GEN:TV-B-Gone` the default selected pack on fresh installs when no disk packs exist.
  - Header pill displays `GEN:TVBG` (Cyan); `8 PACK` cycles `USER -> GEN:TV-B-Gone -> <synced DB packs>`; `4 RUN/STOP`, `5 TRY`, `6 SAVE` (`PromoteToUser`), `7 ACT`, `Z/C` interval, `R` Smart Rank work identically to synced packs.
- **MCP Integration (`internal/mcpserver`)**:
  - `ir_list_databases` includes `{"id": "GEN:TV-B-Gone", "remote_count": 100, "virtual": true, "source_url": "embedded://tv-b-gone"}`.
  - `ir_read_remote`, `ir_transmit_button`, `ir_promote_remote`, and `ir_rank_probe_pack` accept `pack: "GEN:TV-B-Gone"`. `ir_delete_pack_files` refuses virtual packs with `ErrReadOnlyPack`.

### Acceptance Criteria (Given / When / Then)

1. **Zero-Network First-Boot Probe**:
   - **Given** a fresh CardputerZero with empty `~/ir-studio/remotes/` and empty `~/ir-studio/databases/` (never ran `--sync`),
   - **When** the user opens the `PROBE` tab,
   - **Then** `GEN:TV-B-Gone` is pre-selected with `100` `Power` candidates ready to fire via `4 RUN` or `5 TRY`, and `0` filesystem errors occur.
2. **Valid Protocol Encoding for Every Embedded Code**:
   - **Given** all `100` entries returned by `TVBGoneEntries()`,
   - **When** each `entry.Signal` is encoded through `irfile.DefaultRegistry().Encode(sig)` and `ToLIRCPulses()`,
   - **Then** 100% succeed with odd-length positive LIRC pulse trains and valid carriers (`36–40 kHz`).
3. **Promote Winning Virtual Code to UserLibrary**:
   - **Given** the user pauses `GEN:TV-B-Gone` on `Samsung_TV` and presses `6 SAVE` (or calls `ir_promote_remote` with `pack: "GEN:TV-B-Gone"`),
   - **When** `Store.PromoteToUser` runs,
   - **Then** a collision-safe `.ir` file (e.g. `Samsung_TV.ir`) is written atomically inside `~/ir-studio/remotes/`, appears in `SAVED → USER`, and the virtual pack remains immutable.
4. **Virtual Pack Read-Only Protection**:
   - **Given** a caller invokes `Store.DeleteDatabaseRemote("GEN:TV-B-Gone", "Samsung_TV.ir")` or MCP `ir_delete_pack_files`,
   - **When** validation executes,
   - **Then** the operation fails with `ErrReadOnlyPack` and touches zero files.

### Edge Cases & Test Plan

- **Edge cases**: Duplicate `(Protocol, Address, Command)` tuples in the embedded table (must be unique across all 100 entries); `ActionOff` vs `ActionPower` filter compatibility (`Power` matches `ActionPower` and `ActionAll`); colon `:` in `GEN:TV-B-Gone` must not bypass path sandboxing for non-virtual packs (`GEN:../etc` MUST be rejected).
- **Tests (`internal/library/tvbgone_test.go`, `internal/mcpserver/mcpserver_test.go`)**:
  - Assert `len(TVBGoneEntries()) == 100`, zero duplicate `(Protocol, Address, Command)` keys, all 3 categories (`TV`, `Projector`, `Soundbar`) represented, and 100% round-trip `Encode` -> `ToLIRCPulses` success.
  - Test `FilterPackCandidates`, `PromoteToUser` collision handling (`_2.ir`), `ErrReadOnlyPack` on delete, and `GEN:../escape` sandbox rejection.

---

## US-08.2: Smart AC State-Frame Controller (`AC` Sub-View)

**As a** user controlling mini-split air conditioners without recording 50 separate RAW buttons per temperature/mode combination,
**I want** pure-Go parametric AC state-frame encoders with valid checksums, an interactive `AC` controller sub-view on the `320×170` LCD, and an `ir_send_ac_state` MCP tool,
**So that** I can dial exact temperature, mode, fan, and swing settings on the fly, transmit them directly, or save any state as a standard Flipper `.ir` button.

### Domain Contract (`internal/irfile/ac_state.go`, `internal/irfile/ac_brands.go`)

- **SSOT Types & Strategy Interface**:
  ```go
  type ACBrand string

  const (
      ACBrandGree       ACBrand = "Gree"
      ACBrandMidea      ACBrand = "Midea"
      ACBrandDaikin     ACBrand = "Daikin"
      ACBrandLG         ACBrand = "LG_AC"
      ACBrandSamsung    ACBrand = "Samsung_AC"
      ACBrandMitsubishi ACBrand = "Mitsubishi"
  )

  type ACMode string // "COOL", "HEAT", "DRY", "FAN", "AUTO"
  type ACFan  string // "AUTO", "LOW", "MED", "HIGH"

  const (
      ACMinTempC = 16
      ACMaxTempC = 30
  )

  type ACState struct {
      Brand ACBrand `json:"brand"`
      Power bool    `json:"power"`
      Mode  ACMode  `json:"mode"`
      TempC int     `json:"temp_c"` // validated in [16, 30]
      Fan   ACFan   `json:"fan"`
      Swing bool    `json:"swing"`
  }

  type ACEncoder interface {
      Brand() ACBrand
      Carrier() CarrierSpec
      ChecksumKind() string // "NIBBLE", "COMPLEMENT", "SUM8"
      EncodeBytes(state ACState) ([]byte, error)
      EncodeSignal(state ACState) (IRSignal, error)
  }

  type ACRegistry struct { ... }
  func DefaultACRegistry() *ACRegistry
  func ValidateACState(s ACState) error
  ```
- **Brand Protocol & Checksum Specifications (All `38000 Hz / 33%`)**:
  - **`Gree`** (8 bytes / 67 bits across 2 subframes, 20 ms gap, header `9000/-4500 µs`): 4-bit nibble checksum in high nibble of byte 7 (`((modeBits + tempOffset + swingFlag + 10) & 0x0F)`).
  - **`Midea`** (6 bytes / 48 bits, header `4400/-4400 µs`, repeated once with 5.2 ms gap): byte-complement pairs (`b[1] == ^b[0]`, `b[3] == ^b[2]`, `b[5] == ^b[4]`).
  - **`Daikin`** (19-byte state subframe preceded by 8-byte header subframe, 29 ms gap, header `3480/-1750 µs`): `SUM8` checksum (`sum(bytes[0..17]) & 0xFF == bytes[18]`).
  - **`LG_AC`** (28-bit frame packed into 4 bytes, header `8400/-4200 µs`): 4-bit nibble sum (`sum(nibbles[0..5]) & 0x0F == nibble[6]`).
  - **`Samsung_AC`** (14-byte / 2-subframe HVAC packet, header `3000/-9000 µs`): nibble-count checksum byte in frame header + state subframe.
  - **`Mitsubishi`** (18-byte / 144-bit frame, header `3400/-1750 µs`): `SUM8` checksum (`sum(bytes[0..16]) & 0xFF == bytes[17]`).
- **UI Sub-View (`internal/ui/view_ac_ctrl.go`, `ACControlState` in `state.go`)**:
  - Toggled from `REMOTE` tab via `1` (`Key1`) or `8 AC` (when viewing a USER remote or empty deck); `Esc` returns to `REMOTE` button grid.
  - **`320×170` Layout (`y = 18..142`)**:
    - Sub-header (`y = 19..31`): Brand pill (`Gree` Cyan), `[R]Brand`, Power pill (`ON` Emerald / `OFF` Red), Checksum badge (`CHK:OK` Emerald).
    - Main Card (`y = 34..110`): Large temperature readout (`22C`, scale 3), Mode pill (`COOL`/`HEAT`/`DRY`/`FAN`/`AUTO`), Fan (`FAN:AUTO`) and Swing (`SWG:ON`) indicators.
    - Meta footer (`y = 113..139`): `Z/C:Temp F/X:Mode R:Brand` + encoded frame stats (`38kHz 142p SUM8`).
  - **Keybindings (46-key TCA8418)**:
    - `Z / C` (`KeyLeft / KeyRight`): Temperature `-1°C / +1°C` clamped to `[16, 30]`.
    - `F / X` (`KeyUp / KeyDown`): Cycle Mode `COOL -> HEAT -> DRY -> FAN -> AUTO`.
    - `R` (`KeyR`): Cycle Brand (`Gree -> Midea -> Daikin -> LG_AC -> Samsung_AC -> Mitsubishi`).
    - Bottom Softkeys `4..8`: `4 PWR` (toggle Power), `5 FAN` (cycle `AUTO/LOW/MED/HIGH`), `6 SWING` (toggle Swing), `7 SEND` (also `Enter` — encode & submit via `TXWorker`), `8 SAVE` (append/create `AC_<Brand>.ir` in `UserLibrary` with button name `<Mode>_<Temp>C` or `Power_Off`).
- **MCP Tool (`internal/mcpserver/tools_ac.go`, registered in `schema.go` & `server.go`)**:
  - Name: `ir_send_ac_state`
  - Arguments:
    - `confirm_ir` (bool, **required**, must be `true`)
    - `brand` (enum: `Gree`, `Midea`, `Daikin`, `LG_AC`, `Samsung_AC`, `Mitsubishi`, **required**)
    - `power` (bool, **required**)
    - `mode` (enum: `COOL`, `HEAT`, `DRY`, `FAN`, `AUTO`, **required**)
    - `temp_c` (integer `16..30`, **required**)
    - `fan` (enum: `AUTO`, `LOW`, `MED`, `HIGH`, optional, default `"AUTO"`)
    - `swing` (bool, optional, default `false`)
    - `save_as` (string, optional — sandboxed UserLibrary `.ir` filename to persist the encoded button)
  - Output JSON: `transmitted`, `brand`, `power`, `mode`, `temp_c`, `fan`, `swing`, `carrier_hz`, `pulse_count`, `checksum_kind`, `checksum_hex`, `duration_ms`, `mode_hw`, `saved_remote` (if `save_as` set).

### Acceptance Criteria (Given / When / Then)

1. **Valid Checksums Across All 6 Brands**:
   - **Given** any valid `ACState` (`temp_c` in `16..30`, any `Mode`/`Fan`/`Power`/`Swing`) for each of the 6 brands,
   - **When** `EncodeBytes` and `EncodeSignal` execute,
   - **Then** `EncodeBytes` produces the brand's exact frame length with a mathematically valid checksum (`SUM8`, nibble sum, or byte-complement), and `EncodeSignal` yields a valid `SignalRaw` `IRSignal` with odd-length positive `RawData` at `38000 Hz / 33%`.
2. **Interactive AC Control & Save as `.ir`**:
   - **Given** the user opens `AC` mode in `REMOTE` (`Gree`, `COOL`, `22°C`),
   - **When** the user presses `C` twice (`24°C`), `5 FAN` (`HIGH`), `7 SEND`, and `8 SAVE`,
   - **Then** `TXWorker` transmits the updated `24°C` frame non-blockingly, and `AC_Gree.ir` is written atomically in `~/ir-studio/remotes/` containing button `COOL_24C` readable by `irfile.Parse`.
3. **MCP `ir_send_ac_state` Gate & Validation**:
   - **Given** an MCP client calls `ir_send_ac_state` with `confirm_ir: false` or `temp_c: 12` or `brand: "Unknown"`,
   - **When** the tool handler validates arguments,
   - **Then** zero IR pulses are emitted and `isError: true` is returned with a clear validation message.
   - **When** called with `confirm_ir: true`, `brand: "Daikin"`, `power: true`, `mode: "COOL"`, `temp_c: 22`,
   - **Then** the frame is transmitted under `lircMu` and structured JSON with `checksum_hex` and `pulse_count` is returned.

### Edge Cases & Test Plan

- **Edge cases**: `temp_c` `< 16` or `> 30` rejected by `ValidateACState`; `power: false` encodes valid brand-specific OFF state while preserving valid checksum; `save_as` path traversal (`../evil.ir`) rejected by `ValidateSafeName`; `irml.InspectACFrame` verifies `SUM8` checksum on encoded Daikin and Mitsubishi signals.
- **Tests (`internal/irfile/ac_state_test.go`, `internal/mcpserver/tools_ac_test.go`, `internal/ui/ui_test.go`)**:
  - Table-driven tests across all 6 brands verifying checksum equations, `22C` vs `24C` byte diffs, OFF frames, out-of-range temperature/mode/brand errors, and `.ir` write/parse round-trip.
  - UI test covering `Z/C` clamping (`16..30`), `F/X` mode cycle, `R` brand cycle, `4..8` softkeys, and `SAVE` persistence.

---

## US-08.3: Carrier Frequency Sweeper (`36 / 38 / 40 / 56 kHz`)

**As a** user troubleshooting a finicky IR receiver whose true carrier frequency is unknown (e.g., Sony `40 kHz`, Cisco/Motorola `56 kHz`, Philips RC5 `36 kHz`),
**I want** a one-tap **Carrier Frequency Sweeper** that rapidly resends the target signal across `[36000, 38000, 40000, 56000] Hz` and unconditionally restores `38000 Hz / 33%`,
**So that** I can immediately identify or trigger non-standard receivers without manually cycling settings four times.

### Domain Contract (`internal/lirc/sweeper.go`)

- **SSOT Constants & Types**:
  ```go
  var SweepCarrierPresets = [4]uint32{36000, 38000, 40000, 56000}

  const (
      DefaultSweepGap = 80 * time.Millisecond
      MinSweepGap     = 40 * time.Millisecond
      MaxSweepGap     = 1000 * time.Millisecond
  )

  type SweepStepResult struct {
      FreqHz     uint32 `json:"freq_hz"`
      DutyPct    uint32 `json:"duty_pct"`
      DurationMs int64  `json:"duration_ms"`
      Sent       bool   `json:"sent"`
      Err        string `json:"err,omitempty"`
  }

  type SweepReport struct {
      SignalName      string            `json:"signal_name"`
      Steps           []SweepStepResult `json:"steps"`
      RestoredFreqHz  uint32            `json:"restored_freq_hz"`
      RestoredDutyPct uint32            `json:"restored_duty_pct"`
      Cancelled       bool              `json:"cancelled"`
  }

  func SweepCarrier(
      ctx context.Context,
      xcvr *Transceiver,
      reg *irfile.ProtocolRegistry,
      sig irfile.IRSignal,
      freqs []uint32,
      dutyPct uint32,
      gap time.Duration,
      onStep func(stepIdx int, freqHz uint32),
  ) (SweepReport, error)
  ```
- **Execution & Restoration Rules**:
  - Defaults: empty `freqs` uses `SweepCarrierPresets[:]`; `dutyPct == 0` uses `DefaultDutyPct` (`33%`); `gap` clamped to `[MinSweepGap, MaxSweepGap]`.
  - Pre-validates every frequency via `ValidateCarrier` and encodes `sig` once before emitting any step.
  - Emits each frequency via `xcvr.Send(ctx, pulses, CarrierSpec{FreqHz: hz, DutyPct: dutyPct})` (enforcing `TransceiverGate` optical settle guard + unconditional deferred restoration to `38000 Hz / 33%`), waiting `gap` via `sleepWithContext(ctx, gap)` between steps.
  - Unconditionally guarantees `xcvr.Transport()` is restored to `38000 Hz / 33%` after completion, error, or mid-sweep cancellation (`ctx.Done()`).
- **UI Softkey Access (`internal/ui/view_remote.go`, `internal/ui/view_rx.go`)**:
  - **`REMOTE` tab**: `5 SWEEP` (`KeySoft5`) launches an async carrier sweep on the focused button (re-assigning `LEARN+` to `6 LEARN+` and `RENAME` to `KeyR`, keeping `4 SEND`, `7 DEL`, `8 PROMO/AC`).
  - **`RX` tab**: When a capture is held (`!RX.Learning && RX.LastCap != nil`), `5 SWEEP` (`KeySoft5`, plus `KeyR` shortcut anytime a capture exists) sweeps the active capture (`activeRXPulses()`). Existing `KeySoft6` (`DIFF`), `KeySoft7` (`SAVE`), and `KeySoft8` (`CARR`) remain unchanged.
  - Progress displays via non-blocking toast/header badge (`SWEEP 40kHz (3/4)`) and cancels immediately (`< 50 ms`) on `Esc`.
- **MCP Tool (`internal/mcpserver/tools_ac.go` or `tools_tx.go`, `IRBackend.SweepCarrier`)**:
  - Name: `ir_sweep_carrier`
  - Arguments:
    - `confirm_ir` (bool, **required**, must be `true`)
    - Target (either `remote_name` + `button_name` [+ optional `pack`] OR `pulses` signed array)
    - `frequencies_hz` (optional integer array, each `36000..56000`, max 8 entries, default `[36000, 38000, 40000, 56000]`)
    - `duty_pct` (optional integer `25..50`, default `33`)
    - `gap_ms` (optional integer `40..1000`, default `80`)
  - Output JSON: `SweepReport` + `mode` (`LIRC`/`SIM`).

### Acceptance Criteria (Given / When / Then)

1. **4-Step Carrier Sweep & Deferred Restoration**:
   - **Given** a valid signal (`Power`) and `SimTransport` (or `/dev/lirc0`),
   - **When** `SweepCarrier` runs with default presets,
   - **Then** `SetSendCarrier` is invoked for `36000`, `38000`, `40000`, and `56000` Hz in order, 4 bursts are written, and `SimTransport.CarrierState()` equals `{FreqHz: 38000, DutyPct: 33}` upon return.
2. **Mid-Sweep Cancellation Restores Hardware Defaults**:
   - **Given** a sweep is cancelled via `context.CancelFunc` after step 2 (`38000 Hz`),
   - **When** `SweepCarrier` returns,
   - **Then** `report.Cancelled == true`, remaining steps (`40000`, `56000`) are not sent, and `CarrierState()` is unconditionally restored to `38000 Hz / 33%`.
3. **MCP `ir_sweep_carrier` Confirmation Gate & Validation**:
   - **Given** an MCP client calls `ir_sweep_carrier` with `confirm_ir: false` or an out-of-range frequency (`99000 Hz`),
   - **When** handled,
   - **Then** `isError: true` is returned and zero pulses are transmitted.
   - **When** called with `confirm_ir: true` and a valid remote/button (or `GEN:TV-B-Gone`),
   - **Then** all 4 carrier steps execute under `lircMu` and return structured `SweepReport` JSON.

### Edge Cases & Test Plan

- **Edge cases**: `frequencies_hz` exceeding 8 entries rejected; empty signal / no `RX.LastCap` shows Amber toast `"No signal to sweep"`; concurrent sweep press while a sweep is running cancels previous sweep cleanly before starting or ignores duplicate trigger; `nil` `Transceiver` returns descriptive error.
- **Tests (`internal/lirc/sweeper_test.go`, `internal/mcpserver/tools_ac_test.go`, `internal/ui/ui_test.go`)**:
  - Verify exact sequence `[36000, 38000, 40000, 56000]`, carrier restoration after normal completion, mid-sweep cancellation, and simulated ioctl error under `-race`.
  - Verify MCP `ir_sweep_carrier` gate (`confirm_ir`), frequency bounds, and UI `REMOTE`/`RX` softkey sweep triggering without blocking `Step()`.

---

## Architecture & File Decomposition (`<= 350` Lines / File)

| Package | New / Updated Files | Responsibility |
| --- | --- | --- |
| `internal/library` | `tvbgone.go`, `tvbgone_codes.go`, `store.go`, `promote.go` | `GEN:TV-B-Gone` 100-code virtual pack, `IsVirtualPack`, virtual `LoadRemote`/`PromoteToUser` |
| `internal/irfile` | `ac_state.go`, `ac_brands.go` | `ACState`, `ACEncoder`, `ACRegistry`, 6 brand parametric encoders + checksums |
| `internal/lirc` | `sweeper.go` | `SweepCarrier` multi-carrier helper with unconditional `38 kHz / 33%` restore |
| `internal/ui` | `state.go`, `view_probe.go`, `view_remote.go`, `view_ac_ctrl.go`, `view_rx.go`, `chrome.go` | `GEN:TV-B-Gone` pack selection, `AC` sub-view (`Z/C`, `F/X`, `4..8`), `SWEEP` softkeys, Help page sync |
| `internal/mcpserver` | `schema.go`, `server.go`, `backend.go`, `tools_ac.go`, `tools_library.go` | `ir_send_ac_state`, `ir_sweep_carrier` (`confirm_ir: true`), `GEN:TV-B-Gone` virtual pack support (23 total tools) |
