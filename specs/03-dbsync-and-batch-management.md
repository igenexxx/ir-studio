# Epic 3: Remote DB Delta Sync & Batch Management (`ir-studio`)

> **Status**: Specification (SDD)  
> **Bounded Contexts**: `ir-dbsync` (`internal/dbsync`), `ir-library` (`internal/library`), `ir-runner` (`internal/runner`), `ir-ui` (`internal/ui/view_saved.go`, `internal/ui/view_probe.go`, `internal/ui/view_settings.go`, `internal/ui/view_sources.go`)  
> **Hardware Target**: M5Stack CardputerZero (Raspberry Pi CM0, 512 MB RAM, 320×170 RGB565 `/dev/fb0`, 46-key TCA8418 keyboard, Wi-Fi / SSH)

---

## Epic Overview

`Lucaslhm/Flipper-IRDB` and community IR repositories contain thousands of multi-button `.ir` remote files organized by category (`ACs`, `TVs`, `Fans`, `LEDs`, `Projectors`, `Audio`, etc.). Downloading entire repositories repeatedly wastes bandwidth, SD-card write cycles, and time on a Raspberry Pi CM0, and users need ergonomic ways on a 46-key keyboard to prune unwanted remotes or remove entire packs without manually SSH-ing in.

Epic 3 establishes:
1. **Remote Source Registry (`sources.json`)** — strict, HTTPS-only registry supporting GitHub Trees API (targeting specific `Flipper-IRDB` subfolders) and HTTPS JSON manifests.
2. **Incremental Delta Sync (`dbsync.Syncer`)** — HTTP `ETag` (`304 Not Modified`) + per-file `SHA`/`SHA-256` state in `.sync-state.json`, 1 MiB file cap, `.ir` parse validation before disk write, safe default `prune: false` (optional `prune: true`), CLI `ir-studio --sync`, and non-blocking `SET → Sync DB Sources`.
3. **Multi-Select (`[x]`) Single & 2-Step Batch Deletion** — works in both `SAVED` (`UserLibrary`) and downloaded `DB:*` packs in `PROBE`, recording deleted pack filenames in `.sync-state.json` `ignored` so future syncs never re-download them.
4. **Dedicated `SET → Manage DB Sources` Sub-Screen** — on-device management for toggling sources `ON`/`OFF`, deleting only the downloaded folder (`5 DIR`), or deleting both source registration and folder (`6 ALL`) with 2-step confirmation.

### Architectural & Package Boundaries

```text
internal/dbsync/
  sources.go       # DatabaseSource schema, NormalizeAndValidate, ParseGitHubURL, LoadSources/SaveSources/Upsert/Remove
  state.go         # SyncState (.sync-state.json), Load/SaveSyncState, MarkIgnored, DeletePackDir, atomic write
  syncer.go        # Incremental GitHub Trees & Manifest delta syncer (ETag, SHA, 1 MiB cap, irfile.Parse validation)
internal/runner/
  runner.go        # CLI --sync flag entrypoint (runs SyncAll with signal context and prints structured summary)
internal/ui/
  view_saved.go    # [x] multi-select (Space/1/D) + 2-step delete in ~/ir-studio/remotes/
  view_probe.go    # [x] multi-select (Space/1/D) + 2-step delete in downloaded DB:* packs (+ MarkIgnored)
  view_settings.go # SET tab: "Sync DB Sources" (non-blocking) & "Manage DB Sources" sub-screen entry
  view_sources.go  # Manage DB Sources sub-screen (ON/OFF toggle, 5 DIR, 6 ALL, 2-step confirm)
```

---

## User Stories

### US-03.1: Remote Source Registry (`sources.json`) for GitHub Trees & HTTPS Manifests

**As a** CardputerZero `ir-studio` user or automation agent,  
**I want** a validated remote source registry at `~/.config/ir-studio/sources.json` supporting GitHub repository subfolders (e.g., `Lucaslhm/Flipper-IRDB` subfolders `ACs`, `TVs`, `Fans`, `LEDs`, `Projectors`) and HTTPS JSON manifests,  
**So that** I can subscribe selectively to only the device categories I need without pulling gigabytes of unrelated files or exposing the device to insecure URLs.

#### Acceptance Criteria

1. **Given** a `DatabaseSource` record with `type: "github"`,  
   **When** `NormalizeAndValidate()` runs,  
   **Then** it normalizes `id` to lowercase trimmed ASCII (`^[a-z0-9][a-z0-9_-]{0,31}$`), rejects reserved IDs (`"user"`, `"root"`), requires non-empty `owner` and `repo` without `/`, strips leading/trailing `/` from `subpath`, rejects `..` in `subpath`, and defaults empty `branch` to `"main"`.

2. **Given** a `DatabaseSource` record with `type: "manifest"`,  
   **When** `NormalizeAndValidate()` runs,  
   **Then** it requires `url` to use `https://` with a non-empty host (allowing `http://` strictly for loopback `127.0.0.1`, `localhost`, or `::1` for local testing) and rejects `file://`, `ftp://`, or plaintext remote `http://` URLs with `ErrInvalidSource`.

3. **Given** a standard GitHub web URL such as `https://github.com/Lucaslhm/Flipper-IRDB/tree/main/ACs` or `https://github.com/Lucaslhm/Flipper-IRDB/tree/main/TVs`,  
   **When** `ParseGitHubURL(rawURL)` is called,  
   **Then** it extracts `owner="Lucaslhm"`, `repo="Flipper-IRDB"`, `branch="main"`, `subpath="ACs"` (or `"TVs"`), stripping optional `.git` suffixes.

4. **Given** `UpsertSource(path, src)` or `RemoveSource(path, id)` modifies `~/.config/ir-studio/sources.json`,  
   **When** persisted to disk,  
   **Then** the parent directory is created with `0700` permissions and `sources.json` is written atomically via temp file + `Chmod(0600)` + `os.Rename`.

5. **Given** `~/.config/ir-studio/sources.json` does not exist yet,  
   **When** `LoadSources(path)` is called,  
   **Then** it returns `nil, nil` (empty source list, no error).

#### Canonical `sources.json` Schema Example

```json
[
  {
    "id": "flipper-acs",
    "type": "github",
    "owner": "Lucaslhm",
    "repo": "Flipper-IRDB",
    "branch": "main",
    "subpath": "ACs",
    "enabled": true,
    "prune": false
  },
  {
    "id": "flipper-tvs",
    "type": "github",
    "owner": "Lucaslhm",
    "repo": "Flipper-IRDB",
    "branch": "main",
    "subpath": "TVs",
    "enabled": true,
    "prune": false
  },
  {
    "id": "custom-lab",
    "type": "manifest",
    "url": "https://example.org/ir-packs/manifest.json",
    "enabled": false,
    "prune": false
  }
]
```

#### Edge Cases

- **Duplicate ID on `UpsertSource`**: Replaces the existing entry with the same normalized `id` in place rather than creating duplicates.
- **Removing a non-existent ID**: Returns `ErrSourceNotFound` without rewriting `sources.json`.
- **Malformed JSON in `sources.json`**: `LoadSources` returns a wrapped parse error clearly naming the file path; never silently overwrites user config.
- **Invalid ID characters**: Spaces, uppercase (normalized first), dots (`foo.bar`), slashes (`a/b`), leading dash (`-bad`), or length `> 32` all fail with `ErrInvalidSource`.

#### Invariants

- **Strict Source ID Invariant**: `id` MUST match `^[a-z0-9][a-z0-9_-]{0,31}$` and MUST NOT be `"user"` or `"root"`, guaranteeing safe 1-to-1 mapping to `~/ir-studio/databases/<id>/`.
- **HTTPS-Only Remote Invariant**: Non-loopback remote URLs MUST use `https://`.
- **Atomic Config Permission Invariant**: `~/.config/ir-studio/sources.json` MUST always be written atomically with `0600` permissions.

#### Verification Plan

- **Unit Tests (`internal/dbsync/sources_test.go`)**:
  - Table-driven validation tests covering valid/invalid IDs, reserved IDs, GitHub owner/repo/subpath traversal, HTTPS vs HTTP vs loopback URLs, and `ParseGitHubURL` variants.
  - `UpsertSource` / `LoadSources` / `RemoveSource` round-trip tests verifying `0600` file mode and `errors.Is(err, ErrSourceNotFound)`.
- **Fuzz Test**: `FuzzParseGitHubURL(f *testing.F)` verifying zero panics on arbitrary malformed URLs.

---

### US-03.2: Incremental Delta Sync (`ETag`, `SHA`, 1 MiB Cap, `.ir` Validation, `--sync` & UI)

**As a** user syncing `Flipper-IRDB` packs over Wi-Fi on CardputerZero,  
**I want** incremental delta synchronization using HTTP `ETag` (`304 Not Modified`), per-file `SHA`/`SHA-256` tracking in `.sync-state.json`, a 1 MiB file cap, strict `.ir` parse validation, safe default append/update (`prune: false`), optional `prune: true`, CLI `ir-studio --sync`, and non-blocking `SET → Sync DB Sources`,  
**So that** repeat syncs finish in under a second, corrupt or oversized upstream files never enter my library, and the UI never freezes during downloads.

#### Acceptance Criteria

1. **Given** a synced `github` source (`~/ir-studio/databases/<id>/.sync-state.json` has a saved `etag`),  
   **When** `Syncer.SyncSource(ctx, src)` queries `GET https://api.github.com/repos/<owner>/<repo>/git/trees/<branch>?recursive=1` with `If-None-Match: <etag>`,  
   **Then** upon receiving `304 Not Modified`, `Syncer` performs **zero** blob downloads, updates `last_sync`, and reports `Status: "up-to-date" (0 downloaded, 0 pruned)`.

2. **Given** the upstream tree or manifest changed (`200 OK` with new `ETag`),  
   **When** `Syncer` diffs remote `.ir` entries against `SyncState.Files` and `SyncState.Ignored`,  
   **Then**:
   - Any filename present in `SyncState.Ignored` is **skipped** (never re-downloaded).
   - Any filename whose remote hash (Git blob `sha` for GitHub; `sha256` for manifest) matches `SyncState.Files[name]` **and** exists on disk is **skipped**.
   - Only new or hash-changed `.ir` files are downloaded.

3. **Given** a remote `.ir` file is downloaded during sync,  
   **When** `Syncer` reads the HTTP response body,  
   **Then**:
   - Body reading is capped strictly at `MaxIRFileBytes = 1 MiB` (`1 << 20` bytes via `io.LimitReader(body, MaxIRFileBytes+1)`); oversized payloads are rejected with `ErrFileTooLarge` and counted as `Skipped`.
   - For `manifest` sources providing a `sha256`, the computed SHA-256 of the downloaded bytes MUST match before acceptance.
   - The payload is validated with `irfile.Parse(bytes)` **before** writing to disk; invalid/HTML/404-page payloads are rejected and never written to `~/ir-studio/databases/<id>/`.
   - Valid `.ir` files are written atomically (`0644`) into `~/ir-studio/databases/<id>/<safeName>.ir`, and `.sync-state.json` is updated atomically.

4. **Given** an upstream `.ir` file was removed from the remote repository,  
   **When** `Syncer` completes a `200 OK` sync:
   - If `src.Prune == false` (default): the local `.ir` file is **preserved** (append/update only).
   - If `src.Prune == true`: the previously tracked local `.ir` file is deleted from `~/ir-studio/databases/<id>/` and removed from `SyncState.Files` (never touching `.sync-state.json` or files outside the pack dir).

5. **Given** the user runs `ir-studio --sync` from CLI (or SSH),  
   **When** executed,  
   **Then** it loads `~/.config/ir-studio/sources.json`, syncs all `enabled: true` sources sequentially with `signal.NotifyContext` cancellation support, prints a concise English per-source summary (`flipper-acs: +12 updated, 0 pruned, 2 skipped (ETag updated)`), and exits `0` if all enabled sources succeeded (or non-zero if any enabled source failed).

6. **Given** the user selects `Sync DB Sources` inside the `SET` tab on CardputerZero,  
   **When** activated,  
   **Then** syncing executes in a background goroutine (never blocking the `55 ms` UI render loop), streams live progress to the `SET` status line/toast (`Syncing flipper-acs...`), supports clean cancellation on app exit, and refreshes `library.Store` packs immediately upon completion.

#### `.sync-state.json` & Manifest Schemas

```json
// ~/ir-studio/databases/<id>/.sync-state.json
{
  "etag": "W/\"abc123def456\"",
  "files": {
    "Daikin_ARC480.ir": "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391",
    "LG_AKB749.ir": "8ab686eafeb1f44702738c8b0f24f2567c36da6d"
  },
  "ignored": {
    "Broken_Unknown.ir": true
  },
  "last_sync": "2026-11-21T15:30:00Z"
}
```

```json
// HTTPS JSON Manifest format (for type: "manifest")
{
  "version": 1,
  "files": [
    {
      "name": "Samsung_BN59.ir",
      "url": "https://example.org/ir-packs/Samsung_BN59.ir",
      "sha256": "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
    }
  ]
}
```

#### Edge Cases

- **GitHub API rate limit (`403` / `429`)**: Returns a clear English error (`GitHub rate limit exceeded; retry later or use manifest`) without corrupting existing local files or clearing `.sync-state.json`.
- **Nested subdirectories inside a GitHub `subpath` (e.g., `ACs/Daikin/ARC480.ir`)**: Flattened deterministically to a sandbox-safe filename (e.g., `Daikin__ARC480.ir`) so two brands with `Generic.ir` do not collide while keeping a flat pack directory.
- **Context cancellation mid-sync**: Stops downloading further files immediately and persists `.sync-state.json` for files already successfully written so the next sync resumes cleanly from where it stopped.
- **No enabled sources**: `ir-studio --sync` and `SET → Sync DB Sources` report `No enabled DB sources configured` and succeed cleanly.
- **Concurrent sync click in UI**: Guarded by an atomic/mutex `syncRunning` flag; pressing `Sync DB Sources` while already running shows `Sync already in progress` instead of spawning duplicate goroutines.

#### Invariants

- **Validated-Before-Write Invariant**: No downloaded byte stream may be written to `~/ir-studio/databases/<id>/` until it passes BOTH `<= 1 MiB` size check AND `irfile.Parse` validation.
- **Ignored-File Persistence Invariant**: Any filename recorded in `SyncState.Ignored` MUST NEVER be re-downloaded by future incremental syncs (unless the entire pack directory is deleted via `5 DIR` / `6 ALL`).
- **Default Non-Destructive Sync Invariant**: `prune` MUST default to `false`.
- **UI Non-Blocking Invariant**: Network I/O MUST NEVER execute on the UI render/input goroutine.

#### Verification Plan

- **Unit & Mock-HTTP Tests (`internal/dbsync/syncer_test.go`)**:
  - Use `net/http/httptest.NewServer` (loopback `127.0.0.1`) to test:
    - Initial GitHub Trees sync (`200 OK`) downloading valid `.ir` files and writing `.sync-state.json`.
    - Second sync sending `If-None-Match` and receiving `304 Not Modified` (assert 0 file GETs).
    - Delta update: 1 unchanged file skipped, 1 changed SHA re-downloaded, 1 `ignored` file skipped.
    - `prune: false` retains removed upstream file; `prune: true` deletes it.
    - Adversarial payloads: `> 1 MiB` file rejected (`ErrFileTooLarge`), HTML/garbage `.ir` rejected by `irfile.Parse`, manifest SHA-256 mismatch rejected, path-traversal filename (`../../evil.ir`) rejected, `429` rate limit preserves existing pack.
    - Mid-sync `context.Cancel()` saves partial progress cleanly.
- **Race & Coverage**: `go test -race -cover ./internal/dbsync/...` (`>= 90%` coverage).

---

### US-03.3: `[x]` Multi-Select Single & 2-Step Batch Deletion (`SAVED` & `DB:*` Packs)

**As a** user managing dozens of captured remotes in `SAVED` or pruning unwanted models from a downloaded `DB:*` pack in `PROBE`,  
**I want** `[x]` multi-select (`Space`/`1`/`D`) with single and 2-step batch deletion — and automatic recording of deleted pack files in `.sync-state.json` `ignored`,  
**So that** I can rapidly clean up unwanted `.ir` files using the 46-key keyboard without accidental deletion and without future syncs bringing deleted pack files back.

#### Acceptance Criteria

1. **Given** the user is in `SAVED` (`~/ir-studio/remotes/`) or viewing a downloaded `DB:<pack>` in `PROBE` (`~/ir-studio/databases/<pack>/`),  
   **When** the user presses `Space` (when not actively running `PROBE`) or `1`,  
   **Then** the highlighted item toggles its `[x]` selection mark (`[ ]` ↔ `[x]`), updates the selection counter in the status bar (e.g., `SEL:3`), and advances the cursor down by 1 row (clamped at the last row).

2. **Given** 0 items are marked with `[x]`,  
   **When** the user presses `D` (or the `DEL` softkey),  
   **Then** the UI enters **Step 1 confirmation** for the single highlighted remote (`DEL Daikin_ARC480.ir? [D/Enter=Yes, Esc=No]`) without deleting anything yet.

3. **Given** `N >= 1` items are marked with `[x]`,  
   **When** the user presses `D` (or the `DEL` softkey),  
   **Then** the UI enters **Step 1 confirmation** for all `N` marked items (`DEL 5 remotes? [D/Enter=Yes, Esc=No]`).

4. **Given** the UI is in **Step 1 confirmation**,  
   **When** the user presses `D` or `Enter` (Step 2 confirm),  
   **Then**:
   - **In `SAVED`**: all targeted `.ir` files are deleted from `~/ir-studio/remotes/`.
   - **In `PROBE` (`DB:<pack>`)**: all targeted `.ir` files are deleted from `~/ir-studio/databases/<pack>/` AND `dbsync.MarkIgnored(packDir, deletedNames)` records each deleted filename in `.sync-state.json` `ignored` (and removes it from `files`).
   - All `[x]` marks are cleared, the list reloads, cursor is clamped to valid bounds, and a summary toast displays (`Deleted 5 remotes`).

5. **Given** the UI is in **Step 1 confirmation**,  
   **When** the user presses `Esc`, `Backspace`, `F`, `X`, or switches tabs/packs,  
   **Then** the pending deletion is canceled immediately with zero files deleted.

#### Edge Cases

- **`Space` key disambiguation in `PROBE`**: When `ProbeSession.State == RUN`, `Space` triggers instant `STOP` (`US-02.3`) and does **not** toggle `[x]`. When `IDLE`/`PAUSE`/`DONE`, `Space` or `1` toggles `[x]`.
- **Empty list**: Pressing `Space`, `1`, or `D` on an empty `SAVED` or `DB:*` list is a safe no-op.
- **Partial filesystem failure during batch delete**: Continues deleting remaining marked files, records every successfully deleted pack file in `ignored`, and displays an amber toast reporting both deleted and failed counts (`Deleted 4, failed 1`).
- **Action-filtered `PROBE` view**: Deleting a candidate in a filtered view deletes that candidate's underlying `.ir` remote file from the pack (and records its `.ir` basename in `.sync-state.json` `ignored`), then refreshes the filtered candidate list.
- **Switching packs or tabs while items are marked**: Clears the `[x]` selection map so marks from `DB:ACs` can never accidentally delete files in `DB:TVs` or `SAVED`.

#### Invariants

- **2-Step Destructive Confirmation Invariant**: No file deletion in the UI may ever execute on a single keypress; explicit second confirmation (`D`/`Enter`) is mandatory.
- **Sync-Ignore Consistency Invariant**: Every file deleted from a synced `DB:<pack>` MUST be recorded in that pack's `.sync-state.json` `ignored` map before reloading the list.
- **Sandbox Deletion Invariant**: Batch deletion MUST validate every target path stays strictly inside `~/ir-studio/remotes/` (for `SAVED`) or `~/ir-studio/databases/<pack>/` (for `DB:*`).

#### Verification Plan

- **Unit Tests (`internal/dbsync/state_test.go`, `internal/library/store_test.go`, `internal/ui/view_saved_test.go`, `internal/ui/view_probe_test.go`)**:
  - Test `MarkIgnored` adds filenames to `Ignored`, removes them from `Files`, and persists atomically.
  - Test single delete (0 marked) vs batch delete (`N` marked) 2-step confirmation state machine, including cancellation via `Esc`, `F`/`X`, and tab switch.
  - Test `Space` during `PROBE RUN` stops probe instead of marking `[x]`.
  - Adversarial path-traversal rejection in batch delete.
- **Coverage & Race**: `go test -race -cover ./internal/dbsync/... ./internal/library/... ./internal/ui/...` (`>= 90%`).

---

### US-03.4: Dedicated `SET → Manage DB Sources` Sub-Screen

**As a** CardputerZero `ir-studio` user,  
**I want** a dedicated `SET → Manage DB Sources` sub-screen where I can toggle sources `ON`/`OFF`, delete only the downloaded pack folder (`5 DIR`), or delete both the source entry and its folder (`6 ALL`) with 2-step confirmation,  
**So that** I can manage SD-card storage and active `Flipper-IRDB` packs directly on the handheld without editing JSON files over SSH.

#### Acceptance Criteria

1. **Given** the user is on the `SET` tab,  
   **When** the user selects `Manage DB Sources` and presses `Enter`,  
   **Then** `ir-studio` opens the `Manage DB Sources` sub-screen listing all entries from `~/.config/ir-studio/sources.json` with:
   - Cursor highlight (`F`/`X` navigation),
   - Enabled badge (`ON` in emerald `#10e191` / `OFF` in amber `#f59e0b`),
   - Source ID (`flipper-acs`, `flipper-tvs`, etc.) and type tag (`GH` or `MAN`),
   - Local pack file count (`142 .ir` or `0 .ir`) on the right.

2. **Given** a source is highlighted on `Manage DB Sources`,  
   **When** the user presses `4 (TOG)`, `Space`, or `Enter`,  
   **Then** `src.Enabled` toggles (`true` ↔ `false`), persists atomically to `~/.config/ir-studio/sources.json` (`0600`), and updates the `ON`/`OFF` badge immediately.

3. **Given** a source is highlighted on `Manage DB Sources`,  
   **When** the user presses `5 (DIR)` (Step 1),  
   **Then** the UI displays a 2-step confirmation prompt (`DEL folder <id>/? [5/Enter=Yes, Esc=No]`);  
   **When** confirmed (Step 2 via `5` or `Enter`),  
   **Then** `dbsync.DeletePackDir(databasesDir, src.ID)` removes `~/ir-studio/databases/<id>/` (all `.ir` files + `.sync-state.json`), **keeps** the source entry in `sources.json`, refreshes `library.Store`, and shows toast `Deleted dir: <id>`.

4. **Given** a source is highlighted on `Manage DB Sources`,  
   **When** the user presses `6 (ALL)` (Step 1),  
   **Then** the UI displays a 2-step confirmation prompt (`DEL source+dir <id>? [6/Enter=Yes, Esc=No]`);  
   **When** confirmed (Step 2 via `6` or `Enter`),  
   **Then** `ir-studio` removes the source from `~/.config/ir-studio/sources.json` (`RemoveSource`) **and** removes `~/ir-studio/databases/<id>/` (`DeletePackDir`), refreshes `library.Store`, clamps the cursor, and shows toast `Removed source+dir: <id>`.

5. **Given** a 2-step confirmation (`DIR` or `ALL`) is active on `Manage DB Sources`,  
   **When** the user presses `Esc`, `Backspace`, `F`, `X`, or a conflicting action key,  
   **Then** confirmation is canceled immediately with zero filesystem or config changes.

6. **Given** the bottom navigation bar on `Manage DB Sources` (`y = 143..169`, centers `x = [49, 105, 161, 217, 273]`),  
   **When** rendered on the 320×170 LCD,  
   **Then** the 5 softkey tiles map cleanly to `4: TOG`, `5: DIR`, `6: ALL`, `7: SYNC`, `8: BACK` (and `Esc` also returns to `SET`).

#### Edge Cases

- **No sources configured (`sources.json` empty or missing)**: Renders a clean empty state (`No DB sources configured. Add via MCP or ~/.config/ir-studio/sources.json`) and makes `4`/`5`/`6` safe no-ops.
- **`5 (DIR)` pressed when pack folder was never synced / already deleted**: `os.RemoveAll` succeeds idempotently and shows `Folder already empty` / `Deleted dir: <id>` without error.
- **`5 (DIR)` or `6 (ALL)` while a background sync is running**: Refused cleanly with amber toast `Sync in progress; wait to delete` to prevent racing with active writes.
- **Deleting the currently active `PROBE` pack via `5 (DIR)` or `6 (ALL)`**: Resets `PROBE` active pack safely to the next available pack (or `USER`) so `PROBE` never holds a stale directory reference.
- **Invalid/tampered source ID in `sources.json`**: `DeletePackDir` re-validates `validateSourceID(id)` and checks `filepath.Rel(absBase, target)` before `os.RemoveAll`, guaranteeing it can never delete `~/ir-studio/databases/` itself or any parent directory.

#### Invariants

- **Sandboxed Recursive Delete Invariant**: `DeletePackDir(databasesDir, id)` MUST validate `id` against `^[a-z0-9][a-z0-9_-]{0,31}$` and verify `rel != "." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)` before calling `os.RemoveAll`.
- **2-Step Confirmation Invariant**: Both `5 (DIR)` and `6 (ALL)` MUST require explicit 2-step confirmation and auto-cancel on navigation (`F`/`X`) or `Esc`.
- **Sync Mutual-Exclusion Invariant**: Deleting a pack folder or source while `Syncer` is actively running MUST be rejected.

#### Verification Plan

- **Unit Tests (`internal/dbsync/state_test.go`, `internal/ui/view_sources_test.go`)**:
  - Adversarial `DeletePackDir` tests: `id = ""`, `"."`, `".."`, `"../remotes"`, `"user"`, `"root"`, `"a/b"` all return `ErrInvalidSource` and leave `databasesDir` untouched.
  - UI state machine tests for `Manage DB Sources`:
    - `4 (TOG)` / `Space` / `Enter` toggles `Enabled` and writes `sources.json` (`0600`).
    - `5 (DIR)` 2-step confirm deletes pack dir only and preserves `sources.json` entry.
    - `6 (ALL)` 2-step confirm deletes both `sources.json` entry and pack dir.
    - Navigation (`F`/`X`) or `Esc` during Step 1 cancels confirmation without side effects.
    - Active sync blocks `5 (DIR)` and `6 (ALL)`.
- **Coverage & Race**: `go test -race -cover ./internal/dbsync/... ./internal/ui/...` (`>= 90%`).
