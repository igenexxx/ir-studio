# Epic 2: Universal Remote Probe & Separated Databases (`ir-studio`)

> **Status**: Specification (SDD)  
> **Bounded Contexts**: `ir-library` (`internal/library`), `ir-core` (`internal/irfile`, `internal/lirc`), `ir-config` (`internal/config`), `ir-ui` (`internal/ui/view_probe.go`, `internal/ui/view_saved.go`)  
> **Hardware Target**: M5Stack CardputerZero (Raspberry Pi CM0, 512 MB RAM, 320×170 RGB565 `/dev/fb0`, 46-key TCA8418 keyboard, Linux `/dev/lirc*` 38 kHz IR TX/RX)

---

## Epic Overview

Unlike Sub-GHz `.sub` captures (one signal per file), a Flipper Zero `.ir` file represents an **entire physical remote control** containing anywhere from 1 to 60+ named buttons (`Power`, `Off`, `Cool`, `Temp_up`, `Mute`, `Vol_up`, `HDMI_1`, etc.). Community repositories such as `Lucaslhm/Flipper-IRDB` contain thousands of `.ir` files across device categories (`ACs`, `TVs`, `Fans`, `LEDs`, `Projectors`).

Epic 2 establishes:
1. **Strict Storage Separation** between the user's writable remote collection (`UserLibrary`) and read-only external `DatabasePack` directories, with lazy `.ir` parsing to stay within CardputerZero's 512 MB RAM budget.
2. **Action-Filtered Universal `PROBE`** — a Flipper Universal Remote / TV-B-Gone style auto-scanner that filters a multi-button pack down to a single target action (e.g., `ACs` + `Power` or `Cool`; `TVs` + `Power` or `Mute`) and transmits candidates at a user-configured interval (`0.3s`–`5.0s`).
3. **Human-Reaction-Safe Stop & Rewind** — instant hardware-safe `STOP` with a 12-slot rewind trail (`>` last sent, `*` previous 12 sent), upward `F`/`X` backtracking, and `5 (TRY)` single-button verification.
4. **Whole-Remote Promotion (`6 SAVE`)** — one-key copy of the **entire winning `.ir` remote** (every button, not just the probed button) into the user's writable library with collision-safe naming.

### Architectural & Package Boundaries

```text
internal/library/
  store.go         # UserLibrary & DatabasePack discovery, lazy metadata index, path sandbox, PromoteRemoteToUser
  filter.go        # ActionFilter normalization, canonical action aliases, pack-to-candidate projection
  probe.go         # ProbeSession state machine (IDLE/RUN/PAUSE/DONE), 12-slot RecentSent ring, interval bounds
  txworker.go      # Async TXWorker consuming irfile.Signal over lirc.Transmitter with instant context cancel
internal/config/
  config.go        # Persists ProbeIntervalMs (300..5000 ms, default 800 ms) in ~/.config/ir-studio/config.json
internal/ui/
  view_probe.go    # PROBE tab renderer & key handler (pack/action selectors, >, * trail, 4..8 softkeys)
  view_saved.go    # SAVED tab renderer & key handler (UserLibrary remotes, button counts, open in REMOTE)
```

---

## User Stories

### US-02.1: Strict Storage Separation & Lazy-Parsed External Database Packs

**As a** CardputerZero `ir-studio` user,  
**I want** my personal learned/saved remotes (`~/ir-studio/remotes/*.ir`) to be strictly separated from external database packs (`~/ir-studio/databases/<pack>/*.ir`) with lazy `.ir` parsing,  
**So that** browsing or probing thousands of `Flipper-IRDB` remotes never corrupts my personal library, never modifies external packs during normal editing/learning, and stays fast within 512 MB RAM.

#### Acceptance Criteria

1. **Given** `~/ir-studio/remotes/` contains user `.ir` files and `~/ir-studio/databases/acs/` and `~/ir-studio/databases/tvs/` contain synced `.ir` packs,  
   **When** `library.Store` initializes and enumerates libraries,  
   **Then** `ListUserRemotes()` returns only `.ir` files from `~/ir-studio/remotes/` (sorted lexicographically, case-insensitive) and `ListDatabasePacks()` returns distinct `DatabasePack` descriptors (`ID: "acs"`, `ID: "tvs"`, etc.) without merging external pack files into `UserLibrary`.

2. **Given** a `DatabasePack` contains 1,500 `.ir` files (some with large `RAW` AC pulse arrays),  
   **When** the user opens the `PROBE` tab or switches packs,  
   **Then** `Store` enumerates pack entries using filesystem metadata (`Name`, `Path`, `SizeBytes`, `ModTime`) and does **not** eagerly parse all `RAW` pulse arrays into memory; full `irfile.Parse` runs lazily on demand when a candidate is selected for `PROBE` transmission, `5 (TRY)`, `REMOTE` view, or explicit indexing.

3. **Given** an external pack directory contains hidden metadata files (`.sync-state.json`), non-`.ir` files (`README.md`, `.DS_Store`), or subdirectories,  
   **When** `ListPackEntries(packID)` scans `~/ir-studio/databases/<packID>/`,  
   **Then** only regular files ending in `.ir` (case-insensitive) directly inside the pack directory are returned; `.sync-state.json` and non-`.ir` artifacts are silently ignored.

4. **Given** any write/edit/learn/append-button/rename-button/noise-purge operation targeting a remote,  
   **When** the target path resolves outside `~/ir-studio/remotes/` (including inside `~/ir-studio/databases/<pack>/` or via `../` traversal or symlinks),  
   **Then** `Store` rejects the operation with a typed sentinel error (`ErrReadOnlyPack` or `ErrPathOutsideSandbox`) and performs zero filesystem mutations.

5. **Given** `~/ir-studio/remotes/` or `~/ir-studio/databases/` does not exist on first launch,  
   **When** `Store` initializes or saves a user remote,  
   **Then** missing directories are created safely (`0755` for data dirs) without failing read-only listing calls (which return an empty slice `[]RemoteEntry{}`).

#### Edge Cases

- **Corrupt or empty `.ir` file in a pack**: Lazy parse returns an error for that single entry; `PROBE` skips it with a non-fatal status counter (`skipped`) rather than crashing or aborting the pack listing.
- **Symlink pointing outside sandbox**: `filepath.EvalSymlinks` (or `os.Lstat` check) prevents writing or deleting targets outside `~/ir-studio/remotes/` or `~/ir-studio/databases/<pack>/`.
- **Huge `.ir` file (`> 1 MiB`)**: Rejected at lazy-load boundary with `ErrFileTooLarge` before allocating pulse slices.
- **Mixed-case extensions (`.IR`, `.Ir`)**: Matched case-insensitively on read; newly saved user files always normalize to lowercase `.ir`.
- **Missing `$HOME`**: Falls back deterministically to a configured base path injected via constructor DI (never panics on empty `$HOME`).

#### Invariants

- **External-DB Read-Only Invariant**: Learning, button editing, renaming, and `PURGE` MUST ONLY mutate `~/ir-studio/remotes/`. External packs in `~/ir-studio/databases/<pack>/` are strictly read-only to all remote-editing and probe operations (only modified by explicit `dbsync` or `US-03.3`/`US-03.4` pack management).
- **Path-Sandbox Invariant**: Every file read/write helper validates `filepath.Rel(absRoot, absTarget)` has no `..` prefix and is not absolute.
- **Lazy Memory Bound Invariant**: Opening `PROBE` on a 1,000-file pack MUST NOT retain parsed `[]int32` raw pulse buffers for unselected files in heap memory.

#### Verification Plan

- **Unit Tests (`internal/library/store_test.go`)**:
  - Table-driven tests verifying separation between `remotes/` and `databases/<pack>/`.
  - Adversarial path-traversal tests (`../databases/acs/foo.ir`, `/etc/passwd`, `a/b.ir`, `foo\bar.ir`, symlink escape) verifying `errors.Is(err, ErrPathOutsideSandbox)` and `errors.Is(err, ErrReadOnlyPack)`.
  - Verify `.sync-state.json`, `.txt`, and subdirectories are excluded from `ListPackEntries`.
  - Benchmark/allocation test confirming `ListPackEntries` on 1,000 synthetic `.ir` files does not invoke full pulse decoding.
- **Race & Coverage**: `go test -race -cover ./internal/library/...` with `>= 90%` statement coverage.

---

### US-02.2: Action-Filtered Universal `PROBE` with Configurable Interval

**As a** user trying to control an unknown TV, air conditioner, fan, LED strip, or projector without knowing its brand or model,  
**I want** to select a device category pack (e.g., `DB:ACs` or `DB:TVs`) and an action filter (e.g., `Power`, `Cool`, `Off`, `Mute`), configure the transmission interval (`0.3s`–`5.0s`), and auto-probe matching buttons sequentially,  
**So that** `ir-studio` behaves like a Flipper Universal Remote / TV-B-Gone and only transmits relevant buttons instead of wasting time on `Menu` or `Num_7`.

#### Acceptance Criteria

1. **Given** the user is on the `PROBE` tab with an active pack (e.g., `DB:ACs` containing `Daikin_ARC480.ir`, `LG_AKB749.ir`, `Midea_RG57.ir`),  
   **When** the user selects an `ActionFilter` (`Power`, `Off`, `Cool`, `Heat`, `Mute`, `Vol+`, `Vol-`, `Input`, or `All`),  
   **Then** `library.FilterPackCandidates` projects the pack into an ordered list of `ProbeCandidate{RemotePath, RemoteName, ButtonName, ButtonIndex}` matching the filter via case-insensitive normalized alias matching (e.g., `Power` matches `Power`, `POWER`, `Pwr`, `Power_on`, `On_off`, `Toggle`; `Cool` matches `Cool`, `Cool_24`, `Mode_cool`; `Off` matches `Off`, `Power_off`, `Standby`; `Mute` matches `Mute`, `Muting`), deduplicating to at most one primary matching button per remote unless `ActionFilter == "All"`.

2. **Given** a remote in the active pack has **no** button matching the selected `ActionFilter` (e.g., a projector remote with no `Cool` button),  
   **When** the candidate list is built,  
   **Then** that remote is cleanly omitted from the filtered candidate list (never causing an empty TX or error), and the header shows the exact filtered count (e.g., `142/210`).

3. **Given** the user adjusts the probe interval on the `PROBE` tab (or in `SET`),  
   **When** the user cycles or sets the interval,  
   **Then** the interval is clamped strictly to `[300 ms, 5000 ms]` (`0.3s`–`5.0s`, canonical step ladder: `0.3s`, `0.5s`, `0.8s`, `1.0s`, `1.5s`, `2.0s`, `3.0s`, `5.0s`, default `0.8s`), applied immediately to `ProbeSession`, and atomically persisted as `probe_interval_ms` in `~/.config/ir-studio/config.json` (`0600`).

4. **Given** `ProbeSession` is in `IDLE` or `PAUSE` with `>= 1` filtered candidates,  
   **When** the user presses `4 (RUN)` (or `Enter` when idle),  
   **Then** `ProbeSession` transitions to `RUN` and dispatches each candidate asynchronously to `TXWorker` spaced by `ProbeInterval` (measured after the previous IR frame transmission finishes, so long `200 ms` AC `RAW` frames never overlap with the next candidate).

5. **Given** `ProbeSession` is running on the 320×170 LCD,  
   **When** each candidate is transmitted,  
   **Then** the UI renders without frame drops (`55 ms` render cadence): active pack pill (`DB:ACs`), action filter pill (`ACT:Power`), interval pill (`0.8s`), progress (`042/142`), highlighted candidate row with remote name + matched button + protocol/carrier (`38k NEC` or `38k RAW`), and bottom softkeys aligned at `x = [49, 105, 161, 217, 273]`.

6. **Given** `ProbeSession` transmits the final candidate (`idx == len(candidates)-1`),  
   **When** transmission completes,  
   **Then** `ProbeSession` transitions cleanly to `DONE`, stops scheduling further TX jobs, and displays a `Probe complete (N sent)` status toast.

#### Edge Cases

- **Empty pack or 0 candidates matching filter**: Pressing `4 (RUN)` does not start `TXWorker`; displays amber toast `No buttons match "<filter>"` and remains `IDLE`.
- **Long AC `RAW` signal (`~250 ms`) with `0.3s` interval**: Interval timer starts **after** `TXWorker` finishes transmitting the current signal (post-TX gap), guaranteeing zero carrier collision and respecting LIRC TX/RX mutual exclusion.
- **Invalid/corrupt `probe_interval_ms` in `config.json` (e.g., `0`, `-500`, `99999`)**: `config.Load` clamps to `[300, 5000]` (or defaults to `800 ms` when `0`) without failing startup.
- **Switching pack or action filter while `RUN`**: Immediately cancels active `TXWorker` context, resets `ProbeSession` to `IDLE`, clears the rewind trail, and rebuilds the candidate list for the new pack/filter.
- **Button name variations in `Flipper-IRDB`**: Underscores, hyphens, spaces, and casing (`POWER_OFF`, `Power-Off`, `power off`, `Off`) normalize cleanly via a single canonical normalizer function (`NormalizeButtonAction`).

#### Invariants

- **Interval Bounds Invariant**: `ProbeIntervalMs` MUST always satisfy `300 <= ProbeIntervalMs <= 5000` (`MinProbeInterval = 300 * time.Millisecond`, `MaxProbeInterval = 5000 * time.Millisecond`).
- **Single-Flight TX Invariant**: At most ONE IR transmission may execute on `/dev/lirc*` at any instant; `ProbeSession` MUST wait for `TXWorker` completion before starting the inter-probe delay timer.
- **SSOT Action Alias Invariant**: Canonical action filter definitions and alias matching rules exist in exactly one place (`internal/library/filter.go`), shared by `PROBE` UI and MCP tools.
- **UI Non-Blocking Invariant**: No LIRC ioctl or `.ir` disk read runs synchronously on the 55 ms UI draw loop.

#### Verification Plan

- **Unit Tests (`internal/library/filter_test.go`, `internal/library/probe_test.go`, `internal/config/config_test.go`)**:
  - Table-driven alias matching tests covering real `Flipper-IRDB` naming patterns (`Power`, `PWR`, `Power_on`, `Off`, `Cool_24`, `Mode_cool`, `Mute`, `Vol_dn`, empty/weird unicode names).
  - State machine transition tests: `IDLE -> RUN -> PAUSE -> RUN -> DONE`, pack/filter switch cancellation, empty candidate list guard.
  - Interval clamping tests (`-1`, `0`, `299`, `300`, `800`, `5000`, `5001`) and `config.json` round-trip persistence with `0600` permissions.
- **Concurrency & Timing Tests**: Run with `go test -race` using a mock `Transmitter` with simulated `50 ms` TX duration to verify zero overlapping TX calls and deterministic spacing.

---

### US-02.3: Instant `STOP`, 12-Slot Reaction-Time Rewind Trail, Backtracking & `5 (TRY)` Verify

**As a** user watching a TV turn on or listening for an air conditioner to beep during an auto-probe,  
**I want** instant `STOP` (`4 STOP` / `Space` / `Enter`), a visual 12-slot rewind trail (`>` last sent, `*` previous 12 sent), upward `F`/`X` backtracking, and `5 (TRY)` single-button verification,  
**So that** even when my human reaction time plus appliance boot delay causes the probe to overshoot by 2–8 remotes, I can immediately backtrack and confirm the exact winning remote.

#### Acceptance Criteria

1. **Given** `ProbeSession` is in state `RUN` and actively transmitting or waiting between candidates,  
   **When** the user presses `4 (STOP)`, `Space`, or `Enter`,  
   **Then** `TXWorker` cancels the in-flight `context.Context` immediately (returning LIRC TX to idle), `ProbeSession` transitions to `PAUSE` within `< 20 ms`, and the list cursor locks onto `LastSentIdx`.

2. **Given** `ProbeSession` has transmitted candidates at indices `0, 1, ..., 19` and is stopped at index `19`,  
   **When** the `PROBE` list renders on the 320×170 LCD,  
   **Then** candidate `19` (`LastSentIdx`) displays the `>` marker (highlighted in cyan/emerald), candidates `7..18` (the 12 preceding transmitted candidates in `RecentSent`) display the `*` marker (highlighted in amber), and unsent or older candidates display a blank gutter.

3. **Given** `ProbeSession` is in `PAUSE` after overshooting the winning remote by 4 slots,  
   **When** the user presses `F` (Up) to backtrack onto a `*`-marked candidate (or `X` Down to move forward),  
   **Then** the cursor moves one row per keypress without clearing the `>` / `*` markers and without resuming auto-probe, scrolling the viewport smoothly around the cursor.

4. **Given** the user has backtracked the cursor to a specific candidate (e.g., index `15`, `Daikin_ARC480.ir [Power]`),  
   **When** the user presses `5 (TRY)`,  
   **Then** `ir-studio` lazily loads that candidate's exact button signal and transmits **only that single button** once asynchronously via `TXWorker` (displaying a brief `TX: Daikin_ARC480 [Power]` badge), without advancing the cursor and without restarting auto-probe.

5. **Given** the user presses `5 (TRY)` while a previous `TRY` transmission is still in flight,  
   **When** the second `5 (TRY)` arrives,  
   **Then** the in-flight TX context is canceled cleanly and replaced by the new single-button TX (no queued TX storm, no race condition).

6. **Given** the user paused, tested a candidate, determined it was a false positive, and presses `4 (RUN)` again,  
   **When** `ProbeSession` resumes,  
   **Then** it resumes scanning from the current cursor position (or `LastSentIdx + 1` if cursor was not moved) and continues appending to the 12-slot `RecentSent` trail.

#### Edge Cases

- **Stopping after fewer than 12 transmissions (e.g., 3 sent)**: `LastSentIdx` is `2` (`>`), `RecentSent` contains only `[0, 1]` (`*`); no negative indices or phantom markers.
- **Stopping before the very first candidate finishes transmitting**: `LastSentIdx` is `-1` (or `0` if TX started); `RecentSent` is empty; cursor stays at `0` without panicking.
- **More than 12 transmissions (e.g., 100 sent)**: `RecentSent` behaves as a fixed-capacity 12-slot ring (`MaxRewindTrail = 12`), evicting the oldest `*` marker with zero unbounded slice growth.
- **Pressing `5 (TRY)` while `ProbeSession` is `RUN`**: Automatically pauses `ProbeSession` first (canceling auto-probe), then fires single-button `TRY` on the highlighted candidate.
- **Re-probing after Smart Rank (`R`) reorders candidates**: Reordering resets `LastSentIdx = -1` and clears `RecentSent` so trail markers never point to wrong candidates.

#### Invariants

- **Fixed Rewind Capacity Invariant**: `RecentSent` holds at most `MaxRewindTrail = 12` prior indices (excluding `LastSentIdx`); `LastSentIdx` and `RecentSent` never contain duplicate or out-of-bounds indices.
- **Immediate Cancellation Invariant**: Pressing `4 (STOP)` / `Space` / `Enter` MUST cancel the active `TXWorker` context immediately without waiting for `ProbeInterval` to elapse.
- **Marker Semantics Invariant**: `>` ALWAYS denotes the single most recently transmitted candidate; `*` ALWAYS denotes the up-to-12 immediately preceding transmitted candidates.

#### Verification Plan

- **Unit Tests (`internal/library/probe_test.go`, `internal/library/txworker_test.go`)**:
  - Table-driven tests for `RecordSent(idx)` across 0, 1, 5, 12, 13, and 100 transmissions verifying exact `LastSentIdx`, `RecentSent` membership, `MarkerFor(idx)` (`'>'`, `'*'`, `' '`), and zero duplicate entries.
  - Adversarial test verifying `Stop()` cancels a blocked mock `Transmitter` within `< 50 ms` and leaves `State == ProbePaused`.
  - Rapid `TRY` spam test (20 consecutive calls under `-race`) verifying clean cancellation and single-flight hardware serialization.
- **UI Snapshot / Logic Tests (`internal/ui/view_probe_test.go`)**:
  - Verify `F`/`X` backtracking preserves `>`/`*` markers, `5 (TRY)` triggers single-shot TX without moving cursor, and `4`/`Space`/`Enter` stops active probe.

---

### US-02.4: One-Key `6 (SAVE)` Whole-Remote Promotion (`PromoteRemoteToUser`)

**As a** user who just found a working TV or AC remote during `PROBE` and verified it with `5 (TRY)`,  
**I want** to press a single key (`6 SAVE`) to copy the **entire winning `.ir` remote file** (all buttons: `Power`, `Temp+`, `Temp-`, `Mode`, `Fan`, `Vol+`, `Mute`, etc.) from the read-only external pack into my writable `~/ir-studio/remotes/` library,  
**So that** I immediately have the full multi-button remote ready in `SAVED` and `REMOTE` without manually re-learning 20+ buttons or risking accidental overwrite of existing remotes.

#### Acceptance Criteria

1. **Given** the user has highlighted a candidate from an external `DatabasePack` (e.g., `~/ir-studio/databases/acs/Daikin_ARC480.ir` matched on button `Power`, which contains 24 buttons total),  
   **When** the user presses `6 (SAVE)` (invoking `Store.PromoteRemoteToUser(srcPath)`),  
   **Then** `Store` copies the **complete `.ir` file** byte-for-byte (preserving all 24 buttons, Flipper headers, protocol/address/command entries, raw carrier/duty/data, and comments) into `~/ir-studio/remotes/Daikin_ARC480.ir` via atomic temp-file + rename (`0644`).

2. **Given** `~/ir-studio/remotes/Daikin_ARC480.ir` already exists in `UserLibrary`,  
   **When** `PromoteRemoteToUser` is invoked again for a file with the same basename,  
   **Then** `Store` does **not** overwrite the existing user remote; instead it deterministically selects the first unused collision-safe filename (`Daikin_ARC480_2.ir`, `Daikin_ARC480_3.ir`, ...) and returns the resulting `PromotedResult{DestPath, DestName, ButtonCount}`.

3. **Given** `PromoteRemoteToUser` succeeds,  
   **When** the operation completes,  
   **Then**:
   - The source file in `~/ir-studio/databases/<pack>/` remains completely unchanged (read-only invariant).
   - The `PROBE` UI displays an emerald toast: `Saved: Daikin_ARC480.ir (24 btns)`.
   - `SAVED` tab immediately lists the promoted remote, and opening it in `REMOTE` tab exposes all 24 buttons for immediate transmission.

4. **Given** the user is currently browsing `USER` pack in `PROBE` (i.e., the file is already inside `~/ir-studio/remotes/`),  
   **When** the user presses `6 (SAVE)`,  
   **Then** `Store` returns `ErrAlreadyInUserLibrary` (or UI shows muted toast `Already in User Library`) without creating a duplicate copy.

5. **Given** the source `.ir` file in the external pack is corrupt or fails `irfile.Parse` validation,  
   **When** `PromoteRemoteToUser` is called,  
   **Then** `Store` rejects the promotion with a parse error and leaves zero temporary or partial files in `~/ir-studio/remotes/`.

#### Edge Cases

- **Collision exhaustion**: Cap suffix search at a sane upper bound (e.g., `_2`..`_999`); if all 999 exist, return `ErrNameCollisionExhausted` rather than looping forever.
- **Filename sanitization**: Strip directory components and sanitize any unusual characters while preserving valid Flipper `.ir` basename characters (`A-Z`, `a-z`, `0-9`, `_`, `-`, `.`).
- **Disk full / write error during atomic copy**: Temp file `.ir-promote-*.tmp` is unconditionally removed in `defer`; existing `~/ir-studio/remotes/` contents remain intact.
- **Pressing `6 (SAVE)` while `ProbeSession` is `RUN`**: Pauses probe cleanly (or promotes the `LastSentIdx` / highlighted candidate safely without racing `TXWorker`).

#### Invariants

- **Whole-Remote Fidelity Invariant**: `PromoteRemoteToUser` MUST copy every button in the winning `.ir` file (never stripping unprobed buttons).
- **Non-Destructive Promotion Invariant**: `PromoteRemoteToUser` MUST NEVER overwrite an existing file in `~/ir-studio/remotes/` and MUST NEVER modify or delete the source file in `~/ir-studio/databases/<pack>/`.
- **Atomic Write Invariant**: Promoted files appear in `~/ir-studio/remotes/` atomically (`CreateTemp` -> `Chmod(0644)` -> `Write` -> `Close` -> `Rename`), preventing half-written `.ir` files on power loss.

#### Verification Plan

- **Unit Tests (`internal/library/store_test.go`)**:
  - Happy path: promote a multi-button `.ir` file (e.g., 5 parsed + 2 raw buttons) and assert byte-for-byte equality, exact `ButtonCount == 7`, and untouched source file.
  - Collision sequence: promote same basename 3 times and assert `Remote.ir`, `Remote_2.ir`, `Remote_3.ir` all exist and earlier files are untouched.
  - Adversarial cases: source already in `UserLibrary` (`ErrAlreadyInUserLibrary`), path traversal outside databases dir (`ErrPathOutsideSandbox`), corrupt `.ir` source (no temp file left behind), read-only destination dir error cleanup.
- **Coverage & Race**: `go test -race -cover ./internal/library/... ./internal/ui/...` (`>= 90%` coverage on `store.go`, `filter.go`, `probe.go`).
