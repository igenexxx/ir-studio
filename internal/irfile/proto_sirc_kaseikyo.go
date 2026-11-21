package irfile

const (
	sircCarrierHz   uint32 = 40000
	sircHeaderMark  int32  = 2400
	sircHeaderSpace int32  = -600
	sircZeroMark    int32  = 600
	sircOneMark     int32  = 1200
	sircBitSpace    int32  = -600

	kaseikyoHeaderMark  int32 = 3360
	kaseikyoHeaderSpace int32 = -1680
	kaseikyoBitMark     int32 = 420
	kaseikyoZeroSpace   int32 = -420
	kaseikyoOneSpace    int32 = -1260
	kaseikyoPulseCount        = 2 + 48*2 + 1
)

type sircCodec struct{}

func (sircCodec) Name() string         { return "SIRC" }
func (sircCodec) Carrier() CarrierSpec { return CarrierSpec{FreqHz: sircCarrierHz, DutyPct: 33} }

func (sircCodec) Encode(sig IRSignal) (PulseTrain, error) {
	cmd := uint32(sig.Command[0] & 0x7F)
	addr := uint32(sig.Address[0])
	ext := uint32(sig.Address[1])

	bits := 12
	var word uint32
	switch {
	case ext != 0:
		bits = 20
		word = cmd | ((addr & 0x1F) << 7) | (ext << 12)
	case addr > 0x1F:
		bits = 15
		word = cmd | (addr << 7)
	default:
		word = cmd | ((addr & 0x1F) << 7)
	}

	out := make(PulseTrain, 0, 2+bits*2-1)
	out = append(out, sircHeaderMark, sircHeaderSpace)
	for i := range bits {
		if (word>>i)&1 == 1 {
			out = append(out, sircOneMark)
		} else {
			out = append(out, sircZeroMark)
		}
		if i < bits-1 {
			out = append(out, sircBitSpace)
		}
	}
	return out, nil
}

func (sircCodec) Decode(pulses PulseTrain) (IRSignal, bool) {
	if len(pulses) < 25 || !matchDuration(pulses[0], sircHeaderMark) || !matchDuration(pulses[1], sircHeaderSpace) {
		return IRSignal{}, false
	}
	body := pulses[2:]
	if len(body)%2 == 0 && matchDuration(body[len(body)-1], sircBitSpace) {
		body = body[:len(body)-1]
	}
	if len(body)%2 == 0 {
		return IRSignal{}, false
	}
	bitCount := (len(body) + 1) / 2
	if bitCount != 12 && bitCount != 15 && bitCount != 20 {
		return IRSignal{}, false
	}
	word, ok := decodeSIRCBits(body, bitCount)
	if !ok {
		return IRSignal{}, false
	}
	return unpackSIRC(word, bitCount), true
}

func decodeSIRCBits(body PulseTrain, bitCount int) (uint32, bool) {
	var word uint32
	for i := range bitCount {
		mark := body[i*2]
		switch {
		case matchDuration(mark, sircOneMark):
			word |= 1 << i
		case matchDuration(mark, sircZeroMark):
		default:
			return 0, false
		}
		if i < bitCount-1 && !matchDuration(body[i*2+1], sircBitSpace) {
			return 0, false
		}
	}
	return word, true
}

func unpackSIRC(word uint32, bitCount int) IRSignal {
	cmd := byte(word & 0x7F)
	var addr, ext byte
	switch bitCount {
	case 12:
		addr = byte((word >> 7) & 0x1F)
	case 15:
		addr = byte((word >> 7) & 0xFF)
	case 20:
		addr = byte((word >> 7) & 0x1F)
		ext = byte((word >> 12) & 0xFF)
	}
	return IRSignal{
		Kind:      SignalParsed,
		Protocol:  "SIRC",
		Address:   [4]byte{addr, ext},
		Command:   [4]byte{cmd},
		Frequency: sircCarrierHz,
		DutyCycle: DefaultDutyCycle,
	}
}

type kaseikyoCodec struct{}

func (kaseikyoCodec) Name() string         { return "Kaseikyo" }
func (kaseikyoCodec) Carrier() CarrierSpec { return CarrierSpec{FreqHz: 38000, DutyPct: 33} }

func (kaseikyoCodec) Encode(sig IRSignal) (PulseTrain, error) {
	var b [6]byte
	b[0] = sig.Address[0]
	b[1] = sig.Address[1]
	vp := kaseikyoVendorParity(b[0], b[1])
	b[2] = vp | ((sig.Address[2] & 0x0F) << 4)
	b[3] = (sig.Address[3] & 0x0F) | ((sig.Command[0] & 0x0F) << 4)
	b[4] = sig.Command[1]
	b[5] = b[2] ^ b[3] ^ b[4]

	out := make(PulseTrain, 0, kaseikyoPulseCount)
	out = append(out, kaseikyoHeaderMark, kaseikyoHeaderSpace)
	for _, v := range b {
		for bit := range 8 {
			out = append(out, kaseikyoBitMark)
			if (v>>bit)&1 == 1 {
				out = append(out, kaseikyoOneSpace)
			} else {
				out = append(out, kaseikyoZeroSpace)
			}
		}
	}
	return append(out, kaseikyoBitMark), nil
}

func (kaseikyoCodec) Decode(pulses PulseTrain) (IRSignal, bool) {
	if len(pulses) < kaseikyoPulseCount || len(pulses) > kaseikyoPulseCount+1 {
		return IRSignal{}, false
	}
	if !matchDuration(pulses[0], kaseikyoHeaderMark) || !matchDuration(pulses[1], kaseikyoHeaderSpace) {
		return IRSignal{}, false
	}
	b, ok := decodeKaseikyoBytes(pulses[2:])
	if !ok {
		return IRSignal{}, false
	}
	if (b[2]&0x0F) != kaseikyoVendorParity(b[0], b[1]) || b[5] != (b[2]^b[3]^b[4]) {
		return IRSignal{}, false
	}
	return IRSignal{
		Kind:      SignalParsed,
		Protocol:  "Kaseikyo",
		Address:   [4]byte{b[0], b[1], b[2] >> 4, b[3] & 0x0F},
		Command:   [4]byte{b[3] >> 4, b[4]},
		Frequency: 38000,
		DutyCycle: DefaultDutyCycle,
	}, true
}

func decodeKaseikyoBytes(body PulseTrain) ([6]byte, bool) {
	var b [6]byte
	idx := 0
	for i := range 6 {
		var v byte
		for bit := range 8 {
			if !matchDuration(body[idx], kaseikyoBitMark) {
				return b, false
			}
			switch {
			case matchDuration(body[idx+1], kaseikyoOneSpace):
				v |= 1 << bit
			case matchDuration(body[idx+1], kaseikyoZeroSpace):
			default:
				return b, false
			}
			idx += 2
		}
		b[i] = v
	}
	if !matchDuration(body[idx], kaseikyoBitMark) {
		return b, false
	}
	return b, true
}

func kaseikyoVendorParity(lo, hi byte) byte {
	return (lo ^ (lo >> 4) ^ hi ^ (hi >> 4)) & 0x0F
}

type rawCodec struct{}

func (rawCodec) Name() string         { return "RAW" }
func (rawCodec) Carrier() CarrierSpec { return CarrierSpec{FreqHz: DefaultFreqHz, DutyPct: DefaultDutyPct} }

func (rawCodec) Encode(sig IRSignal) (PulseTrain, error) {
	return FromRawData(sig.RawData)
}

func (rawCodec) Decode(_ PulseTrain) (IRSignal, bool) {
	return IRSignal{}, false
}
