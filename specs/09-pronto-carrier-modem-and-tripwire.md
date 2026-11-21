# Epic 9: Pronto Hex, Discrete TV-Service Pack, Hardware Carrier Measure, IR Optical Modem & Tripwire

## Epic Overview

Epic 9 unlocks five hardware-first IR/LIRC capabilities for **IR Studio** (`ir-studio`, pure Go 1.27+, `CGO_ENABLED=0`, zero external dependencies) on M5Stack CardputerZero (`/dev/lirc*` + `SIM` fallback):

1. **`US-09.1` — Pronto Hex (`0000 006D ...`) Lossless Import/Export**: Bidirectional conversion between Philips Pronto CCF learned hex strings and `irfile.IRSignal`.
2. **`US-09.2` — Built-in Discrete & Service TV Pack (`GEN:TV-Service`)**: Read-only compiled-in virtual pack of discrete `Power_ON`/`Power_OFF`/`HDMI_1..4` and factory/service commands for LG, Samsung, Sony, Panasonic, and Philips.
3. **`US-09.3` — Hardware RX Carrier Frequency Measurement**: Pure-Go `LIRC_SET_MEASURE_CARRIER_MODE` ioctl activation and `LIRC_MODE2_FREQUENCY` packet parsing in `RXWorker`.
4. **`US-09.4` — 38 kHz IR Optical Data Modem (`internal/irmodem`)**: Framed packet codec (preamble + length + payload `<= 64 B` + CRC-8-CCITT) over pulse-distance `PulseTrain` tolerating `±120 µs` jitter.
5. **`US-09.5` — Optical Tripwire / Proximity Sensor (`internal/lirc/tripwire.go`)**: Bounded 38 kHz TX ping burst + RX reflection/beam-break detector synchronized through `TransceiverGate` with `context.Context` cancellation and `SIM` fallback.

---

## US-09.1: Pronto Hex (`0000 006D ...`) Lossless Import / Export

**As a** user importing discrete IR codes from AVSForum, RemoteCentral, or Global Caché databases,
**I want** pure-Go `ParseProntoHex` and `FormatProntoHex` functions and MCP tools,
**So that** I can convert Pronto Hex strings into Flipper `.ir` `IRSignal` records and export saved buttons back to Pronto Hex without external tools.

### Domain Contract (`internal/irfile/pronto.go`)

```go
const (
    ProntoClockPeriodUs = 0.241246 // 1 / 4.145146 MHz
    ProntoRawFormat     = 0x0000
    MaxProntoBurstPairs = 512      // bounds untrusted input (1024 durations)
)

var ErrInvalidProntoHex = errors.New("irfile: invalid pronto hex")

func ParseProntoHex(name, hexStr string) (IRSignal, error)
func FormatProntoHex(sig IRSignal) (string, error)
```

- **Carrier Formula**: `freqHz = round(1_000_000 / (float64(w1) * 0.241246))`, validated within `[MinFreqHz, MaxFreqHz]` (`10000..100000 Hz`). Reverse: `w1 = round(1_000_000 / (float64(freqHz) * 0.241246))`.
- **Burst Pairs**: Header `w0 w1 w2 w3` requires `w0 == 0x0000`, `w1 > 0`, `pairs = w2 + w3` with `1 <= pairs <= MaxProntoBurstPairs`, and exact token count `4 + 2*pairs`. Each burst word must be non-zero (`1..0xFFFF`); duration in µs = `round(cycles * cycleUs)` clamped to `[MinLIRCPulseUs, MaxLIRCPulseUs]`.
- **Export**: Encodes `SignalParsed` via `DefaultRegistry().Encode(sig)` or reads `SignalRaw.RawData`. If duration count is odd, appends a `40000 µs` lead-out space to form an even number of burst pairs.

### Acceptance Criteria (Given / When / Then)
- **Given** a valid 38 kHz Pronto Hex string (`0000 006D 0022 0000 ...`), **When** `ParseProntoHex("Power_ON", s)` and `FormatProntoHex(sig)` run, **Then** carrier frequency equals `38029 Hz` (`w1 = 0x006D`), burst pairs match within `±1` carrier cycle, and re-parsing produces identical `w1` and durations.
- **Given** a non-`0000` format (`5000 ...`), truncated pair list, `w1 == 0`, zero burst duration, or `> 512` pairs, **When** `ParseProntoHex` runs, **Then** it returns an error wrapping `ErrInvalidProntoHex`.

---

## US-09.2: Built-in Discrete & Service TV Pack (`GEN:TV-Service`)

**As a** home-theater installer or TV technician using CardputerZero offline,
**I want** a compiled-in read-only `GEN:TV-Service` virtual pack with discrete `Power_ON`/`Power_OFF`/`HDMI_1..4` and vendor service commands (`LG IN_START`/`EZ_ADJUST`, `Samsung Service_Info`/`Service_Menu`/`Service_Mute`/`Service_Factory`, `Sony`/`Panasonic`/`Philips` discrete codes),
**So that** I can transmit or promote discrete and service commands without toggling power state or requiring network sync.

### Domain Contract (`internal/library/tvservice.go`)

```go
const VirtualPackTVService = "GEN:TV-Service"

func TVServiceRemotes() []RemoteEntry // ReadOnly: true, SourcePack: VirtualPackTVService
```

- **Store Integration**:
  - `IsVirtualPack(pack)` matches strictly `pack == VirtualPackTVBGone || pack == VirtualPackTVService` (blocking `GEN:../escape`).
  - `Store.ListDatabasePacks()` returns both `GEN:TV-B-Gone` and `GEN:TV-Service` (`Virtual: true`) before disk packs.
  - `Store.ListPackRemotes(VirtualPackTVService)`, `Store.LoadRemote`, and `Store.PromoteToUser` work with zero disk I/O for reads and collision-safe promotion into `UserLibrary`.
  - `Store.DeleteDatabaseRemote(VirtualPackTVService, ...)` returns `ErrReadOnlyPack`.

### Acceptance Criteria (Given / When / Then)
- **Given** an empty `~/ir-studio/databases/` directory, **When** `Store.ListDatabasePacks()` and `Store.ListPackRemotes("GEN:TV-Service")` are called, **Then** `GEN:TV-Service` appears with `Virtual: true`, every remote has `ReadOnly: true`, and 100% of embedded signals encode via `irfile.DefaultRegistry().Encode` to odd-length positive LIRC pulse trains.
- **Given** a caller attempts to delete a file from `GEN:TV-Service`, **When** `DeleteDatabaseRemote` runs, **Then** it fails with `ErrReadOnlyPack`.

---

## US-09.3: Hardware RX Carrier Frequency Measurement

**As a** user diagnosing non-38 kHz remotes (e.g., 36 kHz RC5, 40 kHz Sony, 56 kHz Cisco/Motorola),
**I want** `internal/lirc` to enable `LIRC_SET_MEASURE_CARRIER_MODE` when `/dev/lirc*` advertises `LIRC_CAN_MEASURE_CARRIER` and parse `LIRC_MODE2_FREQUENCY` packets in `RXWorker`,
**So that** `RXCapture.CarrierEstHz` reports the exact hardware-measured carrier frequency instead of falling back to `38000 Hz`.

### Domain Contract (`internal/lirc/ioctl.go`, `transport_linux.go`, `rx_worker.go`)

```go
const (
    LIRC_SET_MEASURE_CARRIER_MODE uintptr = 0x4004691d // _IOW('i', 0x1d, __u32)
    LIRC_CAN_MEASURE_CARRIER      uint32  = 0x02000000
    LIRC_MODE2_FREQUENCY          uint32  = 0x02000000
)

type CarrierMeasurer interface {
    SetMeasureCarrier(enable bool) error
}
```

- When `configureLinuxTransport` opens an RX device with `feat & LIRC_CAN_MEASURE_CARRIER != 0`, it enables carrier measurement (`ENOTTY`/`EINVAL` ignored gracefully).
- `RXWorker` accumulates `LIRC_MODE2_FREQUENCY` values within `[irfile.MinFreqHz, irfile.MaxFreqHz]` during a frame and stores their arithmetic mean in `RXCapture.CarrierEstHz` upon `flushFrame`, resetting on frame flush or TX-active mute.

### Acceptance Criteria (Given / When / Then)
- **Given** `LIRC_MODE2_FREQUENCY | 40000` packets interleaved with a valid pulse frame on `SimTransport`, **When** `RXWorker` flushes the capture, **Then** `RXCapture.CarrierEstHz == 40000` (overriding the 38 kHz default).
- **Given** no `LIRC_MODE2_FREQUENCY` packets arrive, **When** `RXWorker` flushes, **Then** `RXCapture.CarrierEstHz` falls back to decoded protocol nominal frequency or `38000 Hz`.

---

## US-09.4: 38 kHz IR Optical Data Modem (`internal/irmodem`)

**As a** developer or field researcher transferring short telemetry/keys optically between two CardputerZero units,
**I want** a pure-Go `internal/irmodem` codec that encodes `<= 64 B` payloads into a framed, CRC-8-protected 38 kHz `PulseTrain`,
**So that** data survives `±120 µs` demodulator jitter over `/dev/lirc0` + `/dev/lirc1` without silent corruption.

### Domain Contract (`internal/irmodem/modem.go`)

```go
const (
    MaxPayloadBytes = 64
    CarrierHz       = 38000
    DutyPct         = 33
    FrameSyncByte   = 0xA5
    HeaderMarkUs    = 4000
    HeaderSpaceUs   = 2000
    BitMarkUs       = 560
    Bit0SpaceUs     = 560
    Bit1SpaceUs     = 1690
    StopMarkUs      = 560
    TimingTolUs     = 250 // >= ±120 µs jitter tolerance
)

var (
    ErrPayloadTooLarge  = errors.New("irmodem: payload exceeds 64 bytes")
    ErrBadPreamble      = errors.New("irmodem: missing or invalid preamble")
    ErrTruncatedFrame   = errors.New("irmodem: truncated modem frame")
    ErrInvalidTiming    = errors.New("irmodem: pulse timing outside tolerance")
    ErrChecksumMismatch = errors.New("irmodem: CRC-8 checksum mismatch")
)

func CRC8(data []byte) byte // CRC-8-CCITT (poly 0x07, init 0x00)
func EncodeFrame(payload []byte) (irfile.PulseTrain, error)
func DecodeFrame(pulses irfile.PulseTrain) ([]byte, error)
```

- **Wire Frame**: `[Sync(0xA5), Len(0..64), Payload(0..64 B), CRC8(Sync..Payload)]`, MSB-first pulse-distance modulation (`BitMarkUs` + `Bit0SpaceUs`/`Bit1SpaceUs`) terminated by `StopMarkUs`.

### Acceptance Criteria (Given / When / Then)
- **Given** any payload of `0..64` bytes, **When** encoded with `EncodeFrame`, perturbed by `±120 µs` jitter on every pulse, and decoded with `DecodeFrame`, **Then** the exact original payload bytes are returned.
- **Given** a `65`-byte payload or a single flipped bit in the pulse train, **When** `EncodeFrame` or `DecodeFrame` runs, **Then** `ErrPayloadTooLarge` or `ErrChecksumMismatch` is returned respectively.

---

## US-09.5: Optical Tripwire / Proximity Sensor (`internal/lirc/tripwire.go`)

**As a** user testing optical reflection or beam-break detection using CardputerZero's co-located IR emitter and receiver,
**I want** `lirc.TripwireProbe` with `context.Context` cancellation, `TransceiverGate` synchronization, and `SIM` fallback,
**So that** I can run bounded 38 kHz proximity/beam-break probes safely from Go or MCP without leaving the IR LED energized or racing `RXWorker`.

### Domain Contract (`internal/lirc/tripwire.go`)

```go
type TripwireMode string

const (
    TripwireReflective TripwireMode = "reflective"
    TripwireBeamBreak  TripwireMode = "beam_break"
)

type TripwireConfig struct {
    Mode         TripwireMode  // default TripwireReflective
    CarrierHz    uint32        // 36000..56000, default 38000
    DutyPct      uint32        // 1..99, default 33
    PingCount    int           // 1..10, default 3
    PingPulseUs  uint32        // 200..2000, default 600
    ListenWindow time.Duration // 5ms..500ms, default 35ms
}

type TripwireReading struct {
    Timestamp      time.Time     `json:"timestamp"`
    Mode           TripwireMode  `json:"mode"`
    CarrierHz      uint32        `json:"carrier_hz"`
    PingsSent      int           `json:"pings_sent"`
    EchoesReceived int           `json:"echoes_received"`
    ReflectionPct  float64       `json:"reflection_pct"`
    Triggered      bool          `json:"triggered"`
    HardwareMode   string        `json:"hardware_mode"`
    DurationMs     int64         `json:"duration_ms"`
}

func TripwireProbe(ctx context.Context, xcvr *Transceiver, cfg TripwireConfig) (TripwireReading, error)
```

### Acceptance Criteria (Given / When / Then)
- **Given** `SimTransport` with loopback enabled (`reflective` mode), **When** `TripwireProbe` runs 3 pings, **Then** `EchoesReceived == 3`, `ReflectionPct == 100`, `Triggered == true`, and carrier/duty are restored to `38000 Hz / 33%`.
- **Given** `SimTransport` with loopback disabled (`beam_break` mode), **When** `TripwireProbe` runs, **Then** `EchoesReceived == 0`, `Triggered == true`, and cancellation via `ctx` aborts promptly while restoring defaults.

---

## MCP Tools & Security Gates (23 → 28 Total Tools)

| Tool | Required Gate | Summary |
| :--- | :--- | :--- |
| `ir_import_pronto` | Path sandbox if `save_as` set | Parse Pronto Hex into `IRSignal`; optionally save/upsert into `UserLibrary` |
| `ir_export_pronto` | Read-only | Export a saved button or raw pulse train as Pronto Hex `0000 ...` |
| `ir_modem_send` | **`confirm_ir: true`** | Encode `<= 64 B` UTF-8/hex payload via `irmodem.EncodeFrame` and TX at 38 kHz |
| `ir_modem_recv` | Serialized `lircMu` | Capture or decode a `PulseTrain` via `irmodem.DecodeFrame` |
| `ir_tripwire_probe` | **`confirm_ir: true`** | Execute bounded `lirc.TripwireProbe` (`1..10` pings) with guaranteed default restore |

## Adversarial Test Plan

- `internal/irfile/pronto_test.go`: Round-trip carrier/pair accuracy, invalid headers (`5000`, `0000 0000`), truncated words, oversized pairs (`> 512`), zero durations, `FuzzParseProntoHex`.
- `internal/library/tvservice_test.go`: All embedded remotes/buttons encode cleanly to odd-length LIRC pulses, `PromoteToUser` collision handling, `ErrReadOnlyPack` on delete, `GEN:../escape` rejection.
- `internal/lirc/tripwire_test.go` & `lirc_test.go`: `LIRC_MODE2_FREQUENCY` exact averaging & reset, `TripwireProbe` reflective/beam-break/cancel/restore/bounds checks.
- `internal/irmodem/modem_test.go`: `0..64 B` round-trips, `65 B` rejection, `±120 µs` jitter survival, bit-flip CRC-8 detection, `FuzzDecodeFrame`.
- `internal/mcpserver/`: 28-tool schema/handler bijection, `confirm_ir: true` enforcement on `ir_modem_send` and `ir_tripwire_probe`, path-sandbox tests on `ir_import_pronto` `save_as`.
