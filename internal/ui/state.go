package ui

import (
	"ir-studio/internal/config"
	"ir-studio/internal/dbsync"
	"ir-studio/internal/irfile"
	"ir-studio/internal/irml"
	"ir-studio/internal/library"
	"ir-studio/internal/lirc"
)

// Tab identifies one of the 6 IR Studio screens.
type Tab uint8

const (
	TabRX Tab = iota
	TabRemote
	TabProbe
	TabSaved
	TabMacro
	TabSet
	tabCount
)

// PillLabel returns the compact 3-letter header pill label for t.
func (t Tab) PillLabel() string {
	switch t {
	case TabRX:
		return "RX"
	case TabRemote:
		return "REM"
	case TabProbe:
		return "PRB"
	case TabSaved:
		return "SAV"
	case TabMacro:
		return "MAC"
	case TabSet:
		return "SET"
	default:
		return "RX"
	}
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

type asyncEvent struct {
	kind       string
	probeSnap  *library.ProbeSnapshot
	macroProg  *library.MacroProgress
	syncDone   bool
	syncMsg    string
	syncErr    error
}
