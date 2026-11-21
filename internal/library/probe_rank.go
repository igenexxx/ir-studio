package library

import (
	"context"
	"strconv"
	"time"

	"ir-studio/internal/irfile"
	"ir-studio/internal/irml"
	"ir-studio/internal/lirc"
)

// ProbeState represents the lifecycle state of a ProbeSession.
type ProbeState uint8

const (
	ProbeIdle ProbeState = iota
	ProbeRunning
	ProbePaused
	ProbeDone
)

// String returns the canonical UI/log label for ProbeState.
func (s ProbeState) String() string {
	switch s {
	case ProbeIdle:
		return "IDLE"
	case ProbeRunning:
		return "RUN"
	case ProbePaused:
		return "PAUSE"
	case ProbeDone:
		return "DONE"
	default:
		return "UNKNOWN"
	}
}

// RankByEmbedding reorders candidates descending by cosine similarity to query
// using irml.RankCandidates and resets the rewind trail so markers never skew.
func (p *ProbeSession) RankByEmbedding(query irml.IREmbedding, carrierHz uint32, reg *irfile.ProtocolRegistry) {
	if reg == nil {
		reg = irfile.DefaultRegistry()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	n := len(p.candidates)
	if n == 0 {
		return
	}
	fpEntries := make([]irml.FingerprintEntry, n)
	for i, c := range p.candidates {
		fpEntries[i] = buildFingerprintEntry(i, c, reg)
	}
	matches := irml.RankCandidates(query, carrierHz, fpEntries, make([]irml.MatchResult, 0, n))
	reordered := make([]ProbeCandidate, 0, n)
	for _, m := range matches {
		idx, err := strconv.Atoi(m.RemoteID)
		if err == nil && idx >= 0 && idx < n {
			cand := p.candidates[idx]
			cand.Score = m.Similarity
			reordered = append(reordered, cand)
		}
	}
	p.candidates = reordered
	p.cursor = 0
	p.lastSentIdx = -1
	p.recentLen = 0
}

func buildFingerprintEntry(idx int, c ProbeCandidate, reg *irfile.ProtocolRegistry) irml.FingerprintEntry {
	carrier := c.Signal.CarrierSpec().FreqHz
	var pulses irfile.PulseTrain
	if c.Signal.Kind == irfile.SignalRaw {
		pulses, _ = irfile.FromRawData(c.Signal.RawData)
	} else {
		pulses, _ = reg.Encode(c.Signal)
	}
	return irml.FingerprintEntry{
		RemoteID:   strconv.Itoa(idx),
		Pack:       c.Pack,
		SignalName: c.Signal.Name,
		CarrierHz:  carrier,
		Embedding:  irml.ExtractEmbedding(pulses, carrier),
	}
}

func (p *ProbeSession) candidateAt(idx int) (ProbeCandidate, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if idx < 0 || idx >= len(p.candidates) {
		return ProbeCandidate{}, false
	}
	return p.candidates[idx], true
}

func (p *ProbeSession) candidateCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.candidates)
}

func sendAndAwaitTX(ctx context.Context, tx *lirc.TXWorker, sig irfile.IRSignal, repeat int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	reqID := probeReqIDCounter.Add(1)
	if err := tx.Submit(lirc.TXRequest{
		ID:     reqID,
		Signal: sig,
		Repeat: max(repeat, 1),
		Ctx:    ctx,
	}); err != nil {
		return err
	}
	events := tx.Events()
	for {
		select {
		case <-ctx.Done():
			tx.CancelCurrent()
			return ctx.Err()
		case ev, ok := <-events:
			if !ok {
				return context.Canceled
			}
			if ev.ID != reqID {
				continue
			}
			switch ev.Kind {
			case lirc.TXCompleted:
				return nil
			case lirc.TXCancelled, lirc.TXError:
				if ev.Err != nil {
					return ev.Err
				}
				return context.Canceled
			}
		}
	}
}

func sleepWithCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
