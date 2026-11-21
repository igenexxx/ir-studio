# Epic 4: On-Device Pure-Go IR ML (`internal/irml`)

## Epic Overview

`ir-studio` brings zero-allocation, pure-Go on-device machine learning to 38 kHz Infrared analysis on the CardputerZero (ARM Cortex-A53, 512 MB RAM, `CGO_ENABLED=0`, zero Python/ONNX/TFLite runtimes). Unlike Sub-GHz RF where signals are short OOK keyfobs or weather sensors, IR captures suffer from TSOP receiver AGC pulse-stretching (`±80..150 µs`), ambient sunlight/PWM LED lamp spikes (`< 120 µs`), massive stateful HVAC/AC bursts (`64–200+` bits across multiple sub-frames), and half-broken remotes where only a single button still works.

The `internal/irml` bounded context solves these four IR-specific problems deterministically and in real time:
1. **1D K-Means IR Pulse Denoiser (`internal/irml/cluster.go`)** — absorbs sub-120 µs EMI glitches and quantizes jittery RAW mark/space timings to clean centroids to rescue protocol decoding (`ML-CLEAN`).
2. **IR Signal Classifier (`internal/irml/classifier.go`)** — separates `TV/Remote` frames, `AC State-Frame` HVAC bursts, and `Ambient Noise`, powering **Smart Save Guard** and bulk **`7 (PURGE)`** / `ir_purge_noise`.
3. **8D IR Fingerprint k-NN (`internal/irml/fingerprint.go`)** — matches a single captured button against `Flipper-IRDB` remotes (`Match: Daikin_ARC480.ir (96%)`) for one-key full-remote recovery.
4. **Smart Probe Ranking (`R`)** — reorders the active `PROBE` database pack by descending 8D cosine similarity against the last captured IR button.

### Package & Architectural Boundaries

| File | Responsibility | Hot-Path Allocation Budget |
| --- | --- | --- |
| `internal/irml/cluster.go` | Glitch absorption, header/sync separation, 1D K-Means quantization (`k=2`) | `0 allocs/op` (caller-provided scratch/dst buffers) |
| `internal/irml/features.go` | 12-feature IR signal extraction (`IRFeatureVector [12]float32`) | `0 allocs/op` |
| `internal/irml/classifier.go` | Pure-Go `12→24→3` ReLU/Softmax MLP (`TV/Remote`, `AC State-Frame`, `Ambient Noise`) | `0 allocs/op` |
| `internal/irml/model_weights.go` | Pre-trained deterministic float32 MLP weights & biases (SSOT) | `0 allocs/op` (package-level fixed arrays) |
| `internal/irml/fingerprint.go` | L2-normalized `IREmbedding [8]float32`, RAW & parsed synthesis, k-NN matcher, pack ranker | `0 allocs/op` on `CosineSimilarity` & `Match` |

> **Dependency Rule**: `internal/irml` is pure domain math. It MUST NEVER import `internal/lirc`, `internal/ui`, `internal/mcpserver`, or OS/hardware packages.

---

## US-04.1: Pure-Go 0-Alloc 1D K-Means IR Pulse Denoiser

**As a** CardputerZero user capturing IR signals from aging remotes or under noisy room lighting,  
**I want** `ir-studio` to automatically absorb `< 120 µs` EMI glitches and quantize `±80..150 µs` jittery RAW mark/space pulses to clean 1D K-Means centroids,  
**So that** degraded captures are rescued into valid `NEC`, `NECext`, `Samsung32`, `RC5`, `RC6`, or `SIRC` protocol decodes (`ML-CLEAN`) without heap allocations or UI lag.

### Domain Contract (`internal/irml/cluster.go`)

- **Constants (SSOT)**:
  - `DefaultIRMinGlitchUs int32 = 120` (pulses with `|p| < 120 µs` are EMI/AGC glitches)
  - `DefaultIRHeaderMinUs int32 = 2200` (separates IR AGC preamble headers like 9000/4500 µs or 4500/4500 µs and inter-frame gaps from data bit symbols)
  - `MaxKMeansIters = 20`
  - `MinIRDataPulses = 10`
- **Value Object (`IRPulseClusters`)**:
  - `HeaderMarkUs float64`, `HeaderSpaceUs float64`
  - `ShortUs float64`, `LongUs float64`, `GapUs float64`
  - `LongShortRatio float64`, `JitterRatio float64`
  - `DataPulses int`, `GlitchesRemoved int`, `QuantizedCount int`
  - `EncodingHint string` (`"Pulse-Distance"`, `"Pulse-Width"`, `"Bi-Phase/Manchester"`, `"Unstructured"`)
  - `Valid bool`
- **Zero-Allocation API**:
  - `DenoiseIRPulsesInto(dst, raw []int32, minGlitchUs int32) (n int, glitches int)`
  - `QuantizeIRPulsesInto(dst, cleaned []int32, clusters IRPulseClusters) (n int, quantized int)`
  - `ClusterIRTimings(raw []int32, scratch []int32, minGlitchUs int32) IRPulseClusters`

### Acceptance Criteria (Given / When / Then)

1. **Glitch Absorption & Alternation Preservation**:
   - **Given** a captured RAW IR pulse train containing `< 120 µs` ambient/receiver EMI spikes (e.g. `[+9000, -4500, +560, -45, +515, -1690, ...]`),
   - **When** `DenoiseIRPulsesInto` executes with `minGlitchUs = 120`,
   - **Then** every pulse with `|p| < 120 µs` is absorbed into the preceding pulse (preserving total frame period), adjacent same-sign pulses are merged so `+mark / -space` alternation is strictly maintained, and `glitches` accurately counts absorbed spikes.
2. **Protocol Rescue via 1D K-Means Quantization (`ML-CLEAN`)**:
   - **Given** a jittery `NEC`, `NECext`, `Samsung32`, `RC5`, `RC6`, or `SIRC` RAW capture with `±80..150 µs` timing distortion where direct protocol decoding initially fails,
   - **When** `ClusterIRTimings` computes data centroids (`ShortUs`, `LongUs`) while preserving header/sync pulses (`|p| >= 2200 µs`) and `QuantizeIRPulsesInto` snaps data pulses within `±35%` of their assigned centroid,
   - **Then** re-running the `irfile` protocol decoder on the quantized buffer succeeds, and the UI displays the **`ML-CLEAN`** badge in Magenta (`#e879f9`) alongside the rescued protocol, address, and command.
3. **Header & Inter-Frame Gap Preservation**:
   - **Given** an `NEC` frame (`+9000, -4500` header, `560/1690 µs` data, `-40000` inter-frame gap) or `Samsung32` frame (`+4500, -4500` header),
   - **When** 1D K-Means runs on the capture,
   - **Then** header marks/spaces and inter-frame gaps are excluded from the data centroid calculation and remain intact in the quantized output so protocol header matching is never corrupted.
4. **Zero-Allocation Hot Path**:
   - **Given** a pre-allocated `dst` and `scratch` slice of length `>= len(raw)` (up to `1024` pulses),
   - **When** `DenoiseIRPulsesInto`, `ClusterIRTimings`, and `QuantizeIRPulsesInto` are benchmarked with `go test -bench=. -benchmem`,
   - **Then** all three functions report `0 B/op` and `0 allocs/op`.

### Edge Cases

- **Empty or nil input**: Returns `n = 0, glitches = 0, Valid = false` without panic.
- **Zero-valued elements (`0`)**: Counted as glitches and skipped cleanly without breaking sign alternation.
- **Leading glitch pulse**: If `raw[0]` has `|p| < 120 µs` and `len(dst) == 0`, it is discarded cleanly (not absorbed into a nonexistent prior element).
- **Headerless Bi-Phase protocols (`RC5` / `RC6`)**: `HeaderMarkUs` and `HeaderSpaceUs` report `0` (for RC5) or `2666/889 µs` (for RC6); `ShortUs ≈ 889 µs`, `LongUs ≈ 1778 µs` (`LongShortRatio ≈ 2.0`), `EncodingHint = "Bi-Phase/Manchester"`.
- **Uniform pulse train (e.g. 50% square wave / LED flicker)**: `ShortUs == LongUs`, `LongShortRatio == 1.0`, K-Means exits early without division by zero.
- **Extreme int32 values (`math.MinInt32`, `math.MaxInt32`)**: Overflow-safe `int64` magnitude clamping prevents signed integer wraparound.
- **Short buffer (`len(dst) < len(raw)`)**: Clamps writes safely to `len(dst)` without index-out-of-range panics.

### Invariants

1. **Sign-Alternation Invariant**: For every output slice `out = dst[:n]` with `n >= 2`, `out[i]` and `out[i-1]` MUST have strictly opposite signs (`(out[i] > 0) != (out[i-1] > 0)`), and no element may equal `0`.
2. **Period Conservation Invariant**: Absorbing an internal glitch `< 120 µs` MUST add its duration to the preceding pulse so cumulative frame timing does not drift.
3. **Deterministic Convergence Invariant**: 1D K-Means MUST initialize centroids deterministically from sorted/min-max quantiles (no `math/rand`) and terminate within `<= 20` iterations.
4. **Zero-Allocation Invariant**: Hot-path functions MUST allocate `0` heap bytes (`testing.AllocsPerRun == 0`).

### Verification Plan

- **Table-driven unit tests (`internal/irml/cluster_test.go`)**:
  - Synthetic jittered (`±80..150 µs`) + glitch-injected (`30..115 µs`) fixtures for all 6 protocols (`NEC`, `NECext`, `Samsung32`, `RC5`, `RC6`, `SIRC`) verifying rescue from decode failure to exact decoded address/command.
  - Boundary tests: `nil`, empty, single pulse, all-glitches, `0` elements, `math.MinInt32`, `len(dst) < len(raw)`, leading glitch, uniform square wave.
- **Fuzz test (`FuzzDenoiseAndClusterIR`)**: Random `[]int32` slices verifying zero panics and strict sign-alternation invariant on every output.
- **Benchmarks (`BenchmarkDenoiseIRPulsesInto`, `BenchmarkClusterIRTimings`)**: Using Go 1.27 `for b.Loop()` asserting `0 allocs/op` and `< 50 µs` per 256-pulse frame on ARM64.

---

## US-04.2: IR Signal Classifier, Smart Save Guard & Bulk Noise Purge

**As a** user capturing IR remotes in brightly lit rooms or managing a growing library of `.ir` files,  
**I want** `ir-studio` to classify every IR signal into `TV/Remote`, `AC State-Frame`, or `Ambient Noise`, warn me before saving noise, and let me bulk-purge noisy captures,  
**So that** my User Library (`~/ir-studio/remotes/`) stays clean and HVAC state-frames are automatically recognized for specialized inspection.

### Domain Contract (`internal/irml/features.go`, `internal/irml/classifier.go`)

- **Signal Classes (`IRSignalClass`)**:
  - `ClassTVRemote` (`"TV/Remote"`) — 12–32 bit short frames (`20..90` pulses per frame: NEC, Samsung32, RC5, RC6, SIRC, Kaseikyo short).
  - `ClassACStateFrame` (`"AC State-Frame"`) — 64–200+ bit stateful HVAC bursts (`128..600+` pulses, multi-subframe gaps, long headers/trailers).
  - `ClassAmbientNoise` (`"Ambient Noise"`) — sunlight AGC chatter, 100/120/200/400 Hz PWM LED lamp flicker, fluorescent ballast interference, unstructured jitter (`JitterRatio > 0.35` or lacking valid IR framing).
- **Feature Vector (`IRFeatureVector [12]float32`, normalized to `[0, 1]`)**:
  0. `CarrierNorm` — `(carrierHz - 30000) / 30000`
  1. `HasHeader` — `1.0` if valid IR AGC preamble (`2200..11000 µs`) present, else `0.0`
  2. `HeaderMarkNorm` — `headerMarkUs / 10000`
  3. `HeaderSpaceNorm` — `headerSpaceUs / 6000`
  4. `ShortUsNorm` — `shortUs / 1500`
  5. `LongUsNorm` — `longUs / 3000`
  6. `RatioNorm` — `longShortRatio / 5.0`
  7. `JitterRatio` — cluster jitter `[0, 1]`
  8. `PulsesPerFrameNorm` — `pulsesPerFrame / 300`
  9. `SubframeCountNorm` — `subframes / 4.0`
  10. `GlitchDensity` — `glitchesRemoved / max(rawLen, 1)`
  11. `DutyCycle` — mark duration / total active frame duration `[0, 1]`
- **Classifier Output (`IRPrediction`)**:
  - `Class IRSignalClass`, `Label string`, `Confidence float32`, `Probs [3]float32`, `IsNoise bool` (`Class == ClassAmbientNoise && Confidence >= 0.85`).
- **Inference**: Pure-Go `12→24 ReLU→3 Softmax` MLP using fixed-size stack arrays (`0 allocs/op`), sanitizing `NaN`/`Inf` inputs into `[0, 1]`.

### Acceptance Criteria (Given / When / Then)

1. **Three-Way IR Classification**:
   - **Given** a captured RAW pulse train (and carrier frequency, defaulting to `38000 Hz` if unmeasured),
   - **When** `ExtractIRFeatures` and `ClassifyIR` execute,
   - **Then**:
     - Standard 12–32 bit TV/media remotes (`NEC`, `Samsung32`, `RC5`, `RC6`, `SIRC`) classify as `TV/Remote` with confidence `>= 0.80`.
     - Stateful HVAC bursts (`64–200+` bits, e.g. Daikin/Gree/Mitsubishi/Fujitsu/Midea) classify as `AC State-Frame` with confidence `>= 0.80`.
     - Sunlight flicker, 100/120 Hz PWM LED interference, and random EMI bursts classify as `Ambient Noise` with `IsNoise == true` (`confidence >= 0.85`).
2. **Smart Save Guard (UI)**:
   - **Given** a live capture in `RX` (or `REMOTE` Learn) classified as `Ambient Noise` (`IsNoise == true`),
   - **When** the user presses `6 (SAVE)`,
   - **Then** an Amber confirmation guard prompt appears (`"ML: Ambient Noise (94%)! Save anyway? [Enter=Yes / Esc=Cancel]"`), preventing accidental junk saves unless explicitly confirmed.
3. **Bulk Noise Purge (`7 PURGE` in `SAVED` tab)**:
   - **Given** the user is viewing `SAVED` (`~/ir-studio/remotes/*.ir`),
   - **When** the user presses `7 (PURGE)`,
   - **Then** `ir-studio` scans all User Library `.ir` files, identifies files whose buttons are all classified as `Ambient Noise` (`IsNoise == true`), shows a 2-step confirmation count (`"Purge N noise file(s)? [7=Confirm / Esc=Cancel]"`), and upon confirmation deletes ONLY those files from `~/ir-studio/remotes/`.
4. **MCP Tool `ir_purge_noise`**:
   - **Given** `mcp_enabled: true` and an MCP client calls `ir_purge_noise`,
   - **When** called with default arguments (`dry_run: true`),
   - **Then** it returns the list of candidate noise files in `~/ir-studio/remotes/` with their confidence scores without deleting anything; when called with `dry_run: false` and `confirm: true`, it deletes only those noise files and reports the deleted paths.

### Edge Cases

- **Mixed `.ir` file (valid buttons + 1 noisy button)**: A multi-button `.ir` file in `~/ir-studio/remotes/` containing at least one valid `TV/Remote` or `AC State-Frame` button (or any `type: parsed` button) MUST NOT be flagged for whole-file purge by `7 (PURGE)` / `ir_purge_noise`.
- **NaN / Inf / out-of-range feature values**: `ClassifyIR` clamps every input to `[0, 1]` and replaces `NaN`/`±Inf` with `0`, guaranteeing valid probabilities summing to `1.0 ± 1e-5`.
- **Parsed-only `.ir` files**: Files with `type: parsed` signals are always treated as valid non-noise (`TV/Remote` or `AC State-Frame` per protocol/bit-length) and never purged.
- **Zero noise files found**: `7 (PURGE)` displays a non-destructive toast `"No noise captures found"` and does not enter confirmation state.

### Invariants

1. **External-DB Read-Only Invariant**: `7 (PURGE)` and `ir_purge_noise` MUST NEVER scan for deletion or delete any file in `~/ir-studio/databases/<pack>/`.
2. **Probability Simplex Invariant**: `Probs[0] + Probs[1] + Probs[2]` MUST equal `1.0` (within `1e-5`), with each probability in `[0, 1]`.
3. **Zero-Allocation Inference Invariant**: `ClassifyIR(feat)` MUST execute with `0 allocs/op`.

### Verification Plan

- **Table-driven unit tests (`internal/irml/classifier_test.go`)**:
  - Representative fixtures for `TV/Remote` (NEC, RC5, SIRC, Samsung32), `AC State-Frame` (Daikin 152-bit, Gree 67-bit, Mitsubishi 144-bit), and `Ambient Noise` (100 Hz LED PWM, 120 Hz LED PWM, random 40–300 µs EMI spikes, empty/short glitches).
  - Verify `NaN`/`±Inf` sanitization, probability simplex sum, and `IsNoise` threshold (`>= 0.85`).
  - Verify mixed `.ir` file protection and External-DB read-only guard.
- **Benchmark (`BenchmarkClassifyIR`)**: Verify `0 allocs/op` and `< 5 µs/op`.

---

## US-04.3: 8D IR Fingerprint k-NN (Single-Button Remote Model Recovery)

**As a** user holding a half-broken remote where only 1 button (e.g. `Vol+` or `Mute`) still transmits,  
**I want** `ir-studio` to extract an 8D IR timing fingerprint from that single button and match it against `Flipper-IRDB`,  
**So that** I immediately see the exact remote model (`Match: Daikin_ARC480.ir (96%)`) and can save the entire multi-button `.ir` remote with one keypress.

### Domain Contract (`internal/irml/fingerprint.go`)

- **Value Object (`IREmbedding [8]float32`, L2-normalized unit vector)**:
  - `0: CarrierFreqNorm` — `(freqHz - 30000) / 30000` (defaults to `38000 Hz` when demodulated RX lacks carrier measurement)
  - `1: HeaderMarkNorm` — `clamp(headerMarkUs / 10000, 0, 1)`
  - `2: HeaderSpaceNorm` — `clamp(headerSpaceUs / 6000, 0, 1)`
  - `3: Bit0RatioNorm` — `clamp(shortUs / 1500, 0, 1)` (bit-0 symbol / base TE scale)
  - `4: Bit1RatioNorm` — `clamp(longShortRatio / 5.0, 0, 1)` (bit-1 to bit-0 pulse/space ratio)
  - `5: FrameLenNorm` — `clamp(pulsesPerFrame / 400, 0, 1)`
  - `6: RepeatPatternNorm` — `0.0` = single-shot, `0.5` = full-frame repeat, `1.0` = dedicated repeat code (e.g. NEC `9000/-2250/+560 µs`)
  - `7: DutyCycle` — `clamp(markTime / totalActiveFrameTime, 0, 1)`
- **Types**:
  - `IRFingerprintEntry` (`RemoteName`, `ButtonName`, `PackName`, `Path`, `CarrierHz`, `Protocol`, `Embedding`)
  - `IRMatchResult` (`RemoteName`, `ButtonName`, `PackName`, `Path`, `Protocol`, `Similarity float32`, `CarrierDeltaHz int64`)
  - `IRFingerprintIndex` (precomputed in-memory index over installed `Flipper-IRDB` packs + User Library; lazy-built on background goroutine so UI never stalls).
- **Carrier Gate Constant**:
  - `MaxCarrierDeltaHz int64 = 4000` (`±4 kHz` gate when both query and candidate have known measured carrier frequencies; bypassed if query `CarrierHz == 0` for demodulated TSOP38238 receivers).

### Acceptance Criteria (Given / When / Then)

1. **RAW & Parsed Embedding Synthesis**:
   - **Given** either a live RAW IR capture (`ComputeIREmbeddingFromRaw`) or a `type: parsed` button from a Flipper `.ir` file (`ComputeIREmbeddingFromParsed`),
   - **When** the 8D embedding is computed,
   - **Then** the resulting `[8]float32` vector is L2-normalized (`||v||_2 == 1.0 ± 1e-5`, or zero vector for invalid/empty input) and captures carrier frequency, header mark/space, bit-0/bit-1 ratios, frame length, repeat pattern, and duty cycle.
2. **Instant Single-Button Remote Identification**:
   - **Given** a populated `IRFingerprintIndex` built from installed `Flipper-IRDB` packs (e.g. `ACs`, `TVs`, `Audio`),
   - **When** the user captures **1 working button** from a remote in `RX` (e.g. a Daikin AC or Samsung TV button),
   - **Then** `FingerprintIndex.MatchTopK(query, carrierHz, k=3)` returns the highest-scoring matches descending by cosine similarity, and the `RX` UI displays the top match badge (e.g., `Match: Daikin_ARC480.ir (96%)` in Magenta `#e879f9`) whenever `Similarity >= 0.85`.
3. **One-Key Full-Remote Save (`PromoteRemoteToUser`)**:
   - **Given** a `Flipper-IRDB` match badge (`Match: <Remote>.ir (<pct>%)`) is displayed in `RX`,
   - **When** the user triggers the one-key promote action (`8 PROMOTE` / confirm),
   - **Then** `ir-studio` copies the **entire matched `.ir` remote file** (all buttons: Power, Temp+/-, Mode, Fan, Vol+/-, etc.) from the read-only `~/ir-studio/databases/<pack>/` into `~/ir-studio/remotes/` with collision-safe naming and shows a confirmation toast (`"Saved Daikin_ARC480.ir (14 buttons)"`).
4. **Zero-Allocation k-NN Matching**:
   - **Given** an `IRFingerprintIndex` of 2,000 entries,
   - **When** `CosineSimilarity` and `MatchBest` execute,
   - **Then** both report `0 allocs/op` and complete in `< 200 µs` on ARM64.

### Edge Cases

- **Zero-vector query or entry**: `CosineSimilarity` returns `0.0` (never `NaN` from `0/0`).
- **Demodulated receiver without carrier measurement (`queryCarrierHz == 0`)**: Carrier gate is relaxed (dimension 0 uses neutral `38 kHz` prior) so TSOP38238 captures still match `36–40 kHz` IRDB entries accurately.
- **Cross-carrier mismatch when measured (`36 kHz` vs `56 kHz`)**: `|queryCarrierHz - entry.CarrierHz| > 4000 Hz` rejects the candidate even if timing ratios look similar.
- **No match above threshold (`< 0.85`)**: UI displays `Match: None` in Muted (`#94a3b8`) and disables the promote shortcut.
- **Multiple buttons in the same `.ir` remote**: `MatchTopK` deduplicates by `Path` (keeping the highest-scoring button per remote file) so the top-3 list shows 3 distinct remote models rather than 3 buttons from the same file.

### Invariants

1. **Unit-Norm Invariant**: Every non-zero `IREmbedding` returned by `ComputeIREmbeddingFromRaw` or `ComputeIREmbeddingFromParsed` MUST satisfy `| ||v||_2 - 1.0 | < 1e-5`.
2. **Cosine Bounds Invariant**: `CosineSimilarity(a, b)` MUST always return a finite `float32` in `[0.0, 1.0]`.
3. **Deterministic Tie-Breaking Invariant**: Equal similarity scores (within `1e-6`) MUST break ties deterministically by `RemoteName` ascending, then `ButtonName` ascending.
4. **External-DB Immutability Invariant**: Matching and promoting MUST NEVER modify source `.ir` files in `~/ir-studio/databases/`.

### Verification Plan

- **Table-driven unit tests (`internal/irml/fingerprint_test.go`)**:
  - Verify `ComputeIREmbeddingFromRaw` and `ComputeIREmbeddingFromParsed` produce `>= 0.92` cosine similarity for matching protocol families (`NEC`, `Samsung32`, `RC5`, `RC6`, `SIRC`, `Daikin AC`) and `< 0.75` across distinct families (e.g. `NEC` vs `RC5` vs `Daikin AC`).
  - Verify carrier-gate rejection (`36 kHz` vs `56 kHz`), `queryCarrierHz == 0` relaxation, zero-vector safety, and per-remote deduplication in `MatchTopK`.
- **Benchmarks (`BenchmarkCosineSimilarity`, `BenchmarkIRFingerprintMatchBest`)**: Assert `0 allocs/op` and `< 200 µs` for 2,000 entries.

---

## US-04.4: Smart Probe Ranking (`R`)

**As a** user running Universal Remote `PROBE` against a TV or AC whose exact model is unknown,  
**I want** to press `R` to rank the active `PROBE` pack by 8D IR cosine similarity against my last captured IR button,  
**So that** `PROBE` transmits the closest brand, carrier, and protocol candidates first instead of scanning alphabetically from `Acer` to `Zenith`.

### Domain Contract (`internal/irml/fingerprint.go` + `internal/ui`)

- **Pure Function**:
  - `RankCandidatesByEmbedding(query IREmbedding, queryCarrierHz uint32, candidates []IRFingerprintEntry) []IRMatchResult`
- **UI State Integration (`ProbeState`)**:
  - `SmartRanked bool` — toggled by `R` in `PROBE` tab.
  - `LastQueryEmbedding IREmbedding` + `HasQueryEmbedding bool` — updated whenever a valid non-noise IR signal is captured in `RX` or `REMOTE` Learn.
- **MCP Integration**:
  - `ir_rank_probe_pack` accepts a pack name and either raw pulses or a saved remote/button reference, returning the top-N ranked remotes with similarity scores.

### Acceptance Criteria (Given / When / Then)

1. **Activation with Last Captured Signal**:
   - **Given** the user has captured at least one non-noise IR signal in `RX` (or `REMOTE` Learn) and switches to the `PROBE` tab with a pack loaded (e.g. `ACs` or `TVs`),
   - **When** the user presses `R`,
   - **Then** the `PROBE` candidate list is reordered descending by 8D IR cosine similarity against the last captured signal, the header displays a Magenta **`RANK:ML`** pill, each row shows its similarity percentage (e.g. `96%`), and the probe cursor resets to index `0` (highest similarity).
2. **Toggle Back to Default Order**:
   - **Given** `SmartRanked == true` in `PROBE`,
   - **When** the user presses `R` again,
   - **Then** the candidate list restores its default alphabetical order, the `RANK:ML` pill clears, and a toast confirms `"Probe order: Alphabetical"`.
3. **No Prior Capture Guard**:
   - **Given** no valid IR capture exists in the current session (`HasQueryEmbedding == false`),
   - **When** the user presses `R` in `PROBE`,
   - **Then** the list order does not change and an Amber toast explains `"Capture an IR button in RX first to enable Smart Rank"`.
4. **Safe Interaction with Active Auto-Probe**:
   - **Given** `ProbeSession` is actively running (`RUN`),
   - **When** the user presses `R`,
   - **Then** the active probe pauses safely (never mutating the slice concurrently with `TXWorker`), re-ranks the list, clears stale `RecentSent` rewind indices, and displays `"Ranked by IR ML — press 4 START"`.

### Edge Cases

- **Pack switch while `SmartRanked == true`**: Switching to a different `PROBE` pack automatically applies Smart Ranking to the new pack if `HasQueryEmbedding == true`, without crashing on empty packs.
- **Action-filter interaction**: Smart Ranking operates on the currently action-filtered candidate list (e.g. `ACs` filtered to `Power`), preserving the active action filter.
- **All candidates rejected by carrier gate**: Candidates outside `±4 kHz` are placed at the bottom with `0%` similarity rather than silently disappearing from the `PROBE` pack, so the user can still probe them if needed.

### Invariants

1. **Permutation Invariant**: Smart Ranking MUST be a strict permutation of the active candidate list (same count, no dropped or duplicated entries).
2. **Thread-Safety Invariant**: Re-ranking MUST NEVER mutate the candidate slice while `TXWorker` is reading it; `ProbeSession` MUST pause/cancel active TX before reordering.
3. **Determinism Invariant**: Identical query embeddings and packs MUST always produce the exact same ranked order.

### Verification Plan

- **Unit tests (`internal/irml/fingerprint_test.go`)**:
  - Verify `RankCandidatesByEmbedding` places matching-protocol/carrier candidates first, preserves all candidates (permutation check), handles empty slices, and breaks ties deterministically.
- **UI state tests (`internal/ui/view_probe_test.go`)**:
  - Verify `R` toggle with/without prior capture, pause-before-rerank safety under `-race`, and action-filter preservation.
