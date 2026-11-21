package lirc

import (
	"context"
	"sync"
	"time"

	"ir-studio/internal/irfile"
)

const (
	rxCaptureCap = 16
	rxReadBufLen = 64
)

// RXCapture is an immutable snapshot of a received IR frame.
type RXCapture struct {
	Timestamp    time.Time
	Pulses       irfile.PulseTrain
	Decoded      *irfile.IRSignal
	CarrierEstHz uint32
}

// RXWorker continuously frames LIRC mode2 packets into RXCapture events.
type RXWorker struct {
	transport Transport
	gate      *TransceiverGate
	registry  *irfile.ProtocolRegistry
	captures  chan RXCapture

	mu         sync.Mutex
	cancelLoop context.CancelFunc
	wg         sync.WaitGroup
}

// NewRXWorker creates an RXWorker backed by tr, gate, and registry.
func NewRXWorker(tr Transport, gate *TransceiverGate, registry *irfile.ProtocolRegistry) *RXWorker {
	if gate == nil {
		gate = NewTransceiverGate(DefaultSettleGuard)
	}
	if registry == nil {
		registry = irfile.DefaultRegistry()
	}
	return &RXWorker{
		transport: tr,
		gate:      gate,
		registry:  registry,
		captures:  make(chan RXCapture, rxCaptureCap),
	}
}

// Start launches the RXWorker goroutine until ctx is cancelled or Close is called.
func (r *RXWorker) Start(ctx context.Context) {
	loopCtx, cancel := context.WithCancel(ctx)
	r.mu.Lock()
	r.cancelLoop = cancel
	r.mu.Unlock()

	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer close(r.captures)
		r.runLoop(loopCtx)
	}()
}

// Captures returns the bounded read-only RXCapture channel.
func (r *RXWorker) Captures() <-chan RXCapture { return r.captures }

// Close cancels the RX loop and waits for the goroutine to exit cleanly.
func (r *RXWorker) Close() {
	r.mu.Lock()
	cancel := r.cancelLoop
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	r.wg.Wait()
}

func (r *RXWorker) runLoop(ctx context.Context) {
	buf := make([]uint32, rxReadBufLen)
	frame := make(irfile.PulseTrain, 0, 128)

	for {
		n, err := r.transport.ReadMode2(ctx, buf)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		if r.gate.IsTXActive() {
			frame = frame[:0]
			continue
		}
		for _, pkt := range buf[:n] {
			frame = r.processPacket(frame, pkt)
		}
	}
}

func (r *RXWorker) processPacket(frame irfile.PulseTrain, pkt uint32) irfile.PulseTrain {
	kind := pkt & LIRC_MODE2_MASK
	val := pkt & LIRC_VALUE_MASK

	if kind == LIRC_MODE2_TIMEOUT {
		r.flushFrame(frame)
		return frame[:0]
	}
	if val == 0 {
		return frame
	}
	if kind == LIRC_MODE2_SPACE {
		if len(frame) == 0 {
			return frame
		}
		if val >= DefaultRecTimeoutUs {
			r.flushFrame(frame)
			return frame[:0]
		}
		if val < MinPulseUs {
			return frame
		}
		frame = appendCoalesced(frame, -int32(val))
	} else if kind == LIRC_MODE2_PULSE {
		if val < MinPulseUs {
			return frame
		}
		frame = appendCoalesced(frame, int32(val))
	}
	if len(frame) >= MaxFramePulses {
		r.flushFrame(frame)
		return frame[:0]
	}
	return frame
}

func appendCoalesced(frame irfile.PulseTrain, dur int32) irfile.PulseTrain {
	if len(frame) > 0 && (frame[len(frame)-1] > 0) == (dur > 0) {
		frame[len(frame)-1] += dur
		return frame
	}
	return append(frame, dur)
}

func (r *RXWorker) flushFrame(frame irfile.PulseTrain) {
	if len(frame) > 0 && frame[len(frame)-1] < 0 {
		frame = frame[:len(frame)-1]
	}
	if len(frame) < MinFramePulses {
		return
	}
	cp := make(irfile.PulseTrain, len(frame))
	copy(cp, frame)

	capEvt := RXCapture{
		Timestamp:    time.Now(),
		Pulses:       cp,
		CarrierEstHz: DefaultCarrierHz,
	}
	if sig, ok := r.registry.Decode(cp); ok {
		capEvt.Decoded = &sig
		if sig.Frequency != 0 {
			capEvt.CarrierEstHz = sig.Frequency
		}
	}
	r.sendDropOldest(capEvt)
}

func (r *RXWorker) sendDropOldest(c RXCapture) {
	select {
	case r.captures <- c:
	default:
		select {
		case <-r.captures:
		default:
		}
		select {
		case r.captures <- c:
		default:
		}
	}
}
