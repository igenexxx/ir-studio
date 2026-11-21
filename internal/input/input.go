package input

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// KeyAction represents a high-level CardputerZero keyboard action.
type KeyAction int

const (
	KeyNone KeyAction = iota
	KeyUp
	KeyDown
	KeyLeft
	KeyRight
	KeyEnter
	KeyEsc
	KeyTab
	KeySoft4
	KeySoft5
	KeySoft6
	KeySoft7
	KeySoft8
	KeyR
	KeyH
	KeySpace
	KeyDel
	Key1
	KeyQ
)

// Linux input_event constants (arm64 / amd64 24-byte struct).
const (
	evKey          uint16 = 0x01
	keyValPress    int32  = 1
	keyValRepeat   int32  = 2
	evdevEventSize        = 24
	evdevChanBuf          = 32

	codeEsc       uint16 = 1
	code1         uint16 = 2
	code4         uint16 = 5
	code5         uint16 = 6
	code6         uint16 = 7
	code7         uint16 = 8
	code8         uint16 = 9
	code9         uint16 = 10
	code0         uint16 = 11
	codeBackspace uint16 = 14
	codeTab       uint16 = 15
	codeQ         uint16 = 16
	codeR         uint16 = 19
	codeEnter     uint16 = 28
	codeD         uint16 = 32
	codeF         uint16 = 33
	codeH         uint16 = 35
	codeZ         uint16 = 44
	codeX         uint16 = 45
	codeC         uint16 = 46
	codeSpace     uint16 = 57
	codeUp        uint16 = 103
	codeLeft      uint16 = 105
	codeRight     uint16 = 106
	codeDown      uint16 = 108
	codeDelete    uint16 = 111
)

// Reader provides an event channel of keyboard actions and a cleanup method.
type Reader interface {
	Events() <-chan KeyAction
	Close() error
}

// MapLinuxKeyCode converts a Linux evdev keycode into a CardputerZero KeyAction.
func MapLinuxKeyCode(code uint16) KeyAction {
	switch code {
	case codeEsc:
		return KeyEsc
	case codeQ:
		return KeyQ
	case codeTab, code9, code0:
		return KeyTab
	case codeEnter:
		return KeyEnter
	case codeSpace:
		return KeySpace
	case codeUp, codeF:
		return KeyUp
	case codeDown, codeX:
		return KeyDown
	case codeLeft, codeZ:
		return KeyLeft
	case codeRight, codeC:
		return KeyRight
	case code4:
		return KeySoft4
	case code5:
		return KeySoft5
	case code6:
		return KeySoft6
	case code7:
		return KeySoft7
	case code8:
		return KeySoft8
	case codeR:
		return KeyR
	case codeH:
		return KeyH
	case codeDelete, codeBackspace:
		return KeyDel
	case code1, codeD:
		return Key1
	default:
		return KeyNone
	}
}

// ParseEvdevEvent decodes a 24-byte Linux input_event buffer into a KeyAction.
func ParseEvdevEvent(buf []byte) (KeyAction, bool) {
	if len(buf) < evdevEventSize {
		return KeyNone, false
	}
	evType := binary.LittleEndian.Uint16(buf[16:18])
	evCode := binary.LittleEndian.Uint16(buf[18:20])
	evVal := int32(binary.LittleEndian.Uint32(buf[20:24]))
	if evType != evKey || (evVal != keyValPress && evVal != keyValRepeat) {
		return KeyNone, false
	}
	act := MapLinuxKeyCode(evCode)
	return act, act != KeyNone
}

// EvdevReader reads CardputerZero TCA8418 keyboard events from /dev/input/event*.
type EvdevReader struct {
	ch    chan KeyAction
	done  chan struct{}
	files []*os.File
	once  sync.Once
}

// StartEvdevReader opens CardputerZero keyboard evdev nodes and starts background readers.
func StartEvdevReader() *EvdevReader {
	r := &EvdevReader{
		ch:   make(chan KeyAction, evdevChanBuf),
		done: make(chan struct{}),
	}
	for _, p := range findKeyboardEvdevPaths() {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		r.files = append(r.files, f)
		go readEvdevLoop(f, r.ch, r.done)
	}
	return r
}

// Events returns the read-only KeyAction stream.
func (r *EvdevReader) Events() <-chan KeyAction { return r.ch }

// Close stops all evdev reader goroutines and closes opened device files.
func (r *EvdevReader) Close() error {
	r.once.Do(func() {
		close(r.done)
		for _, f := range r.files {
			_ = f.Close()
		}
	})
	return nil
}

func readEvdevLoop(dev *os.File, ch chan<- KeyAction, done <-chan struct{}) {
	buf := make([]byte, evdevEventSize)
	for {
		select {
		case <-done:
			return
		default:
		}
		n, err := dev.Read(buf)
		if err != nil || n < evdevEventSize {
			return
		}
		if act, ok := ParseEvdevEvent(buf[:n]); ok {
			select {
			case ch <- act:
			default:
			}
		}
	}
}

func findKeyboardEvdevPaths() []string {
	var out []string
	byPath, _ := filepath.Glob("/dev/input/by-path/*i2c*-event")
	out = append(out, byPath...)
	if len(out) == 0 {
		all, _ := filepath.Glob("/dev/input/event*")
		for _, p := range all {
			if !strings.Contains(p, "mouse") {
				out = append(out, p)
			}
		}
	}
	return out
}

// MockReader is an in-memory Reader for headless tests and simulators.
type MockReader struct {
	ch   chan KeyAction
	once sync.Once
}

// NewMockReader constructs a buffered MockReader.
func NewMockReader() *MockReader {
	return &MockReader{ch: make(chan KeyAction, evdevChanBuf)}
}

// Send injects a KeyAction non-blockingly into the mock stream.
func (m *MockReader) Send(act KeyAction) {
	select {
	case m.ch <- act:
	default:
	}
}

// Events returns the read-only KeyAction channel.
func (m *MockReader) Events() <-chan KeyAction { return m.ch }

// Close closes the mock channel once.
func (m *MockReader) Close() error {
	m.once.Do(func() { close(m.ch) })
	return nil
}
