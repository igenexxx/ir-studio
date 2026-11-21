package lirc

import (
	"errors"
	"fmt"
	"time"

	"ir-studio/internal/irfile"
)

// Linux LIRC ioctl request numbers (SSOT from linux/lirc.h).
const (
	LIRC_GET_FEATURES            uintptr = 0x80046900
	LIRC_SET_SEND_MODE           uintptr = 0x40046911
	LIRC_SET_REC_MODE            uintptr = 0x40046912
	LIRC_SET_SEND_CARRIER        uintptr = 0x40046913
	LIRC_SET_SEND_DUTY_CYCLE     uintptr = 0x40046915
	LIRC_SET_REC_TIMEOUT         uintptr = 0x40046918
	LIRC_SET_REC_TIMEOUT_REPORTS uintptr = 0x40046919
)

// Linux LIRC mode & feature bitmasks (SSOT).
const (
	LIRC_MODE_PULSE     uint32 = 0x00000002
	LIRC_MODE_MODE2     uint32 = 0x00000004
	LIRC_CAN_SEND_PULSE uint32 = 0x00000002
	LIRC_CAN_REC_MODE2  uint32 = 0x00040000
)

// Linux LIRC mode2 packet bitmasks and packet type tags (SSOT).
const (
	LIRC_MODE2_MASK    uint32 = 0xFF000000
	LIRC_VALUE_MASK    uint32 = 0x00FFFFFF
	LIRC_MODE2_SPACE   uint32 = 0x00000000
	LIRC_MODE2_PULSE   uint32 = 0x01000000
	LIRC_MODE2_TIMEOUT uint32 = 0x03000000
)

// Carrier, duty cycle, framing, and safety bounds (SSOT).
const (
	MinCarrierHz     uint32 = 36000
	MaxCarrierHz     uint32 = 56000
	DefaultCarrierHz uint32 = 38000

	MinDutyPct     uint32 = 1
	MaxDutyPct     uint32 = 99
	DefaultDutyPct uint32 = 33

	DefaultRecTimeoutUs uint32 = 15000
	MinPulseUs          uint32 = 100
	MaxFramePulses             = 1024
	MinFramePulses             = 6

	MaxRepeats           = 10
	DefaultInterFrameGap = 45 * time.Millisecond
	DefaultSettleGuard   = 20 * time.Millisecond
)

var (
	ErrDeviceOpen          = errors.New("lirc: failed to open device")
	ErrInvalidCarrier      = errors.New("lirc: carrier frequency out of range [36000, 56000] Hz")
	ErrInvalidDutyCycle    = errors.New("lirc: duty cycle out of range [1, 99]%")
	ErrInvalidPulseTrain   = errors.New("lirc: invalid pulse train")
	ErrTXQueueFull         = errors.New("lirc: TX queue full")
	ErrRepeatLimitExceeded = errors.New("lirc: repeat count exceeds safety limit")
	ErrUnsupportedFeature  = errors.New("lirc: unsupported hardware feature")
)

// DefaultCarrier returns the canonical default carrier specification (38 kHz, 33%).
func DefaultCarrier() irfile.CarrierSpec {
	return irfile.CarrierSpec{FreqHz: DefaultCarrierHz, DutyPct: DefaultDutyPct}
}

// ValidateCarrier checks that spec's frequency and duty cycle fall within hardware bounds.
func ValidateCarrier(spec irfile.CarrierSpec) error {
	if spec.FreqHz < MinCarrierHz || spec.FreqHz > MaxCarrierHz {
		return fmt.Errorf("%d Hz: %w", spec.FreqHz, ErrInvalidCarrier)
	}
	if spec.DutyPct < MinDutyPct || spec.DutyPct > MaxDutyPct {
		return fmt.Errorf("%d%%: %w", spec.DutyPct, ErrInvalidDutyCycle)
	}
	return nil
}

// ValidateTxPulses verifies that pulses are non-empty, strictly positive,
// and odd-length (stripping a trailing space if even-length) for LIRC_MODE_PULSE.
func ValidateTxPulses(pulses []uint32) ([]uint32, error) {
	if len(pulses) == 0 {
		return nil, ErrInvalidPulseTrain
	}
	for _, d := range pulses {
		if d == 0 || d > irfile.MaxLIRCPulseUs {
			return nil, ErrInvalidPulseTrain
		}
	}
	n := len(pulses)
	if n%2 == 0 {
		n--
	}
	if n == 0 {
		return nil, ErrInvalidPulseTrain
	}
	return pulses[:n], nil
}
