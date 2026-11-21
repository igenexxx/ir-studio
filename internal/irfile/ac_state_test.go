package irfile

import (
	"bytes"
	"errors"
	"testing"
)

func TestACRegistryAllBrandsEncodeAndChecksums(t *testing.T) {
	reg := DefaultACRegistry()
	brands := reg.Brands()
	if len(brands) != 6 {
		t.Fatalf("expected 6 AC brands, got %d: %v", len(brands), brands)
	}

	for _, brand := range brands {
		t.Run(string(brand), func(t *testing.T) {
			enc, ok := reg.Lookup(brand)
			if !ok {
				t.Fatalf("missing encoder for %s", brand)
			}

			s22 := ACState{Brand: brand, Power: true, Mode: ACModeCool, TempC: 22, Fan: ACFanHigh, Swing: true}
			s24 := ACState{Brand: brand, Power: true, Mode: ACModeCool, TempC: 24, Fan: ACFanHigh, Swing: true}
			sOff := ACState{Brand: brand, Power: false, Mode: ACModeCool, TempC: 22, Fan: ACFanAuto, Swing: false}

			b22, err := enc.EncodeBytes(s22)
			if err != nil {
				t.Fatalf("EncodeBytes(22C): %v", err)
			}
			b24, err := enc.EncodeBytes(s24)
			if err != nil {
				t.Fatalf("EncodeBytes(24C): %v", err)
			}
			bOff, err := enc.EncodeBytes(sOff)
			if err != nil {
				t.Fatalf("EncodeBytes(OFF): %v", err)
			}
			if bytes.Equal(b22, b24) {
				t.Fatalf("%s: 22C and 24C produced identical frames: %x", brand, b22)
			}
			if bytes.Equal(b22, bOff) {
				t.Fatalf("%s: ON and OFF produced identical frames: %x", brand, b22)
			}

			verifyBrandChecksum(t, brand, b22, s22)
			verifyBrandChecksum(t, brand, b24, s24)
			verifyBrandChecksum(t, brand, bOff, sOff)

			sig, err := enc.EncodeSignal(s22)
			if err != nil {
				t.Fatalf("EncodeSignal: %v", err)
			}
			if sig.Kind != SignalRaw || sig.Frequency != DefaultFreqHz || len(sig.RawData)%2 != 1 {
				t.Fatalf("%s: invalid RAW signal: kind=%s freq=%d len=%d", brand, sig.Kind, sig.Frequency, len(sig.RawData))
			}
			if sig.Name != "COOL_22C" {
				t.Fatalf("expected signal name COOL_22C, got %q", sig.Name)
			}
			for idx, v := range sig.RawData {
				if v == 0 {
					t.Fatalf("%s: zero pulse at index %d", brand, idx)
				}
			}

			// Verify Flipper .ir Write -> Parse round-trip.
			var buf bytes.Buffer
			if err := Write(&buf, &IRFile{Version: SupportedVersion, Signals: []IRSignal{sig}}); err != nil {
				t.Fatalf("Write .ir: %v", err)
			}
			parsed, err := Parse(&buf)
			if err != nil || len(parsed.Signals) != 1 || len(parsed.Signals[0].RawData) != len(sig.RawData) {
				t.Fatalf("Parse round-trip failed: parsed=%+v err=%v", parsed, err)
			}
		})
	}
}

func verifyBrandChecksum(t *testing.T, brand ACBrand, b []byte, s ACState) {
	t.Helper()
	switch brand {
	case ACBrandGree:
		if len(b) != 8 {
			t.Fatalf("Gree len=%d want 8", len(b))
		}
		swing := byte(0)
		if s.Swing {
			swing = 1
		}
		want := ((greeModeBits(s.Mode) + byte(s.TempC-ACMinTempC) + swing + 10) & 0x0F) << 4
		if b[7] != want {
			t.Fatalf("Gree checksum byte got 0x%02X, want 0x%02X", b[7], want)
		}
	case ACBrandMidea:
		if len(b) != 6 || b[1] != ^b[0] || b[3] != ^b[2] || b[5] != ^b[4] {
			t.Fatalf("Midea complement invariant failed: %x", b)
		}
	case ACBrandDaikin:
		if len(b) != 19 || acSum8(b[:18]) != b[18] {
			t.Fatalf("Daikin SUM8 failed: got 0x%02X want 0x%02X", b[18], acSum8(b[:18]))
		}
	case ACBrandLG:
		if len(b) != 4 {
			t.Fatalf("LG_AC len=%d want 4", len(b))
		}
		sum := (b[0] >> 4) + (b[0] & 0x0F) + (b[1] >> 4) + (b[1] & 0x0F) + (b[2] >> 4) + (b[2] & 0x0F)
		if (b[3] >> 4) != (sum & 0x0F) {
			t.Fatalf("LG_AC nibble sum failed: got %x want %x", b[3]>>4, sum&0x0F)
		}
	case ACBrandSamsung:
		if len(b) != 14 {
			t.Fatalf("Samsung_AC len=%d want 14", len(b))
		}
		if b[1] != samsungChecksumByte(b[0:7]) || b[8] != samsungChecksumByte(b[7:14]) {
			t.Fatalf("Samsung_AC subframe checksum failed: %x", b)
		}
	case ACBrandMitsubishi:
		if len(b) != 18 || acSum8(b[:17]) != b[17] {
			t.Fatalf("Mitsubishi SUM8 failed: got 0x%02X want 0x%02X", b[17], acSum8(b[:17]))
		}
	}
}

func TestValidateACStateErrors(t *testing.T) {
	reg := DefaultACRegistry()
	cases := []struct {
		name  string
		state ACState
		want  error
	}{
		{"temp_too_low", ACState{Brand: ACBrandGree, Mode: ACModeCool, TempC: 15}, ErrInvalidACState},
		{"temp_too_high", ACState{Brand: ACBrandGree, Mode: ACModeCool, TempC: 31}, ErrInvalidACState},
		{"bad_mode", ACState{Brand: ACBrandGree, Mode: "TURBO", TempC: 22}, ErrInvalidACState},
		{"bad_fan", ACState{Brand: ACBrandGree, Mode: ACModeCool, TempC: 22, Fan: "MAX"}, ErrInvalidACState},
		{"empty_brand", ACState{Brand: "", Mode: ACModeCool, TempC: 22}, ErrUnsupportedACBrand},
		{"unknown_brand", ACState{Brand: "UnknownAC", Mode: ACModeCool, TempC: 22}, ErrUnsupportedACBrand},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := reg.EncodeSignal(tc.state)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got err=%v, want %v", err, tc.want)
			}
		})
	}
}
