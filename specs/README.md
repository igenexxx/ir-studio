# `ir-studio` — Spec-Driven Development (SDD) Index

Authoritative SDD specification suite for **IR Studio** (`ir-studio`), a pure-Go (`CGO_ENABLED=0`), zero-dependency 38 kHz Infrared studio for the **M5Stack CardputerZero** (`320×170` RGB565 `/dev/fb0`, 46-key TCA8418 keyboard, Linux `/dev/lirc*`, Flipper `.ir` format).

## Architecture Contracts (Read First)

- **[DOMAIN.md](../DOMAIN.md)** — Bounded Contexts, Ubiquitous Language, and Domain Invariants.
- **[DESIGN.md](../DESIGN.md)** — 4-Layer Go 1.27+ Package Architecture, `320×170` Design System, 6-Tab Layout (`RX`, `REMOTE`, `PROBE`, `SAVED`, `MACRO`, `SET`), and Code Quality Caps.

## Epics & User Stories

| Epic | Spec Document | Stories | Scope |
| --- | --- | --- | --- |
| **Epic 1** | [01-hardware-lirc-and-irfile.md](01-hardware-lirc-and-irfile.md) | `US-01.1` – `US-01.5` | Pure-Go `/dev/lirc*` ioctl TX/RX + `SIM`, Flipper `.ir` multi-button codec, `NEC`/`NECext`/`Samsung32`/`RC5`/`RC6`/`SIRC`/`Kaseikyo`/`RAW`, async TX/RX workers |
| **Epic 2** | [02-universal-probe-and-databases.md](02-universal-probe-and-databases.md) | `US-02.1` – `US-02.4` | Separated `UserLibrary` (`~/ir-studio/remotes/`) vs `DatabasePack` (`~/ir-studio/databases/<pack>/`), Action-Filtered Universal `PROBE` (`Power`/`Cool`/`Mute`), `0.3s–5.0s`, instant `STOP` + 12-slot rewind `>`/`*`, `TRY`, whole-remote `SAVE` |
| **Epic 3** | [03-dbsync-and-batch-management.md](03-dbsync-and-batch-management.md) | `US-03.1` – `US-03.4` | `sources.json` (`Flipper-IRDB` GitHub Trees + HTTPS JSON manifests), `ETag`/`SHA` `.sync-state.json` delta sync + `ignored`, `--sync`, `[x]` multi-select single/batch delete, `SET → Manage DB Sources` |
| **Epic 4** | [04-on-device-ir-ml.md](04-on-device-ir-ml.md) | `US-04.1` – `US-04.4` | Pure-Go 0-alloc `internal/irml`: 1D K-Means IR Pulse Denoiser (`ML-CLEAN`), `TV/Remote` vs `AC State-Frame` vs `Ambient Noise` Classifier + Smart Save Guard & `PURGE`, 8D IR Fingerprint k-NN 1-button `Flipper-IRDB` lookup, Smart Probe Ranking (`R`) |
| **Epic 5** | [05-ir-exclusive-features.md](05-ir-exclusive-features.md) | `US-05.1` – `US-05.4` | Virtual Remote Deck (`REMOTE` tab + Learn/Append/Rename/Delete buttons), AC State-Frame Bit-Diff Inspector, Multi-Device IR Macros (`MACRO` tab), Ambient IR Interference & Carrier (`36–56 kHz`) Monitor |
| **Epic 6** | [06-mcp-server-and-security.md](06-mcp-server-and-security.md) | `US-06.1` – `US-06.3` | Default-deny pure-Go JSON-RPC 2.0 stdio MCP server (`--mcp`), 21 typed tools (`confirm_ir: true`, `confirm: true`), HTTPS/1 MiB/sandbox/non-root security invariants |
| **Epic 7** | [07-ui-packaging-and-portal.md](07-ui-packaging-and-portal.md) | `US-07.1` – `US-07.3` | `320×170` LCD UX & 46-key ergonomics, `cmd/shots` visual QA, `.deb` APPLaunch packaging, Learning Portal guides + interactive IR waveform lab + glossary |
| **Epic 8** | [08-tvbgone-ac-and-sweeper.md](08-tvbgone-ac-and-sweeper.md) | `US-08.1` – `US-08.3` | Built-in offline `GEN:TV-B-Gone` virtual pack (top-100 TV/Projector/Soundbar Power codes), Smart AC State-Frame Controller (`Gree`/`Midea`/`Daikin`/`LG_AC`/`Samsung_AC`/`Mitsubishi` + `ir_send_ac_state`), Carrier Frequency Sweeper (`36/38/40/56 kHz` + `ir_sweep_carrier`) |

## Recommended Implementation Order

1. **Epic 1** (`internal/irfile`, `internal/lirc`, `internal/config`)
2. **Epic 2 & 3** (`internal/library`, `internal/dbsync`)
3. **Epic 4** (`internal/irml` — 0-alloc ML core + benchmarks)
4. **Epic 5 & 7** (`internal/ui` 6 tabs + `cmd/ir-studio` + `cmd/shots` + `.deb`)
5. **Epic 6 & 7** (`internal/mcpserver` 21 tools + Learning Portal sync)
6. **Epic 8** (`GEN:TV-B-Gone` virtual pack, parametric AC encoders + `AC` sub-view, Carrier Sweeper, 2 new MCP tools -> 23 total + Learning Portal sync)
