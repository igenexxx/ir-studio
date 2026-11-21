package irfile

const (
	necHeaderMark   int32 = 9000
	necHeaderSpace  int32 = -4500
	necRepeatSpace  int32 = -2250
	necBitMark      int32 = 562
	necZeroSpace    int32 = -562
	necOneSpace     int32 = -1687
	necPulseCount         = 2 + 32*2 + 1 // header + 32 bits + stop mark

	samsungHeaderMark  int32 = 4500
	samsungHeaderSpace int32 = -4500
	samsungBitMark     int32 = 550
	samsungZeroSpace   int32 = -550
	samsungOneSpace    int32 = -1650
)

type necCodec struct{}

func (necCodec) Name() string         { return "NEC" }
func (necCodec) Carrier() CarrierSpec { return CarrierSpec{FreqHz: 38000, DutyPct: 33} }

func (necCodec) Encode(sig IRSignal) (PulseTrain, error) {
	if sig.Repeat {
		return PulseTrain{necHeaderMark, necRepeatSpace, necBitMark}, nil
	}
	frame := [4]byte{sig.Address[0], ^sig.Address[0], sig.Command[0], ^sig.Command[0]}
	return encodePulseDist32(necHeaderMark, necHeaderSpace, necBitMark, necZeroSpace, necOneSpace, frame), nil
}

func (necCodec) Decode(pulses PulseTrain) (IRSignal, bool) {
	if len(pulses) == 3 &&
		matchDuration(pulses[0], necHeaderMark) &&
		matchDuration(pulses[1], necRepeatSpace) &&
		matchDuration(pulses[2], necBitMark) {
		return IRSignal{
			Kind:      SignalParsed,
			Protocol:  "NEC",
			Frequency: 38000,
			DutyCycle: DefaultDutyCycle,
			Repeat:    true,
		}, true
	}
	frame, ok := decodePulseDist32(pulses, necHeaderMark, necHeaderSpace, necBitMark, necZeroSpace, necOneSpace)
	if !ok || frame[1] != ^frame[0] || frame[3] != ^frame[2] {
		return IRSignal{}, false
	}
	return IRSignal{
		Kind:      SignalParsed,
		Protocol:  "NEC",
		Address:   [4]byte{frame[0]},
		Command:   [4]byte{frame[2]},
		Frequency: 38000,
		DutyCycle: DefaultDutyCycle,
	}, true
}

type necExtCodec struct{}

func (necExtCodec) Name() string         { return "NECext" }
func (necExtCodec) Carrier() CarrierSpec { return CarrierSpec{FreqHz: 38000, DutyPct: 33} }

func (necExtCodec) Encode(sig IRSignal) (PulseTrain, error) {
	frame := [4]byte{sig.Address[0], sig.Address[1], sig.Command[0], sig.Command[1]}
	return encodePulseDist32(necHeaderMark, necHeaderSpace, necBitMark, necZeroSpace, necOneSpace, frame), nil
}

func (necExtCodec) Decode(pulses PulseTrain) (IRSignal, bool) {
	frame, ok := decodePulseDist32(pulses, necHeaderMark, necHeaderSpace, necBitMark, necZeroSpace, necOneSpace)
	if !ok {
		return IRSignal{}, false
	}
	return IRSignal{
		Kind:      SignalParsed,
		Protocol:  "NECext",
		Address:   [4]byte{frame[0], frame[1]},
		Command:   [4]byte{frame[2], frame[3]},
		Frequency: 38000,
		DutyCycle: DefaultDutyCycle,
	}, true
}

type samsung32Codec struct{}

func (samsung32Codec) Name() string         { return "Samsung32" }
func (samsung32Codec) Carrier() CarrierSpec { return CarrierSpec{FreqHz: 38000, DutyPct: 33} }

func (samsung32Codec) Encode(sig IRSignal) (PulseTrain, error) {
	frame := [4]byte{sig.Address[0], sig.Address[0], sig.Command[0], ^sig.Command[0]}
	return encodePulseDist32(
		samsungHeaderMark, samsungHeaderSpace,
		samsungBitMark, samsungZeroSpace, samsungOneSpace,
		frame,
	), nil
}

func (samsung32Codec) Decode(pulses PulseTrain) (IRSignal, bool) {
	frame, ok := decodePulseDist32(
		pulses,
		samsungHeaderMark, samsungHeaderSpace,
		samsungBitMark, samsungZeroSpace, samsungOneSpace,
	)
	if !ok || frame[0] != frame[1] || frame[3] != ^frame[2] {
		return IRSignal{}, false
	}
	return IRSignal{
		Kind:      SignalParsed,
		Protocol:  "Samsung32",
		Address:   [4]byte{frame[0]},
		Command:   [4]byte{frame[2]},
		Frequency: 38000,
		DutyCycle: DefaultDutyCycle,
	}, true
}

func encodePulseDist32(hdrMark, hdrSpace, bitMark, zeroSpace, oneSpace int32, frame [4]byte) PulseTrain {
	out := make(PulseTrain, 0, necPulseCount)
	out = append(out, hdrMark, hdrSpace)
	for _, b := range frame {
		for bit := range 8 {
			out = append(out, bitMark)
			if (b>>bit)&1 == 1 {
				out = append(out, oneSpace)
			} else {
				out = append(out, zeroSpace)
			}
		}
	}
	return append(out, bitMark)
}

func decodePulseDist32(pulses PulseTrain, hdrMark, hdrSpace, bitMark, zeroSpace, oneSpace int32) ([4]byte, bool) {
	var frame [4]byte
	if len(pulses) < necPulseCount || len(pulses) > necPulseCount+1 {
		return frame, false
	}
	if !matchDuration(pulses[0], hdrMark) || !matchDuration(pulses[1], hdrSpace) {
		return frame, false
	}
	idx := 2
	for byteIdx := range 4 {
		var b byte
		for bit := range 8 {
			if !matchDuration(pulses[idx], bitMark) {
				return frame, false
			}
			space := pulses[idx+1]
			switch {
			case matchDuration(space, oneSpace):
				b |= 1 << bit
			case matchDuration(space, zeroSpace):
			default:
				return frame, false
			}
			idx += 2
		}
		frame[byteIdx] = b
	}
	if !matchDuration(pulses[idx], bitMark) {
		return frame, false
	}
	return frame, true
}
