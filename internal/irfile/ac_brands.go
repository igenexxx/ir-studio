package irfile

// acTiming holds pulse-distance timing constants (µs) for an AC brand.
type acTiming struct {
	hdrMark   uint32
	hdrSpace  uint32
	bitMark   uint32
	zeroSpace uint32
	oneSpace  uint32
	gapSpace  uint32
	msbFirst  bool
}

var acDefaultCarrier = CarrierSpec{FreqHz: DefaultFreqHz, DutyPct: DefaultDutyPct}

func acSum8(data []byte) byte {
	var sum byte
	for _, b := range data {
		sum += b
	}
	return sum
}

func appendACBits(raw []uint32, t acTiming, data []byte, bitCount int) []uint32 {
	for i := range bitCount {
		b := data[i/8]
		bitIdx := i % 8
		if t.msbFirst {
			bitIdx = 7 - bitIdx
		}
		raw = append(raw, t.bitMark)
		if (b>>bitIdx)&1 == 1 {
			raw = append(raw, t.oneSpace)
		} else {
			raw = append(raw, t.zeroSpace)
		}
	}
	return raw
}

func buildACRawSignal(name string, carrier CarrierSpec, raw []uint32) IRSignal {
	return IRSignal{
		Name:      name,
		Kind:      SignalRaw,
		Protocol:  "RAW",
		Frequency: carrier.FreqHz,
		DutyCycle: carrier.DutyFloat(),
		RawData:   raw,
	}
}

func encodeACSubframes(name string, carrier CarrierSpec, t acTiming, frames [][]byte) IRSignal {
	totalBits := 0
	for _, f := range frames {
		totalBits += len(f) * 8
	}
	raw := make([]uint32, 0, len(frames)*3+totalBits*2)
	for idx, f := range frames {
		if idx > 0 && t.gapSpace > 0 {
			raw = append(raw, t.gapSpace)
		}
		raw = append(raw, t.hdrMark, t.hdrSpace)
		raw = appendACBits(raw, t, f, len(f)*8)
		raw = append(raw, t.bitMark)
	}
	return buildACRawSignal(name, carrier, raw)
}

func normalizeFan(f ACFan) ACFan {
	if f == "" {
		return ACFanAuto
	}
	return f
}

// --- 1. Gree (8 bytes / 67 bits across 2 subframes, 4-bit nibble checksum) ---

type greeACEncoder struct{}

func (greeACEncoder) Brand() ACBrand       { return ACBrandGree }
func (greeACEncoder) Carrier() CarrierSpec { return acDefaultCarrier }
func (greeACEncoder) ChecksumKind() string { return "NIBBLE" }

func (greeACEncoder) EncodeBytes(s ACState) ([]byte, error) {
	if err := ValidateACState(s); err != nil {
		return nil, err
	}
	modeBits := greeModeBits(s.Mode)
	fanBits := greeFanBits(normalizeFan(s.Fan))
	var b [8]byte
	b[0] = modeBits | (fanBits << 4)
	if s.Power {
		b[0] |= 0x08
	}
	tempOffset := byte(s.TempC - ACMinTempC)
	b[1] = tempOffset & 0x0F
	b[3] = 0x50 // Gree fixed protocol identifier bits
	swingFlag := byte(0)
	if s.Swing {
		b[4] = 0x01
		swingFlag = 1
	}
	b[5] = 0x20
	chk := (modeBits + tempOffset + swingFlag + 10) & 0x0F
	b[7] = chk << 4
	return b[:], nil
}

func (g greeACEncoder) EncodeSignal(s ACState) (IRSignal, error) {
	b, err := g.EncodeBytes(s)
	if err != nil {
		return IRSignal{}, err
	}
	t := acTiming{hdrMark: 9000, hdrSpace: 4500, bitMark: 620, zeroSpace: 540, oneSpace: 1600, gapSpace: 20000}
	raw := make([]uint32, 0, 145)
	raw = append(raw, t.hdrMark, t.hdrSpace)
	raw = appendACBits(raw, t, b[:4], 32)
	raw = appendACBits(raw, t, []byte{0x02}, 3) // 3-bit subframe footer (0,1,0)
	raw = append(raw, t.bitMark, t.gapSpace)
	raw = appendACBits(raw, t, b[4:8], 32)
	raw = append(raw, t.bitMark)
	return buildACRawSignal(ACSignalName(s), g.Carrier(), raw), nil
}

func greeModeBits(m ACMode) byte {
	switch m {
	case ACModeAuto:
		return 0x00
	case ACModeCool:
		return 0x01
	case ACModeDry:
		return 0x02
	case ACModeFan:
		return 0x03
	case ACModeHeat:
		return 0x04
	default:
		return 0x01
	}
}

func greeFanBits(f ACFan) byte {
	switch f {
	case ACFanLow:
		return 0x01
	case ACFanMed:
		return 0x02
	case ACFanHigh:
		return 0x03
	default:
		return 0x00
	}
}

// --- 2. Midea (6 bytes / 48 bits, byte-complement pairs, repeated once) ---

type mideaACEncoder struct{}

func (mideaACEncoder) Brand() ACBrand       { return ACBrandMidea }
func (mideaACEncoder) Carrier() CarrierSpec { return acDefaultCarrier }
func (mideaACEncoder) ChecksumKind() string { return "COMPLEMENT" }

func (mideaACEncoder) EncodeBytes(s ACState) ([]byte, error) {
	if err := ValidateACState(s); err != nil {
		return nil, err
	}
	var b [6]byte
	b[0] = 0xB2 // Midea standard HVAC header byte
	if !s.Power {
		b[2], b[4] = 0x7B, 0xE0
	} else {
		b[2] = mideaFanByte(normalizeFan(s.Fan), s.Swing)
		b[4] = (mideaTempNibble(s.TempC) << 4) | mideaModeNibble(s.Mode)
	}
	b[1], b[3], b[5] = ^b[0], ^b[2], ^b[4]
	return b[:], nil
}

func (m mideaACEncoder) EncodeSignal(s ACState) (IRSignal, error) {
	b, err := m.EncodeBytes(s)
	if err != nil {
		return IRSignal{}, err
	}
	t := acTiming{hdrMark: 4400, hdrSpace: 4400, bitMark: 540, zeroSpace: 540, oneSpace: 1620, gapSpace: 5200, msbFirst: true}
	return encodeACSubframes(ACSignalName(s), m.Carrier(), t, [][]byte{b, b}), nil
}

func mideaFanByte(f ACFan, swing bool) byte {
	var v byte = 0xBF
	switch f {
	case ACFanLow:
		v = 0x9F
	case ACFanMed:
		v = 0x5F
	case ACFanHigh:
		v = 0x3F
	}
	if swing {
		v &^= 0x04
	}
	return v
}

func mideaModeNibble(m ACMode) byte {
	switch m {
	case ACModeDry, ACModeFan:
		return 0x04
	case ACModeAuto:
		return 0x08
	case ACModeHeat:
		return 0x0C
	default:
		return 0x00
	}
}

func mideaTempNibble(tempC int) byte {
	tbl := [15]byte{0x0, 0x0, 0x1, 0x3, 0x2, 0x6, 0x7, 0x5, 0x4, 0xC, 0xD, 0x9, 0x8, 0xA, 0xB}
	return tbl[tempC-ACMinTempC]
}

// --- 3. Daikin (8-byte header subframe + 19-byte state subframe, SUM8) ---

type daikinACEncoder struct{}

func (daikinACEncoder) Brand() ACBrand       { return ACBrandDaikin }
func (daikinACEncoder) Carrier() CarrierSpec { return acDefaultCarrier }
func (daikinACEncoder) ChecksumKind() string { return "SUM8" }

func (daikinACEncoder) EncodeBytes(s ACState) ([]byte, error) {
	if err := ValidateACState(s); err != nil {
		return nil, err
	}
	var b [19]byte
	b[0], b[1], b[2] = 0x11, 0xDA, 0x27
	b[5] = daikinModeByte(s.Mode, s.Power)
	b[6] = byte(s.TempC * 2)
	b[8] = daikinFanByte(normalizeFan(s.Fan), s.Swing)
	b[18] = acSum8(b[:18])
	return b[:], nil
}

func (d daikinACEncoder) EncodeSignal(s ACState) (IRSignal, error) {
	stateFrame, err := d.EncodeBytes(s)
	if err != nil {
		return IRSignal{}, err
	}
	hdrFrame := []byte{0x11, 0xDA, 0x27, 0x00, 0xC5, 0x00, 0x00, 0xD7}
	t := acTiming{hdrMark: 3480, hdrSpace: 1750, bitMark: 430, zeroSpace: 430, oneSpace: 1300, gapSpace: 29000}
	return encodeACSubframes(ACSignalName(s), d.Carrier(), t, [][]byte{hdrFrame, stateFrame}), nil
}

func daikinModeByte(m ACMode, power bool) byte {
	var v byte = 0x30
	switch m {
	case ACModeAuto:
		v = 0x00
	case ACModeDry:
		v = 0x20
	case ACModeHeat:
		v = 0x40
	case ACModeFan:
		v = 0x60
	}
	if power {
		v |= 0x01
	}
	return v
}

func daikinFanByte(f ACFan, swing bool) byte {
	var v byte = 0xA0
	switch f {
	case ACFanLow:
		v = 0x30
	case ACFanMed:
		v = 0x50
	case ACFanHigh:
		v = 0x70
	}
	if swing {
		v |= 0x0F
	}
	return v
}
