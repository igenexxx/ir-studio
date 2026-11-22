package irml

import (
	"cmp"
	"math"
)

const (
	// DefaultCarrierHz is the standard 38 kHz IR carrier prior used when
	// demodulated receivers do not measure carrier frequency.
	DefaultCarrierHz uint32 = 38000

	// MaxCarrierDeltaHz is the ±4 kHz carrier-frequency gate for k-NN matching.
	MaxCarrierDeltaHz int64 = 4000

	similarityTieEpsilon float32 = 1e-6
)

// IREmbedding is an L2-normalized 32D learned IR timing fingerprint produced by PulseNet-IR.
type IREmbedding [PulseNetEmbedDim]float32

// FingerprintEntry represents an indexed remote button in IRFingerprintIndex.
type FingerprintEntry struct {
	RemoteID   string
	Pack       string
	SignalName string
	CarrierHz  uint32
	Embedding  IREmbedding
}

// MatchResult represents a ranked similarity match from IRFingerprintIndex.
type MatchResult struct {
	RemoteID       string
	Pack           string
	SignalName     string
	Similarity     float32
	CarrierDeltaHz int64
}

// IRFingerprintIndex stores precomputed 8D IR embeddings for fast, zero-alloc
// carrier-gated cosine k-NN matching.
type IRFingerprintIndex struct {
	entries []FingerprintEntry
}

// ExtractEmbedding computes an L2-normalized 32D IR fingerprint from a signed
// microsecond pulse train using PulseNet-IR Head B.
func ExtractEmbedding(pulses []int32, carrierHz uint32) IREmbedding {
	var emb IREmbedding
	if len(pulses) == 0 {
		return emb
	}
	if m := defaultPulseNet(); m != nil {
		ws := pulseWS.Get().(*PulseNetWorkspace)
		res := m.Infer(ws, pulses, carrierHz)
		pulseWS.Put(ws)
		return res.Embedding
	}
	clusters := ClusterPulses(pulses)
	if !clusters.Valid {
		return emb
	}
	stats := analyzeFrameStats(pulses)
	freq := carrierHz
	if freq == 0 {
		freq = DefaultCarrierHz
	}
	ratio := 1.0
	if clusters.ShortUs > 0 {
		ratio = clusters.LongUs / clusters.ShortUs
	}
	duty := float64(0)
	if stats.activeUs > 0 {
		duty = float64(stats.markUs) / float64(stats.activeUs)
	}
	emb[0] = clampUnit(float32(float64(freq)-30000.0) / 30000.0)
	emb[1] = clampUnit(float32(clusters.HeaderUs / 10000.0))
	emb[2] = clampUnit(float32(clusters.HeaderSpaceUs / 6000.0))
	emb[3] = clampUnit(float32(clusters.ShortUs / 1500.0))
	emb[4] = clampUnit(float32(ratio / 5.0))
	emb[5] = clampUnit(float32(float64(stats.uniqueFramePulses) / 400.0))
	emb[6] = repeatPatternFeature(stats)
	emb[7] = clampUnit(float32(duty))
	return normalizeEmbedding(emb)
}

// CosineSimilarity computes the cosine similarity in [0, 1] between two
// L2-normalized IREmbedding vectors. Returns 0 for zero or non-finite vectors.
func CosineSimilarity(a, b IREmbedding) float32 {
	var dot, normA, normB float64
	for i := range a {
		va := float64(a[i])
		vb := float64(b[i])
		if math.IsNaN(va) || math.IsInf(va, 0) || math.IsNaN(vb) || math.IsInf(vb, 0) {
			return 0
		}
		dot += va * vb
		normA += va * va
		normB += vb * vb
	}
	if normA <= 0 || normB <= 0 {
		return 0
	}
	sim := dot / math.Sqrt(normA*normB)
	if sim < 0 {
		return 0
	}
	if sim > 1 {
		return 1
	}
	return float32(sim)
}

// Add appends a FingerprintEntry to the index.
func (idx *IRFingerprintIndex) Add(entry FingerprintEntry) {
	idx.entries = append(idx.entries, entry)
}

// Len returns the number of entries in the index.
func (idx *IRFingerprintIndex) Len() int {
	return len(idx.entries)
}

// Match finds the top-K matching remotes in the index using ±4 kHz carrier
// gating and cosine similarity, writing results descending into caller-owned
// dst without heap allocations. Capacity of dst determines K.
func (idx *IRFingerprintIndex) Match(query IREmbedding, queryCarrierHz uint32, dst []MatchResult) []MatchResult {
	k := cap(dst)
	if k == 0 || len(idx.entries) == 0 || isZeroEmbedding(query) {
		return dst[:0]
	}
	out := dst[:0]
	for i := range idx.entries {
		entry := &idx.entries[i]
		delta, ok := carrierWithinGate(queryCarrierHz, entry.CarrierHz)
		if !ok {
			continue
		}
		sim := CosineSimilarity(query, entry.Embedding)
		if sim <= 0 {
			continue
		}
		cand := MatchResult{
			RemoteID:       entry.RemoteID,
			Pack:           entry.Pack,
			SignalName:     entry.SignalName,
			Similarity:     sim,
			CarrierDeltaHz: delta,
		}
		out = insertTopK(out, cand, k)
	}
	return out
}

// RankCandidates ranks all candidates descending by cosine similarity into dst
// without dropping out-of-gate candidates (which receive similarity 0).
func RankCandidates(query IREmbedding, queryCarrierHz uint32, candidates []FingerprintEntry, dst []MatchResult) []MatchResult {
	out := dst[:0]
	for i := range candidates {
		c := &candidates[i]
		delta, ok := carrierWithinGate(queryCarrierHz, c.CarrierHz)
		sim := float32(0)
		if ok && !isZeroEmbedding(query) {
			sim = CosineSimilarity(query, c.Embedding)
		}
		out = append(out, MatchResult{
			RemoteID:       c.RemoteID,
			Pack:           c.Pack,
			SignalName:     c.SignalName,
			Similarity:     sim,
			CarrierDeltaHz: delta,
		})
	}
	sortMatchesInPlace(out)
	return out
}

func insertTopK(out []MatchResult, cand MatchResult, k int) []MatchResult {
	if cand.RemoteID != "" {
		for i := range out {
			if out[i].RemoteID == cand.RemoteID {
				if matchBetter(cand, out[i]) {
					out[i] = cand
					bubbleUpMatch(out, i)
				}
				return out
			}
		}
	}
	if len(out) < k {
		out = append(out, cand)
		bubbleUpMatch(out, len(out)-1)
		return out
	}
	if matchBetter(cand, out[k-1]) {
		out[k-1] = cand
		bubbleUpMatch(out, k-1)
	}
	return out
}

func bubbleUpMatch(out []MatchResult, idx int) {
	for idx > 0 && matchBetter(out[idx], out[idx-1]) {
		out[idx], out[idx-1] = out[idx-1], out[idx]
		idx--
	}
}

func sortMatchesInPlace(out []MatchResult) {
	for i := 1; i < len(out); i++ {
		bubbleUpMatch(out, i)
	}
}

func matchBetter(a, b MatchResult) bool {
	diff := a.Similarity - b.Similarity
	if diff > similarityTieEpsilon {
		return true
	}
	if diff < -similarityTieEpsilon {
		return false
	}
	if a.CarrierDeltaHz != b.CarrierDeltaHz {
		return a.CarrierDeltaHz < b.CarrierDeltaHz
	}
	if c := cmp.Compare(a.RemoteID, b.RemoteID); c != 0 {
		return c < 0
	}
	return cmp.Compare(a.SignalName, b.SignalName) < 0
}

func carrierWithinGate(queryHz, entryHz uint32) (int64, bool) {
	if queryHz == 0 || entryHz == 0 {
		return 0, true
	}
	delta := int64(queryHz) - int64(entryHz)
	if delta < 0 {
		delta = -delta
	}
	return delta, delta <= MaxCarrierDeltaHz
}

func repeatPatternFeature(st frameStats) float32 {
	switch {
	case st.hasShortRepeat:
		return 1.0
	case st.hasFullRepeat:
		return 0.5
	default:
		return 0.0
	}
}

func normalizeEmbedding(v IREmbedding) IREmbedding {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum <= 0 {
		return IREmbedding{}
	}
	inv := float32(1.0 / math.Sqrt(sum))
	for i := range v {
		v[i] *= inv
	}
	return v
}

func isZeroEmbedding(v IREmbedding) bool {
	for _, x := range v {
		if x != 0 {
			return false
		}
	}
	return true
}

func clampUnit(v float32) float32 {
	if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) || v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
