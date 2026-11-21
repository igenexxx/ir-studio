package irml

const (
	// MinTachBlades and MaxTachBlades bound supported rotor blade counts.
	MinTachBlades = 1
	MaxTachBlades = 12

	maxTachPeriods     = 256
	minTachSamples     = 2
	minTachPeriodUs    = 200       // 5000 Hz blade-pass ceiling
	maxTachPeriodUs    = 1_000_000 // 1 Hz blade-pass floor
	tachOutlierTolFrac = 0.35      // Reject periods outside ±35% of median
)

// RPMReport summarizes an optical IR tachometer speed measurement.
type RPMReport struct {
	RPM          float32 `json:"rpm"`
	BladeFreqHz  float32 `json:"blade_freq_hz"`
	Blades       int     `json:"blades"`
	StabilityPct float32 `json:"stability_pct"`
	Samples      int     `json:"samples"`
	Valid        bool    `json:"valid"`
}

// AnalyzeRPM computes rotor RPM and blade-pass frequency from reflected signed
// microsecond pulses (+mark / -space) divided by blades, using a median filter
// to reject outlier glitches with zero heap allocations.
func AnalyzeRPM(pulses []int32, blades int) RPMReport {
	if blades < MinTachBlades || blades > MaxTachBlades {
		return RPMReport{Blades: blades, Valid: false}
	}

	var periods [maxTachPeriods]float32
	total := extractTachPeriods(pulses, &periods)
	if total < minTachSamples {
		return RPMReport{Blades: blades, Samples: total, Valid: false}
	}

	insertionSortFloat32(periods[:total])
	rawMedian := medianSortedFloat32(periods[:total])
	if rawMedian <= 0 {
		return RPMReport{Blades: blades, Samples: total, Valid: false}
	}

	inliers, filteredPeriod, relMAD := filterTachInliers(periods[:total], rawMedian)
	if inliers < minTachSamples || filteredPeriod <= 0 {
		return RPMReport{Blades: blades, Samples: inliers, Valid: false}
	}

	bladeFreqHz := float32(1_000_000.0) / filteredPeriod
	rpm := (bladeFreqHz * 60.0) / float32(blades)
	inlierRatio := float32(inliers) / float32(total)
	stability := clampPct((1.0 - relMAD) * inlierRatio * 100.0)

	return RPMReport{
		RPM:          rpm,
		BladeFreqHz:  bladeFreqHz,
		Blades:       blades,
		StabilityPct: stability,
		Samples:      inliers,
		Valid:        true,
	}
}

func extractTachPeriods(pulses []int32, periods *[maxTachPeriods]float32) int {
	n := 0
	var markUs, spaceUs int64
	inSpace := false

	for _, p := range pulses {
		if p == 0 {
			continue
		}
		if p > 0 {
			if inSpace && markUs > 0 && spaceUs > 0 {
				n = recordTachPeriod(markUs+spaceUs, n, periods)
				if n >= maxTachPeriods {
					return n
				}
				markUs, spaceUs, inSpace = 0, 0, false
			}
			markUs += int64(p)
			continue
		}
		if markUs == 0 {
			continue
		}
		spaceUs += -int64(p)
		inSpace = true
	}
	if inSpace && markUs > 0 && spaceUs > 0 && n < maxTachPeriods {
		n = recordTachPeriod(markUs+spaceUs, n, periods)
	}
	return n
}

func recordTachPeriod(periodUs int64, idx int, periods *[maxTachPeriods]float32) int {
	if periodUs < minTachPeriodUs || periodUs > maxTachPeriodUs {
		return idx
	}
	periods[idx] = float32(periodUs)
	return idx + 1
}

func filterTachInliers(sorted []float32, median float32) (count int, filteredPeriod float32, relMAD float32) {
	lo := median * (1.0 - tachOutlierTolFrac)
	hi := median * (1.0 + tachOutlierTolFrac)

	first, last := -1, -1
	for i, v := range sorted {
		if v >= lo && v <= hi {
			if first == -1 {
				first = i
			}
			last = i
		}
	}
	if first == -1 {
		return 0, 0, 1
	}

	inliers := sorted[first : last+1]
	filteredPeriod = medianSortedFloat32(inliers)
	if filteredPeriod <= 0 {
		return 0, 0, 1
	}
	var madSum float32
	for _, v := range inliers {
		d := v - filteredPeriod
		if d < 0 {
			d = -d
		}
		madSum += d
	}
	relMAD = (madSum / float32(len(inliers))) / filteredPeriod
	return len(inliers), filteredPeriod, relMAD
}
