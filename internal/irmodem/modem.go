package irmodem

import (
	"errors"
	"fmt"

	"ir-studio/internal/irfile"
)

const (
	// MaxPayloadBytes is the maximum payload size for a single 38 kHz optical modem frame.
	MaxPayloadBytes = 64

	// CarrierHz and DutyPct are the canonical 38 kHz optical modem carrier defaults.
	CarrierHz uint32 = 38000
	DutyPct   uint32 = 33

	// FrameSyncByte distinguishes irmodem frames from consumer TV/AC IR remotes.
	FrameSyncByte byte = 0xA5

	// Pulse-distance modulation timings in microseconds (robust against ±120 µs demodulator jitter).
	HeaderMarkUs  int32 = 4000
	HeaderSpaceUs int32 = 2000
	BitMarkUs     int32 = 560
	Bit0SpaceUs   int32 = 560
	Bit1SpaceUs   int32 = 1690
	StopMarkUs    int32 = 560

	// TimingTolUs is the symmetric pulse/space timing tolerance (>= ±120 µs).
	TimingTolUs int32 = 250

	crc8Poly byte = 0x07
)

var (
	ErrPayloadTooLarge  = errors.New("irmodem: payload exceeds 64 bytes")
	ErrBadPreamble      = errors.New("irmodem: missing or invalid preamble")
	ErrTruncatedFrame   = errors.New("irmodem: truncated modem frame")
	ErrInvalidTiming    = errors.New("irmodem: pulse timing outside tolerance")
	ErrChecksumMismatch = errors.New("irmodem: CRC-8 checksum mismatch")
)

// CRC8 computes CRC-8-CCITT (polynomial 0x07, init 0x00) over data.
func CRC8(data []byte) byte {
	var crc byte
	for _, b := range data {
		crc ^= b
		for range 8 {
			if crc&0x80 != 0 {
				crc = (crc << 1) ^ crc8Poly
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

// EncodeFrame frames payload (<= 64 bytes) with [Sync(0xA5), Len, Payload..., CRC8]
// and modulates it into a strictly alternating 38 kHz pulse-distance PulseTrain.
func EncodeFrame(payload []byte) (irfile.PulseTrain, error) {
	if len(payload) > MaxPayloadBytes {
		return nil, fmt.Errorf("len=%d: %w", len(payload), ErrPayloadTooLarge)
	}
	wire := buildWireBytes(payload)
	pulses := make(irfile.PulseTrain, 0, 2+len(wire)*16+1)
	pulses = append(pulses, HeaderMarkUs, -HeaderSpaceUs)
	for _, b := range wire {
		pulses = appendModemByte(pulses, b)
	}
	pulses = append(pulses, StopMarkUs)
	return pulses, nil
}

// DecodeFrame locates the modem preamble in pulses, demodulates pulse-distance bits
// tolerating ±250 µs jitter (>= ±120 µs), verifies sync and CRC-8, and returns payload.
func DecodeFrame(pulses irfile.PulseTrain) ([]byte, error) {
	if err := irfile.ValidatePulseTrain(pulses); err != nil {
		return nil, fmt.Errorf("%v: %w", err, ErrBadPreamble)
	}
	start := findPreamble(pulses)
	if start < 0 {
		return nil, ErrBadPreamble
	}
	pos := start + 2
	syncByte, nextPos, err := decodeModemByte(pulses, pos)
	if err != nil {
		return nil, err
	}
	if syncByte != FrameSyncByte {
		return nil, fmt.Errorf("sync=0x%02X: %w", syncByte, ErrBadPreamble)
	}
	length, nextPos, err := decodeModemByte(pulses, nextPos)
	if err != nil {
		return nil, err
	}
	if int(length) > MaxPayloadBytes {
		return nil, fmt.Errorf("len=%d: %w", length, ErrPayloadTooLarge)
	}
	wire := make([]byte, 2+int(length))
	wire[0] = syncByte
	wire[1] = length
	for i := range int(length) {
		b, np, decErr := decodeModemByte(pulses, nextPos)
		if decErr != nil {
			return nil, decErr
		}
		wire[2+i] = b
		nextPos = np
	}
	gotCRC, nextPos, err := decodeModemByte(pulses, nextPos)
	if err != nil {
		return nil, err
	}
	if err := verifyStopMark(pulses, nextPos); err != nil {
		return nil, err
	}
	if wantCRC := CRC8(wire); gotCRC != wantCRC {
		return nil, fmt.Errorf("got 0x%02X want 0x%02X: %w", gotCRC, wantCRC, ErrChecksumMismatch)
	}
	payload := make([]byte, length)
	copy(payload, wire[2:])
	return payload, nil
}

func buildWireBytes(payload []byte) []byte {
	wire := make([]byte, 2+len(payload)+1)
	wire[0] = FrameSyncByte
	wire[1] = byte(len(payload))
	copy(wire[2:], payload)
	wire[len(wire)-1] = CRC8(wire[:len(wire)-1])
	return wire
}

func appendModemByte(pulses irfile.PulseTrain, b byte) irfile.PulseTrain {
	for shift := 7; shift >= 0; shift-- {
		space := Bit0SpaceUs
		if (b>>uint(shift))&1 != 0 {
			space = Bit1SpaceUs
		}
		pulses = append(pulses, BitMarkUs, -space)
	}
	return pulses
}

func findPreamble(pulses irfile.PulseTrain) int {
	for i := 0; i+1 < len(pulses); i++ {
		if pulses[i] > 0 && pulses[i+1] < 0 {
			if withinTol(pulses[i], HeaderMarkUs) && withinTol(-pulses[i+1], HeaderSpaceUs) {
				return i
			}
		}
	}
	return -1
}

func decodeModemByte(pulses irfile.PulseTrain, pos int) (byte, int, error) {
	if pos+16 > len(pulses) {
		return 0, pos, ErrTruncatedFrame
	}
	var out byte
	for i := range 8 {
		mark := pulses[pos+i*2]
		space := pulses[pos+i*2+1]
		if mark <= 0 || space >= 0 || !withinTol(mark, BitMarkUs) {
			return 0, pos, ErrInvalidTiming
		}
		spaceUs := -space
		out <<= 1
		switch {
		case withinTol(spaceUs, Bit1SpaceUs):
			out |= 1
		case withinTol(spaceUs, Bit0SpaceUs):
		default:
			return 0, pos, ErrInvalidTiming
		}
	}
	return out, pos + 16, nil
}

func verifyStopMark(pulses irfile.PulseTrain, pos int) error {
	if pos >= len(pulses) {
		return ErrTruncatedFrame
	}
	if pulses[pos] <= 0 || !withinTol(pulses[pos], StopMarkUs) {
		return ErrInvalidTiming
	}
	return nil
}

func withinTol(got, want int32) bool {
	diff := got - want
	if diff < 0 {
		diff = -diff
	}
	return diff <= TimingTolUs
}
