# Epic 10: LED / Screen PWM Flicker Meter & Optical IR RPM Tachometer

## Epic Overview

Epic 10 adds two hardware-first optical instrumentation capabilities to **IR Studio** (`ir-studio`, pure Go 1.27+, `CGO_ENABLED=0`, zero external dependencies) on M5Stack CardputerZero (`/dev/lirc0` TX + `/dev/lirc1` RX + deterministic `SIM` fallback):

1. **`US-10.1` — LED / Screen PWM Flicker Meter (`internal/irml/flicker.go`, `internal/lirc/tachometer.go`)**: Pure RX-only analysis of `/dev/lirc1` pulse-period streams (IR TX strictly off) to measure dominant PWM flicker frequency (`Hz`), duty cycle (`%`), timing jitter (`%`), and IEEE 1789-2015 eye-safety classification (`NO_FLICKER`, `SAFE`, `LOW_RISK`, `HIGH_FLICKER`).
2. **`US-10.2` — Optical IR RPM Tachometer (`internal/irml/tachometer.go`, `internal/lirc/tachometer.go`)**: Bounded 38 kHz probe bursts on `/dev/lirc0` paired with `/dev/lirc1` reflected pulse-period capture through `TransceiverGate`, dividing blade-pass frequency by configurable blade count (`1..12`) using a zero-allocation median-filtered estimator with guaranteed `38000 Hz / 33%` restoration.

---

## US-10.1: LED / Screen PWM Flicker Meter

**As a** hardware engineer or display/lighting evaluator using CardputerZero,
**I want** an RX-only PWM flicker meter that measures dominant frequency, duty cycle, jitter, and IEEE 1789 eye-safety risk from `/dev/lirc1` without enabling the IR emitter,
**So that** I can diagnose harmful low-frequency PWM dimming on LED bulbs, monitors, and OLED screens with zero external instruments.

### Domain Contract (`internal/irml/flicker.go`)

```go
type FlickerRisk uint8

const (
	FlickerNone    FlickerRisk = iota // Insufficient or non-periodic pulses
	FlickerSafe                       // IEEE 1789 NOEL: >= 3125 Hz (or >= 99% DC duty)
	FlickerLowRisk                    // IEEE 1789 Low-Risk: 1250 Hz <= f < 3125 Hz
	FlickerHigh                       // IEEE 1789 High-Risk: f < 1250 Hz (100% PWM depth)
)

func (r FlickerRisk) String() string // "NO_FLICKER" | "SAFE" | "LOW_RISK" | "HIGH_FLICKER"

type FlickerReport struct {
	FreqHz    float32     `json:"freq_hz"`
	DutyPct   float32     `json:"duty_pct"`
	JitterPct float32     `json:"jitter_pct"`
	Periods   int         `json:"periods"`
	Risk      FlickerRisk `json:"risk"`
	RiskLabel string      `json:"risk_label"`
}

// AnalyzeFlicker computes dominant PWM flicker metrics with 0 B/op, 0 allocs/op.
func AnalyzeFlicker(pulses []int32) FlickerReport
```

### IEEE 1789-2015 Flicker Risk Bands (100% PWM Modulation Depth)

For rectangular PWM dimming where the light source switches fully on/off (`Mod% ≈ 100%`):

| Band | Condition | `FlickerRisk` | `RiskLabel` |
| --- | --- | --- | --- |
| **No Flicker** | `< 2` valid periods in `[100 µs, 200000 µs]` (`5 Hz..10 kHz`) or `JitterPct > 45%` | `FlickerNone` | `"NO_FLICKER"` |
| **Safe (NOEL)** | `FreqHz >= 3125 Hz` (IEEE 1789 `Mod% <= 0.0333 * f`) or `DutyPct >= 99%` / `<= 1%` | `FlickerSafe` | `"SAFE"` |
| **Low Risk** | `1250 Hz <= FreqHz < 3125 Hz` (IEEE 1789 `Mod% <= 0.08 * f`) | `FlickerLowRisk` | `"LOW_RISK"` |
| **High Flicker** | `FreqHz < 1250 Hz` (e.g., 100 Hz / 120 Hz / 240 Hz / 480 Hz PWM) | `FlickerHigh` | `"HIGH_FLICKER"` |

- **Zero-Allocation Hot Path**: `AnalyzeFlicker` pairs consecutive `+mark` and `-space` durations into full periods (`periodUs = markUs + spaceUs`), stores up to `256` periods and duty ratios in fixed stack arrays, sorts in-place to extract median period and median duty cycle, and computes mean absolute relative deviation (`JitterPct`). Verified at `0 B/op, 0 allocs/op` via `BenchmarkAnalyzeFlicker` (`for b.Loop()`).

### Hardware Capture Contract (`internal/lirc/tachometer.go`)

```go
const (
	DefaultMeasureWindowMs = 1000
	MinMeasureWindowMs     = 200
	MaxMeasureWindowMs     = 5000
)

func MeasureFlicker(ctx context.Context, tr *Transceiver, windowMs int) (irml.FlickerReport, error)
```

- **TX Strictly Off**: `MeasureFlicker` MUST NOT invoke `WritePulses`, `SetSendCarrier`, `SetSendDutyCycle`, or `Gate().AcquireTX`.
- **SIM Fallback**: When `tr.Transport().IsSim()` is true, returns a deterministic `120 Hz` (`8333 µs` period, `50%` duty) LED PWM train yielding `Risk == FlickerHigh` (`"HIGH_FLICKER"`).
- **Bounds & Cancellation**: `windowMs == 0` defaults to `1000 ms`; values outside `[200, 5000]` return an error. Pre-cancelled `ctx` fails immediately with `ctx.Err()`.

### Acceptance Criteria (Given / When / Then)
- **Given** a 120 Hz 50%-duty PWM pulse train (`+4167, -4166` repeated), **When** `AnalyzeFlicker` runs, **Then** `FreqHz` is `120.0 ± 0.5 Hz`, `DutyPct` is `50.0 ± 0.5%`, `JitterPct < 0.5%`, `Risk == FlickerHigh`, and `RiskLabel == "HIGH_FLICKER"`.
- **Given** a 2000 Hz PWM train and a 4000 Hz PWM train, **When** `AnalyzeFlicker` runs, **Then** they classify as `FlickerLowRisk` (`"LOW_RISK"`) and `FlickerSafe` (`"SAFE"`) respectively.
- **Given** `MeasureFlicker` in `SIM` mode, **When** invoked with `windowMs: 500`, **Then** `SimTransport.TxHistory()` remains empty (`0` TX bursts) and the returned report reports `~120 Hz` `HIGH_FLICKER`.

---

## US-10.2: Optical IR RPM Tachometer

**As a** maker or technician measuring cooling fan, propeller, or motor shaft speed without physical contact,
**I want** an optical IR tachometer that emits bounded 38 kHz probe bursts on `/dev/lirc0` and measures reflected blade-pass periodicity on `/dev/lirc1`,
**So that** I can compute accurate `RPM`, blade-pass frequency (`BladeFreqHz`), and stability `%` for any `1..12`-blade rotor.

### Domain Contract (`internal/irml/tachometer.go`)

```go
const (
	MinTachBlades   = 1
	MaxTachBlades   = 12
	MinTachPeriodUs = 200       // 5000 Hz max blade-pass frequency
	MaxTachPeriodUs = 1_000_000 // 1 Hz min blade-pass frequency
	MinTachSamples  = 2
)

type RPMReport struct {
	RPM          float32 `json:"rpm"`
	BladeFreqHz  float32 `json:"blade_freq_hz"`
	Blades       int     `json:"blades"`
	StabilityPct float32 `json:"stability_pct"`
	Samples      int     `json:"samples"`
	Valid        bool    `json:"valid"`
}

// AnalyzeRPM estimates rotor RPM with outlier rejection and 0 B/op, 0 allocs/op.
func AnalyzeRPM(pulses []int32, blades int) RPMReport
```

### Blade-Division & Median-Filter Math

1. **Period Extraction**: Consecutive `+mark` / `-space` pairs within `[MinTachPeriodUs, MaxTachPeriodUs]` are buffered into a stack array `[256]float32` (`0` heap allocations).
2. **Outlier Rejection**: In-place sort yields raw `medianUs`. Periods outside `medianUs ± 35%` are discarded as optical glitches or missed reflections.
3. **Blade Division**:
   $$\text{BladeFreqHz} = \frac{1{,}000{,}000}{\text{filteredPeriodUs}}, \qquad \text{RPM} = \frac{\text{BladeFreqHz} \times 60}{\text{Blades}}$$
4. **Stability Score**:
   $$\text{StabilityPct} = \text{clamp}_{[0,100]}\!\left(\left(1 - \frac{\text{MAD}_{\text{inliers}}}{\text{filteredPeriodUs}}\right) \cdot \frac{N_{\text{inliers}}}{N_{\text{total}}} \cdot 100\right)$$

### Hardware Capture Contract (`internal/lirc/tachometer.go`)

```go
func MeasureRPM(ctx context.Context, tr *Transceiver, blades int, windowMs int) (irml.RPMReport, error)
```

- **Mutual Exclusion & Carrier Restoration**: Acquires `tr.Gate().AcquireTX(ctx)` with `defer tr.Gate().ReleaseTX()`, sets `38000 Hz / 33%`, and unconditionally restores `38000 Hz / 33%` via `defer tr.restoreDefaults()`.
- **SIM Fallback**: Emits one 38 kHz probe burst on `SimTransport` and evaluates a deterministic `60 Hz` blade-pass reflection train (`+3000 µs / -13667 µs`), yielding **`1800 RPM`** for a 2-blade fan (`Valid: true`, `StabilityPct: 100%`).

### Acceptance Criteria (Given / When / Then)
- **Given** a 60 Hz reflected pulse train (`+3000, -13667` repeated 16×) with 2 injected outlier glitches (`+500, -500`), **When** `AnalyzeRPM(pulses, 2)` runs, **Then** outliers are rejected, `RPM` is `1800 ± 5`, `BladeFreqHz` is `60.0 ± 0.2`, `Valid == true`, and `BenchmarkAnalyzeRPM` reports `0 B/op, 0 allocs/op`.
- **Given** `blades == 0` or `blades == 13`, **When** `AnalyzeRPM` or `MeasureRPM` runs, **Then** `AnalyzeRPM` returns `Valid: false` and `MeasureRPM` returns a validation error without transmitting.
- **Given** `MeasureRPM` runs with a non-default carrier pre-configured on `SimTransport`, **When** it completes or `ctx` cancels, **Then** `SimTransport.CarrierState()` is unconditionally restored to `38000 Hz / 33%`.

---

## MCP Tools (Total: 30 Tools)

| Tool | Direction | Arguments | Safety Gate |
| --- | --- | --- | --- |
| `ir_measure_flicker` | RX-only | `window_ms` (`200..5000`, default `1000`) | Serialized via `lircMu`; TX strictly disabled |
| `ir_measure_rpm` | TX+RX | `confirm_ir` (required `true`), `blades` (`1..12`, default `2`), `window_ms` (`200..5000`, default `1000`) | Requires **`confirm_ir: true`**; serialized via `lircMu` & `TransceiverGate`; guaranteed `38 kHz / 33%` restore |

---

## Adversarial Test Plan

1. **Zero / Empty / Degenerate Inputs**: `nil`, empty slice, single mark, all-negative spaces, zero durations, `math.MinInt32`, `math.MaxInt32` — must never panic or produce `NaN`/`Inf`.
2. **IEEE 1789 Boundary Values**: Exact boundary checks around `1250 Hz`, `3125 Hz`, `99%` duty, and high-jitter (>45%) random noise.
3. **RPM Outlier & Blade Bounds**: Inject 1–3 extreme glitch periods (`300 µs`, `900000 µs`) into a steady 1800 RPM stream; verify `blades` `{0, -1, 1, 2, 12, 13}`.
4. **Zero-Allocation Enforcement**: `BenchmarkAnalyzeFlicker` and `BenchmarkAnalyzeRPM` using Go `for b.Loop()` verified at `0 B/op, 0 allocs/op`.
5. **Hardware & MCP Safety**: Verify `MeasureFlicker` performs `0` TX writes; verify `MeasureRPM` and `ir_measure_rpm` reject `confirm_ir: false`, reject out-of-range `blades`/`window_ms`, honor `context.Context` cancellation, and restore `38000 Hz / 33%`; verify `tools/list` returns exactly `30` tools with 1:1 handler bijection.
