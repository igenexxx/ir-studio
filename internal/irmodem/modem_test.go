package irmodem

import (
	"bytes"
	"errors"
	"testing"

	"ir-studio/internal/irfile"
)

func TestModemRoundTripPayloads(t *testing.T) {
	maxBuf := make([]byte, MaxPayloadBytes)
	for i := range maxBuf {
		maxBuf[i] = byte(i * 7)
	}

	cases := []struct {
		name    string
		payload []byte
	}{
		{name: "empty", payload: []byte{}},
		{name: "single_byte", payload: []byte{0x42}},
		{name: "ascii_text", payload: []byte("CardputerZero 38kHz Optical Link!")},
		{name: "all_zeros", payload: make([]byte, 16)},
		{name: "all_ones", payload: bytes.Repeat([]byte{0xFF}, 16)},
		{name: "max_64_bytes", payload: maxBuf},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pulses, err := EncodeFrame(tc.payload)
			if err != nil {
				t.Fatalf("EncodeFrame failed: %v", err)
			}
			if _, err := pulses.ToLIRCPulses(); err != nil {
				t.Fatalf("ToLIRCPulses failed: %v", err)
			}
			got, err := DecodeFrame(pulses)
			if err != nil {
				t.Fatalf("DecodeFrame failed: %v", err)
			}
			if !bytes.Equal(got, tc.payload) {
				t.Fatalf("payload mismatch: got %x, want %x", got, tc.payload)
			}
		})
	}
}

func TestModemJitterResilience120Us(t *testing.T) {
	payload := []byte("HELLO-IR-MODEM-2026")
	pulses, err := EncodeFrame(payload)
	if err != nil {
		t.Fatalf("EncodeFrame: %v", err)
	}

	// Inject deterministic ±120 µs jitter on every single mark and space.
	jittered := make(irfile.PulseTrain, len(pulses))
	for i, p := range pulses {
		delta := int32(120)
		if i%2 == 1 {
			delta = -120
		}
		if p > 0 {
			jittered[i] = p + delta
		} else {
			jittered[i] = p - delta
		}
	}

	got, err := DecodeFrame(jittered)
	if err != nil {
		t.Fatalf("DecodeFrame under ±120µs jitter failed: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("got %q, want %q", got, payload)
	}
}

func TestModemAdversarialErrors(t *testing.T) {
	// 1. Payload > 64 bytes rejected.
	if _, err := EncodeFrame(make([]byte, MaxPayloadBytes+1)); !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("65B payload got err=%v, want ErrPayloadTooLarge", err)
	}

	// 2. Empty / invalid pulse train rejected.
	if _, err := DecodeFrame(nil); !errors.Is(err, ErrBadPreamble) {
		t.Fatalf("nil pulses got err=%v, want ErrBadPreamble", err)
	}

	// 3. Truncated frame rejected.
	valid, err := EncodeFrame([]byte("ABC"))
	if err != nil {
		t.Fatalf("EncodeFrame: %v", err)
	}
	if _, err := DecodeFrame(valid[:len(valid)-5]); !errors.Is(err, ErrTruncatedFrame) {
		t.Fatalf("truncated frame got err=%v, want ErrTruncatedFrame", err)
	}

	// 4. Bit flip triggers ErrChecksumMismatch.
	flipped := make(irfile.PulseTrain, len(valid))
	copy(flipped, valid)
	// Flip first bit of payload (after 2 preamble + 16 sync + 16 len = index 34, space is 35).
	if flipped[35] == -Bit0SpaceUs {
		flipped[35] = -Bit1SpaceUs
	} else {
		flipped[35] = -Bit0SpaceUs
	}
	if _, err := DecodeFrame(flipped); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("bit flip got err=%v, want ErrChecksumMismatch", err)
	}

	// 5. Out-of-tolerance timing triggers ErrInvalidTiming.
	badTiming := make(irfile.PulseTrain, len(valid))
	copy(badTiming, valid)
	badTiming[35] = -1125 // midpoint between 560 and 1690, outside ±250 tolerance
	if _, err := DecodeFrame(badTiming); !errors.Is(err, ErrInvalidTiming) {
		t.Fatalf("mid-threshold space got err=%v, want ErrInvalidTiming", err)
	}
}

func FuzzDecodeFrame(f *testing.F) {
	if p, err := EncodeFrame([]byte("fuzz")); err == nil {
		raw := make([]byte, 0, len(p)*2)
		for _, v := range p {
			if v < 0 {
				v = -v
			}
			raw = append(raw, byte(v>>8), byte(v))
		}
		f.Add(raw)
	}
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		n := len(data) / 2
		if n == 0 || n > 2048 {
			return
		}
		pt := make(irfile.PulseTrain, n)
		for i := range n {
			dur := int32(uint16(data[i*2])<<8|uint16(data[i*2+1])) + 1
			if i%2 == 0 {
				pt[i] = dur
			} else {
				pt[i] = -dur
			}
		}
		payload, err := DecodeFrame(pt)
		if err == nil && len(payload) > MaxPayloadBytes {
			t.Fatalf("decoded payload exceeded max: %d", len(payload))
		}
	})
}
