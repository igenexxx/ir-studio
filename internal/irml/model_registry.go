package irml

import (
	_ "embed"
	"os"
	"path/filepath"
	"sync"
)

//go:embed models/pulsenet_ir_v1.czml
var embeddedPulseNetIR []byte

var (
	pulseNetOnce sync.Once
	pulseNetDef  *CZMLModel
	pulseNetInfo ModelInfo

	pulseWS = sync.Pool{New: func() any { return new(PulseNetWorkspace) }}
)

// DefaultModelsDir returns $HOME/.config/ir-studio/models.
func DefaultModelsDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".", ".config", "ir-studio", "models")
	}
	return filepath.Join(home, ".config", "ir-studio", "models")
}

// LoadDefaultPulseNet loads a verified override .czml from overridePath if valid,
// falling back cleanly to the embedded PulseNet-IR model.
func LoadDefaultPulseNet(overridePath string) (*CZMLModel, ModelInfo) {
	if overridePath != "" {
		if m, err := LoadCZMLFile(overridePath, DomainIRPulse); err == nil {
			if ValidatePulseNetModel(m) == nil {
				return m, m.Info("override")
			}
		}
	}
	m, err := LoadCZMLBytes(embeddedPulseNetIR, DomainIRPulse)
	if err != nil || ValidatePulseNetModel(m) != nil {
		return nil, ModelInfo{Source: "unavailable"}
	}
	return m, m.Info("embedded")
}

func defaultPulseNet() *CZMLModel {
	pulseNetOnce.Do(func() {
		override := filepath.Join(DefaultModelsDir(), "pulsenet_ir.czml")
		pulseNetDef, pulseNetInfo = LoadDefaultPulseNet(override)
	})
	return pulseNetDef
}

// ActivePulseNetInfo returns provenance metadata for the active PulseNet-IR model.
func ActivePulseNetInfo() ModelInfo {
	_ = defaultPulseNet()
	return pulseNetInfo
}

// DenoisePulsesNeural runs PulseNet-IR's lazy 1D-UNet TSOP AGC denoiser.
func DenoisePulsesNeural(dst, raw []int32, carrierHz uint32) ([]int32, int, bool) {
	m := defaultPulseNet()
	if m == nil || len(raw) == 0 {
		return dst[:0], 0, false
	}
	ws := pulseWS.Get().(*PulseNetWorkspace)
	defer pulseWS.Put(ws)
	out, glitches := m.DenoiseNeuralInto(ws, dst, raw, carrierHz)
	return out, glitches, len(out) > 0
}
