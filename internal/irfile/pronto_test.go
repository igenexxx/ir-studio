package irfile

import (
	"errors"
	"math"
	"strings"
	"testing"
)

func TestProntoHexRoundTripAndCarrierMath(t *testing.T) {
	// Standard 38 kHz Pronto Hex (w1 = 0x006D -> 38029 Hz) with 2 once pairs + 1 repeat pair.
	sample := "0000 006D 0002 0001 0156 00AB 0016 0015 0016 0595"

	sig, err := ParseProntoHex("LG_Power_ON", sample)
	if err != nil {
		t.Fatalf("ParseProntoHex failed: %v", err)
	}
	if sig.Name != "LG_Power_ON" || sig.Kind != SignalRaw || sig.Protocol != "RAW" {
		t.Fatalf("unexpected signal metadata: %+v", sig)
	}
	wantFreq := uint32(math.Round(1_000_000.0 / (0x006D * ProntoClockPeriodUs)))
	if sig.Frequency != wantFreq {
		t.Fatalf("Frequency = %d, want %d", sig.Frequency, wantFreq)
	}
	if len(sig.RawData) != 6 {
		t.Fatalf("len(RawData) = %d, want 6", len(sig.RawData))
	}

	formatted, err := FormatProntoHex(sig)
	if err != nil {
		t.Fatalf("FormatProntoHex failed: %v", err)
	}
	reparsed, err := ParseProntoHex("LG_Power_ON", formatted)
	if err != nil {
		t.Fatalf("re-parse failed: %v", err)
	}
	if reparsed.Frequency != sig.Frequency {
		t.Fatalf("round-trip Frequency = %d, want %d", reparsed.Frequency, sig.Frequency)
	}
	if len(reparsed.RawData) != len(sig.RawData) {
		t.Fatalf("round-trip len = %d, want %d", len(reparsed.RawData), len(sig.RawData))
	}
	for i := range sig.RawData {
		diff := int(reparsed.RawData[i]) - int(sig.RawData[i])
		if diff < -27 || diff > 27 {
			t.Fatalf("pulse[%d] drifted: got %d, want %d", i, reparsed.RawData[i], sig.RawData[i])
		}
	}
}

func TestFormatProntoHexFromParsedNEC(t *testing.T) {
	nec := IRSignal{
		Name:      "Power",
		Kind:      SignalParsed,
		Protocol:  "NEC",
		Address:   [4]byte{0x04, 0x00, 0x00, 0x00},
		Command:   [4]byte{0x08, 0x00, 0x00, 0x00},
		Frequency: 38000,
		DutyCycle: 0.33,
	}
	hexStr, err := FormatProntoHex(nec)
	if err != nil {
		t.Fatalf("FormatProntoHex(NEC): %v", err)
	}
	if !strings.HasPrefix(hexStr, "0000 006D ") {
		t.Fatalf("expected 38 kHz 0000 006D header, got %q", hexStr[:min(16, len(hexStr))])
	}
	parsed, err := ParseProntoHex("Power", hexStr)
	if err != nil {
		t.Fatalf("ParseProntoHex(NEC export): %v", err)
	}
	pt, err := FromRawData(parsed.RawData)
	if err != nil {
		t.Fatalf("FromRawData: %v", err)
	}
	decoded, ok := DefaultRegistry().Decode(pt)
	if !ok || decoded.Protocol != "NEC" || decoded.Address[0] != 0x04 || decoded.Command[0] != 0x08 {
		t.Fatalf("NEC decode after Pronto round-trip failed: ok=%v decoded=%+v", ok, decoded)
	}
}

func TestParseProntoHexAdversarial(t *testing.T) {
	tests := []struct {
		name    string
		sigName string
		input   string
		wantErr error
	}{
		{name: "empty name", sigName: "  ", input: "0000 006D 0001 0000 0016 0015", wantErr: ErrInvalidSignal},
		{name: "empty input", sigName: "A", input: "", wantErr: ErrInvalidProntoHex},
		{name: "short header", sigName: "A", input: "0000 006D 0001", wantErr: ErrInvalidProntoHex},
		{name: "unsupported format 5000", sigName: "A", input: "5000 006D 0001 0000 0016 0015", wantErr: ErrInvalidProntoHex},
		{name: "zero carrier divider", sigName: "A", input: "0000 0000 0001 0000 0016 0015", wantErr: ErrInvalidProntoHex},
		{name: "carrier too high", sigName: "A", input: "0000 0001 0001 0000 0016 0015", wantErr: ErrInvalidProntoHex},
		{name: "carrier too low", sigName: "A", input: "0000 1000 0001 0000 0016 0015", wantErr: ErrInvalidProntoHex},
		{name: "zero burst pairs", sigName: "A", input: "0000 006D 0000 0000", wantErr: ErrInvalidProntoHex},
		{name: "truncated pair", sigName: "A", input: "0000 006D 0002 0000 0016 0015", wantErr: ErrInvalidProntoHex},
		{name: "extra trailing word", sigName: "A", input: "0000 006D 0001 0000 0016 0015 0016", wantErr: ErrInvalidProntoHex},
		{name: "zero duration word", sigName: "A", input: "0000 006D 0001 0000 0000 0015", wantErr: ErrInvalidProntoHex},
		{name: "non-hex token", sigName: "A", input: "0000 006D 0001 0000 ZZZZ 0015", wantErr: ErrInvalidProntoHex},
		{name: "5-digit token", sigName: "A", input: "0000 006D 0001 0000 00016 0015", wantErr: ErrInvalidProntoHex},
		{name: "exceeds max pairs", sigName: "A", input: "0000 006D 0201 0000", wantErr: ErrInvalidProntoHex},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseProntoHex(tc.sigName, tc.input)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("ParseProntoHex got err=%v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestFormatProntoHexAdversarial(t *testing.T) {
	tests := []struct {
		name    string
		sig     IRSignal
		wantErr error
	}{
		{name: "empty raw", sig: IRSignal{Name: "A", Kind: SignalRaw}, wantErr: ErrInvalidSignal},
		{name: "zero raw pulse", sig: IRSignal{Name: "A", Kind: SignalRaw, RawData: []uint32{500, 0}}, wantErr: ErrInvalidPulseTrain},
		{name: "carrier out of range", sig: IRSignal{Name: "A", Kind: SignalRaw, Frequency: 5000, RawData: []uint32{500, 500}}, wantErr: ErrInvalidProntoHex},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := FormatProntoHex(tc.sig)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("FormatProntoHex got err=%v, want %v", err, tc.wantErr)
			}
		})
	}
}

func FuzzParseProntoHex(f *testing.F) {
	f.Add("0000 006D 0002 0001 0156 00AB 0016 0015 0016 0595")
	f.Add("0000 0068 0001 0000 0020 0020")
	f.Add("5000 0000 0000 0001 0000 000C")
	f.Add("")
	f.Fuzz(func(t *testing.T, input string) {
		sig, err := ParseProntoHex("Fuzz", input)
		if err != nil {
			return
		}
		if len(sig.RawData) == 0 || len(sig.RawData)%2 != 0 {
			t.Fatalf("expected non-empty even RawData, got len=%d", len(sig.RawData))
		}
		if _, err := FormatProntoHex(sig); err != nil {
			t.Fatalf("FormatProntoHex failed on parsed signal: %v", err)
		}
	})
}
