# Epic 1 Specification: Hardware LIRC Transceiver, Flipper `.ir` Codec, Protocol Engines & Async Workers

- **Document ID**: `SPEC-01`
- **Bounded Context**: `ir-core` (`internal/lirc`, `internal/irfile`)
- **Status**: Draft Contract (Authoritative for Epic 1 Implementation & Verification)
- **Target Platform**: M5Stack CardputerZero (ARM64 Linux, `CGO_ENABLED=0`, Go 1.27+, `/dev/lirc*`, 38 kHz IR emitter & receiver)

---

## 1. Epic Goals & Non-Goals

### Goals
1. Provide a **pure-Go, zero-CGO** Linux `/dev/lirc*` transceiver adapter with automatic device discovery, carrier/duty configuration, `context.Context` cancellation, and a deterministic `SIM` fallback for desktop development and CI.
2. Implement a complete, lossless **Flipper Zero `.ir` v1** multi-button file parser and serializer supporting both `type: parsed` and `type: raw` signals.
3. Implement an extensible, Open/Closed **IR Protocol Codec Registry** supporting **`NEC`**, **`NECext`**, **`Samsung32`**, **`RC5`**, **`RC6`**, **`SIRC`**, **`Kaseikyo`**, and **`RAW`** with strict encode/decode round-trip and fuzz invariants.
4. Provide asynchronous, non-blocking **`TXWorker`** and **`RXWorker`** goroutines synchronized by **`TransceiverGate`** so the `320×170` UI loop is never blocked and TX/RX never collide.

### Non-Goals
- External `lircd` daemon dependency or shelling out to `ir-ctl` / `mode2`.
- On-device ML clustering/classification (covered in Epic 2: `internal/irml`).
- Library browsing, `ProbeSession`, `Macro`, DB sync, MCP server, and UI views (covered in Epics 2–5).

---

## 2. User Stories & Acceptance Criteria

---

### US-01.1: Pure-Go Linux `/dev/lirc*` Hardware Adapter & `SIM` Fallback

> **As a** CardputerZero user and developer,
> **I want** `ir-studio` to auto-discover `/dev/lirc*` using pure-Go Linux `ioctl` calls and fall back cleanly to `SIM` mode when no LIRC hardware is present,
> **So that** I can transmit and receive IR natively on ARM64 Linux without CGO or root privileges, and develop/test deterministically on any host.

#### Technical Contract (`internal/lirc`)
- **Linux LIRC `ioctl` Constants (SSOT in `ioctl.go`)**:
  - `LIRC_GET_FEATURES = 0x80046900` (`_IOR('i', 0x00, uint32)`)
  - `LIRC_SET_REC_MODE = 0x40046912` (`_IOW('i', 0x12, uint32)`)
  - `LIRC_SET_SEND_MODE = 0x40046911` (`_IOW('i', 0x11, uint32)`)
  - `LIRC_SET_SEND_CARRIER = 0x40046913` (`_IOW('i', 0x13, uint32)`)
  - `LIRC_SET_SEND_DUTY_CYCLE = 0x40046915` (`_IOW('i', 0x15, uint32)`)
  - `LIRC_SET_REC_TIMEOUT = 0x40046918` (`_IOW('i', 0x18, uint32)`)
  - `LIRC_SET_REC_TIMEOUT_REPORTS = 0x40046919` (`_IOW('i', 0x19, uint32)`)
  - Mode & Feature bits: `LIRC_MODE_MODE2 = 0x00000004`, `LIRC_MODE_PULSE = 0x00000002`, `LIRC_CAN_SEND_PULSE = 0x00000002`, `LIRC_CAN_REC_MODE2 = 0x00040000`.
  - `mode2` packet decoding: `LIRC_MODE2_MASK = 0xFF000000`, `LIRC_VALUE_MASK = 0x00FFFFFF`, `LIRC_MODE2_SPACE = 0x00000000`, `LIRC_MODE2_PULSE = 0x01000000`, `LIRC_MODE2_TIMEOUT = 0x03000000`.
- **Carrier & Duty Bounds**:
  - `MinCarrierHz = 36000`, `MaxCarrierHz = 56000`, `DefaultCarrierHz = 38000`.
  - `MinDutyPct = 1`, `MaxDutyPct = 99`, `DefaultDutyPct = 33`.
- **Dependency-Injected `Transport` Interface**:
  ```go
  type Transport interface {
      Features() (uint32, error)
      SetSendMode(mode uint32) error
      SetRecMode(mode uint32) error
      SetSendCarrier(freqHz uint32) error
      SetSendDutyCycle(dutyPct uint32) error
      SetRecTimeout(timeoutUs uint32, enableReports bool) error
      WritePulses(ctx context.Context, pulses []uint32) error
      ReadMode2(ctx context.Context, dst []uint32) (int, error)
      DevicePath() string
      IsSim() bool
      Close() error
  }
  ```
- **Discovery Order**:
  1. If `--sim` flag or `Settings.ForceSim == true` → instantiate `SimTransport`.
  2. If explicit device path configured (e.g., `/dev/lirc1`) → probe only that path; return typed error (`ErrDeviceOpen`) if unavailable unless fallback is requested.
  3. Otherwise scan `/dev/lirc0`..`/dev/lirc7`, probing `LIRC_GET_FEATURES` to select TX/RX-capable devices (supporting split TX-only `/dev/lirc0` + RX-only `/dev/lirc1` pairs if present on hardware). If none found or non-Linux OS → fall back to `SimTransport` with `IsSim() == true`.

#### Acceptance Criteria (Given / When / Then)
1. **Given** a Linux device with `/dev/lirc0` supporting `LIRC_CAN_SEND_PULSE | LIRC_CAN_REC_MODE2`,
   **When** `Discover` is called with default options,
   **Then** it opens `/dev/lirc0` without root privileges, configures `LIRC_MODE_PULSE` for TX and `LIRC_MODE_MODE2` + `LIRC_SET_REC_TIMEOUT_REPORTS(1)` for RX, and reports `IsSim() == false`.
2. **Given** a host with no `/dev/lirc*` devices (or `--sim` enabled),
   **When** `Discover` is called,
   **Then** it returns a `SimTransport` (`IsSim() == true`, `DevicePath() == "SIM"`) that loops back or serves deterministic synthetic IR frames without returning an error.
3. **Given** a `TX` call with `CarrierSpec{FreqHz: 40000, DutyPct: 50}`,
   **When** `WritePulses` completes, fails, or is cancelled via `ctx`,
   **Then** the adapter MUST invoke `SetSendCarrier(38000)` and `SetSendDutyCycle(33)` in a deferred restoration step before returning.
4. **Given** a `TX` call with `FreqHz: 20000` (out of `[36000, 56000]`) or `DutyPct: 0` / `100`,
   **When** `Send` is invoked,
   **Then** it rejects the request with `ErrInvalidCarrier` or `ErrInvalidDutyCycle` before issuing any `ioctl`.
5. **Given** a `TX` pulse slice with even length (ending in a space) or containing a `0` duration,
   **When** validated before `WritePulses`,
   **Then** trailing space is stripped (or rejected with `ErrInvalidPulseTrain` if empty/zero-valued) so the kernel `LIRC_MODE_PULSE` invariant (`odd length`, all values `> 0`) is never violated.

#### Edge Cases & Adversarial Test Plan
- **Edge Cases**: Split TX/RX devices (`/dev/lirc0` TX-only, `/dev/lirc1` RX-only); `EACCES` permission denied vs `ENOENT` absent; `EINTR` during `poll`/`read`/`write`; `ctx` already cancelled before `WritePulses`; `SetSendDutyCycle` returning `ENOTTY` on hardware drivers with fixed duty cycle (log/record capability flag and do not fail TX); carrier restoration error when primary error already occurred (preserve primary error via `errors.Join` or structured wrapping).
- **Tests**: Hand-written mock `Transport` verifying exact ioctl call sequence, carrier/duty restoration on happy path + error + cancel, odd-length pulse enforcement, `SimTransport` loopback and cancellation under `-race`.

---

### US-01.2: Flipper Zero `.ir` Multi-Button File Codec

> **As a** user importing remotes from Flipper Zero / Flipper-IRDB or saving captured buttons,
> **I want** `internal/irfile` to parse and write Flipper `.ir` v1 files with multiple `parsed` and `raw` signals,
> **So that** `ir-studio` is 100% interoperable with the Flipper Zero IR ecosystem.

#### Technical Contract (`internal/irfile`)
- **Canonical Flipper `.ir` v1 Wire Format**:
  ```text
  Filetype: IR signals file
  Version: 1
  #
  name: Power
  type: parsed
  protocol: NEC
  address: 04 00 00 00
  command: 08 00 00 00
  #
  name: AC_Cool_22
  type: raw
  frequency: 38000
  duty_cycle: 0.330000
  data: 9000 4500 560 560 560 1690 560
  ```
- **Core Domain Types**:
  - `SignalKind`: `SignalParsed` (`"parsed"`), `SignalRaw` (`"raw"`).
  - `IRSignal`: `Name string`, `Kind SignalKind`, `Protocol string`, `Address [4]byte`, `Command [4]byte`, `Frequency uint32`, `DutyCycle float32` (`0.01..0.99`), `RawData []uint32`.
  - `IRFile`: `Version int`, `Signals []IRSignal`.
- **API**:
  - `Parse(r io.Reader) (*IRFile, error)`
  - `Write(w io.Writer, f *IRFile) error`
  - `LoadFile(path string) (*IRFile, error)` / `SaveFileAtomic(path string, f *IRFile) error` (write temp file in same dir + `Sync` + `Rename`).

#### Acceptance Criteria (Given / When / Then)
1. **Given** a valid `.ir` file with mixed `type: parsed` and `type: raw` buttons separated by `#` lines,
   **When** `Parse` is called,
   **Then** all buttons are decoded in order with exact `Name`, `Kind`, `Protocol`, 4-byte `Address`/`Command`, `Frequency`, `DutyCycle`, and `RawData` microsecond slices.
2. **Given** a valid `*IRFile`,
   **When** `Write` followed by `Parse` is executed,
   **Then** the resulting `*IRFile` is semantically identical (lossless round-trip).
3. **Given** an `.ir` file with `\r\n` (CRLF) line endings, trailing spaces, or extra `#` comment lines (e.g., `# Samsung TV 2024`),
   **When** `Parse` is called,
   **Then** it parses cleanly without error.
4. **Given** an `.ir` file with missing `Filetype: IR signals file`, unsupported `Version: 2`, unknown `type: foo`, invalid hex in `address`/`command`, out-of-range `frequency` (`< 10000` or `> 100000`), `duty_cycle <= 0 || >= 1`, or zero/negative integers in `data:`,
   **When** `Parse` is called,
   **Then** it returns a descriptive typed sentinel error (`ErrInvalidHeader`, `ErrUnsupportedVersion`, `ErrInvalidSignal`) including the offending line number.
5. **Given** a `SaveFileAtomic` call on an unwritable directory or interrupted write,
   **When** writing fails,
   **Then** no partial/corrupted target `.ir` file is left behind.

#### Edge Cases & Adversarial Test Plan
- **Edge Cases**: Empty file; header-only file (0 signals); very long `data:` line (> 4096 pulses / > 64 KB line buffer — `bufio.Scanner` buffer must be expanded to `>= 1 MB` or use `bufio.Reader`); duplicate button names (preserve order or disambiguate cleanly without panicking); missing final newline; `data:` containing odd vs even number of values.
- **Tests**: Table-driven unit tests covering valid Flipper-IRDB fixtures + all malformed inputs; `FuzzParseIRFile(f *testing.F)` verifying zero panics on arbitrary byte streams and round-trip consistency on valid outputs.

---

### US-01.3: IR Protocol Encoders & Decoders (`NEC`, `NECext`, `Samsung32`, `RC5`, `RC6`, `SIRC`, `Kaseikyo`, `RAW`)

> **As a** user capturing or transmitting IR commands,
> **I want** deterministic encoders and decoders for the 7 major IR protocols plus `RAW` normalization,
> **So that** captured pulses decode into compact Flipper `parsed` signals and `parsed` signals encode into spec-compliant mark/space pulse trains.

#### Technical Contract (`internal/irfile`)
- **Strategy & Registry Pattern (Open/Closed)**:
  ```go
  type ProtocolCodec interface {
      Name() string
      Carrier() CarrierSpec
      Encode(sig IRSignal) (PulseTrain, error)
      Decode(pulses PulseTrain) (IRSignal, bool)
  }
  ```
  - `ProtocolRegistry` holds registered codecs in deterministic priority order (more specific headers before looser ones; `RAW` as explicit fallback).
  - `PulseTrain` is `[]int32` with strict sign alternation (`+mark µs`, `-space µs`), plus `ToLIRCPulses() ([]uint32, error)` converting to odd-length positive `uint32` array for `/dev/lirc*`.

- **Canonical Protocol Timing Specifications (SSOT per `proto_*.go`)**:

| Protocol | Carrier / Duty | Header (Mark / Space µs) | Modulation & Bit Timing (µs) | Frame Bits & Packing (Flipper `.ir` Convention) |
| --- | --- | --- | --- | --- |
| **`NEC`** | `38000 Hz` / `33%` | `9000 / 4500` (repeat: `9000 / 2250 / 562`) | Pulse-distance, LSB-first: `0 = +562/-562`, `1 = +562/-1687`, stop `+562` | 32 bits: `addr, ~addr, cmd, ~cmd`; `Address = [addr, 0, 0, 0]`, `Command = [cmd, 0, 0, 0]` |
| **`NECext`** | `38000 Hz` / `33%` | `9000 / 4500` | Same as NEC (`+562/-562`, `+562/-1687`, stop `+562`), LSB-first | 32 bits: `addrLo, addrHi, cmdLo, cmdHi` (no inverse check); `Address = [addrLo, addrHi, 0, 0]`, `Command = [cmdLo, cmdHi, 0, 0]` |
| **`Samsung32`** | `38000 Hz` / `33%` | `4500 / 4500` | Pulse-distance, LSB-first: `0 = +550/-550`, `1 = +550/-1650`, stop `+550` | 32 bits: `addr, addr, cmd, ~cmd`; `Address = [addr, 0, 0, 0]`, `Command = [cmd, 0, 0, 0]` |
| **`RC5`** | `36000 Hz` / `33%` | None (2 start bits `S1=1, S2=1` + toggle `T`) | Manchester (bi-phase), MSB-first, `TE = 889 µs`: `1 = -889/+889`, `0 = +889/-889` | 14 bits: `S1, S2, T, 5-bit addr, 6-bit cmd` (RC5X uses `~S2` as cmd bit 6); `Address = [addr, 0, 0, 0]`, `Command = [cmd, 0, 0, 0]` |
| **`RC6`** | `36000 Hz` / `33%` | `2666 / 889` | Bi-phase, MSB-first, `TE = 444 µs` (toggle bit is double-width `2*TE = 889 µs`): `1 = +444/-444`, `0 = -444/+444` | Mode 0 (20 bits after header: `SB(1), M2..M0(000), T, 8-bit addr, 8-bit cmd`); `Address = [addr, 0, 0, 0]`, `Command = [cmd, 0, 0, 0]` |
| **`SIRC`** | `40000 Hz` / `33%` | `2400 / 600` | Pulse-width, LSB-first: `0 = +600/-600`, `1 = +1200/-600` | 12-bit (`7-bit cmd + 5-bit addr`), 15-bit (`7-bit cmd + 8-bit addr`), or 20-bit (`7-bit cmd + 5-bit addr + 8-bit ext`); `Address = [addr, ext, 0, 0]`, `Command = [cmd, 0, 0, 0]` |
| **`Kaseikyo`** | `38000 Hz` / `33%` | `3360 / 1680` | Pulse-distance, LSB-first, `TE = 420 µs`: `0 = +420/-420`, `1 = +420/-1260`, stop `+420` | 48 bits: `16-bit vendor`, `4-bit vendor parity + 4-bit genre1`, `4-bit genre2 + 4-bit data0`, `8-bit data1`, `8-bit XOR check`; packed into `Address[4]` + `Command[4]` |
| **`RAW`** | Configurable (default `38000 Hz` / `33%`) | N/A | Direct mark/space pass-through with sign-alternation normalization | Arbitrary odd-length mark/space microsecond sequence |

- **Timing Tolerance**: Decoders MUST accept `±22%` timing jitter (or `±150 µs` floor for short pulses) to tolerate real-world IR photodiode distortion (TSOP/VS1838 pulse stretching).

#### Acceptance Criteria (Given / When / Then)
1. **Given** any valid `IRSignal` for each of `NEC`, `NECext`, `Samsung32`, `RC5`, `RC6`, `SIRC` (12/15/20-bit), and `Kaseikyo`,
   **When** encoded with `codec.Encode(sig)` and immediately decoded with `registry.Decode(pulses)`,
   **Then** `Decode` returns `(decoded, true)` with matching `Protocol`, `Address`, and `Command` (strict round-trip invariant).
2. **Given** an encoded pulse train with simulated photodiode distortion (`±15%` pulse width stretch/shrink),
   **When** passed to `registry.Decode(pulses)`,
   **Then** it still decodes the exact original `Protocol`, `Address`, and `Command`.
3. **Given** an `NEC` frame where `~addr` or `~cmd` does NOT match bitwise inverse,
   **When** passed to `registry.Decode(pulses)`,
   **Then** `NEC` rejects it and `NECext` decodes the full 16-bit address and 16-bit command cleanly.
4. **Given** an `NEC` repeat frame (`+9000, -2250, +562`),
   **When** decoded,
   **Then** it is recognized as a repeat indicator without corrupting or panicking the decoder.
5. **Given** an unrecognized pulse train (e.g., proprietary AC frame),
   **When** passed to `registry.Decode(pulses)`,
   **Then** structured codecs return `false` and the signal is preserved cleanly as `SignalRaw` with valid sign alternation.

#### Edge Cases & Adversarial Test Plan
- **Edge Cases**: Empty `PulseTrain`; single pulse; `math.MinInt32`/`math.MaxInt32` durations; non-alternating signs (`+500, +500`); zero durations; truncated frames (missing stop bit or missing last bit); `Kaseikyo` invalid XOR checksum (must reject); `Samsung32` mismatched `addr` duplicate byte (must reject); `RC5`/`RC6` half-bit boundary misalignment.
- **Tests**:
  - Exhaustive table-driven round-trip tests for all 7 structured protocols + `RAW`.
  - Jitter injection tests (`±15%` and `±20%` tolerance boundaries; `±35%` rejection).
  - `FuzzDecodePulses(f *testing.F)` throwing random `[]int32` slices at `registry.Decode` to guarantee zero panics and bounded runtime.

---

### US-01.4: Asynchronous `TXWorker` with Cancellation & TX/RX Mutual Exclusion

> **As a** user pressing buttons or running probes/macros on CardputerZero,
> **I want** IR transmission to execute asynchronously off the UI thread with instant `ESC` cancellation and guaranteed RX muting,
> **So that** the `320×170` UI stays responsive at ~18 FPS and transmitted pulses never loop back into the RX capture screen.

#### Technical Contract (`internal/lirc`)
- **Types**:
  - `TXRequest`: `ID uint64`, `Signal irfile.IRSignal`, `Repeat int` (clamped `1..10`), `InterFrameGap time.Duration` (default `45 ms`), `Ctx context.Context`.
  - `TXEvent`: `ID uint64`, `Kind TXEventKind` (`TXStarted`, `TXProgress`, `TXCompleted`, `TXCancelled`, `TXError`), `RepeatIdx int`, `RepeatTotal int`, `Err error`.
  - `TransceiverGate`: Coordinates exclusive hardware ownership between `TXWorker` and `RXWorker` (`AcquireTX(ctx)`, `ReleaseTX()` with `SettleGuard >= 20 ms`, `IsTXActive() bool`).
- **Behavior**:
  - `TXWorker.Submit(req TXRequest) error` enqueues non-blockingly into a bounded channel (`cap = 8`), returning `ErrTXQueueFull` if saturated.
  - `TXWorker.CancelCurrent()` cancels the active transmission's derived context immediately.
  - For each `TXRequest`:
    1. Encode `req.Signal` to `PulseTrain` → odd-length `[]uint32` LIRC pulses.
    2. Acquire `TransceiverGate` (pausing `RXWorker` and flushing any stale buffered RX packets).
    3. Configure `SetSendCarrier` and `SetSendDutyCycle`; defer unconditional restoration to `(38000 Hz, 33%)`.
    4. Transmit `Repeat` frames with `select` on `ctx.Done()` during inter-frame gaps.
    5. Wait `SettleGuard` (`>= 20 ms`) after final pulse before releasing `TransceiverGate` so `RXWorker` never sees optical ring-down.
    6. Emit `TXEvent` non-blockingly to subscriber channel.

#### Acceptance Criteria (Given / When / Then)
1. **Given** an active `TXWorker` and `RXWorker` sharing a `TransceiverGate`,
   **When** `TXWorker.Submit` sends a 5-repeat signal,
   **Then** `Submit` returns in `< 1 ms`, `RXWorker` is paused for the entire TX duration plus `>= 20 ms` settle guard, and `TXStarted` → `TXProgress` → `TXCompleted` events are emitted.
2. **Given** a multi-repeat `TXRequest` in progress,
   **When** `CancelCurrent()` or `req.Ctx` cancellation is triggered mid-transmission,
   **Then** transmission aborts within `< 10 ms`, carrier/duty are restored to `(38000 Hz, 33%)`, `TransceiverGate` is released after settle guard, and a `TXCancelled` event is emitted.
3. **Given** a `TXRequest` with `Repeat: 50` (exceeding safety cap `MaxRepeats = 10`),
   **When** submitted,
   **Then** it is rejected with `ErrRepeatLimitExceeded` before touching hardware.

#### Edge Cases & Adversarial Test Plan
- **Edge Cases**: Rapid burst of `Submit` calls filling the queue; cancelling when idle (no-op); closing `TXWorker` while a TX is active (clean goroutine shutdown, zero leaks); concurrent `Submit` + `CancelCurrent` from multiple goroutines under `go test -race`.
- **Tests**: Table-driven tests with mock `Transport` recording timestamps of TX pulses vs RX reads to mathematically prove zero overlap and `>= 20 ms` post-TX settle gap; goroutine leak check on `Close()`.

---

### US-01.5: Non-Blocking `RXWorker` Frame Framing & Noise Filtering

> **As a** user pointing a physical remote at CardputerZero's IR receiver,
> **I want** `RXWorker` to continuously frame `LIRC_MODE_MODE2` pulses in the background, reject ambient-light EMI spikes, and emit decoded `RXCapture` events,
> **So that** live captures appear immediately in the `RX` tab and MCP `ir_receive` tool without ever stalling the UI.

#### Technical Contract (`internal/lirc`)
- **Framing Parameters (SSOT constants)**:
  - `DefaultRecTimeoutUs = 15000` (`15 ms` inter-frame gap ends a frame).
  - `MinPulseUs = 100` (sub-100 µs ambient/fluorescent glitches absorbed or filtered).
  - `MaxFramePulses = 1024` (hard cap per captured frame to bound memory on 512 MB CM0).
  - `MinFramePulses = 6` (reject isolated 1–4 pulse ambient light flickers).
- **Packet Processing**:
  - Reads `uint32` `mode2` packets from `Transport.ReadMode2(ctx, buf)` using a poll/deadline loop so `ctx.Done()` exits within `<= 50 ms`.
  - Discards packets arriving while `TransceiverGate.IsTXActive()` is true.
  - Coalesces consecutive same-type packets (`PULSE` + `PULSE` or `SPACE` + `SPACE`) to preserve strict sign alternation (`+mark`, `-space`).
  - Ignores leading `SPACE` packets before the first `PULSE` mark.
  - Flushes frame on `LIRC_MODE2_TIMEOUT`, space `>= DefaultRecTimeoutUs`, or reaching `MaxFramePulses`.
  - Decodes via injected `ProtocolRegistry` and sends immutable `RXCapture` to a bounded output channel (`cap = 16`); if channel is full, drops oldest unread frame rather than blocking the hardware reader.

#### Acceptance Criteria (Given / When / Then)
1. **Given** a stream of `LIRC_MODE_MODE2` packets representing an `NEC` button press terminated by `LIRC_MODE2_TIMEOUT`,
   **When** `RXWorker` processes the stream,
   **Then** it emits an `RXCapture` containing a strictly sign-alternating `PulseTrain` and `Decoded != nil` with `Protocol == "NEC"`, exact `Address`, and `Command`.
2. **Given** ambient light noise producing 2 short pulses (`80 µs`, `120 µs`) followed by timeout,
   **When** `RXWorker` processes the stream,
   **Then** the noise burst is discarded (`< MinFramePulses`) and no spurious `RXCapture` is emitted.
3. **Given** a slow UI consumer that stops draining the `RXCapture` channel while 50 frames arrive,
   **When** `RXWorker` continues reading,
   **Then** `RXWorker` never blocks or deadlocks, drops oldest unread captures, and stays bounded in memory.
4. **Given** `RXWorker` is running,
   **When** its `context.Context` is cancelled,
   **Then** the goroutine exits cleanly within `<= 50 ms` and closes its output channel.

#### Edge Cases & Adversarial Test Plan
- **Edge Cases**: Leading spaces before first mark; two consecutive `LIRC_MODE2_PULSE` packets without intervening space (must sum durations to preserve alternation); frame exceeding `MaxFramePulses` (flush cleanly at 1024 without OOM); zero-value `mode2` packet; RX packets injected during active TX gate window (must be dropped).
- **Tests**: Table-driven synthetic `mode2` stream tests (NEC, RC5, noise burst, oversized frame, consecutive marks, slow consumer backpressure) run with `go test -race -cover ./internal/lirc/... ./internal/irfile/...` targeting `>= 90%` statement coverage.

---

## 3. Epic 1 Definition of Done (DoD)

- [ ] `internal/lirc` and `internal/irfile` compile cleanly for `GOOS=linux GOARCH=arm64 CGO_ENABLED=0` and host OS.
- [ ] All source files strictly adhere to `DESIGN.md` caps (`<= 350` lines/file, `<= 40` lines/function, `<= 12` fields/struct).
- [ ] `go test -race -cover ./internal/lirc/... ./internal/irfile/...` passes with `>= 90%` coverage.
- [ ] `FuzzParseIRFile` and `FuzzDecodePulses` run cleanly with zero panics.
- [ ] All 7 structured protocols + `RAW` pass lossless round-trip and `±15%` jitter tests.
- [ ] `TXWorker` + `RXWorker` mutual exclusion and carrier/duty restoration invariants are verified by deterministic tests.
