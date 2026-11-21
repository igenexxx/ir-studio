package irfile_test

import (
	"bytes"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ir-studio/internal/irfile"
)

const sampleIR = `Filetype: IR signals file
Version: 1
# Samsung TV 2024
name: Power
type: parsed
protocol: NEC
address: 04 00 00 00
command: 08 00 00 00
#
name: AC_Cool_22
type: raw
frequency: 38000
duty_cycle: 0.330000
data: 9000 4500 560 560 560 1690 560
`

func TestParseAndWriteRoundTrip(t *testing.T) {
	f, err := irfile.Parse(strings.NewReader(sampleIR))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if f.Version != 1 || len(f.Signals) != 2 {
		t.Fatalf("got version=%d signals=%d, want 1 and 2", f.Version, len(f.Signals))
	}
	if f.Signals[0].Name != "Power" || f.Signals[0].Kind != irfile.SignalParsed ||
		f.Signals[0].Protocol != "NEC" || f.Signals[0].Address[0] != 0x04 || f.Signals[0].Command[0] != 0x08 {
		t.Fatalf("unexpected signal 0: %+v", f.Signals[0])
	}
	if f.Signals[1].Name != "AC_Cool_22" || f.Signals[1].Kind != irfile.SignalRaw ||
		f.Signals[1].Frequency != 38000 || len(f.Signals[1].RawData) != 7 {
		t.Fatalf("unexpected signal 1: %+v", f.Signals[1])
	}

	var buf bytes.Buffer
	if err := irfile.Write(&buf, f); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	f2, err := irfile.Parse(&buf)
	if err != nil {
		t.Fatalf("re-Parse failed: %v", err)
	}
	if len(f2.Signals) != len(f.Signals) {
		t.Fatalf("round-trip signal count mismatch: %d vs %d", len(f2.Signals), len(f.Signals))
	}
	if f2.Signals[0].Address != f.Signals[0].Address || f2.Signals[0].Command != f.Signals[0].Command {
		t.Fatalf("round-trip parsed mismatch: %+v vs %+v", f2.Signals[0], f.Signals[0])
	}
	if len(f2.Signals[1].RawData) != len(f.Signals[1].RawData) {
		t.Fatalf("round-trip raw mismatch: %v vs %v", f2.Signals[1].RawData, f.Signals[1].RawData)
	}
}

func TestParseCRLFAndCommentsAndNoTrailingNewline(t *testing.T) {
	crlf := "\xef\xbb\xbf" + strings.ReplaceAll(strings.TrimSuffix(sampleIR, "\n"), "\n", "\r\n")
	f, err := irfile.Parse(strings.NewReader(crlf))
	if err != nil {
		t.Fatalf("Parse CRLF+BOM failed: %v", err)
	}
	if len(f.Signals) != 2 {
		t.Fatalf("got %d signals, want 2", len(f.Signals))
	}
}

func TestParseHeaderOnly(t *testing.T) {
	f, err := irfile.Parse(strings.NewReader("Filetype: IR signals file\nVersion: 1\n"))
	if err != nil {
		t.Fatalf("Parse header-only failed: %v", err)
	}
	if len(f.Signals) != 0 {
		t.Fatalf("got %d signals, want 0", len(f.Signals))
	}
}

func TestParseLargeRawDataLineOver1MiB(t *testing.T) {
	// Verify > 1 MiB data: line does not fail with bufio.Scanner token limit.
	var sb strings.Builder
	sb.WriteString("Filetype: IR signals file\nVersion: 1\n#\nname: HugeAC\ntype: raw\nfrequency: 38000\nduty_cycle: 0.330000\ndata:")
	const count = 200_000 // ~1.2 MB line
	for range count {
		sb.WriteString(" 1234")
	}
	sb.WriteByte('\n')

	f, err := irfile.Parse(strings.NewReader(sb.String()))
	if err != nil {
		t.Fatalf("Parse >1MiB data line failed: %v", err)
	}
	if len(f.Signals) != 1 || len(f.Signals[0].RawData) != count {
		t.Fatalf("got %d pulses, want %d", len(f.Signals[0].RawData), count)
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr error
	}{
		{name: "empty file", input: "", wantErr: irfile.ErrInvalidHeader},
		{name: "wrong filetype", input: "Filetype: SubGhz\nVersion: 1\n", wantErr: irfile.ErrInvalidHeader},
		{name: "unsupported version", input: "Filetype: IR signals file\nVersion: 2\n", wantErr: irfile.ErrUnsupportedVersion},
		{name: "non-numeric version", input: "Filetype: IR signals file\nVersion: abc\n", wantErr: irfile.ErrInvalidHeader},
		{name: "no colon", input: "Filetype IR signals file\n", wantErr: irfile.ErrInvalidHeader},
		{name: "signal before header", input: "name: Power\n", wantErr: irfile.ErrInvalidHeader},
		{name: "field before name", input: "Filetype: IR signals file\nVersion: 1\ntype: parsed\n", wantErr: irfile.ErrInvalidSignal},
		{name: "unknown type", input: "Filetype: IR signals file\nVersion: 1\nname: X\ntype: foo\n", wantErr: irfile.ErrInvalidSignal},
		{name: "bad address hex", input: "Filetype: IR signals file\nVersion: 1\nname: X\ntype: parsed\nprotocol: NEC\naddress: ZZ 00 00 00\n", wantErr: irfile.ErrInvalidSignal},
		{name: "short address", input: "Filetype: IR signals file\nVersion: 1\nname: X\ntype: parsed\nprotocol: NEC\naddress: 04 00\n", wantErr: irfile.ErrInvalidSignal},
		{name: "bad command hex", input: "Filetype: IR signals file\nVersion: 1\nname: X\ntype: parsed\nprotocol: NEC\ncommand: 123 00 00 00\n", wantErr: irfile.ErrInvalidSignal},
		{name: "freq too low", input: "Filetype: IR signals file\nVersion: 1\nname: X\ntype: raw\nfrequency: 5000\nduty_cycle: 0.33\ndata: 500\n", wantErr: irfile.ErrInvalidSignal},
		{name: "freq too high", input: "Filetype: IR signals file\nVersion: 1\nname: X\ntype: raw\nfrequency: 200000\nduty_cycle: 0.33\ndata: 500\n", wantErr: irfile.ErrInvalidSignal},
		{name: "duty zero", input: "Filetype: IR signals file\nVersion: 1\nname: X\ntype: raw\nfrequency: 38000\nduty_cycle: 0\ndata: 500\n", wantErr: irfile.ErrInvalidSignal},
		{name: "duty one", input: "Filetype: IR signals file\nVersion: 1\nname: X\ntype: raw\nfrequency: 38000\nduty_cycle: 1.0\ndata: 500\n", wantErr: irfile.ErrInvalidSignal},
		{name: "zero pulse in data", input: "Filetype: IR signals file\nVersion: 1\nname: X\ntype: raw\nfrequency: 38000\nduty_cycle: 0.33\ndata: 500 0 500\n", wantErr: irfile.ErrInvalidSignal},
		{name: "negative pulse in data", input: "Filetype: IR signals file\nVersion: 1\nname: X\ntype: raw\nfrequency: 38000\nduty_cycle: 0.33\ndata: 500 -100\n", wantErr: irfile.ErrInvalidSignal},
		{name: "empty data", input: "Filetype: IR signals file\nVersion: 1\nname: X\ntype: raw\nfrequency: 38000\nduty_cycle: 0.33\ndata: \n", wantErr: irfile.ErrInvalidSignal},
		{name: "unknown field", input: "Filetype: IR signals file\nVersion: 1\nname: X\nfoo: bar\n", wantErr: irfile.ErrInvalidSignal},
		{name: "missing protocol", input: "Filetype: IR signals file\nVersion: 1\nname: X\ntype: parsed\n", wantErr: irfile.ErrInvalidSignal},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := irfile.Parse(strings.NewReader(tc.input))
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Parse() err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestSaveAndLoadFileAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tv.ir")
	f, err := irfile.Parse(strings.NewReader(sampleIR))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := irfile.SaveFileAtomic(path, f); err != nil {
		t.Fatalf("SaveFileAtomic: %v", err)
	}
	loaded, err := irfile.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if len(loaded.Signals) != 2 {
		t.Fatalf("got %d signals, want 2", len(loaded.Signals))
	}

	// Saving into nonexistent directory must fail without leaving partial files.
	badPath := filepath.Join(dir, "no-such-dir", "tv.ir")
	if err := irfile.SaveFileAtomic(badPath, f); err == nil {
		t.Fatal("expected error saving into nonexistent dir")
	}
	if _, err := os.Stat(badPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no file at %s, got err=%v", badPath, err)
	}

	// Invalid Write & LoadFile calls.
	if _, err := irfile.LoadFile(filepath.Join(dir, "missing.ir")); err == nil {
		t.Fatal("expected error loading missing file")
	}
	var buf bytes.Buffer
	if err := irfile.Write(&buf, nil); !errors.Is(err, irfile.ErrInvalidHeader) {
		t.Fatalf("Write(nil) = %v, want ErrInvalidHeader", err)
	}
	if err := irfile.Write(&buf, &irfile.IRFile{Version: 9}); !errors.Is(err, irfile.ErrUnsupportedVersion) {
		t.Fatalf("Write(v9) = %v, want ErrUnsupportedVersion", err)
	}
	badSigs := []irfile.IRSignal{
		{Name: "", Kind: irfile.SignalParsed, Protocol: "NEC"},
		{Name: "BadKind", Kind: "other"},
		{Name: "BadRawFreq", Kind: irfile.SignalRaw, Frequency: 500, DutyCycle: 0.33, RawData: []uint32{500}},
		{Name: "BadRawDuty", Kind: irfile.SignalRaw, Frequency: 38000, DutyCycle: 0, RawData: []uint32{500}},
		{Name: "BadRawData", Kind: irfile.SignalRaw, Frequency: 38000, DutyCycle: 0.33},
	}
	for _, s := range badSigs {
		if err := irfile.Write(&buf, &irfile.IRFile{Version: 1, Signals: []irfile.IRSignal{s}}); !errors.Is(err, irfile.ErrInvalidSignal) {
			t.Fatalf("Write(%s) = %v, want ErrInvalidSignal", s.Name, err)
		}
	}
}

func TestPulseTrainValidationAndNormalization(t *testing.T) {
	tests := []struct {
		name    string
		pulses  irfile.PulseTrain
		wantLen int
		wantErr error
	}{
		{name: "empty", pulses: nil, wantErr: irfile.ErrInvalidPulseTrain},
		{name: "contains zero", pulses: irfile.PulseTrain{500, 0, 500}, wantErr: irfile.ErrInvalidPulseTrain},
		{name: "min int32", pulses: irfile.PulseTrain{math.MinInt32}, wantErr: irfile.ErrInvalidPulseTrain},
		{name: "same sign consecutive", pulses: irfile.PulseTrain{500, 500}, wantErr: irfile.ErrInvalidPulseTrain},
		{name: "only space", pulses: irfile.PulseTrain{-500}, wantErr: irfile.ErrInvalidPulseTrain},
		{name: "valid odd", pulses: irfile.PulseTrain{9000, -4500, 562}, wantLen: 3},
		{name: "trims trailing space", pulses: irfile.PulseTrain{9000, -4500, 562, -1000}, wantLen: 3},
		{name: "trims leading space", pulses: irfile.PulseTrain{-889, 889, -889, 889}, wantLen: 3},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := tc.pulses.ToLIRCPulses()
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("ToLIRCPulses() err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(out) != tc.wantLen || len(out)%2 != 1 {
				t.Fatalf("got len=%d (%v), want odd len %d", len(out), out, tc.wantLen)
			}
		})
	}

	// FromRawData checks.
	if _, err := irfile.FromRawData(nil); !errors.Is(err, irfile.ErrInvalidPulseTrain) {
		t.Fatalf("FromRawData(nil) = %v, want ErrInvalidPulseTrain", err)
	}
	if _, err := irfile.FromRawData([]uint32{500, 0}); !errors.Is(err, irfile.ErrInvalidPulseTrain) {
		t.Fatalf("FromRawData(with 0) = %v, want ErrInvalidPulseTrain", err)
	}
	pt, err := irfile.FromRawData([]uint32{9000, 4500, 560})
	if err != nil || len(pt) != 3 || pt[0] != 9000 || pt[1] != -4500 || pt[2] != 560 {
		t.Fatalf("FromRawData unexpected result: %v, err=%v", pt, err)
	}
}

func FuzzParseIRFile(f *testing.F) {
	f.Add([]byte(sampleIR))
	f.Add([]byte("Filetype: IR signals file\nVersion: 1\n"))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, data []byte) {
		parsed, err := irfile.Parse(bytes.NewReader(data))
		if err != nil {
			return
		}
		var buf bytes.Buffer
		if err := irfile.Write(&buf, parsed); err != nil {
			t.Fatalf("Write failed on parsed output: %v", err)
		}
		parsed2, err := irfile.Parse(&buf)
		if err != nil {
			t.Fatalf("re-Parse failed on Write output: %v", err)
		}
		if len(parsed2.Signals) != len(parsed.Signals) {
			t.Fatalf("round-trip count mismatch: %d vs %d", len(parsed2.Signals), len(parsed.Signals))
		}
	})
}
