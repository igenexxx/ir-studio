package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	MacroVersion       = 1
	MinStepDelayMs     = 50
	MaxStepDelayMs     = 10000
	DefaultStepDelayMs = 500
	MaxStepsPerMacro   = 32
	MaxMacros          = 64
)

var (
	ErrInvalidMacro      = errors.New("config: invalid macro")
	ErrInvalidRemotePath = errors.New("config: invalid remote path")
	ErrMacroNotFound     = errors.New("config: macro not found")
)

// MacroStep is a single cross-remote IR command inside a Macro.
type MacroStep struct {
	Remote       string `json:"remote"`
	Button       string `json:"button"`
	DelayAfterMs int    `json:"delay_after_ms"`
	Repeat       int    `json:"repeat,omitempty"`
}

// Macro is an ordered sequence of IR steps executed asynchronously.
type Macro struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Steps       []MacroStep `json:"steps"`
}

// MacroFile is the on-disk schema for ~/.config/ir-studio/macros.json.
type MacroFile struct {
	Version int     `json:"version"`
	Macros  []Macro `json:"macros"`
}

// MacrosPath returns $HOME/.config/ir-studio/macros.json.
func MacrosPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "macros.json"), nil
}

// ValidateSafeRemoteName enforces the path-sandbox invariant for macro remote refs:
// bare filename only, rejecting empty, .., /, \, NUL, and control characters.
func ValidateSafeRemoteName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("empty remote name: %w", ErrInvalidRemotePath)
	}
	if strings.Contains(name, "..") || strings.ContainsAny(name, "/\\\x00") {
		return fmt.Errorf("%q contains forbidden path characters: %w", name, ErrInvalidRemotePath)
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%q contains control character: %w", name, ErrInvalidRemotePath)
		}
	}
	return nil
}

// ValidateMacro checks macro metadata, step count, path safety, and delay bounds.
func ValidateMacro(m Macro) error {
	if strings.TrimSpace(m.ID) == "" || strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("macro id and name are required: %w", ErrInvalidMacro)
	}
	if len(m.Steps) > MaxStepsPerMacro {
		return fmt.Errorf("macro %q exceeds %d steps: %w", m.ID, MaxStepsPerMacro, ErrInvalidMacro)
	}
	for i, step := range m.Steps {
		if err := validateMacroStep(step, i == len(m.Steps)-1); err != nil {
			return fmt.Errorf("macro %q step %d: %w", m.ID, i+1, err)
		}
	}
	return nil
}

func validateMacroStep(step MacroStep, isLast bool) error {
	if err := ValidateSafeRemoteName(step.Remote); err != nil {
		return err
	}
	if strings.TrimSpace(step.Button) == "" {
		return fmt.Errorf("empty button name: %w", ErrInvalidMacro)
	}
	if step.DelayAfterMs < 0 || step.DelayAfterMs > MaxStepDelayMs {
		return fmt.Errorf("delay_after_ms %d out of range [0, %d]: %w",
			step.DelayAfterMs, MaxStepDelayMs, ErrInvalidMacro)
	}
	if !isLast && step.DelayAfterMs > 0 && step.DelayAfterMs < MinStepDelayMs {
		return fmt.Errorf("delay_after_ms %d below minimum %d ms: %w",
			step.DelayAfterMs, MinStepDelayMs, ErrInvalidMacro)
	}
	if step.Repeat < 0 || step.Repeat > 10 {
		return fmt.Errorf("repeat %d out of range [0, 10]: %w", step.Repeat, ErrInvalidMacro)
	}
	return nil
}

// LoadMacros reads and validates macros.json. A missing file initializes an empty
// MacroFile cleanly; corrupted JSON returns an error without modifying disk.
func LoadMacros(path string) (*MacroFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &MacroFile{Version: MacroVersion}, nil
		}
		return nil, fmt.Errorf("config: read macros %s: %w", path, err)
	}
	var mf MacroFile
	if err := json.Unmarshal(data, &mf); err != nil {
		return nil, fmt.Errorf("config: parse macros %s: %w", path, err)
	}
	if err := validateMacroFile(&mf); err != nil {
		return nil, err
	}
	return &mf, nil
}

// SaveMacrosAtomic validates mf and writes it atomically with 0600 permissions.
func SaveMacrosAtomic(path string, mf *MacroFile) error {
	if mf == nil {
		return fmt.Errorf("nil macro file: %w", ErrInvalidMacro)
	}
	if mf.Version == 0 {
		mf.Version = MacroVersion
	}
	if err := validateMacroFile(mf); err != nil {
		return err
	}
	data, err := json.MarshalIndent(mf, "", "  ")
	if err != nil {
		return fmt.Errorf("config: marshal macros: %w", err)
	}
	data = append(data, '\n')
	return writeFileAtomic0600(path, data)
}

func validateMacroFile(mf *MacroFile) error {
	if mf.Version != MacroVersion {
		return fmt.Errorf("unsupported macro file version %d: %w", mf.Version, ErrInvalidMacro)
	}
	if len(mf.Macros) > MaxMacros {
		return fmt.Errorf("macro count %d exceeds %d: %w", len(mf.Macros), MaxMacros, ErrInvalidMacro)
	}
	seen := make(map[string]struct{}, len(mf.Macros))
	for _, m := range mf.Macros {
		if err := ValidateMacro(m); err != nil {
			return err
		}
		if _, dup := seen[m.ID]; dup {
			return fmt.Errorf("duplicate macro id %q: %w", m.ID, ErrInvalidMacro)
		}
		seen[m.ID] = struct{}{}
	}
	return nil
}
