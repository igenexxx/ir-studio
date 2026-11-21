package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"ir-studio/internal/config"
	"ir-studio/internal/irfile"
	"ir-studio/internal/irml"
	"ir-studio/internal/lirc"
)

func TestActionFilterMatching(t *testing.T) {
	cases := []struct {
		button string
		action ProbeAction
		want   bool
	}{
		{"Power", ActionPower, true},
		{"POWER_OFF", ActionPower, true},
		{"Pwr-On", ActionPower, true},
		{"Menu", ActionPower, false},
		{"Power_off", ActionOff, true},
		{"Standby", ActionOff, true},
		{"Cool_24", ActionCool, true},
		{"Mode-Cool", ActionCool, true},
		{"Heat_22", ActionHeat, true},
		{"Mute", ActionMute, true},
		{"Muting", ActionMute, true},
		{"Vol_dn", ActionVolDown, true},
		{"Volume-Down", ActionVolDown, true},
		{"Vol_up", ActionVolUp, true},
		{"HDMI_1", ActionInput, true},
		{"Anything", ActionAll, true},
		{"", ActionAll, false},
		{"Sleep_Timer", ProbeAction("sleep"), true},
		{"Menu", ProbeAction("sleep"), false},
	}
	for _, tc := range cases {
		t.Run(tc.button+"_"+string(tc.action), func(t *testing.T) {
			if got := MatchesProbeAction(tc.button, tc.action); got != tc.want {
				t.Fatalf("MatchesProbeAction(%q, %q) = %v, want %v", tc.button, tc.action, got, tc.want)
			}
		})
	}
}

func TestFilterPackCandidates(t *testing.T) {
	store := newTestStore(t)
	writePackFile(t, store, "tvs", "BrandA.ir", sampleNECFile())
	writePackFile(t, store, "tvs", "BrandB.ir", &irfile.IRFile{
		Version: irfile.SupportedVersion,
		Signals: []irfile.IRSignal{
			{Name: "Menu", Kind: irfile.SignalParsed, Protocol: "NEC"},
			{Name: "Vol_dn", Kind: irfile.SignalParsed, Protocol: "NEC"},
		},
	})

	// ActionPower matches BrandA only (BrandB has no Power button).
	cands, skipped, err := FilterPackCandidates(store, "tvs", ActionPower)
	if err != nil || skipped != 0 || len(cands) != 1 || cands[0].Signal.Name != "Power" {
		t.Fatalf("FilterPackCandidates(Power) = (%d, %d, %v)", len(cands), skipped, err)
	}

	// ActionAll includes every button across both remotes (3 + 2 = 5).
	allCands, _, err := FilterPackCandidates(store, "tvs", ActionAll)
	if err != nil || len(allCands) != 5 {
		t.Fatalf("FilterPackCandidates(All) = (%d, %v), want 5", len(allCands), err)
	}
}

func TestProbeSessionRewindAnd12SlotTrail(t *testing.T) {
	cands := make([]ProbeCandidate, 25)
	for i := range cands {
		cands[i] = ProbeCandidate{Signal: sampleNECFile().Signals[0], Pack: "tvs"}
	}
	ps := NewProbeSession(cands, 50*time.Millisecond)
	if ps.Interval() != MinProbeInterval {
		t.Fatalf("expected clamped MinProbeInterval, got %v", ps.Interval())
	}
	ps.SetInterval(99 * time.Second)
	if ps.Interval() != MaxProbeInterval {
		t.Fatalf("expected clamped MaxProbeInterval, got %v", ps.Interval())
	}
	ps.SetInterval(0)
	if ps.Interval() != DefaultProbeInterval {
		t.Fatalf("expected DefaultProbeInterval, got %v", ps.Interval())
	}

	// Record 20 sends (0..19): LastSentIdx=19 ('>'), RecentSent=7..18 ('*'), 0..6 (' ').
	for i := range 20 {
		ps.RecordSent(i)
	}
	snap := ps.Snapshot()
	if snap.LastSentIdx != 19 || len(snap.RecentSent) != MaxRewindTrail {
		t.Fatalf("unexpected snap: last=%d recentLen=%d", snap.LastSentIdx, len(snap.RecentSent))
	}
	if ps.MarkerFor(19) != '>' {
		t.Fatalf("MarkerFor(19) = %q, want '>'", ps.MarkerFor(19))
	}
	for i := 7; i <= 18; i++ {
		if ps.MarkerFor(i) != '*' {
			t.Fatalf("MarkerFor(%d) = %q, want '*'", i, ps.MarkerFor(i))
		}
	}
	for i := range 7 {
		if ps.MarkerFor(i) != ' ' {
			t.Fatalf("MarkerFor(%d) = %q, want ' '", i, ps.MarkerFor(i))
		}
	}

	// Rewind 4 steps from cursor 19 -> 15; rewind 100 -> clamped at 0.
	if got := ps.Rewind(4); got != 15 {
		t.Fatalf("Rewind(4) = %d, want 15", got)
	}
	if got := ps.Rewind(100); got != 0 {
		t.Fatalf("Rewind(100) = %d, want 0", got)
	}
}

func TestProbeSessionSmartRankByEmbedding(t *testing.T) {
	reg := irfile.DefaultRegistry()
	rc5Sig := irfile.IRSignal{
		Name: "Power", Kind: irfile.SignalParsed, Protocol: "RC5",
		Address: [4]byte{0x01}, Command: [4]byte{0x0C},
	}
	necSig := irfile.IRSignal{
		Name: "Power", Kind: irfile.SignalParsed, Protocol: "NEC",
		Address: [4]byte{0x04}, Command: [4]byte{0x08},
	}
	cands := []ProbeCandidate{
		{Remote: RemoteEntry{Name: "RC5_Remote"}, Signal: rc5Sig, Pack: "tvs"},
		{Remote: RemoteEntry{Name: "NEC_Remote"}, Signal: necSig, Pack: "tvs"},
	}
	ps := NewProbeSession(cands, DefaultProbeInterval)
	ps.RecordSent(0) // verify trail resets after ranking

	necPulses, err := reg.Encode(necSig)
	if err != nil {
		t.Fatal(err)
	}
	query := irml.ExtractEmbedding(necPulses, 38000)
	ps.RankByEmbedding(query, 38000, reg)

	ranked := ps.Candidates()
	if ranked[0].Remote.Name != "NEC_Remote" || ranked[0].Score < ranked[1].Score {
		t.Fatalf("expected NEC_Remote ranked first, got %+v", ranked)
	}
	if ps.Snapshot().LastSentIdx != -1 || len(ps.Snapshot().RecentSent) != 0 {
		t.Fatalf("expected rewind trail reset after RankByEmbedding")
	}
}

func newTestTXWorker(t *testing.T) (*lirc.SimTransport, *lirc.TXWorker) {
	t.Helper()
	sim := lirc.NewSimTransport()
	gate := lirc.NewTransceiverGate(time.Millisecond)
	xcvr := lirc.NewTransceiver(sim, gate)
	worker := lirc.NewTXWorker(xcvr, irfile.DefaultRegistry())
	ctx, cancel := context.WithCancel(context.Background())
	worker.Start(ctx)
	t.Cleanup(func() {
		cancel()
		worker.Close()
		_ = sim.Close()
	})
	return sim, worker
}

func TestProbeSessionStartPauseResumeAndDone(t *testing.T) {
	sim, tx := newTestTXWorker(t)
	sig := sampleNECFile().Signals[0]
	cands := []ProbeCandidate{
		{Remote: RemoteEntry{Name: "R1"}, Signal: sig, Pack: "tvs"},
		{Remote: RemoteEntry{Name: "R2"}, Signal: sig, Pack: "tvs"},
		{Remote: RemoteEntry{Name: "R3"}, Signal: sig, Pack: "tvs"},
	}

	// Guard checks.
	empty := NewProbeSession(nil, MinProbeInterval)
	if err := empty.Start(context.Background(), tx, nil); !errors.Is(err, ErrNoCandidates) {
		t.Fatalf("empty Start err=%v, want ErrNoCandidates", err)
	}
	ps := NewProbeSession(cands, MinProbeInterval)
	if err := ps.Start(context.Background(), nil, nil); !errors.Is(err, ErrNilTXWorker) {
		t.Fatalf("nil tx Start err=%v, want ErrNilTXWorker", err)
	}

	firstSent := make(chan struct{}, 1)
	var mu sync.Mutex
	var states []ProbeState
	err := ps.Start(context.Background(), tx, func(s ProbeSnapshot) {
		mu.Lock()
		states = append(states, s.State)
		mu.Unlock()
		if s.SentCount == 1 {
			select {
			case firstSent <- struct{}{}:
			default:
			}
		}
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for first candidate to send, then immediately Pause() during the 300ms interval.
	select {
	case <-firstSent:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first probe TX")
	}
	pauseStart := time.Now()
	ps.Pause()
	if elapsed := time.Since(pauseStart); elapsed > 150*time.Millisecond {
		t.Fatalf("Pause took %v, expected immediate cancellation", elapsed)
	}
	if ps.State() != ProbePaused {
		t.Fatalf("state after Pause = %s, want PAUSE", ps.State())
	}

	// Resume to completion.
	if err := ps.Start(context.Background(), tx, nil); err != nil {
		t.Fatalf("resume Start: %v", err)
	}
	ps.Wait()
	if ps.State() != ProbeDone {
		t.Fatalf("final state = %s, want DONE", ps.State())
	}
	if got := len(sim.TxHistory()); got != 3 {
		t.Fatalf("TX count = %d, want 3", got)
	}
}

func TestMacroRunnerExecuteAndCancel(t *testing.T) {
	sim, tx := newTestTXWorker(t)
	store := newTestStore(t)
	if _, err := store.SaveUserRemote("LivingTV.ir", sampleNECFile()); err != nil {
		t.Fatal(err)
	}

	runner := NewMacroRunner(store, tx)
	macro := config.Macro{
		ID:   "movie-night",
		Name: "Movie Night",
		Steps: []config.MacroStep{
			{Remote: "LivingTV.ir", Button: "Power", DelayAfterMs: 50, Repeat: 1},
			{Remote: "LivingTV.ir", Button: "mute", DelayAfterMs: 0, Repeat: 2},
		},
	}

	var mu sync.Mutex
	var progress []MacroProgress
	if err := runner.Run(context.Background(), macro, func(p MacroProgress) {
		mu.Lock()
		progress = append(progress, p)
		mu.Unlock()
	}); err != nil {
		t.Fatalf("MacroRunner.Run: %v", err)
	}
	runner.Wait()

	if got := len(sim.TxHistory()); got != 3 {
		t.Fatalf("expected 3 total TX frames (1 + 2 repeats), got %d", got)
	}
	mu.Lock()
	last := progress[len(progress)-1]
	mu.Unlock()
	if !last.Done || last.Err != nil || last.Cancelled {
		t.Fatalf("unexpected final macro progress: %+v", last)
	}

	// Cancellation during long inter-step delay must return immediately.
	slowMacro := config.Macro{
		ID:   "slow",
		Name: "Slow",
		Steps: []config.MacroStep{
			{Remote: "LivingTV.ir", Button: "Power", DelayAfterMs: 5000, Repeat: 1},
			{Remote: "LivingTV.ir", Button: "Mute", DelayAfterMs: 0, Repeat: 1},
		},
	}
	step0Done := make(chan struct{}, 1)
	var cancelProg MacroProgress
	if err := runner.Run(context.Background(), slowMacro, func(p MacroProgress) {
		if !p.Done && p.StepIndex == 0 {
			select {
			case step0Done <- struct{}{}:
			default:
			}
		}
		if p.Done {
			mu.Lock()
			cancelProg = p
			mu.Unlock()
		}
	}); err != nil {
		t.Fatalf("slowMacro.Run: %v", err)
	}
	select {
	case <-step0Done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for step 0")
	}
	t0 := time.Now()
	runner.Cancel()
	if time.Since(t0) > 200*time.Millisecond {
		t.Fatalf("Cancel took too long: %v", time.Since(t0))
	}
	mu.Lock()
	cp := cancelProg
	mu.Unlock()
	if !cp.Cancelled || !errors.Is(cp.Err, context.Canceled) {
		t.Fatalf("expected Cancelled macro progress, got %+v", cp)
	}

	// Verify DefaultProbeActions and ProbeState.String coverage.
	if !slices.Contains(DefaultProbeActions(), ActionPower) || ProbeIdle.String() != "IDLE" {
		t.Fatalf("unexpected defaults")
	}
}

func TestProbeAndMacroEdgeCases(t *testing.T) {
	// ProbeState.String all values.
	for _, tc := range []struct {
		s    ProbeState
		want string
	}{
		{ProbeIdle, "IDLE"},
		{ProbeRunning, "RUN"},
		{ProbePaused, "PAUSE"},
		{ProbeDone, "DONE"},
		{ProbeState(99), "UNKNOWN"},
	} {
		if tc.s.String() != tc.want {
			t.Fatalf("ProbeState(%d).String() = %q, want %q", tc.s, tc.s.String(), tc.want)
		}
	}

	// Empty ProbeSession cursor/rewind/rank/record edge cases.
	empty := NewProbeSession(nil, DefaultProbeInterval)
	empty.SetCursor(5)
	if empty.Rewind(2) != 0 {
		t.Fatalf("expected 0 rewind on empty")
	}
	empty.RecordSent(0)
	empty.RankByEmbedding(irml.IREmbedding{}, 38000, nil)

	// Re-sending an index already in RecentSent deduplicates cleanly.
	sig := sampleNECFile().Signals[0]
	ps := NewProbeSession([]ProbeCandidate{
		{Signal: sig}, {Signal: sig}, {Signal: sig},
	}, MinProbeInterval)
	ps.SetCursor(99)
	if ps.Snapshot().Cursor != 2 {
		t.Fatalf("SetCursor clamp failed: %d", ps.Snapshot().Cursor)
	}
	if ps.Rewind(-3) != 2 {
		t.Fatalf("negative Rewind should clamp to 0 steps")
	}
	ps.RecordSent(0)
	ps.RecordSent(1)
	ps.RecordSent(0) // removes 0 from RecentSent and makes 1 recent
	if ps.MarkerFor(0) != '>' || ps.MarkerFor(1) != '*' {
		t.Fatalf("dedupe markers wrong: 0=%q 1=%q", ps.MarkerFor(0), ps.MarkerFor(1))
	}

	// FilterPackCandidates on UserLibrary + corrupt file skipping.
	store := newTestStore(t)
	if _, err := store.SaveUserRemote("Good.ir", sampleNECFile()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.UserDir, "Corrupt.ir"), []byte("bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	cands, skipped, err := FilterPackCandidates(store, "", ActionPower)
	if err != nil || len(cands) != 1 || skipped != 1 {
		t.Fatalf("FilterPackCandidates user = (%d, %d, %v)", len(cands), skipped, err)
	}

	// MacroRunner error guards: nil tx, empty steps, missing button, already running.
	if err := NewMacroRunner(store, nil).Run(context.Background(), config.Macro{ID: "a", Name: "a"}, nil); !errors.Is(err, ErrNilTXWorker) {
		t.Fatalf("nil tx err=%v", err)
	}
	_, tx := newTestTXWorker(t)
	r := NewMacroRunner(store, tx)
	if r.Running() {
		t.Fatalf("expected Running() == false")
	}
	if err := r.Run(context.Background(), config.Macro{ID: "empty", Name: "Empty"}, nil); !errors.Is(err, ErrEmptyMacro) {
		t.Fatalf("empty macro err=%v", err)
	}
	var missProg MacroProgress
	if err := r.Run(context.Background(), config.Macro{
		ID: "miss", Name: "Miss",
		Steps: []config.MacroStep{{Remote: "Good.ir", Button: "NoSuchBtn"}},
	}, func(p MacroProgress) { missProg = p }); err != nil {
		t.Fatalf("Run miss: %v", err)
	}
	r.Wait()
	if !errors.Is(missProg.Err, ErrButtonNotFound) {
		t.Fatalf("expected ErrButtonNotFound, got %v", missProg.Err)
	}
}
