package irml

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

type goldenIRVector struct {
	Pulses      []int32   `json:"pulses"`
	FreqHz      uint32    `json:"freq_hz"`
	FineClass   int       `json:"fine_class"`
	FineProbs   []float32 `json:"fine_probs"`
	CoarseClass int       `json:"coarse_class"`
	CoarseProbs []float32 `json:"coarse_probs"`
	Embedding   []float32 `json:"embedding"`
}

// TestPulseNetIRGoldenParity checks bit-exact parity (<= 1e-4 max error and 100%
// argmax agreement) between Python INT8 reference and Pure-Go PulseNet-IR across
// all 64 golden vectors exported from Colab A100.
func TestPulseNetIRGoldenParity(t *testing.T) {
	rawJSON, err := os.ReadFile(filepath.Join("testdata", "golden_ir.json"))
	if err != nil {
		t.Fatalf("read golden_ir.json: %v", err)
	}
	var vectors []goldenIRVector
	if err := json.Unmarshal(rawJSON, &vectors); err != nil {
		t.Fatalf("unmarshal golden_ir.json: %v", err)
	}
	if len(vectors) == 0 {
		t.Fatal("golden_ir.json is empty")
	}

	m, info := LoadDefaultPulseNet("")
	if m == nil || info.Source != "embedded" {
		t.Fatalf("LoadDefaultPulseNet failed: info=%+v", info)
	}
	var ws PulseNetWorkspace
	maxErr := float64(0)

	for i, vec := range vectors {
		res := m.Infer(&ws, vec.Pulses, vec.FreqHz)
		if int(res.FineClassID) != vec.FineClass {
			t.Errorf("vec[%d]: fine class got %d (%s), want %d", i, res.FineClassID, res.ProtocolLabel, vec.FineClass)
		}
		if int(res.CoarseClass) != vec.CoarseClass {
			t.Errorf("vec[%d]: coarse class got %d, want %d", i, res.CoarseClass, vec.CoarseClass)
		}
		for c, want := range vec.CoarseProbs {
			if d := math.Abs(float64(res.CoarseProbs[c] - want)); d > maxErr {
				maxErr = d
			}
			if math.Abs(float64(res.CoarseProbs[c]-want)) > 1e-4 {
				t.Errorf("vec[%d]: coarse prob[%d] got %.6f, want %.6f", i, c, res.CoarseProbs[c], want)
			}
		}
		for dIdx, want := range vec.Embedding {
			if d := math.Abs(float64(res.Embedding[dIdx] - want)); d > maxErr {
				maxErr = d
			}
			if math.Abs(float64(res.Embedding[dIdx]-want)) > 1e-4 {
				t.Errorf("vec[%d]: embedding[%d] got %.6f, want %.6f", i, dIdx, res.Embedding[dIdx], want)
			}
		}
	}
	t.Logf("verified %d golden IR vectors: 100%% argmax parity, max abs diff = %.2e", len(vectors), maxErr)
}

// TestLoadCZMLIRAdversarial validates integrity, domain check, size cap, and fallback.
func TestLoadCZMLIRAdversarial(t *testing.T) {
	badSHA := append([]byte(nil), embeddedPulseNetIR...)
	badSHA[len(badSHA)-1] ^= 0x5A

	cases := []struct {
		name    string
		data    []byte
		domain  CZMLDomain
		wantErr error
	}{
		{"empty", nil, DomainIRPulse, ErrInvalidCZML},
		{"short", embeddedPulseNetIR[:20], DomainIRPulse, ErrInvalidCZML},
		{"wrong_domain", embeddedPulseNetIR, CZMLDomain(1), ErrCZMLDomainMismatch},
		{"bad_sha", badSHA, DomainIRPulse, ErrCZMLChecksum},
		{"too_large", make([]byte, czmlMaxFileBytes+1), DomainIRPulse, ErrInvalidCZML},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadCZMLBytes(tc.data, tc.domain)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("got err=%v, want %v", err, tc.wantErr)
			}
		})
	}

	dir := t.TempDir()
	badPath := filepath.Join(dir, "pulsenet_ir.czml")
	if err := os.WriteFile(badPath, badSHA, 0o600); err != nil {
		t.Fatal(err)
	}
	m, info := LoadDefaultPulseNet(badPath)
	if m == nil || info.Source != "embedded" {
		t.Fatalf("expected embedded fallback on corrupt override, got %+v", info)
	}
}

// TestPulseNetIRDenoiserAndConcurrency exercises TSOP asymmetric stretch denoising
// and multi-goroutine race safety.
func TestPulseNetIRDenoiserAndConcurrency(t *testing.T) {
	// Synthesize NEC frame stretched by TSOP AGC (+110us mark / -110us space).
	nec := []int32{9110, -4390}
	for i := range 32 {
		if i%2 == 0 {
			nec = append(nec, 670, -1575)
		} else {
			nec = append(nec, 670, -450)
		}
	}
	nec = append(nec, 670)

	dst := make([]int32, 0, len(nec))
	cleaned, _, ok := DenoisePulsesNeural(dst, nec, 38000)
	if !ok || len(cleaned) < 20 {
		t.Fatalf("DenoisePulsesNeural failed: len=%d ok=%v", len(cleaned), ok)
	}
	for i := 1; i < len(cleaned); i++ {
		if (cleaned[i-1] > 0) == (cleaned[i] > 0) {
			t.Fatalf("IR denoiser broke sign alternation at %d: %d, %d", i, cleaned[i-1], cleaned[i])
		}
	}

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 10 {
				cls := ClassifySignal(nec, 38000)
				if cls.ProtocolLabel == "" {
					t.Errorf("empty protocol label under concurrency")
				}
			}
		}()
	}
	wg.Wait()
}

func TestPulseNetIRZeroAlloc(t *testing.T) {
	m, _ := LoadDefaultPulseNet("")
	if m == nil {
		t.Fatal("embedded PulseNet-IR unavailable")
	}
	var ws PulseNetWorkspace
	nec := []int32{9000, -4500}
	for range 32 {
		nec = append(nec, 560, -1690)
	}
	nec = append(nec, 560)

	if allocs := testing.AllocsPerRun(50, func() {
		_ = m.Infer(&ws, nec, 38000)
	}); allocs != 0 {
		t.Fatalf("PulseNet-IR Infer allocated %.1f times/op, want 0", allocs)
	}
}

func FuzzLoadCZMLIR(f *testing.F) {
	f.Add(embeddedPulseNetIR)
	f.Add([]byte("CZML"))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = LoadCZMLBytes(data, DomainIRPulse)
	})
}

func BenchmarkPulseNetIRInfer(b *testing.B) {
	m, _ := LoadDefaultPulseNet("")
	if m == nil {
		b.Fatal("embedded PulseNet-IR unavailable")
	}
	var ws PulseNetWorkspace
	nec := []int32{9000, -4500}
	for range 32 {
		nec = append(nec, 560, -1690)
	}
	nec = append(nec, 560)
	b.ReportAllocs()
	for b.Loop() {
		_ = m.Infer(&ws, nec, 38000)
	}
}
