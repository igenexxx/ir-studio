//go:build linux

package lirc

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const (
	maxLIRCScanDevices = 8
	pollInEvent        = 0x0001
	pollOutEvent       = 0x0004
	pollSliceMs        = 25
)

type pollFD struct {
	fd      int32
	events  int16
	revents int16
}

type linuxTransport struct {
	mu            sync.Mutex
	txFD          int
	rxFD          int
	path          string
	features      uint32
	dutySupported bool
	closed        bool
}

func discoverPlatform(opts DiscoverOptions) (Transport, error) {
	if opts.DevicePath != "" {
		tr, err := openSingleDevice(opts.DevicePath, opts.RecTimeoutUs)
		if err != nil {
			if opts.FallbackToSim {
				return NewSimTransport(), nil
			}
			return nil, err
		}
		return tr, nil
	}
	if tr := scanLIRCDevices(opts.RecTimeoutUs); tr != nil {
		return tr, nil
	}
	return NewSimTransport(), nil
}

func scanLIRCDevices(recTimeoutUs uint32) Transport {
	var txFD, rxFD int = -1, -1
	var txPath, rxPath string
	var txFeat, rxFeat uint32

	for i := range maxLIRCScanDevices {
		path := fmt.Sprintf("/dev/lirc%d", i)
		fd, feat, err := probeLIRCDevice(path)
		if err != nil {
			continue
		}
		canTX := feat&LIRC_CAN_SEND_PULSE != 0
		canRX := feat&LIRC_CAN_REC_MODE2 != 0
		if canTX && canRX {
			closeUnused(txFD, rxFD)
			tr := newLinuxTransport(fd, fd, path, feat)
			_ = configureLinuxTransport(tr, recTimeoutUs)
			return tr
		}
		if canTX && txFD < 0 {
			txFD, txPath, txFeat = fd, path, feat
		} else if canRX && rxFD < 0 {
			rxFD, rxPath, rxFeat = fd, path, feat
		} else {
			_ = syscall.Close(fd)
		}
	}
	if txFD < 0 && rxFD < 0 {
		return nil
	}
	path := combinePaths(txPath, rxPath)
	tr := newLinuxTransport(txFD, rxFD, path, txFeat|rxFeat)
	_ = configureLinuxTransport(tr, recTimeoutUs)
	return tr
}

func openSingleDevice(path string, recTimeoutUs uint32) (Transport, error) {
	fd, feat, err := probeLIRCDevice(path)
	if err != nil {
		return nil, err
	}
	txFD, rxFD := -1, -1
	if feat&LIRC_CAN_SEND_PULSE != 0 {
		txFD = fd
	}
	if feat&LIRC_CAN_REC_MODE2 != 0 {
		rxFD = fd
	}
	if txFD < 0 && rxFD < 0 {
		_ = syscall.Close(fd)
		return nil, fmt.Errorf("%s lacks send/rec capability: %w", path, ErrUnsupportedFeature)
	}
	tr := newLinuxTransport(txFD, rxFD, path, feat)
	if err := configureLinuxTransport(tr, recTimeoutUs); err != nil {
		_ = tr.Close()
		return nil, err
	}
	return tr, nil
}

func probeLIRCDevice(path string) (int, uint32, error) {
	fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, syscall.EACCES) {
			return -1, 0, fmt.Errorf("permission denied on %s; ensure user is in the 'lirc' group: %w", path, ErrDeviceOpen)
		}
		return -1, 0, fmt.Errorf("%s: %v: %w", path, err, ErrDeviceOpen)
	}
	var feat uint32
	if err := ioctlUint32(fd, LIRC_GET_FEATURES, &feat); err != nil {
		_ = syscall.Close(fd)
		return -1, 0, fmt.Errorf("%s LIRC_GET_FEATURES: %v: %w", path, err, ErrDeviceOpen)
	}
	return fd, feat, nil
}

func newLinuxTransport(txFD, rxFD int, path string, feat uint32) *linuxTransport {
	return &linuxTransport{
		txFD:          txFD,
		rxFD:          rxFD,
		path:          path,
		features:      feat,
		dutySupported: true,
	}
}

func configureLinuxTransport(tr *linuxTransport, recTimeoutUs uint32) error {
	if tr.txFD >= 0 {
		if err := tr.SetSendMode(LIRC_MODE_PULSE); err != nil {
			return err
		}
	}
	if tr.rxFD >= 0 {
		if err := tr.SetRecMode(LIRC_MODE_MODE2); err != nil {
			return err
		}
		_ = tr.SetRecTimeout(recTimeoutUs, true)
		if tr.features&LIRC_CAN_MEASURE_CARRIER != 0 {
			_ = tr.SetMeasureCarrier(true)
		}
	}
	return nil
}

func (l *linuxTransport) SetMeasureCarrier(enable bool) error {
	if l.rxFD < 0 || l.features&LIRC_CAN_MEASURE_CARRIER == 0 {
		return ErrUnsupportedFeature
	}
	var mode uint32
	if enable {
		mode = 1
	}
	err := ioctlUint32(l.rxFD, LIRC_SET_MEASURE_CARRIER_MODE, &mode)
	if errors.Is(err, syscall.ENOTTY) || errors.Is(err, syscall.EINVAL) {
		return nil
	}
	return err
}

func (l *linuxTransport) Features() (uint32, error) { return l.features, nil }
func (l *linuxTransport) DevicePath() string        { return l.path }
func (l *linuxTransport) IsSim() bool               { return false }

func (l *linuxTransport) SetSendMode(mode uint32) error {
	if l.txFD < 0 {
		return ErrUnsupportedFeature
	}
	return ioctlUint32(l.txFD, LIRC_SET_SEND_MODE, &mode)
}

func (l *linuxTransport) SetRecMode(mode uint32) error {
	if l.rxFD < 0 {
		return ErrUnsupportedFeature
	}
	return ioctlUint32(l.rxFD, LIRC_SET_REC_MODE, &mode)
}

func (l *linuxTransport) SetSendCarrier(freqHz uint32) error {
	if l.txFD < 0 {
		return ErrUnsupportedFeature
	}
	return ioctlUint32(l.txFD, LIRC_SET_SEND_CARRIER, &freqHz)
}

func (l *linuxTransport) SetSendDutyCycle(dutyPct uint32) error {
	if l.txFD < 0 {
		return ErrUnsupportedFeature
	}
	l.mu.Lock()
	supported := l.dutySupported
	l.mu.Unlock()
	if !supported {
		return nil
	}
	err := ioctlUint32(l.txFD, LIRC_SET_SEND_DUTY_CYCLE, &dutyPct)
	if errors.Is(err, syscall.ENOTTY) || errors.Is(err, syscall.EINVAL) {
		l.mu.Lock()
		l.dutySupported = false
		l.mu.Unlock()
		return nil
	}
	return err
}

func (l *linuxTransport) SetRecTimeout(timeoutUs uint32, enableReports bool) error {
	if l.rxFD < 0 {
		return ErrUnsupportedFeature
	}
	if err := ioctlUint32(l.rxFD, LIRC_SET_REC_TIMEOUT, &timeoutUs); err != nil {
		if !errors.Is(err, syscall.ENOTTY) {
			return err
		}
	}
	var rep uint32
	if enableReports {
		rep = 1
	}
	err := ioctlUint32(l.rxFD, LIRC_SET_REC_TIMEOUT_REPORTS, &rep)
	if errors.Is(err, syscall.ENOTTY) {
		return nil
	}
	return err
}

func (l *linuxTransport) WritePulses(ctx context.Context, pulses []uint32) error {
	if l.txFD < 0 {
		return ErrUnsupportedFeature
	}
	buf := make([]byte, len(pulses)*4)
	for i, p := range pulses {
		binary.LittleEndian.PutUint32(buf[i*4:], p)
	}
	written := 0
	for written < len(buf) {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Linux lirc_dev.c lirc_poll() never sets EPOLLOUT for TX devices
		// (e.g. gpio-ir-tx), so write directly and back off briefly on EAGAIN.
		n, err := syscall.Write(l.txFD, buf[written:])
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if errors.Is(err, syscall.EAGAIN) {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Millisecond):
				continue
			}
		}
		if err != nil {
			return err
		}
		written += n
	}
	return nil
}

func (l *linuxTransport) ReadMode2(ctx context.Context, dst []uint32) (int, error) {
	if l.rxFD < 0 {
		return 0, ErrUnsupportedFeature
	}
	if len(dst) == 0 {
		return 0, nil
	}
	buf := make([]byte, len(dst)*4)
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if err := waitFD(ctx, l.rxFD, pollInEvent); err != nil {
			return 0, err
		}
		n, err := syscall.Read(l.rxFD, buf)
		if errors.Is(err, syscall.EINTR) || errors.Is(err, syscall.EAGAIN) {
			continue
		}
		if err != nil {
			return 0, err
		}
		count := n / 4
		for i := range count {
			dst[i] = binary.LittleEndian.Uint32(buf[i*4:])
		}
		return count, nil
	}
}

func (l *linuxTransport) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	var err error
	if l.txFD >= 0 {
		err = syscall.Close(l.txFD)
	}
	if l.rxFD >= 0 && l.rxFD != l.txFD {
		err = errors.Join(err, syscall.Close(l.rxFD))
	}
	return err
}

func ioctlUint32(fd int, req uintptr, val *uint32) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), req, uintptr(unsafe.Pointer(val)))
	if errno != 0 {
		return errno
	}
	return nil
}

func waitFD(ctx context.Context, fd int, events int16) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Re-initialize ts each iteration: Linux ppoll(2) overwrites ts with
		// remaining time (0 on timeout), which would otherwise spin at 100% CPU.
		ts := syscall.Timespec{Nsec: pollSliceMs * 1_000_000}
		pfd := pollFD{fd: int32(fd), events: events}
		n, _, errno := syscall.Syscall6(
			syscall.SYS_PPOLL,
			uintptr(unsafe.Pointer(&pfd)),
			1,
			uintptr(unsafe.Pointer(&ts)),
			0,
			8,
			0,
		)
		if errno == syscall.EINTR {
			continue
		}
		if errno != 0 {
			return errno
		}
		if n > 0 {
			return nil
		}
	}
}

func closeUnused(fds ...int) {
	for _, fd := range fds {
		if fd >= 0 {
			_ = syscall.Close(fd)
		}
	}
}

func combinePaths(txPath, rxPath string) string {
	switch {
	case txPath != "" && rxPath != "":
		return txPath + "+" + rxPath
	case txPath != "":
		return txPath
	default:
		return rxPath
	}
}
