package irml

const (
	ambientWindowNs    int64 = 1_000_000_000 // 1.0 s sliding window
	ambientBucketCount int   = 20            // 50 ms per bucket
	ambientBucketNs    int64 = ambientWindowNs / int64(ambientBucketCount)

	ambientMinFlickerPeriodNs int64 = 1_000_000  // 1000 Hz upper bound
	ambientMaxFlickerPeriodNs int64 = 20_000_000 // 50 Hz lower bound
	ambientMinFlickerSamples  uint32 = 5

	ambientModerateEdges float32 = 15.0
	ambientHighEdges     float32 = 150.0
	ambientModerateDuty  float32 = 5.0
	ambientHighDuty      float32 = 25.0

	ambientEWMAAlpha float32 = 0.25
)

// AmbientLevel classifies background optical IR interference severity.
type AmbientLevel uint8

const (
	AmbientClean AmbientLevel = iota
	AmbientModerate
	AmbientHigh

	// AmbientSaturated is an alias for AmbientHigh (direct sunlight / AGC saturation).
	AmbientSaturated = AmbientHigh
)

// String returns the canonical uppercase badge label for AmbientLevel.
func (l AmbientLevel) String() string {
	switch l {
	case AmbientClean:
		return "CLEAN"
	case AmbientModerate:
		return "MODERATE"
	case AmbientHigh:
		return "HIGH"
	default:
		return "UNKNOWN"
	}
}

// AmbientSnapshot is an immutable, stack-allocated summary of ambient IR noise.
type AmbientSnapshot struct {
	Level            AmbientLevel
	EdgesPerSec      float32
	NoiseDutyPct     float32
	FlickerHz        float32
	InterferenceHint string
}

type ambientBucket struct {
	edges       uint32
	markUs      uint64
	periodSumUs uint64
	periodCnt   uint32
}

// AmbientMonitor tracks background IR edges over a 1-second sliding window
// with EWMA smoothing and zero heap allocations. Its zero value is ready to use.
type AmbientMonitor struct {
	buckets     [ambientBucketCount]ambientBucket
	headIdx     int
	headStartNs int64
	lastMarkNs  int64
	ewmaEdges   float32
	ewmaDuty    float32
	initialized bool
}

// NewAmbientMonitor returns a ready-to-use AmbientMonitor.
func NewAmbientMonitor() *AmbientMonitor {
	return &AmbientMonitor{}
}

// Reset clears all sliding-window buckets and EWMA state.
func (m *AmbientMonitor) Reset() {
	clear(m.buckets[:])
	m.headIdx = 0
	m.headStartNs = 0
	m.lastMarkNs = 0
	m.ewmaEdges = 0
	m.ewmaDuty = 0
	m.initialized = false
}

// RecordPulse records a single signed microsecond IR pulse edge at timestamp
// nowNs (monotonic nanoseconds). Zero heap allocations.
func (m *AmbientMonitor) RecordPulse(nowNs int64, pulseUs int32) {
	if pulseUs == 0 {
		return
	}
	m.advanceWindow(nowNs)
	b := &m.buckets[m.headIdx]
	b.edges++

	if pulseUs > 0 {
		b.markUs += uint64(absInt64(pulseUs))
		if m.lastMarkNs > 0 && nowNs > m.lastMarkNs {
			dt := nowNs - m.lastMarkNs
			if dt >= ambientMinFlickerPeriodNs && dt <= ambientMaxFlickerPeriodNs {
				b.periodSumUs += uint64(dt / 1000)
				b.periodCnt++
			}
		}
		m.lastMarkNs = nowNs
	}
}

// Snapshot expires stale buckets up to nowNs and returns the current ambient
// IR interference classification without heap allocations.
func (m *AmbientMonitor) Snapshot(nowNs int64) AmbientSnapshot {
	if !m.initialized {
		return AmbientSnapshot{Level: AmbientClean}
	}
	m.advanceWindow(nowNs)

	var totalEdges, periodCnt uint32
	var totalMarkUs, periodSumUs uint64
	for i := range m.buckets {
		totalEdges += m.buckets[i].edges
		totalMarkUs += m.buckets[i].markUs
		periodSumUs += m.buckets[i].periodSumUs
		periodCnt += m.buckets[i].periodCnt
	}

	edgesPerSec := float32(totalEdges)
	dutyPct := min(float32(totalMarkUs)/10000.0, 100.0)
	m.updateEWMA(edgesPerSec, dutyPct)

	flickerHz := float32(0)
	if periodCnt >= ambientMinFlickerSamples && periodSumUs > 0 {
		avgPeriodUs := float32(periodSumUs) / float32(periodCnt)
		flickerHz = 1_000_000.0 / avgPeriodUs
	}

	level := classifyAmbientLevel(edgesPerSec, dutyPct)
	return AmbientSnapshot{
		Level:            level,
		EdgesPerSec:      edgesPerSec,
		NoiseDutyPct:     dutyPct,
		FlickerHz:        flickerHz,
		InterferenceHint: ambientHint(level, dutyPct, flickerHz),
	}
}

func (m *AmbientMonitor) advanceWindow(nowNs int64) {
	if !m.initialized {
		m.headStartNs = nowNs
		m.initialized = true
		return
	}
	if nowNs < m.headStartNs {
		return
	}
	elapsed := nowNs - m.headStartNs
	if elapsed < ambientBucketNs {
		return
	}
	steps := int(elapsed / ambientBucketNs)
	if steps >= ambientBucketCount {
		clear(m.buckets[:])
		m.headIdx = 0
		m.headStartNs = nowNs
		m.lastMarkNs = 0
		return
	}
	for range steps {
		m.headIdx = (m.headIdx + 1) % ambientBucketCount
		m.buckets[m.headIdx] = ambientBucket{}
	}
	m.headStartNs += int64(steps) * ambientBucketNs
}

func (m *AmbientMonitor) updateEWMA(edges, duty float32) {
	if m.ewmaEdges == 0 && m.ewmaDuty == 0 {
		m.ewmaEdges = edges
		m.ewmaDuty = duty
		return
	}
	m.ewmaEdges = ambientEWMAAlpha*edges + (1-ambientEWMAAlpha)*m.ewmaEdges
	m.ewmaDuty = ambientEWMAAlpha*duty + (1-ambientEWMAAlpha)*m.ewmaDuty
}

func classifyAmbientLevel(edgesPerSec, dutyPct float32) AmbientLevel {
	switch {
	case edgesPerSec > ambientHighEdges || dutyPct > ambientHighDuty:
		return AmbientHigh
	case edgesPerSec >= ambientModerateEdges || dutyPct >= ambientModerateDuty:
		return AmbientModerate
	default:
		return AmbientClean
	}
}

func ambientHint(level AmbientLevel, dutyPct, flickerHz float32) string {
	if level == AmbientClean {
		return ""
	}
	if flickerHz >= 80 && flickerHz <= 450 {
		return "LED/PWM"
	}
	if dutyPct > ambientHighDuty {
		return "Sunlight/AGC"
	}
	return "Ambient IR"
}
