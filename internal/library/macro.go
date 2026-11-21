package library

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"ir-studio/internal/config"
	"ir-studio/internal/irfile"
	"ir-studio/internal/lirc"
)

var (
	ErrMacroAlreadyRunning = errors.New("library: macro is already running")
	ErrEmptyMacro          = errors.New("library: macro has no steps")
	ErrButtonNotFound      = errors.New("library: button not found in remote")
)

// MacroProgress reports asynchronous step execution status for UI/MCP.
type MacroProgress struct {
	MacroID   string
	StepIndex int
	StepTotal int
	Remote    string
	Button    string
	Done      bool
	Cancelled bool
	Err       error
}

// MacroRunner executes config.Macro sequences asynchronously via lirc.TXWorker.
type MacroRunner struct {
	store     *Store
	tx        *lirc.TXWorker
	mu        sync.Mutex
	running   bool
	cancelRun context.CancelFunc
	doneCh    chan struct{}
}

// NewMacroRunner creates a MacroRunner backed by store and tx.
func NewMacroRunner(store *Store, tx *lirc.TXWorker) *MacroRunner {
	return &MacroRunner{store: store, tx: tx}
}

// Running reports whether a macro is currently executing.
func (r *MacroRunner) Running() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

// Run validates m and executes its steps asynchronously on a background goroutine.
func (r *MacroRunner) Run(ctx context.Context, m config.Macro, onProgress func(MacroProgress)) error {
	if r.store == nil || r.tx == nil {
		return ErrNilTXWorker
	}
	if err := config.ValidateMacro(m); err != nil {
		return err
	}
	if len(m.Steps) == 0 {
		return ErrEmptyMacro
	}
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return ErrMacroAlreadyRunning
	}
	runCtx, cancel := context.WithCancel(ctx)
	doneCh := make(chan struct{})
	r.running = true
	r.cancelRun = cancel
	r.doneCh = doneCh
	r.mu.Unlock()

	go r.execute(runCtx, m, onProgress, doneCh)
	return nil
}

// Cancel immediately aborts the active macro run and waits for cleanup.
func (r *MacroRunner) Cancel() {
	r.mu.Lock()
	cancel := r.cancelRun
	done := r.doneCh
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

// Wait blocks until the current macro execution completes.
func (r *MacroRunner) Wait() {
	r.mu.Lock()
	done := r.doneCh
	r.mu.Unlock()
	if done != nil {
		<-done
	}
}

func (r *MacroRunner) execute(
	ctx context.Context,
	m config.Macro,
	onProgress func(MacroProgress),
	doneCh chan struct{},
) {
	defer func() {
		r.mu.Lock()
		r.running = false
		r.cancelRun = nil
		r.mu.Unlock()
		close(doneCh)
	}()

	total := len(m.Steps)
	for i, step := range m.Steps {
		if err := r.runStep(ctx, step); err != nil {
			emitMacroProgress(onProgress, MacroProgress{
				MacroID:   m.ID,
				StepIndex: i,
				StepTotal: total,
				Remote:    step.Remote,
				Button:    step.Button,
				Done:      true,
				Cancelled: errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded),
				Err:       err,
			})
			return
		}
		emitMacroProgress(onProgress, MacroProgress{
			MacroID:   m.ID,
			StepIndex: i,
			StepTotal: total,
			Remote:    step.Remote,
			Button:    step.Button,
		})
		if step.DelayAfterMs > 0 && i < total-1 {
			if err := sleepWithCtx(ctx, time.Duration(step.DelayAfterMs)*time.Millisecond); err != nil {
				emitMacroProgress(onProgress, MacroProgress{
					MacroID:   m.ID,
					StepIndex: i,
					StepTotal: total,
					Remote:    step.Remote,
					Button:    step.Button,
					Done:      true,
					Cancelled: true,
					Err:       err,
				})
				return
			}
		}
	}
	emitMacroProgress(onProgress, MacroProgress{
		MacroID:   m.ID,
		StepIndex: total - 1,
		StepTotal: total,
		Done:      true,
	})
}

func (r *MacroRunner) runStep(ctx context.Context, step config.MacroStep) error {
	entry, err := r.store.LoadUserRemote(step.Remote)
	if err != nil {
		return err
	}
	sig, ok := findSignalByName(entry.Signals, step.Button)
	if !ok {
		return fmt.Errorf("%s/%s: %w", step.Remote, step.Button, ErrButtonNotFound)
	}
	return sendAndAwaitTX(ctx, r.tx, sig, max(step.Repeat, 1))
}

func findSignalByName(signals []irfile.IRSignal, name string) (irfile.IRSignal, bool) {
	for _, s := range signals {
		if s.Name == name {
			return s, true
		}
	}
	for _, s := range signals {
		if strings.EqualFold(s.Name, name) {
			return s, true
		}
	}
	return irfile.IRSignal{}, false
}

func emitMacroProgress(fn func(MacroProgress), p MacroProgress) {
	if fn != nil {
		fn(p)
	}
}
