package irml

import (
	"math"
	"testing"
)

func makePWMPulses(freqHz, dutyPct float64, cycles int) []int32 {
	periodUs := 1_000_000.0 / freqHz
	markUs := int32(math.Round(periodUs * (dutyPct / 100.0)))
	spaceUs := int32(math.Round(periodUs - float64(markUs)))
	if markUs < 1 {
		markUs = 1
	}
	if spaceUs < 1 {
		spaceUs = 1
	}
	out := make([]int32, 0, cycles*2)
	for range cycles {
		out = append(out, markUs, -spaceUs)
	}
	return out
}

func TestAnalyzeFlickerIEEE1789Bands(t *testing.T) {
	tests := []struct {
		name      string
		pulses    []int32
		wantRisk  FlickerRisk
		wantLabel string
		wantFreq  float32
		tolHz     float32
		wantDuty  float32
	}{
		{
			name:      "120Hz_50pct_LED_HighFlicker",
			pulses:    makePWMPulses(120, 50, 20),
			wantRisk:  FlickerHigh,
			wantLabel: "HIGH_FLICKER",
			wantFreq:  120,
			tolHz:     1.0,
			wantDuty:  50,
		},
		{
			name:      "240Hz_25pct_OLED_HighFlicker",
			pulses:    makePWMPulses(240, 25, 24),
			wantRisk:  FlickerHigh,
			wantLabel: "HIGH_FLICKER",
			wantFreq:  240,
			tolHz:     1.5,
			wantDuty:  25,
		},
		{
			name:      "2000Hz_50pct_LowRisk",
			pulses:    makePWMPulses(2000, 50, 30),
			wantRisk:  FlickerLowRisk,
			wantLabel: "LOW_RISK",
			wantFreq:  2000,
			tolHz:     10.0,
			wantDuty:  50,
		},
		{
			name:      "4000Hz_50pct_SafeNOEL",
			pulses:    makePWMPulses(4000, 50, 30),
			wantRisk:  FlickerSafe,
			wantLabel: "SAFE",
			wantFreq:  4000,
			tolHz:     20.0,
			wantDuty:  50,
		},
		{
			name:      "120Hz_99.5pct_NearDC_Safe",
			pulses:    makePWMPulses(120, 99.5, 16),
			wantRisk:  FlickerSafe,
			wantLabel: "SAFE",
			wantFreq:  120,
			tolHz:     1.0,
			wantDuty:  99.5,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := AnalyzeFlicker(tc.pulses)
			if got.Risk != tc.wantRisk || got.RiskLabel != tc.wantLabel {
				t.Fatalf("risk = (%v, %q), want (%v, %q); full=%+v", got.Risk, got.RiskLabel, tc.wantRisk, tc.wantLabel, got)
			}
			if diff := float32(math.Abs(float64(got.FreqHz - tc.wantFreq))); diff > tc.tolHz {
				t.Fatalf("freq = %.2f Hz, want %.2f ± %.2f", got.FreqHz, tc.wantFreq, tc.tolHz)
			}
			if diff := float32(math.Abs(float64(got.DutyPct - tc.wantDuty))); diff > 1.5 {
				t.Fatalf("duty = %.2f%%, want %.2f%%", got.DutyPct, tc.wantDuty)
			}
			if got.JitterPct > 1.0 {
				t.Fatalf("expected low jitter for clean PWM, got %.2f%%", got.JitterPct)
			}
		})
	}
}

func TestAnalyzeFlickerAdversarial(t *testing.T) {
	tests := []struct {
		name   string
		pulses []int32
	}{
		{name: "nil_slice", pulses: nil},
		{name: "empty_slice", pulses: []int32{}},
		{name: "single_mark", pulses: []int32{1000}},
		{name: "single_cycle_too_few", pulses: []int32{4000, -4000}},
		{name: "all_spaces", pulses: []int32{-1000, -2000, -3000}},
		{name: "all_zeros", pulses: []int32{0, 0, 0, 0}},
		{name: "min_max_int32_no_panic", pulses: []int32{math.MinInt32, math.MaxInt32, math.MinInt32, math.MaxInt32}},
		{name: "extreme_jitter_noise", pulses: []int32{500, -500, 40000, -40000, 600, -600, 50000, -50000, 700, -700, 45000, -45000}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := AnalyzeFlicker(tc.pulses)
			if got.Risk != FlickerNone || got.RiskLabel != "NO_FLICKER" {
				t.Fatalf("expected FlickerNone/NO_FLICKER, got %+v", got)
			}
			if math.IsNaN(float64(got.FreqHz)) || math.IsInf(float64(got.FreqHz), 0) {
				t.Fatalf("non-finite FreqHz: %+v", got)
			}
		})
	}
}

func TestAnalyzeRPMBladeDivisionAndOutlierRejection(t *testing.T) {
	// 60 Hz blade-pass = 16667 us period (+3000 mark, -13667 space)
	// With 2 blades => 1800 RPM; 3 blades => 1200 RPM; 4 blades => 900 RPM.
	base := make([]int32, 0, 44)
	for range 18 {
		base = append(base, 3000, -13667)
	}
	withGlitches := make([]int32, 0, len(base)+6)
	withGlitches = append(withGlitches, 400, -400) // fast glitch
	withGlitches = append(withGlitches, base...)
	withGlitches = append(withGlitches, 50000, -50000) // slow dropout glitch

	tests := []struct {
		name      string
		pulses    []int32
		blades    int
		wantRPM   float32
		wantValid bool
	}{
		{name: "2_blades_1800_RPM_clean", pulses: base, blades: 2, wantRPM: 1800, wantValid: true},
		{name: "2_blades_1800_RPM_with_glitches", pulses: withGlitches, blades: 2, wantRPM: 1800, wantValid: true},
		{name: "3_blades_1200_RPM", pulses: base, blades: 3, wantRPM: 1200, wantValid: true},
		{name: "12_blades_300_RPM", pulses: base, blades: 12, wantRPM: 300, wantValid: true},
		{name: "1_blade_3600_RPM", pulses: base, blades: 1, wantRPM: 3600, wantValid: true},
		{name: "invalid_0_blades", pulses: base, blades: 0, wantValid: false},
		{name: "invalid_negative_blades", pulses: base, blades: -2, wantValid: false},
		{name: "invalid_13_blades", pulses: base, blades: 13, wantValid: false},
		{name: "nil_pulses", pulses: nil, blades: 2, wantValid: false},
		{name: "single_cycle", pulses: []int32{3000, -13667}, blades: 2, wantValid: false},
		{name: "min_max_int32_no_panic", pulses: []int32{math.MinInt32, math.MaxInt32, math.MinInt32}, blades: 2, wantValid: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := AnalyzeRPM(tc.pulses, tc.blades)
			if got.Valid != tc.wantValid {
				t.Fatalf("Valid = %v, want %v; full=%+v", got.Valid, tc.wantValid, got)
			}
			if !tc.wantValid {
				return
			}
			if diff := float32(math.Abs(float64(got.RPM - tc.wantRPM))); diff > 5.0 {
				t.Fatalf("RPM = %.2f, want %.2f ± 5.0; full=%+v", got.RPM, tc.wantRPM, got)
			}
			if got.StabilityPct < 80.0 || got.StabilityPct > 100.0 {
				t.Fatalf("unexpected StabilityPct %.2f; full=%+v", got.StabilityPct, got)
			}
		})
	}
}

var (
	benchFlickerSink FlickerReport
	benchRPMSink     RPMReport
)

func BenchmarkAnalyzeFlicker(b *testing.B) {
	pulses := makePWMPulses(120, 50, 64)
	b.ReportAllocs()
	for b.Loop() {
		benchFlickerSink = AnalyzeFlicker(pulses)
	}
}

func BenchmarkAnalyzeRPM(b *testing.B) {
	pulses := make([]int32, 0, 128)
	for range 64 {
		pulses = append(pulses, 3000, -13667)
	}
	b.ReportAllocs()
	for b.Loop() {
		benchRPMSink = AnalyzeRPM(pulses, 2)
	}
}
