package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// SSOT carrier, duty, timing, and permission constants.
const (
	DefaultCarrierHz        uint32 = 38000
	DefaultDutyPct          uint32 = 33
	DefaultRecTimeoutUs     uint32 = 15000
	DefaultProbeIntervalMs         = 1200
	DefaultAmbientThreshold        = 150

	MinCarrierHz uint32 = 36000
	MaxCarrierHz uint32 = 56000
	MinDutyPct   uint32 = 20
	MaxDutyPct   uint32 = 50

	MinRecTimeoutUs     uint32 = 5000
	MaxRecTimeoutUs     uint32 = 100000
	MinProbeIntervalMs         = 100
	MaxProbeIntervalMs         = 10000
	MinAmbientThreshold        = 10
	MaxAmbientThreshold        = 1000

	ConfigDirPerm  os.FileMode = 0o700
	ConfigFilePerm os.FileMode = 0o600
)

// AllowedCarrierHz lists the UI-selectable TX carrier presets (SSOT).
var AllowedCarrierHz = [4]uint32{36000, 38000, 40000, 56000}

// AllowedDutyPct lists the UI-selectable TX duty cycle presets (SSOT).
var AllowedDutyPct = [2]uint32{33, 50}

// Settings holds persisted user preferences for ir-studio.
// MCPEnabled and ExpertMode MUST default to false.
type Settings struct {
	MCPEnabled       bool   `json:"mcp_enabled"`
	ExpertMode       bool   `json:"expert_mode"`
	ForceSim         bool   `json:"force_sim"`
	DevicePath       string `json:"device_path"`
	CarrierHz        uint32 `json:"default_carrier_hz"`
	DutyCyclePct     uint32 `json:"default_duty_cycle"`
	RecTimeoutUs     uint32 `json:"rec_timeout_us"`
	ProbeIntervalMs  int    `json:"probe_interval_ms"`
	AmbientThreshold int    `json:"ambient_threshold"`
}

// DefaultSettings returns canonical safe defaults with MCPEnabled == false and ExpertMode == false.
func DefaultSettings() Settings {
	return Settings{
		MCPEnabled:       false,
		ExpertMode:       false,
		ForceSim:         false,
		DevicePath:       "",
		CarrierHz:        DefaultCarrierHz,
		DutyCyclePct:     DefaultDutyPct,
		RecTimeoutUs:     DefaultRecTimeoutUs,
		ProbeIntervalMs:  DefaultProbeIntervalMs,
		AmbientThreshold: DefaultAmbientThreshold,
	}
}

// Sanitize clamps or resets out-of-range settings to SSOT safe defaults.
func Sanitize(s Settings) Settings {
	if s.CarrierHz < MinCarrierHz || s.CarrierHz > MaxCarrierHz {
		s.CarrierHz = DefaultCarrierHz
	}
	if s.DutyCyclePct < MinDutyPct || s.DutyCyclePct > MaxDutyPct {
		s.DutyCyclePct = DefaultDutyPct
	}
	if s.RecTimeoutUs < MinRecTimeoutUs || s.RecTimeoutUs > MaxRecTimeoutUs {
		s.RecTimeoutUs = DefaultRecTimeoutUs
	}
	if s.ProbeIntervalMs < MinProbeIntervalMs || s.ProbeIntervalMs > MaxProbeIntervalMs {
		s.ProbeIntervalMs = DefaultProbeIntervalMs
	}
	if s.AmbientThreshold < MinAmbientThreshold || s.AmbientThreshold > MaxAmbientThreshold {
		s.AmbientThreshold = DefaultAmbientThreshold
	}
	return s
}

// ConfigDir returns $HOME/.config/ir-studio.
func ConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config: resolve home dir: %w", err)
	}
	return filepath.Join(home, ".config", "ir-studio"), nil
}

// ConfigPath returns $HOME/.config/ir-studio/config.json.
func ConfigPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// Load reads Settings from path. Missing files return DefaultSettings() with nil
// error. Corrupted JSON fails closed (MCPEnabled: false) and returns an error.
func Load(path string) (Settings, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return DefaultSettings(), nil
		}
		return DefaultSettings(), fmt.Errorf("config: read %s: %w", path, err)
	}
	s := DefaultSettings()
	if err := json.Unmarshal(data, &s); err != nil {
		return DefaultSettings(), fmt.Errorf("config: parse %s: %w", path, err)
	}
	return Sanitize(s), nil
}

// SaveAtomic writes s to path atomically with 0700 parent dir and 0600 file mode.
func SaveAtomic(path string, s Settings) error {
	s = Sanitize(s)
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("config: marshal settings: %w", err)
	}
	data = append(data, '\n')
	return writeFileAtomic0600(path, data)
}

func writeFileAtomic0600(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, ConfigDirPerm); err != nil {
		return fmt.Errorf("config: mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("config: create temp: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmp.Chmod(ConfigFilePerm); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
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
