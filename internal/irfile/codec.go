package irfile

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type parseState struct {
	hasFiletype bool
	version     int
	signals     []IRSignal
	cur         *IRSignal
	curLine     int
}

// Parse reads a Flipper Zero .ir v1 file from r. It supports arbitrarily long
// data: lines (> 1 MiB), CRLF line endings, and comment lines.
func Parse(r io.Reader) (*IRFile, error) {
	br := bufio.NewReaderSize(r, 64*1024)
	var st parseState
	lineNo := 0

	for {
		rawLine, err := br.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("irfile: read line %d: %w", lineNo+1, err)
		}
		if len(rawLine) == 0 && errors.Is(err, io.EOF) {
			break
		}
		lineNo++
		if parseErr := st.processLine(cleanLine(rawLine, lineNo == 1), lineNo); parseErr != nil {
			return nil, parseErr
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}
	return st.finish()
}

func cleanLine(raw string, first bool) string {
	line := strings.TrimRight(raw, "\r\n")
	if first {
		line = strings.TrimPrefix(line, "\xef\xbb\xbf")
	}
	return strings.TrimSpace(line)
}

func (st *parseState) processLine(line string, lineNo int) error {
	if line == "" {
		return nil
	}
	if strings.HasPrefix(line, "#") {
		return st.flushCurrent()
	}
	key, val, ok := strings.Cut(line, ":")
	if !ok {
		return fmt.Errorf("line %d: expected key: value: %w", lineNo, ErrInvalidHeader)
	}
	key = strings.TrimSpace(key)
	val = strings.TrimSpace(val)

	switch key {
	case "Filetype":
		if val != FiletypeHeader {
			return fmt.Errorf("line %d: unexpected filetype %q: %w", lineNo, val, ErrInvalidHeader)
		}
		st.hasFiletype = true
		return nil
	case "Version":
		return st.parseVersion(val, lineNo)
	default:
		return st.parseSignalField(key, val, lineNo)
	}
}

func (st *parseState) parseVersion(val string, lineNo int) error {
	v, err := strconv.Atoi(val)
	if err != nil {
		return fmt.Errorf("line %d: invalid version %q: %w", lineNo, val, ErrInvalidHeader)
	}
	if v != SupportedVersion {
		return fmt.Errorf("line %d: version %d: %w", lineNo, v, ErrUnsupportedVersion)
	}
	st.version = v
	return nil
}

func (st *parseState) parseSignalField(key, val string, lineNo int) error {
	if !st.hasFiletype || st.version != SupportedVersion {
		return fmt.Errorf("line %d: signal field before valid header: %w", lineNo, ErrInvalidHeader)
	}
	if key == "name" {
		if err := st.flushCurrent(); err != nil {
			return err
		}
		st.cur = &IRSignal{Name: val}
		st.curLine = lineNo
		return nil
	}
	if st.cur == nil {
		return fmt.Errorf("line %d: field %q before signal name: %w", lineNo, key, ErrInvalidSignal)
	}
	return applySignalField(st.cur, key, val, lineNo)
}

func applySignalField(sig *IRSignal, key, val string, lineNo int) error {
	switch key {
	case "type":
		return parseSignalKind(sig, val, lineNo)
	case "protocol":
		sig.Protocol = val
	case "address", "command":
		b, err := parseHex4(val)
		if err != nil {
			return fmt.Errorf("line %d: invalid %s: %w", lineNo, key, ErrInvalidSignal)
		}
		if key == "address" {
			sig.Address = b
		} else {
			sig.Command = b
		}
	case "frequency":
		return parseFrequency(sig, val, lineNo)
	case "duty_cycle":
		return parseDutyCycle(sig, val, lineNo)
	case "data":
		return parseRawData(sig, val, lineNo)
	default:
		return fmt.Errorf("line %d: unknown field %q: %w", lineNo, key, ErrInvalidSignal)
	}
	return nil
}

func parseSignalKind(sig *IRSignal, val string, lineNo int) error {
	switch SignalKind(val) {
	case SignalParsed:
		sig.Kind = SignalParsed
	case SignalRaw:
		sig.Kind = SignalRaw
	default:
		return fmt.Errorf("line %d: unknown signal type %q: %w", lineNo, val, ErrInvalidSignal)
	}
	return nil
}

func parseFrequency(sig *IRSignal, val string, lineNo int) error {
	v, err := strconv.ParseUint(val, 10, 32)
	if err != nil || uint32(v) < MinFreqHz || uint32(v) > MaxFreqHz {
		return fmt.Errorf("line %d: frequency %q out of range [%d, %d]: %w",
			lineNo, val, MinFreqHz, MaxFreqHz, ErrInvalidSignal)
	}
	sig.Frequency = uint32(v)
	return nil
}

func parseDutyCycle(sig *IRSignal, val string, lineNo int) error {
	v, err := strconv.ParseFloat(val, 32)
	if err != nil || v <= 0 || v >= 1 {
		return fmt.Errorf("line %d: duty_cycle %q must be in (0, 1): %w", lineNo, val, ErrInvalidSignal)
	}
	sig.DutyCycle = float32(v)
	return nil
}

func parseRawData(sig *IRSignal, val string, lineNo int) error {
	if val == "" {
		return fmt.Errorf("line %d: empty data: %w", lineNo, ErrInvalidSignal)
	}
	for tok := range strings.SplitSeq(val, " ") {
		if tok == "" {
			continue
		}
		u, err := strconv.ParseUint(tok, 10, 32)
		if err != nil || u == 0 {
			return fmt.Errorf("line %d: invalid data value %q: %w", lineNo, tok, ErrInvalidSignal)
		}
		sig.RawData = append(sig.RawData, uint32(u))
	}
	if len(sig.RawData) == 0 {
		return fmt.Errorf("line %d: empty data: %w", lineNo, ErrInvalidSignal)
	}
	return nil
}

func parseHex4(val string) ([4]byte, error) {
	var out [4]byte
	fields := strings.Fields(val)
	if len(fields) != 4 {
		return out, ErrInvalidSignal
	}
	for i, f := range fields {
		if len(f) != 2 {
			return out, ErrInvalidSignal
		}
		v, err := strconv.ParseUint(f, 16, 8)
		if err != nil {
			return out, ErrInvalidSignal
		}
		out[i] = byte(v)
	}
	return out, nil
}

func (st *parseState) flushCurrent() error {
	if st.cur == nil {
		return nil
	}
	if err := validateSignal(*st.cur); err != nil {
		return fmt.Errorf("line %d: %w", st.curLine, err)
	}
	st.signals = append(st.signals, *st.cur)
	st.cur = nil
	return nil
}

func (st *parseState) finish() (*IRFile, error) {
	if !st.hasFiletype || st.version != SupportedVersion {
		return nil, fmt.Errorf("missing Filetype or Version header: %w", ErrInvalidHeader)
	}
	if err := st.flushCurrent(); err != nil {
		return nil, err
	}
	return &IRFile{Version: st.version, Signals: st.signals}, nil
}

func validateSignal(sig IRSignal) error {
	if strings.TrimSpace(sig.Name) == "" {
		return fmt.Errorf("empty signal name: %w", ErrInvalidSignal)
	}
	switch sig.Kind {
	case SignalParsed:
		if strings.TrimSpace(sig.Protocol) == "" {
			return fmt.Errorf("parsed signal %q missing protocol: %w", sig.Name, ErrInvalidSignal)
		}
	case SignalRaw:
		if sig.Frequency < MinFreqHz || sig.Frequency > MaxFreqHz {
			return fmt.Errorf("raw signal %q invalid frequency: %w", sig.Name, ErrInvalidSignal)
		}
		if sig.DutyCycle <= 0 || sig.DutyCycle >= 1 {
			return fmt.Errorf("raw signal %q invalid duty_cycle: %w", sig.Name, ErrInvalidSignal)
		}
		if len(sig.RawData) == 0 {
			return fmt.Errorf("raw signal %q missing data: %w", sig.Name, ErrInvalidSignal)
		}
	default:
		return fmt.Errorf("signal %q missing valid type: %w", sig.Name, ErrInvalidSignal)
	}
	return nil
}

// Write serializes f into Flipper Zero .ir v1 format.
func Write(w io.Writer, f *IRFile) error {
	if f == nil {
		return fmt.Errorf("nil IRFile: %w", ErrInvalidHeader)
	}
	version := f.Version
	if version == 0 {
		version = SupportedVersion
	}
	if version != SupportedVersion {
		return fmt.Errorf("version %d: %w", version, ErrUnsupportedVersion)
	}
	bw := bufio.NewWriter(w)
	if _, err := fmt.Fprintf(bw, "Filetype: %s\nVersion: %d\n", FiletypeHeader, version); err != nil {
		return err
	}
	for _, sig := range f.Signals {
		if err := writeSignal(bw, sig); err != nil {
			return err
		}
	}
	return bw.Flush()
}

func writeSignal(w *bufio.Writer, sig IRSignal) error {
	if err := validateSignal(sig); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "#\nname: %s\ntype: %s\n", sig.Name, sig.Kind); err != nil {
		return err
	}
	if sig.Kind == SignalParsed {
		_, err := fmt.Fprintf(w, "protocol: %s\naddress: %02X %02X %02X %02X\ncommand: %02X %02X %02X %02X\n",
			sig.Protocol,
			sig.Address[0], sig.Address[1], sig.Address[2], sig.Address[3],
			sig.Command[0], sig.Command[1], sig.Command[2], sig.Command[3])
		return err
	}
	if _, err := fmt.Fprintf(w, "frequency: %d\nduty_cycle: %.6f\ndata:", sig.Frequency, sig.DutyCycle); err != nil {
		return err
	}
	for _, d := range sig.RawData {
		if _, err := fmt.Fprintf(w, " %d", d); err != nil {
			return err
		}
	}
	_, err := w.WriteString("\n")
	return err
}

// LoadFile opens path and parses it as a Flipper Zero .ir v1 file.
func LoadFile(path string) (*IRFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Parse(f)
}

// SaveFileAtomic writes f to a temporary file in the target directory, fsyncs it,
// and atomically renames it into place.
func SaveFileAtomic(path string, f *IRFile) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".irfile-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := Write(tmp, f); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}
