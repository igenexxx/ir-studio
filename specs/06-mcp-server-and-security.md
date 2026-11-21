# Epic 6: MCP Server & Security (`06-mcp-server-and-security.md`)

## Epic Overview

`ir-studio` MUST expose full programmatic parity with its on-device 320×170 UI through a pure-Go **Model Context Protocol (MCP)** server (`internal/mcpserver/`) communicating over **JSON-RPC 2.0 stdio** (`ir-studio --mcp`). External AI agents connect over SSH (`ssh <device> /usr/share/APPLaunch/bin/ir-studio --mcp`) without requiring Python, Node.js, CGO, or root privileges.

Security is enforced by design across every boundary:
- **Default-Deny Opt-In**: `mcp_enabled` defaults to `false` in `~/.config/ir-studio/config.json` (`0600`).
- **Explicit Confirmation Gates**: All IR transmit operations require `confirm_ir: true`; all destructive file/source deletions require `confirm: true`; `ir_purge_noise` defaults to `dry_run: true`.
- **Hardware & Storage Isolation**: All `/dev/lirc*` operations are serialized by a dedicated hardware mutex (`lircMu`), writes are serialized by `writeMu`, external read-only database packs are never mutated by user-library delete/save tools, and every file path is strictly sandboxed.

### Package Decomposition (Anti-God-File Architecture)

```text
internal/mcpserver/
├── server.go          # JSON-RPC 2.0 stdio transport, dispatch loop, Options, NewServer
├── backend.go         # Consumer-owned IRBackend interface + mutex-protected LIRC/SIM adapter
├── schema.go          # SSOT tool definitions & typed JSON Schemas for all 21 tools
├── tools_status.go    # ir_get_status, ir_learn_signal
├── tools_tx.go        # ir_transmit_button, ir_run_macro, ir_list_macros
├── tools_ml.go        # ir_analyze_pulses, ir_classify_signal, ir_match_fingerprint, ir_rank_probe_pack, ir_purge_noise
├── tools_library.go   # ir_list_remotes, ir_read_remote, ir_save_remote, ir_delete_remote, ir_list_databases, ir_promote_remote
└── tools_sources.go   # ir_list_sources, ir_add_source, ir_remove_source, ir_sync_sources, ir_delete_pack_files
```

Every non-test source file MUST stay `<= 350` lines (hard refactor cap `400` lines), and every handler function MUST stay `<= 40` lines by delegating domain logic to `internal/lirc`, `internal/irfile`, `internal/irml`, `internal/library`, and `internal/dbsync`.

---

## US-06.1: Pure-Go JSON-RPC 2.0 Stdio MCP Server & Default-Deny Opt-In Gate

**As a** CardputerZero user and AI-agent operator,  
**I want** `ir-studio --mcp` to run a zero-dependency JSON-RPC 2.0 stdio MCP server that refuses to start unless I explicitly enable MCP in Settings,  
**So that** external agents can automate IR workflows over SSH safely without exposing hardware control by default.

### Acceptance Criteria (Given / When / Then)

1. **Default-Deny Refusal**:
   - **Given** a fresh install with no `~/.config/ir-studio/config.json` (or a config where `"mcp_enabled": false`),
   - **When** a user or SSH client invokes `ir-studio --mcp`,
   - **Then** the process MUST exit with a non-zero exit code (`1`), write zero JSON-RPC frames to `stdout`, and write a clear English instruction to `stderr` explaining how to enable `MCP Server` in the `SET` tab (or set `"mcp_enabled": true` in `~/.config/ir-studio/config.json`).
2. **Opt-In Persistence (`0600`)**:
   - **Given** the user toggles `MCP Server` to `ENABLED` in the `SET` tab,
   - **When** settings are saved,
   - **Then** `~/.config/ir-studio/config.json` MUST be written atomically (temp file + `fsync` + `rename`) with file mode `0600` inside a `0700` parent directory (`~/.config/ir-studio/`), persisting `"mcp_enabled": true`.
3. **JSON-RPC 2.0 Handshake & Lifecycle**:
   - **Given** `"mcp_enabled": true` in `~/.config/ir-studio/config.json`,
   - **When** an MCP client connects via `ir-studio --mcp` and sends `initialize` (`protocolVersion: "2024-11-05"`), `notifications/initialized`, `ping`, `tools/list`, and `tools/call`,
   - **Then** the server responds with valid newline-delimited JSON-RPC 2.0 responses on `stdout` (`serverInfo.name = "ir-studio"`, `capabilities.tools = {}`), ignores notifications without emitting responses, and exits cleanly (`0`) on `stdin` EOF or `SIGINT`/`SIGTERM`.
4. **SIM Fallback Over MCP**:
   - **Given** `/dev/lirc0` is absent or `--sim` is passed with `--mcp`,
   - **When** `ir_get_status` is called,
   - **Then** the server starts cleanly in deterministic simulator mode (`hardware_mode: "SIM"`) so agents and CI pipelines can test every tool without physical IR hardware.
5. **Standard JSON-RPC Error Handling**:
   - **Given** malformed JSON, an unknown RPC method, or invalid tool arguments,
   - **When** the request is processed,
   - **Then** the server returns `-32700` (Parse error), `-32601` (Method not found), or `-32602` (Invalid params) for protocol errors, and structured `isError: true` tool results for domain/validation failures without crashing the stdio loop.

### Edge Cases
- **Corrupted `config.json`**: Treat as `mcp_enabled: false` (fail closed), log a clear English parse error to `stderr`, and exit non-zero.
- **Oversized JSON-RPC line**: Enforce a `bufio.Scanner` buffer cap (e.g., `2 MiB`) to prevent OOM DoS from malformed client input; return `-32700` and continue or cleanly terminate.
- **Unexpected `stdout` pollution**: Zero `fmt.Print*` or `log` writes may go to `stdout` during `--mcp`; all diagnostic logs MUST target `stderr`.
- **Concurrent SSH `--mcp` invocation while UI is open**: If `/dev/lirc0` is busy (`EBUSY`), return a clear structured tool error (`"LIRC device busy; close interactive UI or retry"`) rather than panicking.

### Invariants
- **Default-Deny Opt-In Invariant**: `config.DefaultSettings().MCPEnabled` MUST be `false`. No CLI flag (e.g., `--force-mcp`) may bypass the persisted `mcp_enabled` gate.
- **Zero-Runtime Invariant**: Binary MUST compile with `CGO_ENABLED=0` and zero third-party MCP SDK or scripting-runtime dependencies.
- **Local-Only Invariant**: Stdio is the sole default transport (invoked over SSH); if an optional HTTP/SSE listener is ever added, it MUST bind strictly to `127.0.0.1` and still honor `mcp_enabled == true`.

### Verification Plan
- Table-driven unit tests in `internal/config/config_test.go` verifying default `MCPEnabled == false`, `0600` file permissions, atomic write, and fail-closed behavior on corrupt JSON.
- Unit tests in `internal/runner/runner_test.go` verifying `--mcp` exits non-zero with exact English opt-in guidance when `mcp_enabled: false`.
- End-to-end piped `io.Reader`/`io.Writer` protocol tests in `internal/mcpserver/server_test.go` covering `initialize`, `notifications/initialized`, `ping`, `tools/list`, malformed JSON (`-32700`), unknown method (`-32601`), unknown tool (`-32602`), and clean EOF/context-cancel shutdown under `-race`.

---

## US-06.2: Full MCP Tool Catalog, Typed Schemas & Concurrency Serialization

**As an** AI agent orchestrating IR capture, analysis, transmission, and database curation on CardputerZero,  
**I want** a complete, strongly typed catalog of 21 MCP tools covering every `ir-studio` capability with strict mutex serialization and safety guards,  
**So that** I can reliably inspect hardware, learn remotes, run ML analysis, transmit verified IR frames, and manage `.ir` libraries and Flipper-IRDB sources.

### Complete MCP Tool Catalog (21 Tools)

All tool schemas are defined in a single SSOT file (`internal/mcpserver/schema.go`). Every tool returns structured JSON serialized inside MCP `content: [{type: "text", text: "<json>"}]`.

#### Group 1: Status & Hardware (`tools_status.go`)

| Tool Name | Required / Key Parameters | Safety Gate / Mutex | Structured Output |
| :--- | :--- | :--- | :--- |
| `ir_get_status` | *(none)* | Read-only (`lircMu`) | `hardware_mode` (`LIRC`/`SIM`), `device_path`, `tx_carrier_hz`, `tx_duty_pct`, `ambient_noise_pct`, `ambient_status`, `user_remotes_count`, `database_packs_count`, `macros_count`, `mcp_enabled`, `probe_interval_ms` |
| `ir_learn_signal` | `timeout_ms` (`500..15000`, default `5000`), `carrier_hz` (optional, default `38000`) | `lircMu` (exclusive RX window; `context.Context` timeout) | Captured `button` object (`type`: `parsed`/`raw`, `protocol`, `address`, `command`, `frequency`, `duty_cycle`, `data[]`), `ambient_noise_pct`, `ml_classification`, `fingerprint_match` (if any) |

#### Group 2: Transmit & Macros (`tools_tx.go`)

| Tool Name | Required / Key Parameters | Safety Gate / Mutex | Structured Output |
| :--- | :--- | :--- | :--- |
| `ir_transmit_button` | `confirm_ir: true` **(required)**, `remote_name` (string), `button_name` (string), `pack` (optional external pack ID; omit for User Library), `repeat` (`1..5`, default `1`), `carrier_hz` (optional override `36000..56000`), `duty_pct` (optional `25..50`) | **`confirm_ir: true` required**; `lircMu` (exclusive TX, restores carrier/duty on exit) | `transmitted: true`, `remote_name`, `button_name`, `protocol`, `carrier_hz`, `duty_pct`, `duration_ms`, `mode` (`LIRC`/`SIM`) |
| `ir_run_macro` | `confirm_ir: true` **(required)**, `macro_name` (string) | **`confirm_ir: true` required**; `lircMu` (per-step TX with `context.Context` cancel between delays; max total runtime `30 s`) | `macro_name`, `steps_executed`, `steps_total`, `elapsed_ms`, `step_results[]` |
| `ir_list_macros` | *(none)* | Read-only | `macros[]` (`name`, `description`, `step_count`, `total_delay_ms`, `steps[]`) |

#### Group 3: On-Device ML Analysis (`tools_ml.go`)

| Tool Name | Required / Key Parameters | Safety Gate / Mutex | Structured Output |
| :--- | :--- | :--- | :--- |
| `ir_analyze_pulses` | `pulses` (`[]int` alternating `+mark`/`-space` µs) **or** (`remote_name`, `button_name`, optional `pack`) | Pure CPU (0-alloc hot path) | `clusters` (`short_us`, `long_us`, `sync_us`, `jitter_ratio`, `glitches_removed`), `denoised_pulses[]`, `decoded_protocol` (if decoded) |
| `ir_classify_signal` | `pulses` (`[]int`) **or** (`remote_name`, `button_name`, optional `pack`) | Pure CPU | `label` (`NEC`/`RC5`/`RC6`/`SAMSUNG`/`SIRC`/`AC_RAW`/`UNKNOWN`/`NOISE`), `confidence` (`0..1`), `probabilities{}`, `features[12]` |
| `ir_match_fingerprint` | `pulses` (`[]int`) **or** (`remote_name`, `button_name`), `carrier_hz` (default `38000`), `top_k` (`1..10`, default `5`), `pack` (optional pack filter) | Pure CPU + read-only library scan | `embedding[8]`, `matches[]` (`remote_name`, `pack`, `button_name`, `similarity_pct`, `protocol`) sorted descending |
| `ir_rank_probe_pack` | `pack` (string, required), `action_filter` (optional, e.g. `"Power"`), `reference_pulses` (`[]int`) **or** (`reference_remote`, `reference_button`) | Pure CPU + read-only pack scan | `pack`, `action_filter`, `ranked_candidates[]` (`remote_name`, `button_name`, `similarity_pct`, `rank`) |
| `ir_purge_noise` | `dry_run` (bool, **default `true`**), `confirm` (bool, **required `true` when `dry_run: false`**), `min_confidence` (`0.5..0.99`, default `0.75`) | **`dry_run: true` default**; `confirm: true` required when `dry_run: false`; `writeMu`; **User Library ONLY** | `dry_run`, `scanned_count`, `flagged_remotes[]` (`name`, `reason`, `noise_confidence`), `deleted_count` |

#### Group 4: Library & Database Sources (`tools_library.go`, `tools_sources.go`)

| Tool Name | Required / Key Parameters | Safety Gate / Mutex | Structured Output |
| :--- | :--- | :--- | :--- |
| `ir_list_remotes` | `filter` (optional substring) | Read-only (User Library `~/ir-studio/remotes/`) | `remotes[]` (`name`, `button_count`, `protocols[]`, `size_bytes`, `modified_at`) |
| `ir_read_remote` | `name` (string, required), `pack` (optional external pack ID) | Read-only; path-sandboxed; 1 MiB cap | `name`, `pack`, `filetype`, `version`, `buttons[]` (`name`, `type`, `protocol`, `address`, `command`, `frequency`, `duty_cycle`, `data[]`) |
| `ir_save_remote` | `name` (string, required, `.ir` auto-appended), `buttons[]` (non-empty array), `overwrite` (bool, default `false`) | `writeMu`; path-sandboxed to `~/ir-studio/remotes/`; 1 MiB cap; validates `.ir` schema | `saved: true`, `name`, `path`, `button_count`, `size_bytes` |
| `ir_delete_remote` | `name` (string, required), `confirm: true` **(required)** | **`confirm: true` required**; `writeMu`; **User Library ONLY** | `deleted: true`, `name` |
| `ir_list_databases` | *(none)* | Read-only (`~/ir-studio/databases/<pack>/`) | `packs[]` (`id`, `remote_count`, `last_synced`, `source_url`) |
| `ir_promote_remote` | `pack` (string, required), `remote_name` (string, required), `target_name` (optional) | `writeMu`; copies full `.ir` remote from read-only pack into `~/ir-studio/remotes/` with collision-safe naming | `promoted: true`, `source_pack`, `source_remote`, `saved_as`, `button_count` |
| `ir_list_sources` | *(none)* | Read-only (`~/.config/ir-studio/sources.json`) | `sources[]` (`id`, `kind`, `url`, `branch`, `subpath`, `enabled`, `prune`, `last_synced`) |
| `ir_add_source` | `id` (slug), `kind` (`"github_tree"` / `"json_manifest"`), `url` (HTTPS-only), `branch` (optional), `subpath` (optional), `prune` (bool, default `false`) | `writeMu`; HTTPS-only validation; slug validation | `added: true`, `source` |
| `ir_remove_source` | `ids` (`[]string`, non-empty batch list), `delete_files` (bool, default `false`), `confirm: true` **(required)** | **`confirm: true` required**; `writeMu`; path-sandboxed pack IDs | `removed_ids[]`, `not_found_ids[]`, `files_deleted_packs[]` |
| `ir_sync_sources` | `ids` (optional `[]string`; omit to sync all enabled sources) | `writeMu`; HTTPS-only; ETag + SHA delta sync; 1 MiB per-file cap; `.ir` parse validation | `results[]` (`id`, `status`, `added`, `updated`, `skipped`, `pruned`, `errors[]`) |
| `ir_delete_pack_files` | `pack` (string, required), `files` (`[]string`, non-empty relative `.ir` filenames), `confirm: true` **(required)** | **`confirm: true` required**; `writeMu`; strictly sandboxed inside `~/ir-studio/databases/<pack>/` | `pack`, `deleted[]`, `not_found[]` |

### Acceptance Criteria (Given / When / Then)

1. **Tool Discovery (`tools/list`)**:
   - **Given** an initialized MCP session,
   - **When** the client calls `tools/list`,
   - **Then** all 21 tools above are returned with complete JSON Schema `type`, `properties`, `required`, `minimum`/`maximum`, and `enum` constraints sourced from `schema.go`.
2. **IR Transmit Confirmation Gate**:
   - **Given** a client calls `ir_transmit_button` or `ir_run_macro` with `confirm_ir` omitted or `false`,
   - **When** the handler validates parameters,
   - **Then** the hardware backend MUST NOT be invoked, and the server returns `isError: true` with a clear message stating `"confirm_ir: true is required to transmit IR signals"`.
3. **Destructive Delete & Purge Guards**:
   - **Given** a client calls `ir_delete_remote`, `ir_remove_source`, or `ir_delete_pack_files` without `confirm: true`, or calls `ir_purge_noise` without `dry_run`,
   - **When** executed,
   - **Then** destructive tools refuse with `isError: true` and mutate zero files, while `ir_purge_noise` defaults to `dry_run: true` and only reports candidate noise files without deleting them.
4. **Hardware Mutex Serialization**:
   - **Given** concurrent `ir_learn_signal` and `ir_transmit_button` calls (or two rapid TX calls),
   - **When** processed,
   - **Then** `lircMu` serializes all hardware calls so `/dev/lirc*` TX and RX ioctls never interleave, and carrier/duty cycle are restored after every TX.
5. **External-DB Read-Only Isolation**:
   - **Given** a file exists in `~/ir-studio/databases/acs/Daikin.ir`,
   - **When** a client calls `ir_delete_remote` or `ir_purge_noise` (`dry_run: false, confirm: true`),
   - **Then** `~/ir-studio/databases/acs/Daikin.ir` MUST NEVER be modified or deleted; only `ir_promote_remote` copies it into `~/ir-studio/remotes/`, and only explicit `ir_delete_pack_files` (with `confirm: true`) or `ir_remove_source` (`delete_files: true, confirm: true`) may remove synced pack files.

### Edge Cases
- **Batch `ir_remove_source` with partial misses**: If `ids: ["acs", "nonexistent"]` is passed with `confirm: true`, remove `"acs"`, record `"nonexistent"` in `not_found_ids`, and return partial success cleanly rather than aborting half-way.
- **Macro referencing a deleted remote or button**: `ir_run_macro` pre-validates all steps before transmitting step 1; if any remote/button is missing, return `isError: true` with zero IR frames emitted.
- **Collision on `ir_promote_remote`**: If `Daikin_ARC480.ir` already exists in `~/ir-studio/remotes/`, auto-suffix (`Daikin_ARC480_2.ir`) instead of silently overwriting user edits.
- **Empty `pulses` or odd-length `RAW` pulse slice**: `ir_analyze_pulses` and `ir_classify_signal` return `isError: true` with descriptive validation errors rather than panicking or dividing by zero.

### Invariants
- **Schema-Handler Parity Invariant**: Every tool name registered in `schema.go` MUST have a matching dispatch handler, and every dispatch handler MUST exist in `schema.go` (verified by unit test).
- **TX Safety Invariant**: Every IR-emitting tool MUST require `confirm_ir: true`, cap repeats (`<= 5`) and macro total duration (`<= 30 s`), and honor `context.Context` cancellation.
- **Mutex Lock-Order Invariant**: When both locks are needed, acquire `writeMu` before `lircMu` (never vice versa) to prevent deadlocks.

### Verification Plan
- Table-driven unit tests per domain file (`tools_status_test.go`, `tools_tx_test.go`, `tools_ml_test.go`, `tools_library_test.go`, `tools_sources_test.go`) using hand-written stub `IRBackend` and `t.TempDir()`.
- Adversarial negative tests for every guard (`confirm_ir: false/missing`, `confirm: false/missing`, `dry_run` default, out-of-range carrier/repeat/timeout, empty arrays, nonexistent macros/packs).
- Schema-to-handler bijection test asserting exact 21-tool parity.
- Concurrency stress test under `go test -race` verifying `lircMu` and `writeMu` prevent data races and interleaving.

---

## US-06.3: Security Invariants & Least-Privilege Execution

**As a** security-conscious CardputerZero owner,  
**I want** strict network, filesystem, size, and OS privilege boundaries enforced across `ir-studio` and its MCP server,  
**So that** untrusted `.ir` packs, remote URLs, or buggy MCP clients cannot compromise my device or escape the app's storage sandbox.

### Acceptance Criteria (Given / When / Then)

1. **HTTPS-Only Remote Sources**:
   - **Given** a user or MCP agent calls `ir_add_source` or `ir_sync_sources` with `http://`, `file://`, `ftp://`, or `javascript:` URLs,
   - **When** the URL is validated,
   - **Then** the request MUST be rejected with a typed validation error (`ErrInsecureScheme`); only `https://` URLs with a valid host are accepted (HTTP redirects to `http://` MUST also be blocked by a custom `http.Client.CheckRedirect`).
2. **1 MiB File Size Cap**:
   - **Given** an `.ir` file from a remote source, local disk, or `ir_save_remote` payload exceeds `1048576` bytes (`1 MiB`),
   - **When** downloaded, read, or saved,
   - **Then** the reader MUST enforce `io.LimitReader(r, MaxIRFileBytes+1)` and reject the file with `ErrFileTooLarge` before allocating unbounded memory or writing to disk.
3. **Path-Traversal & Symlink Escape Rejection**:
   - **Given** any tool receives a `name`, `pack`, `id`, or `files[]` entry containing `..`, leading/trailing `/`, `\`, NUL bytes (`\x00`), control characters, or a symlink pointing outside the owning root directory,
   - **When** path resolution runs,
   - **Then** the request MUST be rejected with `ErrInvalidPath` before any `os.Open`, `os.WriteFile`, or `os.Remove` syscall occurs.
4. **Non-Root Execution via Linux Device Groups**:
   - **Given** `ir-studio` is installed on CardputerZero via its `.deb` package,
   - **When** executed by the standard unprivileged user (either from APPLaunch UI or over SSH via `--mcp`),
   - **Then** the binary runs completely non-root (UID `!= 0`, no `sudo`, no setuid bit), relying solely on membership in Linux groups `lirc` (`/dev/lirc*`), `video` (`/dev/fb0`), and `input` (`/dev/input/event*`).
5. **Sensitive Config File Permissions**:
   - **Given** `ir-studio` creates or updates `~/.config/ir-studio/config.json`, `sources.json`, or `macros.json`,
   - **When** written to disk,
   - **Then** the config directory MUST have permissions `0700` and all JSON files MUST have permissions `0600`.

### Edge Cases
- **Encoded / Double-Dot Path Tricks**: Reject `%2e%2e`, `..` in any path segment, Windows-style `..\`, absolute paths (`/etc/passwd`), and whitespace-only names.
- **Symlink Inside `~/ir-studio/remotes/`**: Even if a filename has no slashes (`evil.ir`), resolve with `filepath.EvalSymlinks` (or `os.Lstat` rejecting symlinks) so `ir_read_remote` / `ir_delete_remote` cannot read or unlink targets outside `~/ir-studio/remotes/`.
- **Redirect Downgrade Attack**: A remote `https://` source returning `302 Location: http://...` MUST be blocked by `CheckRedirect`.
- **Zip-Bomb / Chunked Stream without `Content-Length`**: `io.LimitReader` caps bytes read regardless of missing or spoofed `Content-Length` headers.
- **Missing Group Membership**: If `/dev/lirc0` returns `EACCES`, report an actionable English diagnostic (`"Permission denied on /dev/lirc0; ensure user is in the 'lirc' group"`) and allow clean fallback to `SIM` mode where appropriate.

### Invariants
- **Strict Sandbox Invariant**: Every filesystem path operation MUST pass through a single canonical `ValidateSafeName` / `ResolveSandboxedPath(root, rel)` helper shared across `internal/library`, `internal/dbsync`, and `internal/mcpserver` (SSOT).
- **Memory-Bound Invariant**: `MaxIRFileBytes = 1 << 20` (`1 MiB`) is a single exported constant enforced on HTTP download streams, disk reads, and MCP `ir_save_remote` writes.
- **Zero-Root Invariant**: Neither `ir-studio`, `scripts/build_deb.sh` desktop launchers, nor MCP instructions may require `root` or `sudo` at runtime.

### Verification Plan
- Table-driven adversarial security test suite (`internal/library/sandbox_test.go`, `internal/dbsync/security_test.go`, `internal/mcpserver/security_test.go`) testing:
  - 15+ path traversal payloads (`../`, `a/../../b`, `/etc/passwd`, `a\b`, `foo\x00.ir`, symlink escape).
  - Scheme rejection (`http://`, `file:///etc/passwd`, `ftp://`, `https://` redirecting to `http://`).
  - Boundary size tests at `1048575`, `1048576` (pass), and `1048577` bytes (fail with `errors.Is(err, ErrFileTooLarge)`).
  - Go fuzz test `FuzzValidateSafeName(f *testing.F)` ensuring no panics and zero paths escaping `t.TempDir()`.
  - File permission assertions (`info.Mode().Perm() == 0600`) for all config/source/macro files.
