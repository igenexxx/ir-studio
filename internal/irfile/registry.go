package irfile

import (
	"fmt"
	"math"
)

const (
	timingTolPercent  = 22
	timingTolFloorUs  = 150
	defaultRAWCarrier = DefaultFreqHz
)

// ProtocolCodec encodes and decodes a single IR protocol family.
type ProtocolCodec interface {
	Name() string
	Carrier() CarrierSpec
	Encode(sig IRSignal) (PulseTrain, error)
	Decode(pulses PulseTrain) (IRSignal, bool)
}

// ProtocolRegistry dispatches encoding and decoding across registered codecs
// in deterministic priority order.
type ProtocolRegistry struct {
	order  []ProtocolCodec
	byName map[string]ProtocolCodec
}

// NewProtocolRegistry constructs an empty registry.
func NewProtocolRegistry() *ProtocolRegistry {
	return &ProtocolRegistry{
		byName: make(map[string]ProtocolCodec),
	}
}

// DefaultRegistry returns a registry pre-populated with all 7 structured
// protocols plus RAW in deterministic priority order.
func DefaultRegistry() *ProtocolRegistry {
	r := NewProtocolRegistry()
	r.Register(necCodec{})
	r.Register(necExtCodec{})
	r.Register(samsung32Codec{})
	r.Register(rc6Codec{})
	r.Register(rc5Codec{})
	r.Register(sircCodec{})
	r.Register(kaseikyoCodec{})
	r.Register(rawCodec{})
	return r
}

// Register adds or replaces a ProtocolCodec in the registry.
func (r *ProtocolRegistry) Register(codec ProtocolCodec) {
	if codec == nil {
		return
	}
	name := codec.Name()
	if _, exists := r.byName[name]; exists {
		for i, c := range r.order {
			if c.Name() == name {
				r.order[i] = codec
				break
			}
		}
	} else {
		r.order = append(r.order, codec)
	}
	r.byName[name] = codec
}

// Lookup returns the codec registered under name.
func (r *ProtocolRegistry) Lookup(name string) (ProtocolCodec, bool) {
	c, ok := r.byName[name]
	return c, ok
}

// Names returns all registered protocol names in priority order.
func (r *ProtocolRegistry) Names() []string {
	out := make([]string, len(r.order))
	for i, c := range r.order {
		out[i] = c.Name()
	}
	return out
}

// Encode encodes sig into a signed PulseTrain using its protocol or RAW codec.
func (r *ProtocolRegistry) Encode(sig IRSignal) (PulseTrain, error) {
	name := sig.Protocol
	if sig.Kind == SignalRaw || name == "" || name == "RAW" {
		name = "RAW"
	}
	codec, ok := r.Lookup(name)
	if !ok {
		return nil, fmt.Errorf("protocol %q: %w", name, ErrUnsupportedProtocol)
	}
	return codec.Encode(sig)
}

// Decode tries each registered structured codec in priority order and returns
// the first match. Unrecognized or invalid pulse trains return (IRSignal{}, false).
func (r *ProtocolRegistry) Decode(pulses PulseTrain) (IRSignal, bool) {
	if err := ValidatePulseTrain(pulses); err != nil {
		return IRSignal{}, false
	}
	for _, codec := range r.order {
		if codec.Name() == "RAW" {
			continue
		}
		if sig, ok := codec.Decode(pulses); ok {
			return sig, true
		}
	}
	return IRSignal{}, false
}

// DecodeOrRaw attempts structured decoding and falls back to a SignalRaw
// preserving the normalized pulse timings.
func (r *ProtocolRegistry) DecodeOrRaw(pulses PulseTrain, carrier CarrierSpec) (IRSignal, error) {
	if err := ValidatePulseTrain(pulses); err != nil {
		return IRSignal{}, err
	}
	if sig, ok := r.Decode(pulses); ok {
		return sig, nil
	}
	lircPulses, err := NormalizeForLIRC(pulses)
	if err != nil {
		return IRSignal{}, err
	}
	freq := carrier.FreqHz
	if freq == 0 {
		freq = defaultRAWCarrier
	}
	return IRSignal{
		Kind:      SignalRaw,
		Protocol:  "RAW",
		Frequency: freq,
		DutyCycle: carrier.DutyFloat(),
		RawData:   lircPulses,
	}, nil
}

// matchDuration checks whether got matches want in sign and within +-22% (min +-150us).
func matchDuration(got, want int32) bool {
	if got == 0 || want == 0 || got == math.MinInt32 || want == math.MinInt32 {
		return false
	}
	if (got > 0) != (want > 0) {
		return false
	}
	ag := absInt32(got)
	aw := absInt32(want)
	tol := max((aw*timingTolPercent)/100, timingTolFloorUs)
	diff := ag - aw
	if diff < 0 {
		diff = -diff
	}
	return diff <= tol
}

func absInt32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}
