package irfile

import (
	"errors"
	"fmt"
	"strings"
)

// ACBrand identifies a supported parametric mini-split AC protocol family.
type ACBrand string

const (
	ACBrandGree       ACBrand = "Gree"
	ACBrandMidea      ACBrand = "Midea"
	ACBrandDaikin     ACBrand = "Daikin"
	ACBrandLG         ACBrand = "LG_AC"
	ACBrandSamsung    ACBrand = "Samsung_AC"
	ACBrandMitsubishi ACBrand = "Mitsubishi"
)

// ACMode identifies the HVAC operating mode.
type ACMode string

const (
	ACModeCool ACMode = "COOL"
	ACModeHeat ACMode = "HEAT"
	ACModeDry  ACMode = "DRY"
	ACModeFan  ACMode = "FAN"
	ACModeAuto ACMode = "AUTO"
)

// ACFan identifies the HVAC blower fan speed.
type ACFan string

const (
	ACFanAuto ACFan = "AUTO"
	ACFanLow  ACFan = "LOW"
	ACFanMed  ACFan = "MED"
	ACFanHigh ACFan = "HIGH"
)

const (
	// ACMinTempC and ACMaxTempC are the canonical HVAC setpoint bounds (°C).
	ACMinTempC = 16
	ACMaxTempC = 30
)

var (
	ErrUnsupportedACBrand = errors.New("irfile: unsupported AC brand")
	ErrInvalidACState     = errors.New("irfile: invalid AC state")
)

// ACModes returns the canonical UI/MCP cycle order for HVAC modes.
func ACModes() []ACMode {
	return []ACMode{ACModeCool, ACModeHeat, ACModeDry, ACModeFan, ACModeAuto}
}

// ACFans returns the canonical UI/MCP cycle order for HVAC fan speeds.
func ACFans() []ACFan {
	return []ACFan{ACFanAuto, ACFanLow, ACFanMed, ACFanHigh}
}

// ACState is the immutable parametric state for a single AC IR state frame.
type ACState struct {
	Brand ACBrand `json:"brand"`
	Power bool    `json:"power"`
	Mode  ACMode  `json:"mode"`
	TempC int     `json:"temp_c"`
	Fan   ACFan   `json:"fan"`
	Swing bool    `json:"swing"`
}

// DefaultACState returns a safe initial HVAC controller state (Gree COOL 22°C).
func DefaultACState() ACState {
	return ACState{
		Brand: ACBrandGree,
		Power: true,
		Mode:  ACModeCool,
		TempC: 22,
		Fan:   ACFanAuto,
		Swing: false,
	}
}

// ACSignalName returns the canonical Flipper .ir button name for state s.
func ACSignalName(s ACState) string {
	if !s.Power {
		return "Power_Off"
	}
	return fmt.Sprintf("%s_%dC", strings.ToUpper(string(s.Mode)), s.TempC)
}

// ValidateACState verifies brand, mode, fan, and temperature bounds [16, 30].
func ValidateACState(s ACState) error {
	if s.TempC < ACMinTempC || s.TempC > ACMaxTempC {
		return fmt.Errorf("temp_c %d out of range [%d..%d]: %w", s.TempC, ACMinTempC, ACMaxTempC, ErrInvalidACState)
	}
	switch s.Mode {
	case ACModeCool, ACModeHeat, ACModeDry, ACModeFan, ACModeAuto:
	default:
		return fmt.Errorf("mode %q: %w", s.Mode, ErrInvalidACState)
	}
	switch s.Fan {
	case "", ACFanAuto, ACFanLow, ACFanMed, ACFanHigh:
	default:
		return fmt.Errorf("fan %q: %w", s.Fan, ErrInvalidACState)
	}
	if s.Brand == "" {
		return fmt.Errorf("empty brand: %w", ErrUnsupportedACBrand)
	}
	return nil
}

// ACEncoder encodes a parametric ACState into protocol bytes and a raw IRSignal.
type ACEncoder interface {
	Brand() ACBrand
	Carrier() CarrierSpec
	ChecksumKind() string
	EncodeBytes(state ACState) ([]byte, error)
	EncodeSignal(state ACState) (IRSignal, error)
}

// ACRegistry dispatches parametric AC state encoding across registered brands.
type ACRegistry struct {
	order   []ACEncoder
	byBrand map[ACBrand]ACEncoder
}

// NewACRegistry constructs an empty ACRegistry.
func NewACRegistry() *ACRegistry {
	return &ACRegistry{byBrand: make(map[ACBrand]ACEncoder)}
}

// DefaultACRegistry returns a registry with all 6 supported HVAC brands.
func DefaultACRegistry() *ACRegistry {
	r := NewACRegistry()
	r.Register(greeACEncoder{})
	r.Register(mideaACEncoder{})
	r.Register(daikinACEncoder{})
	r.Register(lgACEncoder{})
	r.Register(samsungACEncoder{})
	r.Register(mitsubishiACEncoder{})
	return r
}

// Register adds or replaces an ACEncoder in r.
func (r *ACRegistry) Register(enc ACEncoder) {
	if enc == nil {
		return
	}
	b := enc.Brand()
	if _, ok := r.byBrand[b]; ok {
		for i, cur := range r.order {
			if cur.Brand() == b {
				r.order[i] = enc
				break
			}
		}
	} else {
		r.order = append(r.order, enc)
	}
	r.byBrand[b] = enc
}

// Lookup returns the ACEncoder registered for brand.
func (r *ACRegistry) Lookup(brand ACBrand) (ACEncoder, bool) {
	enc, ok := r.byBrand[brand]
	return enc, ok
}

// Brands returns all registered AC brands in deterministic UI cycle order.
func (r *ACRegistry) Brands() []ACBrand {
	out := make([]ACBrand, len(r.order))
	for i, enc := range r.order {
		out[i] = enc.Brand()
	}
	return out
}

// EncodeBytes validates state and returns the brand's checksummed byte frame.
func (r *ACRegistry) EncodeBytes(state ACState) ([]byte, error) {
	enc, err := r.resolveEncoder(state)
	if err != nil {
		return nil, err
	}
	return enc.EncodeBytes(state)
}

// EncodeSignal validates state and encodes it into a 38 kHz RAW IRSignal.
func (r *ACRegistry) EncodeSignal(state ACState) (IRSignal, error) {
	enc, err := r.resolveEncoder(state)
	if err != nil {
		return IRSignal{}, err
	}
	return enc.EncodeSignal(state)
}

func (r *ACRegistry) resolveEncoder(state ACState) (ACEncoder, error) {
	if err := ValidateACState(state); err != nil {
		return nil, err
	}
	enc, ok := r.Lookup(state.Brand)
	if !ok {
		return nil, fmt.Errorf("brand %q: %w", state.Brand, ErrUnsupportedACBrand)
	}
	return enc, nil
}
