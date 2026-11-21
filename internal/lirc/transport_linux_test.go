//go:build linux

package lirc

import (
	"context"
	"encoding/binary"
	"errors"
	"syscall"
	"testing"
	"time"
)

func TestLinuxTransportPipeAndIoctlEdgeCases(t *testing.T) {
	var fds [2]int
	if err := syscall.Pipe(fds[:]); err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	tr := newLinuxTransport(fds[1], fds[0], "/dev/lirc-pipe", LIRC_CAN_SEND_PULSE|LIRC_CAN_REC_MODE2)
	defer tr.Close()

	if tr.IsSim() || tr.DevicePath() != "/dev/lirc-pipe" {
		t.Fatalf("unexpected identity: sim=%v path=%q", tr.IsSim(), tr.DevicePath())
	}
	feat, err := tr.Features()
	if err != nil || feat != (LIRC_CAN_SEND_PULSE|LIRC_CAN_REC_MODE2) {
		t.Fatalf("Features = (%x, %v)", feat, err)
	}

	// Pipe FD returns ENOTTY on ioctl -> SetSendDutyCycle & SetRecTimeout tolerate ENOTTY!
	if err := tr.SetSendDutyCycle(33); err != nil {
		t.Fatalf("SetSendDutyCycle should tolerate ENOTTY, got: %v", err)
	}
	// Second call hits cached dutySupported == false fast path.
	if err := tr.SetSendDutyCycle(50); err != nil {
		t.Fatalf("SetSendDutyCycle cached fast path failed: %v", err)
	}
	if err := tr.SetRecTimeout(15000, true); err != nil {
		t.Fatalf("SetRecTimeout should tolerate ENOTTY, got: %v", err)
	}

	// WritePulses + ReadMode2 round-trip across real Linux pipe FDs via SYS_PPOLL!
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	want := []uint32{LIRC_MODE2_PULSE | 9000, LIRC_MODE2_SPACE | 4500, LIRC_MODE2_TIMEOUT | 15000}
	if err := tr.WritePulses(ctx, want); err != nil {
		t.Fatalf("WritePulses on pipe: %v", err)
	}
	dst := make([]uint32, 8)
	n, err := tr.ReadMode2(ctx, dst)
	if err != nil || n != len(want) {
		t.Fatalf("ReadMode2 = (%d, %v), want (%d, nil)", n, err, len(want))
	}
	for i := range want {
		if dst[i] != want[i] {
			t.Fatalf("dst[%d] = %x, want %x", i, dst[i], want[i])
		}
	}

	// Empty ReadMode2 returns (0, nil).
	if n0, err0 := tr.ReadMode2(ctx, nil); n0 != 0 || err0 != nil {
		t.Fatalf("ReadMode2(nil) = (%d, %v)", n0, err0)
	}

	// Cancelled ctx on ReadMode2 returns context.Canceled promptly.
	cCtx, cCancel := context.WithCancel(context.Background())
	cCancel()
	if _, err := tr.ReadMode2(cCtx, dst); !errors.Is(err, context.Canceled) {
		t.Fatalf("ReadMode2(canceled) = %v, want Canceled", err)
	}

	// Double Close is idempotent.
	if err := tr.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := tr.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestLinuxTransportUnsupportedAndHelpers(t *testing.T) {
	// Transport with no TX/RX FDs returns ErrUnsupportedFeature.
	tr := newLinuxTransport(-1, -1, "", 0)
	if err := tr.SetSendMode(LIRC_MODE_PULSE); !errors.Is(err, ErrUnsupportedFeature) {
		t.Fatalf("SetSendMode = %v", err)
	}
	if err := tr.SetRecMode(LIRC_MODE_MODE2); !errors.Is(err, ErrUnsupportedFeature) {
		t.Fatalf("SetRecMode = %v", err)
	}
	if err := tr.SetSendCarrier(38000); !errors.Is(err, ErrUnsupportedFeature) {
		t.Fatalf("SetSendCarrier = %v", err)
	}
	if err := tr.SetSendDutyCycle(33); !errors.Is(err, ErrUnsupportedFeature) {
		t.Fatalf("SetSendDutyCycle = %v", err)
	}
	if err := tr.SetRecTimeout(15000, true); !errors.Is(err, ErrUnsupportedFeature) {
		t.Fatalf("SetRecTimeout = %v", err)
	}
	if err := tr.WritePulses(context.Background(), []uint32{500}); !errors.Is(err, ErrUnsupportedFeature) {
		t.Fatalf("WritePulses = %v", err)
	}
	if _, err := tr.ReadMode2(context.Background(), make([]uint32, 4)); !errors.Is(err, ErrUnsupportedFeature) {
		t.Fatalf("ReadMode2 = %v", err)
	}

	// combinePaths & closeUnused helpers.
	if got := combinePaths("/dev/lirc0", "/dev/lirc1"); got != "/dev/lirc0+/dev/lirc1" {
		t.Fatalf("combinePaths both = %q", got)
	}
	if got := combinePaths("/dev/lirc0", ""); got != "/dev/lirc0" {
		t.Fatalf("combinePaths tx = %q", got)
	}
	if got := combinePaths("", "/dev/lirc1"); got != "/dev/lirc1" {
		t.Fatalf("combinePaths rx = %q", got)
	}
	closeUnused(-1, -1)

	// Probing /dev/null fails LIRC_GET_FEATURES with ErrDeviceOpen.
	if _, err := openSingleDevice("/dev/null", DefaultRecTimeoutUs); !errors.Is(err, ErrDeviceOpen) {
		t.Fatalf("openSingleDevice(/dev/null) = %v, want ErrDeviceOpen", err)
	}

	// Discover with default options on host without /dev/lirc* falls back to SIM.
	simTr, err := Discover(DiscoverOptions{})
	if err != nil {
		t.Fatalf("Discover default: %v", err)
	}
	_ = simTr.Close()
	_ = binary.LittleEndian
}
