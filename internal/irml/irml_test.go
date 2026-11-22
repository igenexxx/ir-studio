package irml

import (
	"encoding/binary"
	"math"
	"testing"
)

// --- Synthetic IR Fixture Builders ---

func buildNEC(bits uint32, jitter int32, repeats int) []int32 {
	p := make([]int32, 0, 70+repeats*4)
	p = append(p, 9000+jitter, -(4500 - jitter))
	for i := range 32 {
		j := jitter
		if i%2 == 1 {
			j = -jitter
		}
		p = append(p, 560+j)
		if (bits>>i)&1 == 1 {
			p = append(p, -(1690 - j))
		} else {
			p = append(p, -(560 + j))
		}
	}
	p = append(p, 560)
	for range repeats {
		p = append(p, -40000, 9000, -2250, 560)
	}
	return p
}

func buildSamsung32(bits uint32, jitter int32) []int32 {
	p := make([]int32, 0, 70)
	p = append(p, 4500+jitter, -(4500 - jitter))
	for i := range 32 {
		p = append(p, 560)
		if (bits>>i)&1 == 1 {
			p = append(p, -(1690 + jitter))
		} else {
			p = append(p, -(560 - jitter))
		}
	}
	p = append(p, 560)
	return p
}

func buildRC5(jitter int32) []int32 {
	raw := []int32{
		889, -889, 889, -889, 1778, -889, 889, -1778,
		889, -889, 1778, -1778, 889, -889, 889, -1778,
		1778, -889, 889, -889, 889, -889, 1778, -889,
	}
	out := make([]int32, len(raw))
	for i, v := range raw {
		if v > 0 {
			out[i] = v + jitter
		} else {
			out[i] = v - jitter
		}
	}
	return out
}

func buildSIRC(bits uint16, bitCount int, repeats int) []int32 {
	p := make([]int32, 0, (2+bitCount*2)*(1+repeats))
	for r := range 1 + repeats {
		if r > 0 {
			p = append(p, -25000)
		}
		p = append(p, 2400, -600)
		for i := range bitCount {
			if (bits>>i)&1 == 1 {
				p = append(p, 1200, -600)
			} else {
				p = append(p, 600, -600)
			}
		}
	}
	return p
}

func encodePulseDistanceSubframe(headerMark, headerSpace, markUs, zeroSpace, oneSpace int32, payload []byte, bitCount int) []int32 {
	p := make([]int32, 0, 3+bitCount*2)
	if headerMark > 0 {
		p = append(p, headerMark, -headerSpace)
	}
	for i := range bitCount {
		p = append(p, markUs)
		if (payload[i/8]>>(i%8))&1 == 1 {
			p = append(p, -oneSpace)
		} else {
			p = append(p, -zeroSpace)
		}
	}
	p = append(p, markUs)
	return p
}

func buildDaikinAC(tempByte byte) []int32 {
	sf1 := []byte{0x11, 0xDA, 0x27, 0x00, 0xC5, 0x00, 0x00, 0xD7}
	sf2 := []byte{0x11, 0xDA, 0x27, 0x00, 0x42, 0x00, 0x00, 0x54}
	sf3 := []byte{
		0x11, 0xDA, 0x27, 0x00, 0x00, 0x31, tempByte, 0x00,
		0xA0, 0x00, 0x00, 0x06, 0x60, 0x00, 0x00, 0xC1,
		0x80, 0x00, 0x00,
	}
	var sum byte
	for _, b := range sf3[:len(sf3)-1] {
		sum += b
	}
	sf3[len(sf3)-1] = sum

	var p []int32
	p = append(p, encodePulseDistanceSubframe(3500, 1750, 430, 430, 1300, sf1, len(sf1)*8)...)
	p = append(p, -25000)
	p = append(p, encodePulseDistanceSubframe(3500, 1750, 430, 430, 1300, sf2, len(sf2)*8)...)
	p = append(p, -25000)
	p = append(p, encodePulseDistanceSubframe(3500, 1750, 430, 430, 1300, sf3, len(sf3)*8)...)
	return p
}

func buildGreeAC() []int32 {
	// 35 bits + 32 bits = 67 bits (non-byte-aligned first subframe)
	sf1 := []byte{0x09, 0x0A, 0x20, 0x50, 0x05}
	sf2 := []byte{0x00, 0x20, 0x00, 0x20}
	var p []int32
	p = append(p, encodePulseDistanceSubframe(9000, 4500, 620, 540, 1660, sf1, 35)...)
	p = append(p, -20000)
	p = append(p, encodePulseDistanceSubframe(0, 0, 620, 540, 1660, sf2, 32)...)
	return p
}

func buildLEDNoise(periodUs int32, cycles int) []int32 {
	p := make([]int32, 0, cycles*2)
	half := periodUs / 2
	for range cycles {
		p = append(p, half, -half)
	}
	return p
}

func buildRandomEMINoise() []int32 {
	return []int32{
		45, -90, 310, -75, 1420, -40, 210, -1850,
		60, -95, 880, -35, 110, -520, 40, -1600,
		85, -50, 740, -95, 1950, -30, 150, -65,
	}
}

func assertSignAlternation(t *testing.T, pulses []int32) {
	t.Helper()
	for i, p := range pulses {
		if p == 0 {
			t.Fatalf("pulse[%d] == 0 violates non-zero invariant", i)
		}
		if i > 0 && (p > 0) == (pulses[i-1] > 0) {
			t.Fatalf("pulse[%d]=%d and pulse[%d]=%d have same sign", i-1, pulses[i-1], i, p)
		}
	}
}

// --- 1. Cluster & Denoise Tests ---

func TestClusterAndDenoiseEdgeCases(t *testing.T) {
	tests := []struct {
		name         string
		in           []int32
		wantValid    bool
		wantGlitches int
	}{
		{name: "nil input", in: nil, wantValid: false, wantGlitches: 0},
		{name: "empty input", in: []int32{}, wantValid: false, wantGlitches: 0},
		{name: "single pulse", in: []int32{560}, wantValid: false, wantGlitches: 0},
		{name: "all glitches", in: []int32{30, -45, 0, 110, -80}, wantValid: false, wantGlitches: 5},
		{name: "leading glitch dropped", in: []int32{-45, 9000, -4500, 560, -560, 560, -1690}, wantValid: true, wantGlitches: 1},
		{name: "uniform square wave", in: buildLEDNoise(2500, 10), wantValid: true, wantGlitches: 0},
		{name: "extreme int32 bounds", in: []int32{math.MinInt32, math.MaxInt32, 560, -1690, 560, -560, 560, -1690}, wantValid: true, wantGlitches: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			buf := make([]int32, 0, len(tc.in))
			out, cl := DenoisePulses(buf, tc.in, DefaultGlitchMaxUs)
			assertSignAlternation(t, out)
			if cl.Valid != tc.wantValid {
				t.Errorf("Valid = %v, want %v", cl.Valid, tc.wantValid)
			}
			if cl.GlitchesRemoved != tc.wantGlitches {
				t.Errorf("GlitchesRemoved = %d, want %d", cl.GlitchesRemoved, tc.wantGlitches)
			}
		})
	}
}

func TestDenoiseAbsorbsGlitchesAndQuantizesNEC(t *testing.T) {
	// Inject ±90 µs jitter and <120 µs EMI glitches into a valid NEC frame.
	raw := buildNEC(0x20DF10EF, 90, 0)
	// Inject a -45 µs glitch splitting a mark into two parts.
	injected := make([]int32, 0, len(raw)+4)
	injected = append(injected, raw[:2]...)
	injected = append(injected, 300, -45, 215) // 300+45+215 = 560 µs mark
	injected = append(injected, raw[3:]...)

	dst := make([]int32, 0, len(injected))
	out, cl := DenoisePulses(dst, injected, DefaultGlitchMaxUs)
	assertSignAlternation(t, out)

	if !cl.Valid {
		t.Fatal("expected valid clusters for jittered NEC frame")
	}
	if cl.GlitchesRemoved != 1 {
		t.Errorf("GlitchesRemoved = %d, want 1", cl.GlitchesRemoved)
	}
	if cl.HeaderUs < 8500 || cl.HeaderUs > 9500 {
		t.Errorf("HeaderUs = %.1f, want ~9000", cl.HeaderUs)
	}
	if cl.ShortUs < 480 || cl.ShortUs > 640 {
		t.Errorf("ShortUs = %.1f, want ~560", cl.ShortUs)
	}
	if cl.LongUs < 1550 || cl.LongUs > 1820 {
		t.Errorf("LongUs = %.1f, want ~1690", cl.LongUs)
	}
	if cl.EncodingHint != "Pulse-Distance" {
		t.Errorf("EncodingHint = %q, want Pulse-Distance", cl.EncodingHint)
	}
	if cl.QuantizedCount == 0 {
		t.Error("expected QuantizedCount > 0")
	}
	// Header mark/space must remain intact (not quantized to data centroids).
	if out[0] < 8500 || out[1] > -4000 {
		t.Errorf("header corrupted: out[0]=%d, out[1]=%d", out[0], out[1])
	}
}

func TestClusterEncodingHints(t *testing.T) {
	if cl := ClusterPulses(buildRC5(20)); cl.EncodingHint != "Bi-Phase/Manchester" {
		t.Errorf("RC5 EncodingHint = %q, want Bi-Phase/Manchester", cl.EncodingHint)
	}
	if cl := ClusterPulses(buildSIRC(0xA90, 12, 0)); cl.EncodingHint != "Pulse-Width" {
		t.Errorf("SIRC EncodingHint = %q, want Pulse-Width", cl.EncodingHint)
	}
}

// --- 2. Classifier Tests ---

func TestClassifySignalAccuracy(t *testing.T) {
	tests := []struct {
		name        string
		pulses      []int32
		carrierHz   uint32
		wantClass   SignalClass
		wantNoise   bool
		wantRepeats int
	}{
		{name: "empty", pulses: nil, carrierHz: 38000, wantClass: ClassUnknown},
		{name: "tiny glitch", pulses: []int32{40, -80, 50}, carrierHz: 38000, wantClass: ClassAmbientNoise, wantNoise: true},
		{name: "NEC single", pulses: buildNEC(0x00FF00FF, 30, 0), carrierHz: 38000, wantClass: ClassTVRemote},
		{name: "NEC with repeats", pulses: buildNEC(0x00FF00FF, 30, 2), carrierHz: 38000, wantClass: ClassTVRemote, wantRepeats: 2},
		{name: "Samsung32", pulses: buildSamsung32(0xE0E040BF, 35), carrierHz: 38000, wantClass: ClassTVRemote},
		{name: "RC5 36kHz", pulses: buildRC5(30), carrierHz: 36000, wantClass: ClassTVRemote},
		{name: "SIRC 40kHz", pulses: buildSIRC(0xA90, 12, 2), carrierHz: 40000, wantClass: ClassTVRemote, wantRepeats: 2},
		{name: "Daikin AC 3-subframe", pulses: buildDaikinAC(0x2C), carrierHz: 38000, wantClass: ClassACStateFrame},
		{name: "Gree AC 67-bit", pulses: buildGreeAC(), carrierHz: 38000, wantClass: ClassACStateFrame},
		{name: "100Hz LED flicker", pulses: buildLEDNoise(10000, 20), carrierHz: 38000, wantClass: ClassAmbientNoise, wantNoise: true},
		{name: "120Hz LED flicker", pulses: buildLEDNoise(8332, 20), carrierHz: 38000, wantClass: ClassAmbientNoise, wantNoise: true},
		{name: "400Hz LED flicker", pulses: buildLEDNoise(2500, 30), carrierHz: 38000, wantClass: ClassAmbientNoise, wantNoise: true},
		{name: "Random EMI noise", pulses: buildRandomEMINoise(), carrierHz: 0, wantClass: ClassAmbientNoise, wantNoise: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifySignal(tc.pulses, tc.carrierHz)
			if got.Class != tc.wantClass {
				t.Errorf("Class = %s (conf=%.3f, probs=%v), want %s", got.Class, got.Confidence, got.Probs, tc.wantClass)
			}
			if got.IsNoise != tc.wantNoise {
				t.Errorf("IsNoise = %v, want %v (conf=%.3f)", got.IsNoise, tc.wantNoise, got.Confidence)
			}
			if got.SaveGuardWarn != tc.wantNoise {
				t.Errorf("SaveGuardWarn = %v, want %v", got.SaveGuardWarn, tc.wantNoise)
			}
			if got.RepeatFrames != tc.wantRepeats {
				t.Errorf("RepeatFrames = %d, want %d", got.RepeatFrames, tc.wantRepeats)
			}
			if tc.wantClass != ClassUnknown {
				sum := got.Probs[0] + got.Probs[1] + got.Probs[2]
				if math.Abs(float64(sum)-1.0) > 1e-4 {
					t.Errorf("Probs sum = %f, want 1.0", sum)
				}
			}
		})
	}
}

func TestClassifyFeaturesSanitizesNaNAndInf(t *testing.T) {
	nan := float32(math.NaN())
	posInf := float32(math.Inf(1))
	negInf := float32(math.Inf(-1))
	feat := IRFeatureVector{nan, posInf, negInf, -5.0, 99.0, nan, 0.5, 0.1, 0.2, 0.25, 0.0, 0.5}
	got := ClassifyFeatures(feat)
	sum := got.Probs[0] + got.Probs[1] + got.Probs[2]
	if math.IsNaN(float64(sum)) || math.Abs(float64(sum)-1.0) > 1e-5 {
		t.Fatalf("invalid probability simplex after NaN/Inf input: %v (sum=%f)", got.Probs, sum)
	}
	if ClassUnknown.String() != "Unknown" {
		t.Errorf("unexpected Unknown string: %q", ClassUnknown.String())
	}
}

// --- 3. 8D Fingerprint & Carrier-Gated k-NN Tests ---

func TestExtractEmbeddingUnitNormAndSimilarity(t *testing.T) {
	if z := ExtractEmbedding(nil, 38000); !isZeroEmbedding(z) {
		t.Errorf("expected zero embedding for nil pulses, got %v", z)
	}

	necA := ExtractEmbedding(buildNEC(0x20DF10EF, 15, 0), 38000)
	necB := ExtractEmbedding(buildNEC(0x20DF40BF, 25, 0), 38000)
	rc5 := ExtractEmbedding(buildRC5(10), 36000)
	daikin := ExtractEmbedding(buildDaikinAC(0x2C), 38000)

	for name, emb := range map[string]IREmbedding{"NEC": necA, "RC5": rc5, "Daikin": daikin} {
		var normSq float64
		for _, v := range emb {
			normSq += float64(v) * float64(v)
		}
		if math.Abs(math.Sqrt(normSq)-1.0) > 1e-5 {
			t.Errorf("%s embedding norm = %f, want 1.0", name, math.Sqrt(normSq))
		}
	}

	if sim := CosineSimilarity(necA, necB); sim < 0.95 {
		t.Errorf("NEC-to-NEC similarity = %f, want >= 0.95", sim)
	}
	if sim := CosineSimilarity(necA, rc5); sim > 0.85 {
		t.Errorf("NEC-to-RC5 similarity = %f, want <= 0.85", sim)
	}
	if sim := CosineSimilarity(necA, daikin); sim > 0.90 {
		t.Errorf("NEC-to-Daikin similarity = %f, want <= 0.90", sim)
	}
	if sim := CosineSimilarity(IREmbedding{}, necA); sim != 0 {
		t.Errorf("zero vector similarity = %f, want 0", sim)
	}
	nanEmb := IREmbedding{float32(math.NaN())}
	if sim := CosineSimilarity(nanEmb, necA); sim != 0 {
		t.Errorf("NaN vector similarity = %f, want 0", sim)
	}
}

func TestIRFingerprintIndexCarrierGateAndDedup(t *testing.T) {
	var idx IRFingerprintIndex
	necEmb := ExtractEmbedding(buildNEC(0x20DF10EF, 0, 0), 38000)
	daikinEmb := ExtractEmbedding(buildDaikinAC(0x2C), 38000)
	rc5Emb := ExtractEmbedding(buildRC5(0), 36000)

	idx.Add(FingerprintEntry{RemoteID: "LG_TV.ir", Pack: "TVs", SignalName: "Power", CarrierHz: 38000, Embedding: necEmb})
	idx.Add(FingerprintEntry{RemoteID: "LG_TV.ir", Pack: "TVs", SignalName: "Vol_up", CarrierHz: 38000, Embedding: necEmb})
	idx.Add(FingerprintEntry{RemoteID: "BO_56k.ir", Pack: "Audio", SignalName: "Power", CarrierHz: 56000, Embedding: necEmb})
	idx.Add(FingerprintEntry{RemoteID: "Daikin_ARC480.ir", Pack: "ACs", SignalName: "Cool_22", CarrierHz: 38000, Embedding: daikinEmb})
	idx.Add(FingerprintEntry{RemoteID: "Philips_RC5.ir", Pack: "TVs", SignalName: "Mute", CarrierHz: 36000, Embedding: rc5Emb})

	if idx.Len() != 5 {
		t.Fatalf("Len() = %d, want 5", idx.Len())
	}

	// Query at 38 kHz must reject 56 kHz entry (> ±4 kHz), deduplicate LG_TV.ir,
	// and rank LG_TV.ir/Power first (SupCon 32D embeddings may place unrelated AC protocols at cos <= 0).
	dst := make([]MatchResult, 0, 3)
	matches := idx.Match(necEmb, 38000, dst)
	if len(matches) < 1 || len(matches) > 3 {
		t.Fatalf("len(matches) = %d, want 1..3", len(matches))
	}
	seenIDs := make(map[string]bool, len(matches))
	for _, m := range matches {
		if seenIDs[m.RemoteID] {
			t.Errorf("duplicate RemoteID %q returned by Match: %+v", m.RemoteID, matches)
		}
		seenIDs[m.RemoteID] = true
	}
	if matches[0].RemoteID != "LG_TV.ir" || matches[0].SignalName != "Power" {
		t.Errorf("top match = %+v, want LG_TV.ir/Power (tie-break ascending)", matches[0])
	}
	for _, m := range matches {
		if m.RemoteID == "BO_56k.ir" {
			t.Errorf("56 kHz candidate bypassed ±4 kHz carrier gate: %+v", m)
		}
	}

	// Query with carrierHz == 0 (demodulated TSOP receiver) relaxes carrier gate.
	relaxed := idx.Match(necEmb, 0, make([]MatchResult, 0, 4))
	found56k := false
	for _, m := range relaxed {
		if m.RemoteID == "BO_56k.ir" {
			found56k = true
		}
	}
	if !found56k {
		t.Error("expected queryCarrierHz == 0 to bypass carrier gate and include BO_56k.ir")
	}

	// RankCandidates preserves all entries as a strict permutation with out-of-gate scored 0.
	ranked := RankCandidates(necEmb, 38000, idx.entries, make([]MatchResult, 0, len(idx.entries)))
	if len(ranked) != len(idx.entries) {
		t.Fatalf("RankCandidates len = %d, want %d", len(ranked), len(idx.entries))
	}
	if ranked[len(ranked)-1].RemoteID != "BO_56k.ir" || ranked[len(ranked)-1].Similarity != 0 {
		t.Errorf("expected out-of-gate 56kHz candidate last with 0 sim, got %+v", ranked[len(ranked)-1])
	}
}

// --- 4. AC State-Frame Parser & Diff Tests ---

func TestParseACFrameAndDiff(t *testing.T) {
	cool22 := ParseACFrame(buildDaikinAC(0x2C)) // 22C
	cool24 := ParseACFrame(buildDaikinAC(0x30)) // 24C

	if !cool22.Valid || !cool24.Valid {
		t.Fatalf("expected valid Daikin AC frames: cool22=%v cool24=%v", cool22.Valid, cool24.Valid)
	}
	if len(cool22.SubFrames) != 3 {
		t.Fatalf("len(SubFrames) = %d, want 3", len(cool22.SubFrames))
	}
	if !cool22.ChecksumValid || !cool24.ChecksumValid {
		t.Errorf("expected valid SUM8 checksums: cool22=(%02X vs %02X) cool24=(%02X vs %02X)",
			cool22.ChecksumByte, cool22.ComputedSum8, cool24.ChecksumByte, cool24.ComputedSum8)
	}

	diff := DiffACFrames(cool22, cool24)
	if diff.LengthMismatch {
		t.Error("unexpected LengthMismatch on identical-length Daikin frames")
	}
	if !diff.ChecksumChanged {
		t.Error("expected ChecksumChanged == true")
	}
	if len(diff.Diffs) != 2 {
		t.Fatalf("len(Diffs) = %d, want 2 (temp byte + checksum byte): %+v", len(diff.Diffs), diff.Diffs)
	}
	if diff.Diffs[0].SubFrameIndex != 2 || diff.Diffs[0].ByteIndex != 6 || diff.Diffs[0].BitMask != (0x2C^0x30) {
		t.Errorf("unexpected temp diff: %+v", diff.Diffs[0])
	}

	// Identical frames produce 0 diffs.
	same := DiffACFrames(cool22, cool22)
	if len(same.Diffs) != 0 || same.TotalChangedBits != 0 {
		t.Errorf("expected 0 diffs for identical frames, got %+v", same)
	}

	// Gree 67-bit non-byte-aligned frame & length mismatch diff.
	gree := ParseACFrame(buildGreeAC())
	if !gree.Valid || len(gree.SubFrames) != 2 || gree.TotalBits != 67 {
		t.Fatalf("Gree AC parse failed: valid=%v subframes=%d bits=%d", gree.Valid, len(gree.SubFrames), gree.TotalBits)
	}
	mismatch := DiffACFrames(cool22, gree)
	if !mismatch.LengthMismatch {
		t.Error("expected LengthMismatch == true between Daikin and Gree")
	}

	// Empty and noisy inputs return Valid == false without panic.
	if ParseACFrame(nil).Valid || ParseACFrame(buildRandomEMINoise()).Valid {
		t.Error("expected Valid == false for nil and noisy inputs")
	}
}

// --- 5. Ambient IR Interference Monitor Tests ---

func TestAmbientMonitorLevelsFlickerAndDecay(t *testing.T) {
	var m AmbientMonitor
	const baseNs int64 = 10_000_000_000

	// Zero-value snapshot before any pulses is CLEAN.
	if s := m.Snapshot(baseNs); s.Level != AmbientClean || s.Level.String() != "CLEAN" {
		t.Fatalf("initial snapshot = %+v, want CLEAN", s)
	}

	// Clean idle: 4 edges/s, tiny mark duration.
	for i := range 4 {
		m.RecordPulse(baseNs+int64(i)*200_000_000, 100)
	}
	if s := m.Snapshot(baseNs + 800_000_000); s.Level != AmbientClean {
		t.Errorf("clean idle got %+v, want CLEAN", s)
	}

	// 120 Hz PWM LED lamp interference (~8333 µs period -> 120 marks/s + 120 spaces/s = 240 edges/s).
	m.Reset()
	tNs := baseNs
	for range 120 {
		m.RecordPulse(tNs, 1500)
		m.RecordPulse(tNs+1_500_000, -6833)
		tNs += 8_333_000
	}
	ledSnap := m.Snapshot(tNs)
	if ledSnap.Level != AmbientHigh {
		t.Errorf("120Hz LED level = %s, want HIGH", ledSnap.Level)
	}
	if ledSnap.FlickerHz < 110 || ledSnap.FlickerHz > 130 {
		t.Errorf("120Hz LED FlickerHz = %.1f, want ~120 Hz", ledSnap.FlickerHz)
	}
	if ledSnap.InterferenceHint != "LED/PWM" {
		t.Errorf("120Hz LED hint = %q, want LED/PWM", ledSnap.InterferenceHint)
	}

	// Sunlight saturation (> 25% duty without periodic LED flicker).
	m.Reset()
	for i := range 10 {
		m.RecordPulse(baseNs+int64(i)*80_000_000, 40000) // 400ms total mark in 1s = 40% duty
	}
	sunSnap := m.Snapshot(baseNs + 800_000_000)
	if sunSnap.Level != AmbientHigh || sunSnap.InterferenceHint != "Sunlight/AGC" {
		t.Errorf("sunlight snap = %+v, want HIGH / Sunlight/AGC", sunSnap)
	}

	// Window decay: 1.1 seconds after burst ends, monitor must decay back to CLEAN.
	decayed := m.Snapshot(baseNs + 2_000_000_000)
	if decayed.Level != AmbientClean || decayed.EdgesPerSec != 0 {
		t.Errorf("decayed snap = %+v, want CLEAN with 0 edges/s", decayed)
	}

	// Moderate level check (30 edges/s, low duty).
	m.Reset()
	for i := range 30 {
		m.RecordPulse(baseNs+int64(i)*25_000_000, 200)
	}
	modSnap := m.Snapshot(baseNs + 750_000_000)
	if modSnap.Level != AmbientModerate || modSnap.Level.String() != "MODERATE" {
		t.Errorf("moderate snap = %+v, want MODERATE", modSnap)
	}
}

// --- 6. Zero-Allocation Verification & Benchmarks ---

func TestZeroAllocHotPaths(t *testing.T) {
	nec := buildNEC(0x20DF10EF, 45, 1)
	dstPulses := make([]int32, 0, len(nec))
	emb := ExtractEmbedding(nec, 38000)

	var idx IRFingerprintIndex
	for range 200 {
		idx.Add(FingerprintEntry{RemoteID: "R", Pack: "TVs", SignalName: "Power", CarrierHz: 38000, Embedding: emb})
	}
	matchDst := make([]MatchResult, 0, 3)
	mon := NewAmbientMonitor()

	checks := []struct {
		name string
		fn   func()
	}{
		{"ClusterPulses", func() { _ = ClusterPulses(nec) }},
		{"DenoisePulses", func() { _, _ = DenoisePulses(dstPulses, nec, DefaultGlitchMaxUs) }},
		{"ExtractFeatures", func() { _ = ExtractFeatures(nec, 38000) }},
		{"ClassifySignal", func() { _ = ClassifySignal(nec, 38000) }},
		{"ExtractEmbedding", func() { _ = ExtractEmbedding(nec, 38000) }},
		{"IRFingerprintIndex.Match", func() { _ = idx.Match(emb, 38000, matchDst) }},
		{"AmbientMonitor.RecordPulse", func() { mon.RecordPulse(1_000_000_000, 560) }},
		{"AmbientMonitor.Snapshot", func() { _ = mon.Snapshot(1_000_000_000) }},
	}

	for _, c := range checks {
		allocs := testing.AllocsPerRun(50, c.fn)
		if allocs != 0 {
			t.Errorf("%s allocated %.1f allocs/op, want 0", c.name, allocs)
		}
	}
}

func BenchmarkClusterPulses(b *testing.B) {
	nec := buildNEC(0x20DF10EF, 45, 0)
	for b.Loop() {
		_ = ClusterPulses(nec)
	}
}

func BenchmarkDenoisePulses(b *testing.B) {
	nec := buildNEC(0x20DF10EF, 45, 0)
	dst := make([]int32, 0, len(nec))
	for b.Loop() {
		_, _ = DenoisePulses(dst, nec, DefaultGlitchMaxUs)
	}
}

func BenchmarkExtractFeatures(b *testing.B) {
	nec := buildNEC(0x20DF10EF, 45, 1)
	for b.Loop() {
		_ = ExtractFeatures(nec, 38000)
	}
}

func BenchmarkClassifySignal(b *testing.B) {
	nec := buildNEC(0x20DF10EF, 45, 1)
	for b.Loop() {
		_ = ClassifySignal(nec, 38000)
	}
}

func BenchmarkExtractEmbedding(b *testing.B) {
	nec := buildNEC(0x20DF10EF, 45, 1)
	for b.Loop() {
		_ = ExtractEmbedding(nec, 38000)
	}
}

func BenchmarkFingerprintMatch(b *testing.B) {
	necEmb := ExtractEmbedding(buildNEC(0x20DF10EF, 0, 0), 38000)
	daikinEmb := ExtractEmbedding(buildDaikinAC(0x2C), 38000)
	var idx IRFingerprintIndex
	for i := range 2000 {
		emb := daikinEmb
		if i%50 == 0 {
			emb = necEmb
		}
		idx.Add(FingerprintEntry{RemoteID: "Remote", Pack: "DB", SignalName: "Power", CarrierHz: 38000, Embedding: emb})
	}
	dst := make([]MatchResult, 0, 3)
	for b.Loop() {
		_ = idx.Match(necEmb, 38000, dst)
	}
}

func BenchmarkAmbientMonitorRecordPulse(b *testing.B) {
	mon := NewAmbientMonitor()
	var ts int64 = 1_000_000_000
	for b.Loop() {
		ts += 500_000
		mon.RecordPulse(ts, 560)
	}
}

// --- 7. Fuzz Tests ---

func FuzzDenoisePulses(f *testing.F) {
	f.Add([]byte{0x28, 0x23, 0x00, 0x00, 0x6C, 0xEE, 0xFF, 0xFF, 0x30, 0x02, 0x00, 0x00})
	f.Fuzz(func(t *testing.T, data []byte) {
		n := len(data) / 4
		pulses := make([]int32, n)
		for i := range n {
			pulses[i] = int32(binary.LittleEndian.Uint32(data[i*4:]))
		}
		out, _ := DenoisePulses(make([]int32, 0, n), pulses, DefaultGlitchMaxUs)
		assertSignAlternation(t, out)
	})
}

func FuzzParseACFrame(f *testing.F) {
	f.Add([]byte{0xAC, 0x0D, 0x00, 0x00, 0x2A, 0xF9, 0xFF, 0xFF})
	f.Fuzz(func(t *testing.T, data []byte) {
		n := len(data) / 4
		pulses := make([]int32, n)
		for i := range n {
			pulses[i] = int32(binary.LittleEndian.Uint32(data[i*4:]))
		}
		_ = ParseACFrame(pulses)
	})
}
