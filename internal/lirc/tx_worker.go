package lirc

import (
	"context"
	"errors"
	"sync"
	"time"

	"ir-studio/internal/irfile"
)

const (
	txQueueCap  = 8
	txEventsCap = 16
)

// TXEventKind identifies the lifecycle stage of a TXRequest.
type TXEventKind string

const (
	TXStarted   TXEventKind = "started"
	TXProgress  TXEventKind = "progress"
	TXCompleted TXEventKind = "completed"
	TXCancelled TXEventKind = "cancelled"
	TXError     TXEventKind = "error"
)

// TXRequest represents a queued asynchronous IR transmission.
type TXRequest struct {
	ID            uint64
	Signal        irfile.IRSignal
	Repeat        int
	InterFrameGap time.Duration
	Ctx           context.Context
}

// TXEvent reports asynchronous TX execution progress to UI or MCP consumers.
type TXEvent struct {
	ID          uint64
	Kind        TXEventKind
	RepeatIdx   int
	RepeatTotal int
	Err         error
}

// TXWorker executes queued TXRequests asynchronously with cancellation support.
type TXWorker struct {
	xcvr     *Transceiver
	registry *irfile.ProtocolRegistry
	queue    chan TXRequest
	events   chan TXEvent

	mu         sync.Mutex
	cancelCur  context.CancelFunc
	cancelLoop context.CancelFunc
	wg         sync.WaitGroup
}

// NewTXWorker creates a TXWorker backed by xcvr and registry.
func NewTXWorker(xcvr *Transceiver, registry *irfile.ProtocolRegistry) *TXWorker {
	if registry == nil {
		registry = irfile.DefaultRegistry()
	}
	return &TXWorker{
		xcvr:     xcvr,
		registry: registry,
		queue:    make(chan TXRequest, txQueueCap),
		events:   make(chan TXEvent, txEventsCap),
	}
}

// Start launches the TXWorker background goroutine until ctx is cancelled or Close is called.
func (w *TXWorker) Start(ctx context.Context) {
	loopCtx, cancel := context.WithCancel(ctx)
	w.mu.Lock()
	w.cancelLoop = cancel
	w.mu.Unlock()

	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		defer close(w.events)
		for {
			select {
			case <-loopCtx.Done():
				return
			case req := <-w.queue:
				w.execute(loopCtx, req)
			}
		}
	}()
}

// Submit enqueues req non-blockingly after checking repeat limits.
func (w *TXWorker) Submit(req TXRequest) error {
	if req.Repeat > MaxRepeats {
		return ErrRepeatLimitExceeded
	}
	if req.Repeat <= 0 {
		req.Repeat = 1
	}
	if req.InterFrameGap <= 0 {
		req.InterFrameGap = DefaultInterFrameGap
	}
	select {
	case w.queue <- req:
		return nil
	default:
		return ErrTXQueueFull
	}
}

// CancelCurrent immediately cancels any in-flight transmission.
func (w *TXWorker) CancelCurrent() {
	w.mu.Lock()
	cancel := w.cancelCur
	w.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Events returns the read-only TXEvent channel.
func (w *TXWorker) Events() <-chan TXEvent { return w.events }

// Transceiver returns the underlying LIRC transceiver used by this worker.
func (w *TXWorker) Transceiver() *Transceiver { return w.xcvr }

// Close cancels the active TX and waits for the worker goroutine to exit.
func (w *TXWorker) Close() {
	w.CancelCurrent()
	w.mu.Lock()
	cancel := w.cancelLoop
	w.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	w.wg.Wait()
}

func (w *TXWorker) execute(loopCtx context.Context, req TXRequest) {
	pulses, carrier, err := w.prepareRequest(req)
	if err != nil {
		w.emit(TXEvent{ID: req.ID, Kind: TXError, RepeatTotal: req.Repeat, Err: err})
		return
	}
	reqCtx, cancel := w.deriveContext(loopCtx, req.Ctx)
	defer func() {
		cancel()
		w.setCancelCur(nil)
	}()
	w.setCancelCur(cancel)

	w.emit(TXEvent{ID: req.ID, Kind: TXStarted, RepeatTotal: req.Repeat})
	if err := w.transmitRepeats(reqCtx, req, pulses, carrier); err != nil {
		kind := TXError
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			kind = TXCancelled
		}
		w.emit(TXEvent{ID: req.ID, Kind: kind, RepeatTotal: req.Repeat, Err: err})
		return
	}
	w.emit(TXEvent{ID: req.ID, Kind: TXCompleted, RepeatIdx: req.Repeat, RepeatTotal: req.Repeat})
}

func (w *TXWorker) prepareRequest(req TXRequest) ([]uint32, irfile.CarrierSpec, error) {
	train, err := w.registry.Encode(req.Signal)
	if err != nil {
		return nil, irfile.CarrierSpec{}, err
	}
	lircPulses, err := train.ToLIRCPulses()
	if err != nil {
		return nil, irfile.CarrierSpec{}, err
	}
	carrier := req.Signal.CarrierSpec()
	if req.Signal.Frequency == 0 && req.Signal.Kind == irfile.SignalParsed {
		if codec, ok := w.registry.Lookup(req.Signal.Protocol); ok {
			carrier = codec.Carrier()
		}
	}
	return lircPulses, carrier, nil
}

func (w *TXWorker) transmitRepeats(ctx context.Context, req TXRequest, pulses []uint32, carrier irfile.CarrierSpec) error {
	for i := range req.Repeat {
		if err := w.xcvr.Send(ctx, pulses, carrier); err != nil {
			return err
		}
		w.emit(TXEvent{ID: req.ID, Kind: TXProgress, RepeatIdx: i + 1, RepeatTotal: req.Repeat})
		if i < req.Repeat-1 {
			if err := sleepWithContext(ctx, req.InterFrameGap); err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *TXWorker) deriveContext(loopCtx, reqCtx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(loopCtx)
	if reqCtx != nil {
		stop := context.AfterFunc(reqCtx, cancel)
		return ctx, func() {
			stop()
			cancel()
		}
	}
	return ctx, cancel
}

func (w *TXWorker) setCancelCur(fn context.CancelFunc) {
	w.mu.Lock()
	w.cancelCur = fn
	w.mu.Unlock()
}

func (w *TXWorker) emit(ev TXEvent) {
	select {
	case w.events <- ev:
	default:
	}
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
