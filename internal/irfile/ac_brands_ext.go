package irfile

// --- 4. LG_AC (28-bit frame packed into 4 bytes MSB-first, 4-bit nibble sum) ---

type lgACEncoder struct{}

func (lgACEncoder) Brand() ACBrand       { return ACBrandLG }
func (lgACEncoder) Carrier() CarrierSpec { return acDefaultCarrier }
func (lgACEncoder) ChecksumKind() string { return "NIBBLE" }

func (lgACEncoder) EncodeBytes(s ACState) ([]byte, error) {
	if err := ValidateACState(s); err != nil {
		return nil, err
	}
	var nib [7]byte
	nib[0], nib[1] = 0x08, 0x08
	if !s.Power {
		nib[2], nib[3], nib[4], nib[5] = 0x0C, 0x00, 0x00, 0x05
	} else {
		nib[2] = 0x00
		nib[3] = lgModeNibble(s.Mode)
		nib[4] = byte(s.TempC-15) & 0x0F
		nib[5] = lgFanNibble(normalizeFan(s.Fan), s.Swing)
	}
	var sum byte
	for i := range 6 {
		sum += nib[i]
	}
	nib[6] = sum & 0x0F
	return []byte{
		(nib[0] << 4) | nib[1],
		(nib[2] << 4) | nib[3],
		(nib[4] << 4) | nib[5],
		nib[6] << 4,
	}, nil
}

func (l lgACEncoder) EncodeSignal(s ACState) (IRSignal, error) {
	b, err := l.EncodeBytes(s)
	if err != nil {
		return IRSignal{}, err
	}
	t := acTiming{hdrMark: 8400, hdrSpace: 4200, bitMark: 550, zeroSpace: 550, oneSpace: 1600, msbFirst: true}
	raw := make([]uint32, 0, 2+28*2+1)
	raw = append(raw, t.hdrMark, t.hdrSpace)
	raw = appendACBits(raw, t, b, 28)
	raw = append(raw, t.bitMark)
	return buildACRawSignal(ACSignalName(s), l.Carrier(), raw), nil
}

func lgModeNibble(m ACMode) byte {
	switch m {
	case ACModeDry:
		return 0x01
	case ACModeFan:
		return 0x02
	case ACModeAuto:
		return 0x03
	case ACModeHeat:
		return 0x04
	default:
		return 0x00
	}
}

func lgFanNibble(f ACFan, swing bool) byte {
	var v byte = 0x05
	switch f {
	case ACFanLow:
		v = 0x00
	case ACFanMed:
		v = 0x02
	case ACFanHigh:
		v = 0x04
	}
	if swing {
		v |= 0x08
	}
	return v
}

// --- 5. Samsung_AC (14-byte / 2-subframe HVAC packet, nibble-count checksum) ---

type samsungACEncoder struct{}

func (samsungACEncoder) Brand() ACBrand       { return ACBrandSamsung }
func (samsungACEncoder) Carrier() CarrierSpec { return acDefaultCarrier }
func (samsungACEncoder) ChecksumKind() string { return "NIBBLE" }

func (samsungACEncoder) EncodeBytes(s ACState) ([]byte, error) {
	if err := ValidateACState(s); err != nil {
		return nil, err
	}
	var b [14]byte
	b[0], b[2], b[6] = 0x02, 0x0F, 0xF0
	if !s.Power {
		b[6] = 0xC0
	}
	b[1] = samsungChecksumByte(b[0:7])

	b[7], b[13] = 0x01, 0xF0
	if s.Swing {
		b[9] = 0xAE
	}
	b[11] = byte(s.TempC-ACMinTempC) << 4
	b[12] = (samsungModeNibble(s.Mode) << 4) | samsungFanNibble(normalizeFan(s.Fan))
	if !s.Power {
		b[13] = 0xC0
	}
	b[8] = samsungChecksumByte(b[7:14])
	return b[:], nil
}

func (sa samsungACEncoder) EncodeSignal(s ACState) (IRSignal, error) {
	b, err := sa.EncodeBytes(s)
	if err != nil {
		return IRSignal{}, err
	}
	t := acTiming{hdrMark: 3000, hdrSpace: 9000, bitMark: 550, zeroSpace: 550, oneSpace: 1500, gapSpace: 3000}
	return encodeACSubframes(ACSignalName(s), sa.Carrier(), t, [][]byte{b[:7], b[7:14]}), nil
}

func samsungChecksumByte(sub []byte) byte {
	ones := 0
	for i, v := range sub {
		if i == 1 {
			continue
		}
		for bit := range 8 {
			if (v>>bit)&1 == 1 {
				ones++
			}
		}
	}
	chk := byte((16 - (ones % 16)) & 0x0F)
	return (chk << 4) | (sub[1] & 0x0F)
}

func samsungModeNibble(m ACMode) byte {
	switch m {
	case ACModeAuto:
		return 0x00
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

func samsungFanNibble(f ACFan) byte {
	switch f {
	case ACFanLow:
		return 0x05
	case ACFanMed:
		return 0x09
	case ACFanHigh:
		return 0x0B
	default:
		return 0x01
	}
}

// --- 6. Mitsubishi (18-byte / 144-bit frame, SUM8 checksum) ---

type mitsubishiACEncoder struct{}

func (mitsubishiACEncoder) Brand() ACBrand       { return ACBrandMitsubishi }
func (mitsubishiACEncoder) Carrier() CarrierSpec { return acDefaultCarrier }
func (mitsubishiACEncoder) ChecksumKind() string { return "SUM8" }

func (mitsubishiACEncoder) EncodeBytes(s ACState) ([]byte, error) {
	if err := ValidateACState(s); err != nil {
		return nil, err
	}
	var b [18]byte
	b[0], b[1], b[2], b[3], b[4] = 0x23, 0xCB, 0x26, 0x01, 0x00
	if s.Power {
		b[5] = 0x20
	}
	b[6] = mitsubishiModeByte(s.Mode)
	b[7] = byte(s.TempC - ACMinTempC)
	b[9] = mitsubishiFanByte(normalizeFan(s.Fan), s.Swing)
	b[17] = acSum8(b[:17])
	return b[:], nil
}

func (mi mitsubishiACEncoder) EncodeSignal(s ACState) (IRSignal, error) {
	b, err := mi.EncodeBytes(s)
	if err != nil {
		return IRSignal{}, err
	}
	t := acTiming{hdrMark: 3400, hdrSpace: 1750, bitMark: 450, zeroSpace: 420, oneSpace: 1300}
	return encodeACSubframes(ACSignalName(s), mi.Carrier(), t, [][]byte{b}), nil
}

func mitsubishiModeByte(m ACMode) byte {
	switch m {
	case ACModeHeat:
		return 0x08
	case ACModeDry:
		return 0x10
	case ACModeAuto:
		return 0x20
	case ACModeFan:
		return 0x38
	default:
		return 0x18
	}
}

func mitsubishiFanByte(f ACFan, swing bool) byte {
	var v byte
	switch f {
	case ACFanLow:
		v = 0x01
	case ACFanMed:
		v = 0x02
	case ACFanHigh:
		v = 0x04
	}
	if swing {
		v |= 0x38
	}
	return v
}
