package input

import (
	"encoding/binary"
	"testing"
)

func TestMapLinuxKeyCode(t *testing.T) {
	cases := []struct {
		name string
		code uint16
		want KeyAction
	}{
		{"esc", codeEsc, KeyEsc},
		{"q", codeQ, KeyQ},
		{"tab", codeTab, KeyTab},
		{"key9_tab", code9, KeyTab},
		{"key0_tab", code0, KeyTab},
		{"enter", codeEnter, KeyEnter},
		{"space", codeSpace, KeySpace},
		{"up", codeUp, KeyUp},
		{"f_up", codeF, KeyUp},
		{"down", codeDown, KeyDown},
		{"x_down", codeX, KeyDown},
		{"left", codeLeft, KeyLeft},
		{"z_left", codeZ, KeyLeft},
		{"right", codeRight, KeyRight},
		{"c_right", codeC, KeyRight},
		{"soft4", code4, KeySoft4},
		{"soft5", code5, KeySoft5},
		{"soft6", code6, KeySoft6},
		{"soft7", code7, KeySoft7},
		{"soft8", code8, KeySoft8},
		{"r_rank", codeR, KeyR},
		{"h_help", codeH, KeyH},
		{"del", codeDelete, KeyDel},
		{"backspace", codeBackspace, KeyDel},
		{"key1_mark", code1, Key1},
		{"d_mark", codeD, Key1},
		{"unknown", 999, KeyNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MapLinuxKeyCode(tc.code); got != tc.want {
				t.Fatalf("MapLinuxKeyCode(%d) = %v, want %v", tc.code, got, tc.want)
			}
		})
	}
}

func TestParseEvdevEvent(t *testing.T) {
	makeEvt := func(evType, code uint16, val int32) []byte {
		buf := make([]byte, evdevEventSize)
		binary.LittleEndian.PutUint16(buf[16:18], evType)
		binary.LittleEndian.PutUint16(buf[18:20], code)
		binary.LittleEndian.PutUint32(buf[20:24], uint32(val))
		return buf
	}
	cases := []struct {
		name   string
		buf    []byte
		want   KeyAction
		wantOK bool
	}{
		{"press_enter", makeEvt(evKey, codeEnter, keyValPress), KeyEnter, true},
		{"repeat_down", makeEvt(evKey, codeX, keyValRepeat), KeyDown, true},
		{"release_ignored", makeEvt(evKey, codeEnter, 0), KeyNone, false},
		{"non_key_ignored", makeEvt(0x00, codeEnter, keyValPress), KeyNone, false},
		{"unmapped_key", makeEvt(evKey, 250, keyValPress), KeyNone, false},
		{"short_buffer", make([]byte, 8), KeyNone, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ParseEvdevEvent(tc.buf)
			if got != tc.want || ok != tc.wantOK {
				t.Fatalf("ParseEvdevEvent = (%v, %v), want (%v, %v)", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestMockReader(t *testing.T) {
	mr := NewMockReader()
	mr.Send(KeyTab)
	mr.Send(KeySoft4)
	if got := <-mr.Events(); got != KeyTab {
		t.Fatalf("got %v, want KeyTab", got)
	}
	if got := <-mr.Events(); got != KeySoft4 {
		t.Fatalf("got %v, want KeySoft4", got)
	}
	if err := mr.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Double close must be safe.
	if err := mr.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
