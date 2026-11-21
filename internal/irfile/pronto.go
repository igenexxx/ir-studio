package irfile

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

const (
	// ProntoClockPeriodUs is the Philips Pronto 4.145146 MHz base clock period in microseconds.
	ProntoClockPeriodUs = 0.241246

	// ProntoRawFormat is the Pronto CCF preamble word for raw learned modulated IR.
	ProntoRawFormat uint16 = 0x0000

	// MaxProntoBurstPairs caps parsed/formatted burst pairs to prevent DoS/OOM.
	MaxProntoBurstPairs = 512

	prontoHeaderWords    = 4
	prontoLeadOutSpaceUs = 40000
)

// ErrInvalidProntoHex is returned when a Pronto Hex string is malformed or unsupported.
var ErrInvalidProntoHex = errors.New("irfile: invalid pronto hex")

// ParseProntoHex converts a raw learned Pronto Hex string ("0000 006D ...") into an IRSignal.
func ParseProntoHex(name, hexStr string) (IRSignal, error) {
	trimmedName := strings.TrimSpace(name)
	if trimmedName == "" {
		return IRSignal{}, fmt.Errorf("empty signal name: %w", ErrInvalidSignal)
	}
	words, err := parseProntoWords(hexStr)
	if err != nil {
		return IRSignal{}, err
	}
	freqHz, cycleUs, err := validateProntoHeader(words)
	if err != nil {
		return IRSignal{}, err
	}
	raw, err := prontoWordsToMicros(words[prontoHeaderWords:], cycleUs)
	if err != nil {
		return IRSignal{}, err
	}
	return IRSignal{
		Name:      trimmedName,
		Kind:      SignalRaw,
		Protocol:  "RAW",
		Frequency: freqHz,
		DutyCycle: DefaultDutyCycle,
		RawData:   raw,
	}, nil
}

// FormatProntoHex encodes a parsed or raw IRSignal as a raw learned Pronto Hex string.
func FormatProntoHex(sig IRSignal) (string, error) {
	raw, freqHz, err := extractSignalDurations(sig)
	if err != nil {
		return "", err
	}
	w1, cycleUs, err := freqToProntoWord(freqHz)
	if err != nil {
		return "", err
	}
	padded := ensureEvenBurstPairs(raw)
	pairs := len(padded) / 2
	if pairs == 0 || pairs > MaxProntoBurstPairs {
		return "", fmt.Errorf("burst pair count %d out of range [1..%d]: %w", pairs, MaxProntoBurstPairs, ErrInvalidProntoHex)
	}
	return buildProntoString(w1, uint16(pairs), padded, cycleUs), nil
}

func parseProntoWords(hexStr string) ([]uint16, error) {
	fields := strings.Fields(hexStr)
	if len(fields) < prontoHeaderWords {
		return nil, fmt.Errorf("expected at least %d words, got %d: %w", prontoHeaderWords, len(fields), ErrInvalidProntoHex)
	}
	if len(fields) > prontoHeaderWords+2*MaxProntoBurstPairs {
		return nil, fmt.Errorf("word count %d exceeds limit: %w", len(fields), ErrInvalidProntoHex)
	}
	words := make([]uint16, len(fields))
	for i, tok := range fields {
		if len(tok) == 0 || len(tok) > 4 {
			return nil, fmt.Errorf("word %d (%q) invalid length: %w", i, tok, ErrInvalidProntoHex)
		}
		v, err := strconv.ParseUint(tok, 16, 16)
		if err != nil {
			return nil, fmt.Errorf("word %d (%q): %w", i, tok, ErrInvalidProntoHex)
		}
		words[i] = uint16(v)
	}
	return words, nil
}

func validateProntoHeader(words []uint16) (uint32, float64, error) {
	if words[0] != ProntoRawFormat {
		return 0, 0, fmt.Errorf("unsupported format 0x%04X (only 0000 raw supported): %w", words[0], ErrInvalidProntoHex)
	}
	if words[1] == 0 {
		return 0, 0, fmt.Errorf("zero carrier divider: %w", ErrInvalidProntoHex)
	}
	cycleUs := float64(words[1]) * ProntoClockPeriodUs
	freqHz := uint32(math.Round(1_000_000.0 / cycleUs))
	if freqHz < MinFreqHz || freqHz > MaxFreqHz {
		return 0, 0, fmt.Errorf("carrier %d Hz out of range [%d..%d]: %w", freqHz, MinFreqHz, MaxFreqHz, ErrInvalidProntoHex)
	}
	pairs := int(words[2]) + int(words[3])
	if pairs == 0 || pairs > MaxProntoBurstPairs {
		return 0, 0, fmt.Errorf("burst pairs %d out of range [1..%d]: %w", pairs, MaxProntoBurstPairs, ErrInvalidProntoHex)
	}
	wantWords := prontoHeaderWords + 2*pairs
	if len(words) != wantWords {
		return 0, 0, fmt.Errorf("word count %d does not match header expectation %d: %w", len(words), wantWords, ErrInvalidProntoHex)
	}
	return freqHz, cycleUs, nil
}

func prontoWordsToMicros(burstWords []uint16, cycleUs float64) ([]uint32, error) {
	raw := make([]uint32, len(burstWords))
	for i, cycles := range burstWords {
		if cycles == 0 {
			return nil, fmt.Errorf("zero burst duration at index %d: %w", i, ErrInvalidProntoHex)
		}
		us := uint32(math.Round(float64(cycles) * cycleUs))
		raw[i] = min(max(us, MinLIRCPulseUs), MaxLIRCPulseUs)
	}
	return raw, nil
}

func extractSignalDurations(sig IRSignal) ([]uint32, uint32, error) {
	freqHz := sig.CarrierSpec().FreqHz
	if sig.Kind == SignalRaw {
		if len(sig.RawData) == 0 {
			return nil, 0, fmt.Errorf("empty raw data: %w", ErrInvalidSignal)
		}
		for i, d := range sig.RawData {
			if d == 0 || d > MaxLIRCPulseUs {
				return nil, 0, fmt.Errorf("invalid raw duration at %d: %w", i, ErrInvalidPulseTrain)
			}
		}
		return sig.RawData, freqHz, nil
	}
	pt, err := DefaultRegistry().Encode(sig)
	if err != nil {
		return nil, 0, err
	}
	lircPulses, err := pt.ToLIRCPulses()
	if err != nil {
		return nil, 0, err
	}
	return lircPulses, freqHz, nil
}

func freqToProntoWord(freqHz uint32) (uint16, float64, error) {
	if freqHz < MinFreqHz || freqHz > MaxFreqHz {
		return 0, 0, fmt.Errorf("carrier %d Hz out of range [%d..%d]: %w", freqHz, MinFreqHz, MaxFreqHz, ErrInvalidProntoHex)
	}
	div := math.Round(1_000_000.0 / (float64(freqHz) * ProntoClockPeriodUs))
	if div < 1 || div > math.MaxUint16 {
		return 0, 0, fmt.Errorf("carrier divider out of range: %w", ErrInvalidProntoHex)
	}
	w1 := uint16(div)
	return w1, float64(w1) * ProntoClockPeriodUs, nil
}

func ensureEvenBurstPairs(raw []uint32) []uint32 {
	if len(raw)%2 == 0 {
		return raw
	}
	out := make([]uint32, len(raw)+1)
	copy(out, raw)
	out[len(raw)] = prontoLeadOutSpaceUs
	return out
}

func buildProntoString(w1, pairs uint16, durations []uint32, cycleUs float64) string {
	var b strings.Builder
	b.Grow((prontoHeaderWords + len(durations)) * 5)
	fmt.Fprintf(&b, "%04X %04X %04X 0000", ProntoRawFormat, w1, pairs)
	for _, us := range durations {
		cycles := math.Round(float64(us) / cycleUs)
		if cycles < 1 {
			cycles = 1
		}
		if cycles > math.MaxUint16 {
			cycles = math.MaxUint16
		}
		fmt.Fprintf(&b, " %04X", uint16(cycles))
	}
	return b.String()
}
