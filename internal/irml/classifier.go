package irml

import "math"

const (
	// NoiseConfidenceThreshold is the minimum Ambient Noise probability
	// required to flag IsNoise and trigger Smart Save Guard.
	NoiseConfidenceThreshold float32 = 0.85

	// MinValidSignalPulses is the minimum pulse count for a real IR frame.
	MinValidSignalPulses = 8

	mlpInputDim  = 12
	mlpHiddenDim = 24
	mlpOutputDim = 3
)

// SignalClass identifies the high-level IR signal category predicted by the
// on-device classifier.
type SignalClass uint8

const (
	ClassTVRemote SignalClass = iota
	ClassACStateFrame
	ClassAmbientNoise
	ClassUnknown
)

// String returns the canonical human-readable English label for SignalClass.
func (c SignalClass) String() string {
	switch c {
	case ClassTVRemote:
		return "TV/Remote"
	case ClassACStateFrame:
		return "AC State-Frame"
	case ClassAmbientNoise:
		return "Ambient Noise"
	default:
		return "Unknown"
	}
}

// IRFeatureVector is the stack-allocated [12]float32 normalized feature input
// to the on-device 12->24->3 MLP IR signal classifier.
type IRFeatureVector [mlpInputDim]float32

// Classification holds the on-device MLP prediction and Smart Save Guard flags.
type Classification struct {
	Class         SignalClass
	Confidence    float32
	IsNoise       bool
	SaveGuardWarn bool
	RepeatFrames  int
	Probs         [mlpOutputDim]float32
}

type frameStats struct {
	subframes         int
	uniqueFramePulses int
	repeatFrames      int
	hasShortRepeat    bool
	hasFullRepeat     bool
	markUs            int64
	activeUs          int64
}

func analyzeFrameStats(pulses []int32) frameStats {
	var st frameStats
	var curPulses, firstPulses int
	flush := func() {
		if curPulses == 0 {
			return
		}
		st.subframes++
		switch {
		case st.subframes > 1 && curPulses <= 4:
			st.repeatFrames++
			st.hasShortRepeat = true
		case st.subframes > 1 && firstPulses > 0 && firstPulses <= 105 && absInt(curPulses-firstPulses) <= 4:
			st.repeatFrames++
			st.hasFullRepeat = true
		default:
			st.uniqueFramePulses += curPulses
			if firstPulses == 0 {
				firstPulses = curPulses
			}
		}
		curPulses = 0
	}
	for _, p := range pulses {
		mag := absInt64(p)
		if mag < int64(DefaultGlitchMaxUs) {
			continue
		}
		if p < 0 && mag > int64(DefaultGapMinUs) {
			flush()
			continue
		}
		curPulses++
		st.activeUs += mag
		if p > 0 {
			st.markUs += mag
		}
	}
	flush()
	return st
}

// ExtractFeatures computes the normalized 12-feature IRFeatureVector from a
// signed microsecond pulse train and carrier frequency with zero allocations.
func ExtractFeatures(pulses []int32, carrierHz uint32) IRFeatureVector {
	var feat IRFeatureVector
	if len(pulses) == 0 {
		return feat
	}
	clusters := ClusterPulses(pulses)
	stats := analyzeFrameStats(pulses)

	freq := carrierHz
	if freq == 0 {
		freq = DefaultCarrierHz
	}
	ratio := 1.0
	if clusters.ShortUs > 0 {
		ratio = clusters.LongUs / clusters.ShortUs
	}
	duty := 0.0
	if stats.activeUs > 0 {
		duty = float64(stats.markUs) / float64(stats.activeUs)
	}

	hasHeader := float32(0.0)
	if clusters.HeaderUs >= float64(DefaultHeaderMinUs) && clusters.HeaderUs <= float64(MaxHeaderMarkUs) {
		hasHeader = 1.0
	}

	feat[0] = clampUnit(float32(float64(freq)-30000.0) / 30000.0)
	feat[1] = hasHeader
	feat[2] = clampUnit(float32(clusters.HeaderUs / 10000.0))
	feat[3] = clampUnit(float32(clusters.HeaderSpaceUs / 6000.0))
	feat[4] = clampUnit(float32(clusters.ShortUs / 1500.0))
	feat[5] = clampUnit(float32(clusters.LongUs / 3000.0))
	feat[6] = clampUnit(float32(ratio / 5.0))
	feat[7] = clampUnit(float32(clusters.JitterRatio))
	feat[8] = clampUnit(float32(float64(stats.uniqueFramePulses) / 300.0))
	feat[9] = clampUnit(float32(float64(stats.subframes) / 4.0))
	feat[10] = clampUnit(float32(float64(clusters.GlitchesRemoved) / float64(max(len(pulses), 1))))
	feat[11] = clampUnit(float32(duty))
	return feat
}

// ClassifySignal runs zero-allocation feature extraction and 12->24->3 MLP
// inference to distinguish TV/Remote frames, AC State-Frames, and Ambient Noise.
func ClassifySignal(pulses []int32, carrierHz uint32) Classification {
	if len(pulses) == 0 {
		return Classification{Class: ClassUnknown}
	}
	if len(pulses) < MinValidSignalPulses {
		probs := [mlpOutputDim]float32{0.02, 0.02, 0.96}
		return Classification{
			Class:         ClassAmbientNoise,
			Confidence:    probs[2],
			IsNoise:       true,
			SaveGuardWarn: true,
			Probs:         probs,
		}
	}
	feat := ExtractFeatures(pulses, carrierHz)
	stats := analyzeFrameStats(pulses)
	res := ClassifyFeatures(feat)
	res.RepeatFrames = stats.repeatFrames
	return res
}

// ClassifyFeatures runs the zero-allocation 12->24 ReLU -> 3 Softmax MLP on a
// pre-extracted IRFeatureVector, sanitizing any NaN/Inf/out-of-range inputs.
func ClassifyFeatures(feat IRFeatureVector) Classification {
	var clean IRFeatureVector
	for i := range feat {
		clean[i] = clampUnit(feat[i])
	}
	probs := mlpForward(clean)

	bestIdx := 0
	if probs[1] > probs[bestIdx] {
		bestIdx = 1
	}
	if probs[2] > probs[bestIdx] {
		bestIdx = 2
	}
	cls := SignalClass(bestIdx)
	conf := probs[bestIdx]
	isNoise := cls == ClassAmbientNoise && conf >= NoiseConfidenceThreshold

	return Classification{
		Class:         cls,
		Confidence:    conf,
		IsNoise:       isNoise,
		SaveGuardWarn: isNoise,
		Probs:         probs,
	}
}

func mlpForward(x IRFeatureVector) [mlpOutputDim]float32 {
	var hidden [mlpHiddenDim]float32
	for h := range mlpHiddenDim {
		sum := mlpB1[h]
		for i := range mlpInputDim {
			sum += mlpW1[h][i] * x[i]
		}
		if sum > 0 {
			hidden[h] = sum
		}
	}

	var logits [mlpOutputDim]float32
	maxLogit := float32(-math.MaxFloat32)
	for o := range mlpOutputDim {
		sum := mlpB2[o]
		for h := range mlpHiddenDim {
			sum += mlpW2[o][h] * hidden[h]
		}
		logits[o] = sum
		if sum > maxLogit {
			maxLogit = sum
		}
	}
	return softmax3(logits, maxLogit)
}

func softmax3(logits [mlpOutputDim]float32, maxLogit float32) [mlpOutputDim]float32 {
	var probs [mlpOutputDim]float32
	var sum float64
	for i := range mlpOutputDim {
		e := math.Exp(float64(logits[i] - maxLogit))
		probs[i] = float32(e)
		sum += e
	}
	if sum <= 0 {
		return [mlpOutputDim]float32{1.0 / 3.0, 1.0 / 3.0, 1.0 / 3.0}
	}
	inv := float32(1.0 / sum)
	for i := range mlpOutputDim {
		probs[i] *= inv
	}
	return probs
}
