package irfile

import (
	"errors"
	"math"
)

const (
	// FiletypeHeader is the canonical Flipper Zero .ir file header value.
	FiletypeHeader = "IR signals file"
	// SupportedVersion is the supported Flipper Zero .ir format version.
	SupportedVersion = 1

	// MaxIRFileBytes is the SSOT maximum untrusted .ir file size (1 MiB).
	MaxIRFileBytes = 1 << 20

	// MinFreqHz and MaxFreqHz bound valid carrier frequencies in .ir files.
	MinFreqHz uint32 = 10000
	MaxFreqHz uint32 = 100000

	// DefaultFreqHz, DefaultDutyPct, and DefaultDutyCycle are standard 38 kHz IR defaults.
	DefaultFreqHz    uint32  = 38000
	DefaultDutyPct   uint32  = 33
	DefaultDutyCycle float32 = 0.33

	// MinLIRCPulseUs and MaxLIRCPulseUs bound outbound LIRC pulse durations.
	MinLIRCPulseUs uint32 = 1
	MaxLIRCPulseUs uint32 = 1_000_000
)

var (
	ErrInvalidHeader       = errors.New("irfile: invalid header")
	ErrUnsupportedVersion  = errors.New("irfile: unsupported version")
	ErrInvalidSignal       = errors.New("irfile: invalid signal")
	ErrUnsupportedProtocol = errors.New("irfile: unsupported protocol")
	ErrInvalidPulseTrain   = errors.New("irfile: invalid pulse train")
	ErrFileTooLarge        = errors.New("irfile: file exceeds maximum size")
)

// SignalKind distinguishes parsed protocol signals from raw timing signals.
type SignalKind string

const (
	SignalParsed SignalKind = "parsed"
	SignalRaw    SignalKind = "raw"
)

// CarrierSpec holds carrier frequency (Hz) and duty cycle (%).
type CarrierSpec struct {
	FreqHz  uint32
	DutyPct uint32
}

// DutyFloat returns the duty cycle as a fraction in (0, 1).
func (c CarrierSpec) DutyFloat() float32 {
	if c.DutyPct == 0 {
		return DefaultDutyCycle
	}
	return float32(c.DutyPct) / 100.0
}

// IsZero reports whether both carrier fields are unset.
func (c CarrierSpec) IsZero() bool {
	return c.FreqHz == 0 && c.DutyPct == 0
}

// IRSignal represents a single named IR command inside a Flipper .ir file.
type IRSignal struct {
	Name      string
	Kind      SignalKind
	Protocol  string
	Address   [4]byte
	Command   [4]byte
	Frequency uint32
	DutyCycle float32
	RawData   []uint32
	Repeat    bool
}

// CarrierSpec returns the effective carrier specification for this signal.
func (s IRSignal) CarrierSpec() CarrierSpec {
	freq := s.Frequency
	if freq == 0 {
		freq = DefaultFreqHz
	}
	duty := uint32(math.Round(float64(s.DutyCycle) * 100))
	if duty == 0 {
		duty = DefaultDutyPct
	}
	return CarrierSpec{FreqHz: freq, DutyPct: duty}
}

// IRFile represents a multi-button Flipper Zero .ir v1 file.
type IRFile struct {
	Version int
	Signals []IRSignal
}

// PulseTrain is an ordered slice of signed microsecond durations (+mark, -space)
// that strictly alternate in sign.
type PulseTrain []int32

// ValidatePulseTrain verifies that p is non-empty, contains no zero or MinInt32
// values, and strictly alternates between positive marks and negative spaces.
func ValidatePulseTrain(p PulseTrain) error {
	if len(p) == 0 {
		return ErrInvalidPulseTrain
	}
	for i, v := range p {
		if v == 0 || v == math.MinInt32 {
			return ErrInvalidPulseTrain
		}
		if i > 0 && (v > 0) == (p[i-1] > 0) {
			return ErrInvalidPulseTrain
		}
	}
	return nil
}

// NormalizeForLIRC validates p and converts it into an odd-length positive
// uint32 slice starting and ending with a mark, clamped to [MinLIRCPulseUs, MaxLIRCPulseUs].
func NormalizeForLIRC(p PulseTrain) ([]uint32, error) {
	if err := ValidatePulseTrain(p); err != nil {
		return nil, err
	}
	start, end := 0, len(p)
	if p[start] < 0 {
		start++
	}
	if end > start && p[end-1] < 0 {
		end--
	}
	if end <= start || (end-start)%2 == 0 {
		return nil, ErrInvalidPulseTrain
	}
	out := make([]uint32, end-start)
	for i := range out {
		v := p[start+i]
		if v < 0 {
			v = -v
		}
		u := uint32(v)
		out[i] = min(max(u, MinLIRCPulseUs), MaxLIRCPulseUs)
	}
	return out, nil
}

// ToLIRCPulses converts the pulse train into an odd-length LIRC TX pulse array.
func (p PulseTrain) ToLIRCPulses() ([]uint32, error) {
	return NormalizeForLIRC(p)
}

// FromRawData converts Flipper .ir raw alternating durations into a signed PulseTrain.
func FromRawData(raw []uint32) (PulseTrain, error) {
	if len(raw) == 0 {
		return nil, ErrInvalidPulseTrain
	}
	out := make(PulseTrain, len(raw))
	for i, d := range raw {
		if d == 0 || d > math.MaxInt32 {
			return nil, ErrInvalidPulseTrain
		}
		if i%2 == 0 {
			out[i] = int32(d)
		} else {
			out[i] = -int32(d)
		}
	}
	return out, nil
}
