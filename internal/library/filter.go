package library

import (
	"strings"
)

// ProbeAction filters multi-button .ir remotes down to a single target action.
type ProbeAction string

const (
	ActionPower   ProbeAction = "Power"
	ActionOff     ProbeAction = "Off"
	ActionCool    ProbeAction = "Cool"
	ActionHeat    ProbeAction = "Heat"
	ActionMute    ProbeAction = "Mute"
	ActionVolUp   ProbeAction = "Vol+"
	ActionVolDown ProbeAction = "Vol-"
	ActionInput   ProbeAction = "Input"
	ActionAll     ProbeAction = "All"
)

// DefaultProbeActions returns the canonical ordered list of UI-selectable actions.
func DefaultProbeActions() []ProbeAction {
	return []ProbeAction{
		ActionPower,
		ActionOff,
		ActionCool,
		ActionHeat,
		ActionMute,
		ActionVolUp,
		ActionVolDown,
		ActionInput,
		ActionAll,
	}
}

var actionAliases = map[ProbeAction][]string{
	ActionPower:   {"power", "pwr", "power_on", "power_off", "on_off", "power_toggle", "toggle", "pwr_on", "pwr_off", "standby"},
	ActionOff:     {"off", "power_off", "pwr_off", "standby", "turn_off"},
	ActionCool:    {"cool", "mode_cool", "cooling"},
	ActionHeat:    {"heat", "mode_heat", "heating"},
	ActionMute:    {"mute", "muting", "audio_mute"},
	ActionVolUp:   {"vol_up", "volume_up", "volup", "vol+", "volume+"},
	ActionVolDown: {"vol_dn", "vol_down", "voldown", "volume_down", "volume_dn", "vol-", "volume-"},
	ActionInput:   {"input", "source", "hdmi", "hdmi_1", "av", "tv_av"},
}

var actionPrefixes = map[ProbeAction][]string{
	ActionCool: {"cool_"},
	ActionHeat: {"heat_"},
}

// NormalizeButtonAction canonicalizes button names across Flipper-IRDB variants
// (lowercasing, collapsing separators to underscores).
func NormalizeButtonAction(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	prevUnderscore := false
	for _, r := range s {
		switch {
		case r == '-' || r == ' ' || r == '.' || r == '_':
			if !prevUnderscore && b.Len() > 0 {
				b.WriteByte('_')
				prevUnderscore = true
			}
		default:
			b.WriteRune(r)
			prevUnderscore = false
		}
	}
	return strings.TrimRight(b.String(), "_")
}

// MatchesProbeAction reports whether buttonName matches the given ProbeAction.
func MatchesProbeAction(buttonName string, action ProbeAction) bool {
	if action == "" || strings.EqualFold(string(action), string(ActionAll)) {
		return strings.TrimSpace(buttonName) != ""
	}
	norm := NormalizeButtonAction(buttonName)
	if norm == "" {
		return false
	}
	canonical := canonicalAction(action)
	if aliases, ok := actionAliases[canonical]; ok {
		for _, a := range aliases {
			if norm == a {
				return true
			}
		}
		for _, pfx := range actionPrefixes[canonical] {
			if strings.HasPrefix(norm, pfx) {
				return true
			}
		}
		return false
	}
	customNorm := NormalizeButtonAction(string(action))
	return customNorm != "" && (norm == customNorm || strings.Contains(norm, customNorm))
}

func canonicalAction(action ProbeAction) ProbeAction {
	for _, a := range DefaultProbeActions() {
		if strings.EqualFold(string(a), string(action)) {
			return a
		}
	}
	return action
}

// FilterPackCandidates projects a pack (or UserLibrary when pack == "") into
// ProbeCandidates matching action. Deduplicates to 1 primary button per remote
// unless action == ActionAll. Corrupt .ir files are skipped and counted.
func FilterPackCandidates(store *Store, pack string, action ProbeAction) ([]ProbeCandidate, int, error) {
	var entries []RemoteEntry
	var err error
	if pack == "" {
		entries, err = store.ListUserRemotes()
	} else {
		entries, err = store.ListPackRemotes(pack)
	}
	if err != nil {
		return nil, 0, err
	}
	includeAll := action == "" || strings.EqualFold(string(action), string(ActionAll))
	candidates := make([]ProbeCandidate, 0, len(entries))
	skipped := 0
	for _, entry := range entries {
		loaded, loadErr := store.LoadRemote(entry)
		if loadErr != nil {
			skipped++
			continue
		}
		for _, sig := range loaded.Signals {
			if !MatchesProbeAction(sig.Name, action) {
				continue
			}
			candidates = append(candidates, ProbeCandidate{
				Remote: loaded,
				Signal: sig,
				Pack:   pack,
			})
			if !includeAll {
				break
			}
		}
	}
	return candidates, skipped, nil
}
