package irfile

const (
	rcCarrierHz uint32 = 36000

	rc5UnitUs int32 = 889
	rc5Bits         = 14

	rc6HeaderMark  int32 = 2666
	rc6HeaderSpace int32 = -889
	rc6UnitUs      int32 = 444
	rc6TotalUnits        = 42 // 19 normal bits * 2 + 1 double-width toggle bit * 4
)

type rc5Codec struct{}

func (rc5Codec) Name() string         { return "RC5" }
func (rc5Codec) Carrier() CarrierSpec { return CarrierSpec{FreqHz: rcCarrierHz, DutyPct: 33} }

func (rc5Codec) Encode(sig IRSignal) (PulseTrain, error) {
	addr := sig.Address[0] & 0x1F
	cmd := sig.Command[0] & 0x7F
	s2 := uint16(1)
	if cmd&0x40 != 0 {
		s2 = 0 // RC5X inverted S2 represents command bit 6
	}
	word := (uint16(1) << 13) | (s2 << 12) | (uint16(addr) << 6) | uint16(cmd&0x3F)
	halves := make([]int32, 0, rc5Bits*2)
	for i := rc5Bits - 1; i >= 0; i-- {
		if (word>>i)&1 == 1 {
			halves = append(halves, -rc5UnitUs, rc5UnitUs)
		} else {
			halves = append(halves, rc5UnitUs, -rc5UnitUs)
		}
	}
	return coalesceTrimMarks(halves), nil
}

func (rc5Codec) Decode(pulses PulseTrain) (IRSignal, bool) {
	if len(pulses) < 10 || len(pulses) > 30 || pulses[0] < 0 {
		return IRSignal{}, false
	}
	halves := make([]int32, 0, rc5Bits*2+2)
	halves = append(halves, -rc5UnitUs) // implicit leading space of S1=1
	if !expandUnits(pulses, rc5UnitUs, 2, &halves) {
		return IRSignal{}, false
	}
	if len(halves) == rc5Bits*2-1 {
		halves = append(halves, -rc5UnitUs) // omitted trailing space when last bit is 0
	}
	if len(halves) != rc5Bits*2 {
		return IRSignal{}, false
	}
	word, ok := decodeManchesterMSB(halves)
	if !ok || (word>>13)&1 != 1 {
		return IRSignal{}, false
	}
	s2 := (word >> 12) & 1
	addr := byte((word >> 6) & 0x1F)
	cmd := byte(word & 0x3F)
	if s2 == 0 {
		cmd |= 0x40
	}
	return IRSignal{
		Kind:      SignalParsed,
		Protocol:  "RC5",
		Address:   [4]byte{addr},
		Command:   [4]byte{cmd},
		Frequency: rcCarrierHz,
		DutyCycle: DefaultDutyCycle,
	}, true
}

type rc6Codec struct{}

func (rc6Codec) Name() string         { return "RC6" }
func (rc6Codec) Carrier() CarrierSpec { return CarrierSpec{FreqHz: rcCarrierHz, DutyPct: 33} }

func (rc6Codec) Encode(sig IRSignal) (PulseTrain, error) {
	// Mode 0: SB(1), M2..M0(000), T(0), 8-bit addr, 8-bit cmd = 21 bits?
	// Per SPEC-01: 20 bits after header: SB(1), M2..M0(000), T(0), 8-bit addr, 8-bit cmd -> 1+3+1+8+8 = 21 bits?
	// Wait: let's check bit count carefully:
	// SB(1) + Mode(3) + Toggle(1) + Addr(8) + Cmd(8) = 21 bits!
	// Wait: 20 normal bits (SB+3mode+8addr+8cmd = 20) + 1 double-width toggle = 21 bits = 44 TE units.
	bits := buildRC6Bits(sig.Address[0], sig.Command[0])
	units := make([]int32, 0, len(bits)*2+2)
	for i, b := range bits {
		w := rc6UnitUs
		if i == 4 { // toggle bit is double-width 2*TE
			w = rc6UnitUs * 2
		}
		if b == 1 {
			units = append(units, w, -w)
		} else {
			units = append(units, -w, w)
		}
	}
	body := coalesceTrimMarks(units)
	out := make(PulseTrain, 0, 2+len(body))
	out = append(out, rc6HeaderMark, rc6HeaderSpace)
	return append(out, body...), nil
}

func (rc6Codec) Decode(pulses PulseTrain) (IRSignal, bool) {
	if len(pulses) < 12 || !matchDuration(pulses[0], rc6HeaderMark) || !matchDuration(pulses[1], rc6HeaderSpace) {
		return IRSignal{}, false
	}
	const wantUnits = 44 // 20 single-width bits (40 TE) + 1 double-width toggle bit (4 TE)
	units := make([]int32, 0, wantUnits+2)
	if !expandUnits(pulses[2:], rc6UnitUs, 3, &units) {
		return IRSignal{}, false
	}
	if len(units) == wantUnits-1 {
		units = append(units, -rc6UnitUs) // omitted trailing space on last bit = 1
	}
	if len(units) != wantUnits {
		return IRSignal{}, false
	}
	addr, cmd, ok := parseRC6Units(units)
	if !ok {
		return IRSignal{}, false
	}
	return IRSignal{
		Kind:      SignalParsed,
		Protocol:  "RC6",
		Address:   [4]byte{addr},
		Command:   [4]byte{cmd},
		Frequency: rcCarrierHz,
		DutyCycle: DefaultDutyCycle,
	}, true
}

func buildRC6Bits(addr, cmd byte) [21]byte {
	var bits [21]byte
	bits[0] = 1 // SB = 1; M2..M0 = 0,0,0; T = 0
	for i := range 8 {
		bits[5+i] = (addr >> (7 - i)) & 1
		bits[13+i] = (cmd >> (7 - i)) & 1
	}
	return bits
}

func parseRC6Units(units []int32) (byte, byte, bool) {
	var bits [21]byte
	idx := 0
	for i := range 21 {
		if i == 4 { // double-width toggle bit (4 TE units)
			b, ok := decodeRC6Toggle(units[idx : idx+4])
			if !ok {
				return 0, 0, false
			}
			bits[i] = b
			idx += 4
			continue
		}
		b, ok := decodeBiPhasePair(units[idx], units[idx+1])
		if !ok {
			return 0, 0, false
		}
		bits[i] = b
		idx += 2
	}
	if bits[0] != 1 || bits[1] != 0 || bits[2] != 0 || bits[3] != 0 {
		return 0, 0, false
	}
	var addr, cmd byte
	for i := range 8 {
		addr = (addr << 1) | bits[5+i]
		cmd = (cmd << 1) | bits[13+i]
	}
	return addr, cmd, true
}

func decodeRC6Toggle(u []int32) (byte, bool) {
	if u[0] > 0 && u[1] > 0 && u[2] < 0 && u[3] < 0 {
		return 1, true
	}
	if u[0] < 0 && u[1] < 0 && u[2] > 0 && u[3] > 0 {
		return 0, true
	}
	return 0, false
}

func decodeBiPhasePair(a, b int32) (byte, bool) {
	if a > 0 && b < 0 {
		return 1, true
	}
	if a < 0 && b > 0 {
		return 0, true
	}
	return 0, false
}

func decodeManchesterMSB(halves []int32) (uint16, bool) {
	var word uint16
	for i := 0; i < len(halves); i += 2 {
		word <<= 1
		switch {
		case halves[i] < 0 && halves[i+1] > 0:
			word |= 1
		case halves[i] > 0 && halves[i+1] < 0:
		default:
			return 0, false
		}
	}
	return word, true
}

func expandUnits(pulses PulseTrain, unit int32, maxMult int, dst *[]int32) bool {
	for _, p := range pulses {
		sign := int32(1)
		if p < 0 {
			sign = -1
		}
		matched := 0
		for m := 1; m <= maxMult; m++ {
			if matchDuration(p, sign*unit*int32(m)) {
				matched = m
				break
			}
		}
		if matched == 0 {
			return false
		}
		for range matched {
			*dst = append(*dst, sign*unit)
		}
	}
	return true
}

func coalesceTrimMarks(halves []int32) PulseTrain {
	var out PulseTrain
	for _, h := range halves {
		if len(out) == 0 && h < 0 {
			continue
		}
		if len(out) > 0 && (out[len(out)-1] > 0) == (h > 0) {
			out[len(out)-1] += h
		} else {
			out = append(out, h)
		}
	}
	if len(out) > 0 && out[len(out)-1] < 0 {
		out = out[:len(out)-1]
	}
	return out
}
