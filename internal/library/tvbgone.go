package library

import (
	"fmt"
	"slices"

	"ir-studio/internal/irfile"
)

const (
	// VirtualPackPrefix identifies compiled-in, zero-disk virtual packs.
	VirtualPackPrefix = "GEN:"

	// VirtualPackTVBGone is the built-in offline top-100 universal Power pack.
	VirtualPackTVBGone = "GEN:TV-B-Gone"

	// TVBGoneCodeCount is the exact number of embedded universal Power codes.
	TVBGoneCodeCount = 100
)

// TVBGoneCategory groups embedded universal Power codes by device class.
type TVBGoneCategory string

const (
	CategoryTV        TVBGoneCategory = "TV"
	CategoryProjector TVBGoneCategory = "Projector"
	CategorySoundbar  TVBGoneCategory = "Soundbar"
)

// TVBGoneEntry represents a single compiled-in universal Power signal.
type TVBGoneEntry struct {
	Brand    string
	Category TVBGoneCategory
	Signal   irfile.IRSignal
}

// IsVirtualPack reports whether pack is a known compiled-in virtual pack.
// Strict equality prevents path-sandbox bypasses such as "GEN:../etc".
func IsVirtualPack(pack string) bool {
	return pack == VirtualPackTVBGone
}

// TVBGoneEntries returns a copy of all 100 embedded Power codes in deterministic
// field-priority order (major TVs first, then projectors and soundbars).
func TVBGoneEntries() []TVBGoneEntry {
	out := make([]TVBGoneEntry, len(tvbGoneSpecs))
	for i, sp := range tvbGoneSpecs {
		out[i] = TVBGoneEntry{
			Brand:    sp.brand,
			Category: sp.cat,
			Signal:   sp.toSignal(),
		}
	}
	return out
}

// TVBGoneRemotes projects the 100 embedded Power codes as read-only RemoteEntry
// values belonging to VirtualPackTVBGone, requiring zero disk I/O.
func TVBGoneRemotes() []RemoteEntry {
	counts := make(map[string]int, len(tvbGoneSpecs))
	out := make([]RemoteEntry, len(tvbGoneSpecs))
	for i, sp := range tvbGoneSpecs {
		base := fmt.Sprintf("%s_%s", sp.brand, sp.cat)
		counts[base]++
		name := base
		if counts[base] > 1 {
			name = fmt.Sprintf("%s_%d", base, counts[base])
		}
		rel := name + ".ir"
		sig := sp.toSignal()
		out[i] = RemoteEntry{
			Name:        name,
			RelPath:     rel,
			FullPath:    VirtualPackTVBGone + "/" + rel,
			SourcePack:  VirtualPackTVBGone,
			ReadOnly:    true,
			ButtonCount: 1,
			Protocols:   []string{sig.Protocol},
			Signals:     []irfile.IRSignal{sig},
		}
	}
	return out
}

func lookupVirtualRemote(entry RemoteEntry) (RemoteEntry, error) {
	if entry.SourcePack != VirtualPackTVBGone {
		return RemoteEntry{}, fmt.Errorf("%s: %w", entry.SourcePack, ErrNotFound)
	}
	if len(entry.Signals) > 0 {
		cp := entry
		cp.ReadOnly = true
		cp.ButtonCount = len(cp.Signals)
		cp.Protocols = collectProtocols(cp.Signals)
		cp.Signals = slices.Clone(entry.Signals)
		return cp, nil
	}
	for _, r := range TVBGoneRemotes() {
		if r.RelPath == entry.RelPath || r.Name == entry.Name {
			return r, nil
		}
	}
	return RemoteEntry{}, fmt.Errorf("%s: %w", entry.Name, ErrNotFound)
}
