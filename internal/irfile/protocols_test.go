package irfile_test

import (
	"encoding/binary"
	"errors"
	"testing"

	"ir-studio/internal/irfile"
)

func TestAllProtocolsRoundTripAndJitter(t *testing.T) {
	reg := irfile.DefaultRegistry()

	cases := []struct {
		name string
		sig  irfile.IRSignal
	}{
		{
			name: "NEC standard",
			sig:  irfile.IRSignal{Kind: irfile.SignalParsed, Protocol: "NEC", Address: [4]byte{0x04}, Command: [4]byte{0x08}},
		},
		{
			name: "NEC all ones",
			sig:  irfile.IRSignal{Kind: irfile.SignalParsed, Protocol: "NEC", Address: [4]byte{0xFF}, Command: [4]byte{0xAA}},
		},
		{
			name: "NECext 16-bit non-inverted",
			sig:  irfile.IRSignal{Kind: irfile.SignalParsed, Protocol: "NECext", Address: [4]byte{0x12, 0x34}, Command: [4]byte{0x56, 0x78}},
		},
		{
			name: "Samsung32",
			sig:  irfile.IRSignal{Kind: irfile.SignalParsed, Protocol: "Samsung32", Address: [4]byte{0x07}, Command: [4]byte{0x02}},
		},
		{
			name: "RC5 standard",
			sig:  irfile.IRSignal{Kind: irfile.SignalParsed, Protocol: "RC5", Address: [4]byte{0x05}, Command: [4]byte{0x0C}},
		},
		{
			name: "RC5X extended cmd bit 6",
			sig:  irfile.IRSignal{Kind: irfile.SignalParsed, Protocol: "RC5", Address: [4]byte{0x1F}, Command: [4]byte{0x7E}},
		},
		{
			name: "RC6 mode 0",
			sig:  irfile.IRSignal{Kind: irfile.SignalParsed, Protocol: "RC6", Address: [4]byte{0x04}, Command: [4]byte{0x5C}},
		},
		{
			name: "RC6 mode 0 trailing zero bit",
			sig:  irfile.IRSignal{Kind: irfile.SignalParsed, Protocol: "RC6", Address: [4]byte{0xA5}, Command: [4]byte{0x01}},
		},
		{
			name: "SIRC 12-bit",
			sig:  irfile.IRSignal{Kind: irfile.SignalParsed, Protocol: "SIRC", Address: [4]byte{0x01}, Command: [4]byte{0x15}},
		},
		{
			name: "SIRC 15-bit",
			sig:  irfile.IRSignal{Kind: irfile.SignalParsed, Protocol: "SIRC", Address: [4]byte{0x90}, Command: [4]byte{0x2A}},
		},
		{
			name: "SIRC 20-bit",
			sig:  irfile.IRSignal{Kind: irfile.SignalParsed, Protocol: "SIRC", Address: [4]byte{0x1A, 0x5B}, Command: [4]byte{0x3F}},
		},
		{
			name: "Kaseikyo Panasonic",
			sig:  irfile.IRSignal{Kind: irfile.SignalParsed, Protocol: "Kaseikyo", Address: [4]byte{0x02, 0x20, 0x08, 0x00}, Command: [4]byte{0x03, 0x3D}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pulses, err := reg.Encode(tc.sig)
			if err != nil {
				t.Fatalf("Encode(%s) failed: %v", tc.sig.Protocol, err)
			}
			if err := irfile.ValidatePulseTrain(pulses); err != nil {
				t.Fatalf("encoded pulse train invalid: %v", err)
			}
			// Exact round-trip.
			got, ok := reg.Decode(pulses)
			if !ok {
				t.Fatalf("Decode(%s) returned false", tc.sig.Protocol)
			}
			if got.Protocol != tc.sig.Protocol || got.Address != tc.sig.Address || got.Command != tc.sig.Command {
				t.Fatalf("round-trip mismatch: got %s addr=%X cmd=%X, want %s addr=%X cmd=%X",
					got.Protocol, got.Address, got.Command, tc.sig.Protocol, tc.sig.Address, tc.sig.Command)
			}

			// Photodiode distortion +-15% jitter MUST still decode identically.
			for _, pct := range []int{115, 85} {
				distorted := scalePulses(pulses, pct)
				gotJ, okJ := reg.Decode(distorted)
				if !okJ || gotJ.Protocol != tc.sig.Protocol || gotJ.Address != tc.sig.Address || gotJ.Command != tc.sig.Command {
					t.Fatalf("jitter %d%% failed for %s: ok=%v got=%+v", pct, tc.sig.Protocol, okJ, gotJ)
				}
			}

			// Severe +45% distortion MUST be rejected.
			severe := scalePulses(pulses, 145)
			if gotS, okS := reg.Decode(severe); okS && gotS.Protocol == tc.sig.Protocol {
				t.Fatalf("expected +45%% distortion to be rejected for %s", tc.sig.Protocol)
			}
		})
	}
}

func TestNECRepeatAndNegativeProtocolChecks(t *testing.T) {
	reg := irfile.DefaultRegistry()

	// NEC repeat frame.
	repPulses, err := reg.Encode(irfile.IRSignal{Kind: irfile.SignalParsed, Protocol: "NEC", Repeat: true})
	if err != nil {
		t.Fatalf("Encode NEC repeat: %v", err)
	}
	got, ok := reg.Decode(repPulses)
	if !ok || got.Protocol != "NEC" || !got.Repeat {
		t.Fatalf("Decode NEC repeat failed: ok=%v got=%+v", ok, got)
	}

	// Samsung32 with corrupted address duplicate MUST be rejected.
	sam, err := reg.Encode(irfile.IRSignal{Kind: irfile.SignalParsed, Protocol: "Samsung32", Address: [4]byte{0x07}, Command: [4]byte{0x02}})
	if err != nil {
		t.Fatalf("Encode Samsung32: %v", err)
	}
	// Flip bit 0 of byte 1 (space at index 2 + 8*2 + 1 = 19).
	sam[19] = -550
	if _, ok := reg.Decode(sam); ok {
		t.Fatal("expected Samsung32 with mismatched address duplicate to be rejected")
	}

	// Kaseikyo with corrupted XOR checksum MUST be rejected.
	kas, err := reg.Encode(irfile.IRSignal{Kind: irfile.SignalParsed, Protocol: "Kaseikyo", Address: [4]byte{0x02, 0x20, 0x08, 0x00}, Command: [4]byte{0x03, 0x3D}})
	if err != nil {
		t.Fatalf("Encode Kaseikyo: %v", err)
	}
	// Flip last bit space (index len-2).
	if kas[len(kas)-2] == -420 {
		kas[len(kas)-2] = -1260
	} else {
		kas[len(kas)-2] = -420
	}
	if _, ok := reg.Decode(kas); ok {
		t.Fatal("expected Kaseikyo with bad checksum to be rejected")
	}
}

func TestRawAndRegistryEdgeCases(t *testing.T) {
	reg := irfile.DefaultRegistry()
	if len(reg.Names()) != 8 {
		t.Fatalf("got %d registered codecs, want 8", len(reg.Names()))
	}

	// Unknown pulse train -> Decode returns false, DecodeOrRaw returns SignalRaw.
	unknown := irfile.PulseTrain{3000, -1500, 700, -700, 700, -2100, 700}
	if _, ok := reg.Decode(unknown); ok {
		t.Fatal("expected unknown AC-like pulse train to return false from Decode")
	}
	rawSig, err := reg.DecodeOrRaw(unknown, irfile.CarrierSpec{FreqHz: 40000, DutyPct: 50})
	if err != nil {
		t.Fatalf("DecodeOrRaw failed: %v", err)
	}
	if rawSig.Kind != irfile.SignalRaw || rawSig.Frequency != 40000 || len(rawSig.RawData) != 7 {
		t.Fatalf("unexpected RAW fallback signal: %+v", rawSig)
	}

	// RAW encode round-trip.
	encoded, err := reg.Encode(rawSig)
	if err != nil || len(encoded) != 7 {
		t.Fatalf("Encode RAW failed: %v (len=%d)", err, len(encoded))
	}

	// Unsupported protocol error.
	_, err = reg.Encode(irfile.IRSignal{Kind: irfile.SignalParsed, Protocol: "DoesNotExist"})
	if !errors.Is(err, irfile.ErrUnsupportedProtocol) {
		t.Fatalf("got err=%v, want ErrUnsupportedProtocol", err)
	}

	// Verify Carrier() on all 8 registered codecs + Register replace & nil no-op.
	reg.Register(nil)
	for _, name := range reg.Names() {
		codec, ok := reg.Lookup(name)
		if !ok {
			t.Fatalf("Lookup(%q) failed", name)
		}
		c := codec.Carrier()
		if c.IsZero() || c.FreqHz == 0 || c.DutyFloat() <= 0 {
			t.Fatalf("codec %q returned invalid Carrier: %+v", name, c)
		}
		reg.Register(codec) // replace existing entry in place
	}
	if _, err := reg.DecodeOrRaw(nil, irfile.CarrierSpec{}); !errors.Is(err, irfile.ErrInvalidPulseTrain) {
		t.Fatalf("DecodeOrRaw(nil) = %v, want ErrInvalidPulseTrain", err)
	}
	// DecodeOrRaw with zero CarrierSpec uses defaults.
	rawDef, err := reg.DecodeOrRaw(unknown, irfile.CarrierSpec{})
	if err != nil || rawDef.Frequency != irfile.DefaultFreqHz || rawDef.CarrierSpec().FreqHz != irfile.DefaultFreqHz {
		t.Fatalf("DecodeOrRaw(zero carrier) = (%+v, %v)", rawDef, err)
	}
	if !(irfile.CarrierSpec{}).IsZero() || (irfile.CarrierSpec{}).DutyFloat() != irfile.DefaultDutyCycle {
		t.Fatal("zero CarrierSpec helper mismatch")
	}
}

func scalePulses(in irfile.PulseTrain, pct int) irfile.PulseTrain {
	out := make(irfile.PulseTrain, len(in))
	for i, v := range in {
		out[i] = int32(int64(v) * int64(pct) / 100)
	}
	return out
}

func FuzzDecodePulses(f *testing.F) {
	reg := irfile.DefaultRegistry()
	if p, err := reg.Encode(irfile.IRSignal{Kind: irfile.SignalParsed, Protocol: "NEC", Address: [4]byte{4}, Command: [4]byte{8}}); err == nil {
		f.Add(pulsesToBytes(p))
	}
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		n := min(len(data)/4, 256)
		pulses := make(irfile.PulseTrain, n)
		for i := range n {
			pulses[i] = int32(binary.LittleEndian.Uint32(data[i*4:]))
		}
		_, _ = reg.Decode(pulses)
	})
}

func pulsesToBytes(p irfile.PulseTrain) []byte {
	out := make([]byte, len(p)*4)
	for i, v := range p {
		binary.LittleEndian.PutUint32(out[i*4:], uint32(v))
	}
	return out
}
