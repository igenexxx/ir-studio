package library

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"ir-studio/internal/irfile"
)

func TestTVBGoneCatalogInvariants(t *testing.T) {
	entries := TVBGoneEntries()
	if len(entries) != TVBGoneCodeCount {
		t.Fatalf("len(TVBGoneEntries()) = %d, want %d", len(entries), TVBGoneCodeCount)
	}

	reg := irfile.DefaultRegistry()
	seenKeys := make(map[string]int, len(entries))
	cats := make(map[TVBGoneCategory]int)

	for i, e := range entries {
		if e.Brand == "" {
			t.Fatalf("entry[%d]: empty brand", i)
		}
		if e.Signal.Name != "Power" || e.Signal.Kind != irfile.SignalParsed {
			t.Fatalf("entry[%d] (%s): invalid signal metadata: %+v", i, e.Brand, e.Signal)
		}
		key := fmt.Sprintf("%s|%x|%x", e.Signal.Protocol, e.Signal.Address, e.Signal.Command)
		if prev, dup := seenKeys[key]; dup {
			t.Fatalf("entry[%d] (%s) duplicates entry[%d]: %s", i, e.Brand, prev, key)
		}
		seenKeys[key] = i
		cats[e.Category]++

		train, err := reg.Encode(e.Signal)
		if err != nil {
			t.Fatalf("entry[%d] (%s %s): Encode failed: %v", i, e.Brand, e.Signal.Protocol, err)
		}
		pulses, err := train.ToLIRCPulses()
		if err != nil {
			t.Fatalf("entry[%d] (%s %s): ToLIRCPulses failed: %v", i, e.Brand, e.Signal.Protocol, err)
		}
		if len(pulses)%2 != 1 {
			t.Fatalf("entry[%d] (%s): expected odd pulse count, got %d", i, e.Brand, len(pulses))
		}
		spec := e.Signal.CarrierSpec()
		if spec.FreqHz < 36000 || spec.FreqHz > 40000 {
			t.Fatalf("entry[%d] (%s): unexpected carrier %d Hz", i, e.Brand, spec.FreqHz)
		}
	}

	for _, cat := range []TVBGoneCategory{CategoryTV, CategoryProjector, CategorySoundbar} {
		if cats[cat] == 0 {
			t.Fatalf("category %q has 0 entries", cat)
		}
	}
}

func TestTVBGoneStoreIntegrationAndReadOnlyProtection(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(filepath.Join(dir, "remotes"), filepath.Join(dir, "databases"))

	// Zero-network first boot: ListDatabasePacks still returns GEN:TV-B-Gone and GEN:TV-Service.
	packs, err := store.ListDatabasePacks()
	if err != nil {
		t.Fatalf("ListDatabasePacks: %v", err)
	}
	if len(packs) != 2 || packs[0].ID != VirtualPackTVBGone || !packs[0].Virtual || packs[0].FileCount != TVBGoneCodeCount || packs[1].ID != VirtualPackTVService {
		t.Fatalf("unexpected virtual pack list: %+v", packs)
	}

	// ListPackRemotes + FilterPackCandidates with zero disk files.
	remotes, err := store.ListPackRemotes(VirtualPackTVBGone)
	if err != nil {
		t.Fatalf("ListPackRemotes: %v", err)
	}
	if len(remotes) != TVBGoneCodeCount {
		t.Fatalf("len(remotes) = %d, want %d", len(remotes), TVBGoneCodeCount)
	}

	cands, skipped, err := FilterPackCandidates(store, VirtualPackTVBGone, ActionPower)
	if err != nil {
		t.Fatalf("FilterPackCandidates: %v", err)
	}
	if skipped != 0 || len(cands) != TVBGoneCodeCount {
		t.Fatalf("FilterPackCandidates got %d (skipped=%d), want %d", len(cands), skipped, TVBGoneCodeCount)
	}

	// LoadRemote returns virtual entry without disk I/O.
	loaded, err := store.LoadRemote(remotes[0])
	if err != nil || !loaded.ReadOnly || len(loaded.Signals) != 1 {
		t.Fatalf("LoadRemote virtual failed: entry=%+v err=%v", loaded, err)
	}

	// PromoteToUser twice exercises collision-safe naming (_2.ir).
	p1, err := store.PromoteToUser(remotes[0], "")
	if err != nil {
		t.Fatalf("PromoteToUser #1: %v", err)
	}
	p2, err := store.PromoteToUser(remotes[0], "")
	if err != nil {
		t.Fatalf("PromoteToUser #2: %v", err)
	}
	if p1.RelPath == p2.RelPath || p2.RelPath != "Samsung_TV_2.ir" {
		t.Fatalf("expected collision-safe promotion Samsung_TV_2.ir, got p1=%q p2=%q", p1.RelPath, p2.RelPath)
	}

	// DeleteDatabaseRemote MUST reject virtual pack with ErrReadOnlyPack.
	if err := store.DeleteDatabaseRemote(VirtualPackTVBGone, remotes[0].RelPath); !errors.Is(err, ErrReadOnlyPack) {
		t.Fatalf("DeleteDatabaseRemote virtual got %v, want ErrReadOnlyPack", err)
	}

	// Sandbox check: colon prefix must NOT allow path traversal.
	for _, bad := range []string{"GEN:../etc", "GEN:../../passwd", "GEN:unknown"} {
		if IsVirtualPack(bad) {
			t.Fatalf("IsVirtualPack(%q) must be false", bad)
		}
		if _, err := store.ListPackRemotes(bad); err == nil {
			t.Fatalf("ListPackRemotes(%q) expected error, got nil", bad)
		}
	}
}
