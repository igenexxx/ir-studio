package irml

const (
	maxFlickerPeriods   = 256
	minFlickerPeriods   = 2
	minFlickerPeriodUs  = 100     // 10 kHz upper bound
	maxFlickerPeriodUs  = 200_000 // 5 Hz lower bound
	flickerSafeHz       = 3125.0  // IEEE 1789-2015 NOEL at 100% PWM modulation
	flickerLowRiskHz    = 1250.0  // IEEE 1789-2015 Low-Risk at 100% PWM modulation
	flickerMaxJitterPct = 45.0    // Above this jitter, signal is non-periodic noise
	flickerDCPctHigh    = 99.0
	flickerDCPctLow     = 1.0
)

// FlickerRisk classifies optical PWM flicker eye-safety risk per IEEE 1789-2015.
type FlickerRisk uint8

const (
	FlickerNone FlickerRisk = iota
	FlickerSafe
	FlickerLowRisk
	FlickerHigh
)

// String returns the canonical uppercase risk label for FlickerRisk.
func (r FlickerRisk) String() string {
	switch r {
	case FlickerNone:
		return "NO_FLICKER"
	case FlickerSafe:
		return "SAFE"
	case FlickerLowRisk:
		return "LOW_RISK"
	case FlickerHigh:
		return "HIGH_FLICKER"
	default:
		return "UNKNOWN"
	}
}

// FlickerReport summarizes measured LED/screen PWM flicker characteristics.
type FlickerReport struct {
	FreqHz    float32     `json:"freq_hz"`
	DutyPct   float32     `json:"duty_pct"`
	JitterPct float32     `json:"jitter_pct"`
	Periods   int         `json:"periods"`
	Risk      FlickerRisk `json:"risk"`
	RiskLabel string      `json:"risk_label"`
}

// AnalyzeFlicker extracts dominant PWM frequency, duty cycle, timing jitter,
// and IEEE 1789 eye-safety classification from alternating signed microsecond
// pulses (+mark / -space) with zero heap allocations.
func AnalyzeFlicker(pulses []int32) FlickerReport {
	var periods [maxFlickerPeriods]float32
	var duties [maxFlickerPeriods]float32

	n := extractFlickerCycles(pulses, &periods, &duties)
	if n < minFlickerPeriods {
		return noFlickerReport(n)
	}

	insertionSortFloat32(periods[:n])
	insertionSortFloat32(duties[:n])

	medianPeriod := medianSortedFloat32(periods[:n])
	if medianPeriod <= 0 {
		return noFlickerReport(n)
	}
	medianDuty := clampPct(medianSortedFloat32(duties[:n]))
	jitterPct := computeJitterPct(periods[:n], medianPeriod)
	if jitterPct > flickerMaxJitterPct {
		return noFlickerReport(n)
	}

	freqHz := float32(1_000_000.0) / medianPeriod
	risk := classifyFlickerRisk(freqHz, medianDuty)
	return FlickerReport{
		FreqHz:    freqHz,
		DutyPct:   medianDuty,
		JitterPct: jitterPct,
		Periods:   n,
		Risk:      risk,
		RiskLabel: risk.String(),
	}
}

func noFlickerReport(periods int) FlickerReport {
	return FlickerReport{
		Periods:   periods,
		Risk:      FlickerNone,
		RiskLabel: FlickerNone.String(),
	}
}

func extractFlickerCycles(pulses []int32, periods, duties *[maxFlickerPeriods]float32) int {
	n := 0
	var markUs, spaceUs int64
	inSpace := false

	for _, p := range pulses {
		if p == 0 {
			continue
		}
		if p > 0 {
			if inSpace && markUs > 0 && spaceUs > 0 {
				n = recordFlickerCycle(markUs, spaceUs, n, periods, duties)
				if n >= maxFlickerPeriods {
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
	if inSpace && markUs > 0 && spaceUs > 0 && n < maxFlickerPeriods {
		n = recordFlickerCycle(markUs, spaceUs, n, periods, duties)
	}
	return n
}

func recordFlickerCycle(markUs, spaceUs int64, idx int, periods, duties *[maxFlickerPeriods]float32) int {
	total := markUs + spaceUs
	if total < minFlickerPeriodUs || total > maxFlickerPeriodUs {
		return idx
	}
	periods[idx] = float32(total)
	duties[idx] = (float32(markUs) / float32(total)) * 100.0
	return idx + 1
}

func classifyFlickerRisk(freqHz, dutyPct float32) FlickerRisk {
	if dutyPct >= flickerDCPctHigh || dutyPct <= flickerDCPctLow {
		return FlickerSafe
	}
	if freqHz >= flickerSafeHz {
		return FlickerSafe
	}
	if freqHz >= flickerLowRiskHz {
		return FlickerLowRisk
	}
	return FlickerHigh
}

func computeJitterPct(sorted []float32, median float32) float32 {
	if len(sorted) == 0 || median <= 0 {
		return 0
	}
	var madSum float32
	for _, v := range sorted {
		diff := v - median
		if diff < 0 {
			diff = -diff
		}
		madSum += diff
	}
	mad := madSum / float32(len(sorted))
	return clampPct((mad / median) * 100.0)
}

func insertionSortFloat32(vals []float32) {
	for i := 1; i < len(vals); i++ {
		key := vals[i]
		j := i - 1
		for j >= 0 && vals[j] > key {
			vals[j+1] = vals[j]
			j--
		}
		vals[j+1] = key
	}
}

func medianSortedFloat32(sorted []float32) float32 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	mid := n / 2
	if n%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) * 0.5
}

func clampPct(v float32) float32 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}
