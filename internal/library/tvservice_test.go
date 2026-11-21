package library

import (
	"errors"
	"path/filepath"
	"testing"

	"ir-studio/internal/irfile"
)

func TestTVServiceCatalogInvariants(t *testing.T) {
	remotes := TVServiceRemotes()
	if len(remotes) != TVServiceRemoteCount() || len(remotes) < 5 {
		t.Fatalf("unexpected TVServiceRemotes count: %d", len(remotes))
	}
	reg := irfile.DefaultRegistry()
	wantNames := map[string]bool{
		"LG_Service_Discrete":      false,
		"Samsung_Service_Discrete": false,
		"Sony_Discrete":            false,
	}

	for _, r := range remotes {
		if _, tracked := wantNames[r.Name]; tracked {
			wantNames[r.Name] = true
		}
		if !r.ReadOnly || r.SourcePack != VirtualPackTVService {
			t.Fatalf("remote %s must be ReadOnly in %s: %+v", r.Name, VirtualPackTVService, r)
		}
		if len(r.Signals) == 0 || r.ButtonCount != len(r.Signals) {
			t.Fatalf("remote %s has invalid button count", r.Name)
		}
		seenBtn := make(map[string]bool, len(r.Signals))
		for _, sig := range r.Signals {
			if seenBtn[sig.Name] {
				t.Fatalf("remote %s has duplicate button %q", r.Name, sig.Name)
			}
			seenBtn[sig.Name] = true
			pt, err := reg.Encode(sig)
			if err != nil {
				t.Fatalf("Encode(%s:%s): %v", r.Name, sig.Name, err)
			}
			pulses, err := pt.ToLIRCPulses()
			if err != nil || len(pulses)%2 != 1 {
				t.Fatalf("ToLIRCPulses(%s:%s): len=%d err=%v", r.Name, sig.Name, len(pulses), err)
			}
		}
	}
	for name, ok := range wantNames {
		if !ok {
			t.Fatalf("missing expected TV-Service remote %q", name)
		}
	}
}

func TestTVServiceStoreIntegrationAndReadOnly(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(filepath.Join(dir, "remotes"), filepath.Join(dir, "databases"))

	packs, err := store.ListDatabasePacks()
	if err != nil {
		t.Fatalf("ListDatabasePacks: %v", err)
	}
	found := false
	for _, p := range packs {
		if p.ID == VirtualPackTVService && p.Virtual && p.FileCount == TVServiceRemoteCount() {
			found = true
		}
	}
	if !found {
		t.Fatalf("GEN:TV-Service missing from ListDatabasePacks: %+v", packs)
	}

	remotes, err := store.ListPackRemotes(VirtualPackTVService)
	if err != nil || len(remotes) != TVServiceRemoteCount() {
		t.Fatalf("ListPackRemotes(%s) = (%d, %v)", VirtualPackTVService, len(remotes), err)
	}

	loaded, err := store.LoadRemote(RemoteEntry{
		Name:       "LG_Service_Discrete",
		RelPath:    "LG_Service_Discrete.ir",
		SourcePack: VirtualPackTVService,
		ReadOnly:   true,
	})
	if err != nil || len(loaded.Signals) < 9 {
		t.Fatalf("LoadRemote LG_Service_Discrete failed: %+v err=%v", loaded, err)
	}

	promoted, err := store.PromoteToUser(loaded, "")
	if err != nil {
		t.Fatalf("PromoteToUser: %v", err)
	}
	if promoted.ReadOnly || promoted.RelPath != "LG_Service_Discrete.ir" || promoted.ButtonCount != len(loaded.Signals) {
		t.Fatalf("unexpected promoted entry: %+v", promoted)
	}

	if err := store.DeleteDatabaseRemote(VirtualPackTVService, "LG_Service_Discrete.ir"); !errors.Is(err, ErrReadOnlyPack) {
		t.Fatalf("DeleteDatabaseRemote got %v, want ErrReadOnlyPack", err)
	}
}
