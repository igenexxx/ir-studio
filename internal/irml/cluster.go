package irml

import "math"

const (
	// DefaultGlitchMaxUs is the duration threshold below which pulses are
	// treated as EMI/AGC glitches and absorbed into adjacent pulses.
	DefaultGlitchMaxUs int32 = 120

	// DefaultHeaderMinUs separates IR AGC preamble headers and inter-frame
	// gaps from data bit symbols during 1D K-Means clustering.
	DefaultHeaderMinUs int32 = 2200

	// DefaultGapMinUs is the minimum negative space duration treated as an
	// inter-frame or AC sub-frame separator.
	DefaultGapMinUs int32 = 6000

	// MaxHeaderMarkUs is the upper bound for a valid IR AGC header mark.
	MaxHeaderMarkUs int64 = 12000

	// MaxHeaderSpaceUs is the upper bound for a valid IR AGC header space.
	MaxHeaderSpaceUs int64 = 6500

	// MaxKMeansIters bounds 1D K-Means convergence iterations.
	MaxKMeansIters = 20

	// MinDataPulses is the minimum number of data-range pulses required
	// to form valid timing clusters.
	MinDataPulses = 4

	// QuantizeTolerance is the maximum relative deviation (±35%) for
	// snapping a data pulse to its assigned K-Means centroid.
	QuantizeTolerance = 0.35
)

// PulseClusters holds 1D K-Means timing centroids and diagnostics for an IR
// pulse train. All fields are stack-allocated value types.
type PulseClusters struct {
	ShortUs         float64
	LongUs          float64
	HeaderUs        float64
	HeaderSpaceUs   float64
	GapUs           float64
	JitterRatio     float64
	GlitchesRemoved int
	DataPulses      int
	QuantizedCount  int
	EncodingHint    string
	Valid           bool
}

// ClusterPulses runs a zero-allocation 1D K-Means (k=2) clustering pass over
// the data-range mark/space durations of pulses.
func ClusterPulses(pulses []int32) PulseClusters {
	var c PulseClusters
	if len(pulses) == 0 {
		return c
	}

	minV, maxV := scanClusterBounds(pulses, &c)
	if c.DataPulses < MinDataPulses {
		c.JitterRatio = 1.0
		c.EncodingHint = "Unstructured"
		return c
	}

	c.ShortUs, c.LongUs = runKMeans1D(pulses, minV, maxV)
	c.JitterRatio = computeJitterRatio(pulses, c.ShortUs, c.LongUs)
	c.EncodingHint = inferEncodingHint(pulses, c.ShortUs, c.LongUs)
	c.Valid = true
	return c
}

// DenoisePulses absorbs sub-threshold EMI glitches (< glitchMaxUs) into
// adjacent pulses while preserving strict mark/space sign alternation, then
// quantizes data pulses within ±35% of their 1D K-Means centroids into dst.
// When cap(dst) >= len(src), DenoisePulses performs zero heap allocations.
func DenoisePulses(dst, src []int32, glitchMaxUs int32) ([]int32, PulseClusters) {
	if glitchMaxUs <= 0 {
		glitchMaxUs = DefaultGlitchMaxUs
	}
	out, glitches := absorbGlitches(dst, src, int64(glitchMaxUs))
	clusters := ClusterPulses(out)
	clusters.GlitchesRemoved = glitches
	if clusters.Valid {
		clusters.QuantizedCount = quantizeInPlace(out, clusters.ShortUs, clusters.LongUs)
	}
	return out, clusters
}

func absorbGlitches(dst, src []int32, glitchMax int64) ([]int32, int) {
	if len(src) == 0 {
		if dst == nil {
			return nil, 0
		}
		return dst[:0], 0
	}
	out := dst[:0]
	glitches := 0
	for _, p := range src {
		mag := absInt64(p)
		if mag == 0 || mag < glitchMax {
			glitches++
			if len(out) > 0 && mag > 0 {
				out[len(out)-1] = addMagnitude(out[len(out)-1], mag)
			}
			continue
		}
		if len(out) > 0 && (out[len(out)-1] > 0) == (p > 0) {
			out[len(out)-1] = addMagnitude(out[len(out)-1], mag)
			continue
		}
		out = append(out, clampSigned(p))
	}
	return out, glitches
}

func scanClusterBounds(pulses []int32, c *PulseClusters) (float64, float64) {
	minV := math.MaxFloat64
	maxV := 0.0
	for i, p := range pulses {
		mag := absInt64(p)
		if mag < int64(DefaultGlitchMaxUs) {
			c.GlitchesRemoved++
			continue
		}
		if p < 0 && mag > int64(DefaultGapMinUs) {
			if float64(mag) > c.GapUs {
				c.GapUs = float64(mag)
			}
			continue
		}
		if mag >= int64(DefaultHeaderMinUs) {
			recordHeader(pulses, i, p, mag, c)
			continue
		}
		v := float64(mag)
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
		c.DataPulses++
	}
	return minV, maxV
}

func recordHeader(pulses []int32, i int, p int32, mag int64, c *PulseClusters) {
	if c.HeaderUs == 0 && p > 0 && mag <= MaxHeaderMarkUs {
		c.HeaderUs = float64(mag)
		if i+1 < len(pulses) && pulses[i+1] < 0 {
			sp := absInt64(pulses[i+1])
			if sp <= MaxHeaderSpaceUs {
				c.HeaderSpaceUs = float64(sp)
			}
		}
	}
}

func runKMeans1D(pulses []int32, minV, maxV float64) (float64, float64) {
	if maxV-minV < 1.0 {
		return minV, maxV
	}
	c0 := minV + 0.25*(maxV-minV)
	c1 := minV + 0.75*(maxV-minV)
	for range MaxKMeansIters {
		var sum0, sum1 float64
		var cnt0, cnt1 int
		mid := 0.5 * (c0 + c1)
		for _, p := range pulses {
			mag := absInt64(p)
			if mag < int64(DefaultGlitchMaxUs) || mag >= int64(DefaultHeaderMinUs) {
				continue
			}
			v := float64(mag)
			if v <= mid {
				sum0 += v
				cnt0++
			} else {
				sum1 += v
				cnt1++
			}
		}
		n0, n1 := c0, c1
		if cnt0 > 0 {
			n0 = sum0 / float64(cnt0)
		}
		if cnt1 > 0 {
			n1 = sum1 / float64(cnt1)
		}
		if math.Abs(n0-c0) < 0.5 && math.Abs(n1-c1) < 0.5 {
			c0, c1 = n0, n1
			break
		}
		c0, c1 = n0, n1
	}
	if c0 > c1 {
		c0, c1 = c1, c0
	}
	return c0, c1
}

func computeJitterRatio(pulses []int32, shortUs, longUs float64) float64 {
	if shortUs <= 0 {
		return 1.0
	}
	mid := 0.5 * (shortUs + longUs)
	var errSum float64
	var count int
	for _, p := range pulses {
		mag := absInt64(p)
		if mag < int64(DefaultGlitchMaxUs) || mag >= int64(DefaultHeaderMinUs) {
			continue
		}
		v := float64(mag)
		target := shortUs
		if v > mid && longUs > 0 {
			target = longUs
		}
		errSum += math.Abs(v-target) / target
		count++
	}
	if count == 0 {
		return 1.0
	}
	return min(errSum/float64(count), 1.0)
}

func inferEncodingHint(pulses []int32, shortUs, longUs float64) string {
	if shortUs <= 0 || longUs/shortUs < 1.35 {
		return "Unstructured"
	}
	mid := 0.5 * (shortUs + longUs)
	var markShort, markLong, spaceShort, spaceLong int
	for _, p := range pulses {
		mag := absInt64(p)
		if mag < int64(DefaultGlitchMaxUs) || mag >= int64(DefaultHeaderMinUs) {
			continue
		}
		isLong := float64(mag) > mid
		if p > 0 {
			if isLong {
				markLong++
			} else {
				markShort++
			}
		} else {
			if isLong {
				spaceLong++
			} else {
				spaceShort++
			}
		}
	}
	markBimodal := markShort > 0 && markLong > 0
	spaceBimodal := spaceShort > 0 && spaceLong > 0
	switch {
	case markBimodal && spaceBimodal:
		return "Bi-Phase/Manchester"
	case spaceBimodal:
		return "Pulse-Distance"
	case markBimodal:
		return "Pulse-Width"
	default:
		return "Unstructured"
	}
}

func quantizeInPlace(out []int32, shortUs, longUs float64) int {
	if shortUs <= 0 {
		return 0
	}
	mid := 0.5 * (shortUs + longUs)
	quantized := 0
	for i, p := range out {
		mag := absInt64(p)
		if mag < int64(DefaultGlitchMaxUs) || mag >= int64(DefaultHeaderMinUs) {
			continue
		}
		v := float64(mag)
		target := shortUs
		if v > mid && longUs > 0 {
			target = longUs
		}
		if math.Abs(v-target)/target <= QuantizeTolerance {
			q := int32(math.Round(target))
			if p < 0 {
				q = -q
			}
			out[i] = q
			quantized++
		}
	}
	return quantized
}

func absInt64(v int32) int64 {
	x := int64(v)
	if x < 0 {
		return -x
	}
	return x
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func clampSigned(v int32) int32 {
	if v == math.MinInt32 {
		return -math.MaxInt32
	}
	return v
}

func addMagnitude(existing int32, delta int64) int32 {
	sum := absInt64(existing) + delta
	if sum > math.MaxInt32 {
		sum = math.MaxInt32
	}
	if existing < 0 {
		return -int32(sum)
	}
	return int32(sum)
}
