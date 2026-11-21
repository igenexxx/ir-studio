package library

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"ir-studio/internal/irfile"
	"ir-studio/internal/lirc"
)

const (
	MinProbeInterval     = 300 * time.Millisecond
	MaxProbeInterval     = 5000 * time.Millisecond
	DefaultProbeInterval = 1200 * time.Millisecond
	MaxRewindTrail       = 12
)

// ProbeIntervalPresets lists canonical probe intervals from 300ms to 5000ms.
var ProbeIntervalPresets = []time.Duration{
	300 * time.Millisecond,
	500 * time.Millisecond,
	800 * time.Millisecond,
	1200 * time.Millisecond,
	1500 * time.Millisecond,
	2000 * time.Millisecond,
	3000 * time.Millisecond,
	5000 * time.Millisecond,
}

var (
	ErrNoCandidates    = errors.New("library: no probe candidates")
	ErrProbeAlreadyRun = errors.New("library: probe session already running")
	ErrNilTXWorker     = errors.New("library: nil TXWorker")
	probeReqIDCounter  atomic.Uint64
)

// ProbeCandidate is a single filtered button candidate ready for probing.
type ProbeCandidate struct {
	Remote RemoteEntry
	Signal irfile.IRSignal
	Pack   string
	Score  float32
}

// ProbeSnapshot is an immutable view of ProbeSession progress for UI/MCP.
type ProbeSnapshot struct {
	State       ProbeState
	Cursor      int
	LastSentIdx int
	Total       int
	SentCount   int
	Interval    time.Duration
	RecentSent  []int
	Current     *ProbeCandidate
	Err         error
}

// ProbeSession runs an asynchronous Universal Power-Off / brand sweep with
// a 12-slot RecentSent rewind trail and Smart Probe Ranking.
type ProbeSession struct {
	mu          sync.Mutex
	state       ProbeState
	candidates  []ProbeCandidate
	cursor      int
	lastSentIdx int
	sentCount   int
	recentSent  [MaxRewindTrail]int
	recentLen   int
	interval    time.Duration
	cancelRun   context.CancelFunc
	doneCh      chan struct{}
}

// NewProbeSession initializes a ProbeSession in ProbeIdle state.
func NewProbeSession(candidates []ProbeCandidate, interval time.Duration) *ProbeSession {
	return &ProbeSession{
		state:       ProbeIdle,
		candidates:  slices.Clone(candidates),
		lastSentIdx: -1,
		interval:    ClampProbeInterval(interval),
	}
}

// ClampProbeInterval clamps d to [MinProbeInterval, MaxProbeInterval],
// returning DefaultProbeInterval if d <= 0.
func ClampProbeInterval(d time.Duration) time.Duration {
	if d <= 0 {
		return DefaultProbeInterval
	}
	return min(max(d, MinProbeInterval), MaxProbeInterval)
}

// SetInterval updates the inter-candidate probe interval (clamped to [300ms, 5000ms]).
func (p *ProbeSession) SetInterval(d time.Duration) {
	p.mu.Lock()
	p.interval = ClampProbeInterval(d)
	p.mu.Unlock()
}

// Interval returns the current probe interval.
func (p *ProbeSession) Interval() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.interval
}

// State returns the current ProbeState.
func (p *ProbeSession) State() ProbeState {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

// Candidates returns a shallow copy of the current candidate slice.
func (p *ProbeSession) Candidates() []ProbeCandidate {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.candidates)
}

// SetCursor moves the cursor to idx clamped to [0, len(candidates)-1].
func (p *ProbeSession) SetCursor(idx int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.candidates) == 0 {
		p.cursor = 0
		return
	}
	p.cursor = min(max(idx, 0), len(p.candidates)-1)
}

// Rewind moves the cursor backward by steps (clamped to >= 0).
func (p *ProbeSession) Rewind(steps int) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.candidates) == 0 {
		p.cursor = 0
		return 0
	}
	if steps < 0 {
		steps = 0
	}
	p.cursor = max(p.cursor-steps, 0)
	return p.cursor
}

// RecordSent records idx as the most recently transmitted candidate ('>'),
// pushing the previous lastSentIdx into the 12-slot RecentSent ring ('*').
func (p *ProbeSession) RecordSent(idx int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if idx < 0 || idx >= len(p.candidates) {
		return
	}
	prev := p.lastSentIdx
	if prev >= 0 && prev != idx {
		p.pushRecentLocked(prev)
	}
	p.removeRecentLocked(idx)
	p.lastSentIdx = idx
	p.cursor = idx
	p.sentCount++
}

func (p *ProbeSession) pushRecentLocked(idx int) {
	p.removeRecentLocked(idx)
	if p.recentLen == MaxRewindTrail {
		copy(p.recentSent[:MaxRewindTrail-1], p.recentSent[1:MaxRewindTrail])
		p.recentSent[MaxRewindTrail-1] = idx
		return
	}
	p.recentSent[p.recentLen] = idx
	p.recentLen++
}

func (p *ProbeSession) removeRecentLocked(idx int) {
	for i := range p.recentLen {
		if p.recentSent[i] == idx {
			copy(p.recentSent[i:p.recentLen-1], p.recentSent[i+1:p.recentLen])
			p.recentLen--
			return
		}
	}
}

// MarkerFor returns '>' for LastSentIdx, '*' for any index in RecentSent, or ' '.
func (p *ProbeSession) MarkerFor(idx int) byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	if idx == p.lastSentIdx && p.lastSentIdx >= 0 {
		return '>'
	}
	for i := range p.recentLen {
		if p.recentSent[i] == idx {
			return '*'
		}
	}
	return ' '
}

// Snapshot returns a thread-safe point-in-time snapshot of ProbeSession.
func (p *ProbeSession) Snapshot() ProbeSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.snapshotLocked(nil)
}

func (p *ProbeSession) snapshotLocked(err error) ProbeSnapshot {
	recent := make([]int, p.recentLen)
	copy(recent, p.recentSent[:p.recentLen])
	var cur *ProbeCandidate
	if p.cursor >= 0 && p.cursor < len(p.candidates) {
		c := p.candidates[p.cursor]
		cur = &c
	}
	return ProbeSnapshot{
		State:       p.state,
		Cursor:      p.cursor,
		LastSentIdx: p.lastSentIdx,
		Total:       len(p.candidates),
		SentCount:   p.sentCount,
		Interval:    p.interval,
		RecentSent:  recent,
		Current:     cur,
		Err:         err,
	}
}

// Start launches the asynchronous probe loop on a background goroutine.
func (p *ProbeSession) Start(ctx context.Context, tx *lirc.TXWorker, onProgress func(ProbeSnapshot)) error {
	if tx == nil {
		return ErrNilTXWorker
	}
	p.mu.Lock()
	if len(p.candidates) == 0 {
		p.mu.Unlock()
		return ErrNoCandidates
	}
	if p.state == ProbeRunning {
		p.mu.Unlock()
		return ErrProbeAlreadyRun
	}
	startIdx := p.resolveStartIdxLocked()
	runCtx, cancel := context.WithCancel(ctx)
	doneCh := make(chan struct{})
	p.state = ProbeRunning
	p.cancelRun = cancel
	p.doneCh = doneCh
	p.mu.Unlock()

	go p.runLoop(runCtx, tx, startIdx, onProgress, doneCh)
	return nil
}

func (p *ProbeSession) resolveStartIdxLocked() int {
	if p.state == ProbeDone {
		p.cursor = 0
		return 0
	}
	if p.state == ProbePaused && p.cursor == p.lastSentIdx && p.lastSentIdx+1 < len(p.candidates) {
		return p.lastSentIdx + 1
	}
	return min(max(p.cursor, 0), len(p.candidates)-1)
}

// Pause immediately cancels the active probe run and waits for the goroutine to stop.
func (p *ProbeSession) Pause() {
	p.mu.Lock()
	cancel := p.cancelRun
	done := p.doneCh
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

// Wait blocks until the current probe run finishes (DONE or PAUSE).
func (p *ProbeSession) Wait() {
	p.mu.Lock()
	done := p.doneCh
	p.mu.Unlock()
	if done != nil {
		<-done
	}
}

func (p *ProbeSession) runLoop(
	ctx context.Context,
	tx *lirc.TXWorker,
	startIdx int,
	onProgress func(ProbeSnapshot),
	doneCh chan struct{},
) {
	defer close(doneCh)
	for i := startIdx; ; i++ {
		cand, ok := p.candidateAt(i)
		if !ok {
			p.finishRun(ProbeDone, nil, onProgress)
			return
		}
		if err := sendAndAwaitTX(ctx, tx, cand.Signal, 1); err != nil {
			p.finishRun(ProbePaused, err, onProgress)
			return
		}
		p.RecordSent(i)
		if onProgress != nil {
			onProgress(p.Snapshot())
		}
		if i+1 >= p.candidateCount() {
			p.finishRun(ProbeDone, nil, onProgress)
			return
		}
		if err := sleepWithCtx(ctx, p.Interval()); err != nil {
			p.finishRun(ProbePaused, err, onProgress)
			return
		}
	}
}

func (p *ProbeSession) finishRun(next ProbeState, err error, onProgress func(ProbeSnapshot)) {
	p.mu.Lock()
	p.state = next
	p.cancelRun = nil
	snap := p.snapshotLocked(err)
	p.mu.Unlock()
	if onProgress != nil {
		onProgress(snap)
	}
}
