package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ir-studio/internal/config"
	"ir-studio/internal/irfile"
	"ir-studio/internal/irml"
	"ir-studio/internal/lirc"
)

var (
	// ErrInvalidPath is returned when a filename, pack ID, or relative path escapes the sandbox.
	ErrInvalidPath = errors.New("mcpserver: invalid or unsafe path")
	// ErrInsecureScheme is returned when a remote source URL does not use HTTPS.
	ErrInsecureScheme = errors.New("mcpserver: only https:// URLs are allowed")
	// ErrFileTooLarge is returned when an .ir payload exceeds irfile.MaxIRFileBytes (1 MiB).
	ErrFileTooLarge = errors.New("mcpserver: file exceeds 1 MiB limit")
)

// IRBackend is the consumer-owned hardware abstraction injected into Server.
type IRBackend interface {
	HardwareMode() string
	DevicePath() string
	CarrierSpec() irfile.CarrierSpec
	AmbientSnapshot() irml.AmbientSnapshot
	LearnSignal(ctx context.Context, timeout time.Duration, carrierHz uint32) (lirc.RXCapture, error)
	TransmitSignal(ctx context.Context, sig irfile.IRSignal, repeat int, carrier irfile.CarrierSpec) (time.Duration, error)
}

// HardwareBackend serializes all LIRC/SIM hardware operations with lircMu.
type HardwareBackend struct {
	lircMu   sync.Mutex
	xcvr     *lirc.Transceiver
	rx       *lirc.RXWorker
	registry *irfile.ProtocolRegistry
	ambient  *irml.AmbientMonitor
	settings config.Settings
}

// NewHardwareBackend adapts ServeConfig hardware dependencies into an IRBackend.
func NewHardwareBackend(cfg ServeConfig, registry *irfile.ProtocolRegistry) *HardwareBackend {
	if registry == nil {
		registry = irfile.DefaultRegistry()
	}
	return &HardwareBackend{
		xcvr:     cfg.Transceiver,
		rx:       cfg.RX,
		registry: registry,
		ambient:  irml.NewAmbientMonitor(),
		settings: cfg.Settings,
	}
}

// HardwareMode returns "LIRC" for physical hardware or "SIM" for simulator mode.
func (b *HardwareBackend) HardwareMode() string {
	b.lircMu.Lock()
	defer b.lircMu.Unlock()
	if b.xcvr != nil && b.xcvr.Transport() != nil && !b.xcvr.Transport().IsSim() {
		return "LIRC"
	}
	return "SIM"
}

// DevicePath returns the active LIRC device path or "sim".
func (b *HardwareBackend) DevicePath() string {
	b.lircMu.Lock()
	defer b.lircMu.Unlock()
	if b.xcvr != nil && b.xcvr.Transport() != nil {
		return b.xcvr.Transport().DevicePath()
	}
	if b.settings.DevicePath != "" {
		return b.settings.DevicePath
	}
	return "sim"
}

// CarrierSpec returns the configured default TX carrier frequency and duty cycle.
func (b *HardwareBackend) CarrierSpec() irfile.CarrierSpec {
	b.lircMu.Lock()
	defer b.lircMu.Unlock()
	freq := b.settings.CarrierHz
	if freq == 0 {
		freq = irfile.DefaultFreqHz
	}
	duty := b.settings.DutyCyclePct
	if duty == 0 {
		duty = irfile.DefaultDutyPct
	}
	return irfile.CarrierSpec{FreqHz: freq, DutyPct: duty}
}

// AmbientSnapshot returns the latest ambient IR noise diagnostics.
func (b *HardwareBackend) AmbientSnapshot() irml.AmbientSnapshot {
	b.lircMu.Lock()
	defer b.lircMu.Unlock()
	return b.ambient.Snapshot(time.Now().UnixNano())
}

// LearnSignal captures one IR frame within timeout while holding lircMu.
func (b *HardwareBackend) LearnSignal(ctx context.Context, timeout time.Duration, carrierHz uint32) (lirc.RXCapture, error) {
	b.lircMu.Lock()
	defer b.lircMu.Unlock()

	if carrierHz == 0 {
		carrierHz = irfile.DefaultFreqHz
	}
	rxCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if b.rx != nil {
		select {
		case cap, ok := <-b.rx.Captures():
			if !ok {
				return lirc.RXCapture{}, errors.New("RX worker closed")
			}
			return cap, nil
		case <-rxCtx.Done():
			return lirc.RXCapture{}, rxCtx.Err()
		}
	}
	if b.xcvr == nil || b.xcvr.Transport() == nil || b.xcvr.Transport().IsSim() {
		return b.simulatedCapture(carrierHz)
	}
	return lirc.RXCapture{}, errors.New("LIRC receiver not initialized")
}

func (b *HardwareBackend) simulatedCapture(carrierHz uint32) (lirc.RXCapture, error) {
	sig := irfile.IRSignal{
		Name:      "Sim_Power",
		Kind:      irfile.SignalParsed,
		Protocol:  "NEC",
		Address:   [4]byte{0x04, 0x00, 0x00, 0x00},
		Command:   [4]byte{0x08, 0x00, 0x00, 0x00},
		Frequency: carrierHz,
		DutyCycle: irfile.DefaultDutyCycle,
	}
	pulses, err := b.registry.Encode(sig)
	if err != nil {
		return lirc.RXCapture{}, err
	}
	return lirc.RXCapture{
		Timestamp:    time.Now(),
		Pulses:       pulses,
		Decoded:      &sig,
		CarrierEstHz: carrierHz,
	}, nil
}

// TransmitSignal encodes and emits sig repeat times while holding lircMu.
func (b *HardwareBackend) TransmitSignal(ctx context.Context, sig irfile.IRSignal, repeat int, carrier irfile.CarrierSpec) (time.Duration, error) {
	b.lircMu.Lock()
	defer b.lircMu.Unlock()

	if err := lirc.ValidateCarrier(carrier); err != nil {
		return 0, err
	}
	pulses, err := b.encodeToLIRC(sig)
	if err != nil {
		return 0, err
	}

	start := time.Now()
	for i := range repeat {
		if ctx.Err() != nil {
			return time.Since(start), ctx.Err()
		}
		if b.xcvr != nil {
			if err := b.xcvr.Send(ctx, pulses, carrier); err != nil {
				return time.Since(start), err
			}
		}
		if i+1 < repeat {
			select {
			case <-ctx.Done():
				return time.Since(start), ctx.Err()
			case <-time.After(40 * time.Millisecond):
			}
		}
	}
	return time.Since(start), nil
}

func (b *HardwareBackend) encodeToLIRC(sig irfile.IRSignal) ([]uint32, error) {
	if sig.Kind == irfile.SignalRaw {
		pt, err := irfile.FromRawData(sig.RawData)
		if err != nil {
			return nil, err
		}
		return pt.ToLIRCPulses()
	}
	pt, err := b.registry.Encode(sig)
	if err != nil {
		return nil, err
	}
	return pt.ToLIRCPulses()
}

// ValidateSafeName rejects path traversal, separators, encoded dots, NUL, and control chars.
func ValidateSafeName(name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" || trimmed != name {
		return fmt.Errorf("%w: name must be non-empty without surrounding whitespace", ErrInvalidPath)
	}
	lower := strings.ToLower(name)
	if strings.Contains(name, "..") || strings.Contains(lower, "%2e") {
		return fmt.Errorf("%w: traversal sequence rejected", ErrInvalidPath)
	}
	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("%w: path separator rejected", ErrInvalidPath)
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%w: control character rejected", ErrInvalidPath)
		}
	}
	return nil
}

// ValidateSafeRelPath validates a relative pack file path (allowing safe subdirs, rejecting traversal).
func ValidateSafeRelPath(rel string) error {
	if strings.TrimSpace(rel) == "" {
		return fmt.Errorf("%w: empty relative path", ErrInvalidPath)
	}
	if strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, `\`) || strings.Contains(rel, `\`) {
		return fmt.Errorf("%w: absolute or backslash path rejected", ErrInvalidPath)
	}
	lower := strings.ToLower(rel)
	if strings.Contains(lower, "%2e") {
		return fmt.Errorf("%w: encoded traversal rejected", ErrInvalidPath)
	}
	for seg := range strings.SplitSeq(rel, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("%w: unsafe path segment", ErrInvalidPath)
		}
		if err := ValidateSafeName(seg); err != nil {
			return err
		}
	}
	return nil
}

// ResolveSandboxedPath resolves rel inside root and rejects escapes or symlinks.
func ResolveSandboxedPath(root, rel string) (string, error) {
	if root == "" {
		return "", fmt.Errorf("%w: empty sandbox root", ErrInvalidPath)
	}
	if err := ValidateSafeRelPath(rel); err != nil {
		return "", err
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	target := filepath.Join(absRoot, filepath.FromSlash(rel))
	relCheck, err := filepath.Rel(absRoot, target)
	if err != nil || relCheck == ".." || strings.HasPrefix(relCheck, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: target escapes root", ErrInvalidPath)
	}
	if info, err := os.Lstat(target); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("%w: symlink rejected", ErrInvalidPath)
		}
	}
	return target, nil
}

// ValidateHTTPSURL enforces HTTPS-only remote source URLs with a valid host.
func ValidateHTTPSURL(rawURL string) error {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInsecureScheme, err)
	}
	if !strings.EqualFold(u.Scheme, "https") || u.Host == "" {
		return fmt.Errorf("%w: got %q", ErrInsecureScheme, rawURL)
	}
	return nil
}
