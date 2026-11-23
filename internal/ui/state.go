package ui

import (
	"cmp"
	"context"
	"slices"
	"strings"

	"ir-studio/internal/config"
	"ir-studio/internal/dbsync"
	"ir-studio/internal/irfile"
	"ir-studio/internal/irml"
	"ir-studio/internal/library"
	"ir-studio/internal/lirc"
)

// Tab identifies one of the IR Studio screens.
type Tab uint8

const (
	TabHome Tab = iota
	TabRead
	TabSaved
	TabRemote
	TabProbe
	TabAC
	TabRX
	TabMacro
	TabSet
	tabCount
)

// PillLabel returns a compact short label for t.
func (t Tab) PillLabel() string {
	switch t {
	case TabHome:
		return "HOME"
	case TabRead:
		return "READ"
	case TabSaved:
		return "MY"
	case TabRemote:
		return "REM"
	case TabProbe:
		return "OFF"
	case TabAC:
		return "AC"
	case TabRX:
		return "LAB"
	case TabMacro:
		return "MAC"
	case TabSet:
		return "SET"
	default:
		return "HOME"
	}
}

// Title returns the human-readable breadcrumb header title for t.
func (t Tab) Title() string {
	switch t {
	case TabHome:
		return "IR STUDIO"
	case TabRead:
		return "< READ & REPLAY"
	case TabSaved:
		return "< MY REMOTES"
	case TabRemote:
		return "< REMOTE"
	case TabProbe:
		return "< UNIVERSAL OFF"
	case TabAC:
		return "< SMART AC"
	case TabRX:
		return "< IR LAB"
	case TabMacro:
		return "< MACROS"
	case TabSet:
		return "< SETTINGS"
	default:
		return "IR STUDIO"
	}
}

// HomeState holds main menu cursor, scroll offset, and exit confirmation prompt.
type HomeState struct {
	Cursor      int
	Scroll      int
	ConfirmExit bool
}

// SweepState tracks an active asynchronous multi-carrier TX sweep.
type SweepState struct {
	Active  bool
	StepIdx int
	Total   int
	FreqHz  uint32
	Cancel  context.CancelFunc
}

// ChromeState holds shared header, modal, toast, and device indicator state.
type ChromeState struct {
	Tab         Tab
	HelpOpen    bool
	HelpPage    int
	ToastText   string
	ToastCol    uint16
	ToastTicks  int
	SimMode     bool
	DeviceLabel string
	Ambient     irml.AmbientSnapshot
	TickCount   uint64
	Sweep       SweepState
}

// ACDiffState holds AC State-Frame Inspector baseline/current diff state.
type ACDiffState struct {
	Active   bool
	HasBase  bool
	Baseline irml.ACFrame
	Current  irml.ACFrame
	Diff     irml.ACDiffResult
}

// RXState holds live IR capture, denoising, ML classification, and k-NN matches.
type RXState struct {
	Learning     bool
	DenoiseOn    bool
	DenoiseKind  string
	LastCap      *lirc.RXCapture
	Cleaned      irfile.PulseTrain
	Clusters     irml.PulseClusters
	Class        irml.Classification
	Matches      []irml.MatchResult
	MatchCursor  int
	GuardPending bool
	AC           ACDiffState
	CarrierIdx   int
}

// ACControlState holds the interactive Smart AC State-Frame Controller sub-view.
type ACControlState struct {
	Active   bool
	BrandIdx int
	ModeIdx  int
	FanIdx   int
	TempC    int
	Power    bool
	Swing    bool
	Reg      *irfile.ACRegistry
}

// RemoteState holds the Virtual Remote Deck view state for the active .ir file.
type RemoteState struct {
	Entry        library.RemoteEntry
	Loaded       bool
	Cursor       int
	Scroll       int
	TXActive     bool
	LastTXBtn    string
	ConfirmDel   bool
	LearnPending bool
	FileIdx      int
	RepeatCount  int
	AC           ACControlState
}

// ProbeState holds the Universal Power-Off / Action Probe UI state.
type ProbeState struct {
	Session     *library.ProbeSession
	Snap        library.ProbeSnapshot
	Packs       []string
	PackIdx     int
	ActionIdx   int
	IntervalIdx int
	SmartRanked bool
	Scroll      int
	Candidates  []library.ProbeCandidate
}

// SavedState holds the dual-scope USER vs DB library browser state.
type SavedState struct {
	ScopeUser    bool
	UserEntries  []library.RemoteEntry
	DBPacks      []library.DatabasePack
	DBPackIdx    int
	DBEntries    []library.RemoteEntry
	Cursor       int
	Scroll       int
	Selected     map[string]bool
	ConfirmDel   bool
	ConfirmPurge bool
}

// MacroState holds multi-step IR macro list, editor, and execution progress.
type MacroState struct {
	File       *config.MacroFile
	Cursor     int
	StepCursor int
	EditMode   bool
	Running    bool
	Progress   library.MacroProgress
	ConfirmDel bool
	MacrosPath string
}

// SettingsState holds persisted app preferences and Manage DB Sources sub-view.
type SettingsState struct {
	Settings       config.Settings
	ConfigPath     string
	SourcesPath    string
	Cursor         int
	SubViewSources bool
	Sources        []dbsync.DatabaseSource
	SrcCursor      int
	Syncing        bool
	SyncStatus     string
	ConfirmDelPack bool
}

type sweepAsync struct {
	stepIdx   int
	total     int
	freqHz    uint32
	done      bool
	cancelled bool
	err       error
}

type asyncEvent struct {
	kind      string
	probeSnap *library.ProbeSnapshot
	macroProg *library.MacroProgress
	sweep     *sweepAsync
	fpIndex   *irml.IRFingerprintIndex
	syncDone  bool
	syncMsg   string
	syncErr   error
}

func sortRemoteButtons(sigs []irfile.IRSignal) []irfile.IRSignal {
	out := slices.Clone(sigs)
	slices.SortStableFunc(out, func(a, b irfile.IRSignal) int {
		return cmp.Compare(buttonPriority(a.Name), buttonPriority(b.Name))
	})
	return out
}

func buttonPriority(name string) int {
	n := strings.ToLower(strings.TrimSpace(name))
	switch {
	case strings.HasPrefix(n, "power") || n == "pwr":
		return 0
	case strings.Contains(n, "mute"):
		return 1
	case strings.Contains(n, "vol_up") || n == "vol+":
		return 2
	case strings.Contains(n, "vol_dn") || n == "vol-" || strings.Contains(n, "vol_down"):
		return 3
	case strings.Contains(n, "ch_next") || n == "ch+":
		return 4
	case strings.Contains(n, "ch_prev") || n == "ch-":
		return 5
	default:
		return 10
	}
}
