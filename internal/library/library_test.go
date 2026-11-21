package library

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"ir-studio/internal/irfile"
)

func sampleNECFile() *irfile.IRFile {
	return &irfile.IRFile{
		Version: irfile.SupportedVersion,
		Signals: []irfile.IRSignal{
			{
				Name:     "Power",
				Kind:     irfile.SignalParsed,
				Protocol: "NEC",
				Address:  [4]byte{0x04, 0x00, 0x00, 0x00},
				Command:  [4]byte{0x08, 0x00, 0x00, 0x00},
			},
			{
				Name:     "Mute",
				Kind:     irfile.SignalParsed,
				Protocol: "NEC",
				Address:  [4]byte{0x04, 0x00, 0x00, 0x00},
				Command:  [4]byte{0x09, 0x00, 0x00, 0x00},
			},
			{
				Name:      "RawBtn",
				Kind:      irfile.SignalRaw,
				Protocol:  "RAW",
				Frequency: 38000,
				DutyCycle: 0.33,
				RawData:   []uint32{9000, 4500, 560, 560, 560, 1690, 560, 560, 560},
			},
		},
	}
}

func sampleNoiseFile() *irfile.IRFile {
	// Only 4 pulses (< MinValidSignalPulses=8) -> deterministically classified as Ambient Noise.
	return &irfile.IRFile{
		Version: irfile.SupportedVersion,
		Signals: []irfile.IRSignal{
			{
				Name:      "Glitch",
				Kind:      irfile.SignalRaw,
				Protocol:  "RAW",
				Frequency: 38000,
				DutyCycle: 0.33,
				RawData:   []uint32{120, 400, 90},
			},
		},
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	root := t.TempDir()
	return NewStore(filepath.Join(root, "remotes"), filepath.Join(root, "databases"))
}

func writePackFile(t *testing.T, store *Store, pack, name string, f *irfile.IRFile) string {
	t.Helper()
	dir := filepath.Join(store.DatabasesDir, pack)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir pack: %v", err)
	}
	dst := filepath.Join(dir, name)
	if err := irfile.SaveFileAtomic(dst, f); err != nil {
		t.Fatalf("save pack file: %v", err)
	}
	return dst
}

func TestStoreSeparationAndLazyListing(t *testing.T) {
	store := newTestStore(t)

	// Missing dirs return empty user remotes and the built-in virtual packs.
	if got, err := store.ListUserRemotes(); err != nil || len(got) != 0 {
		t.Fatalf("ListUserRemotes on missing dir = (%v, %v); want empty, nil", got, err)
	}
	if got, err := store.ListDatabasePacks(); err != nil || len(got) != 2 || got[0].ID != VirtualPackTVBGone || got[1].ID != VirtualPackTVService {
		t.Fatalf("ListDatabasePacks on missing dir = (%v, %v); want [GEN:TV-B-Gone GEN:TV-Service], nil", got, err)
	}

	if _, err := store.SaveUserRemote("MyTV", sampleNECFile()); err != nil {
		t.Fatalf("SaveUserRemote: %v", err)
	}
	writePackFile(t, store, "tvs", "Sony_Bravia.ir", sampleNECFile())
	writePackFile(t, store, "acs", "Daikin_ARC.IR", sampleNECFile())

	// Add ignored non-.ir, hidden metadata, and nested subdir in pack.
	if err := os.WriteFile(filepath.Join(store.DatabasesDir, "tvs", ".sync-state.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.DatabasesDir, "tvs", "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(store.DatabasesDir, "tvs", "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}

	userRemotes, err := store.ListUserRemotes()
	if err != nil {
		t.Fatalf("ListUserRemotes: %v", err)
	}
	if len(userRemotes) != 1 || userRemotes[0].RelPath != "MyTV.ir" || userRemotes[0].ReadOnly {
		t.Fatalf("unexpected user remotes: %+v", userRemotes)
	}
	// Verify lazy listing: Signals must be nil before LoadRemote.
	if userRemotes[0].Signals != nil {
		t.Fatalf("expected lazy nil Signals before LoadRemote")
	}

	packs, err := store.ListDatabasePacks()
	if err != nil {
		t.Fatalf("ListDatabasePacks: %v", err)
	}
	if len(packs) != 4 || packs[0].ID != VirtualPackTVBGone || packs[1].ID != VirtualPackTVService || packs[2].ID != "acs" || packs[3].ID != "tvs" || packs[3].FileCount != 1 {
		t.Fatalf("unexpected packs: %+v", packs)
	}

	tvEntries, err := store.ListPackRemotes("tvs")
	if err != nil {
		t.Fatalf("ListPackRemotes: %v", err)
	}
	if len(tvEntries) != 1 || !tvEntries[0].ReadOnly || tvEntries[0].SourcePack != "tvs" || tvEntries[0].Signals != nil {
		t.Fatalf("unexpected tv pack entries: %+v", tvEntries)
	}

	loaded, err := store.LoadRemote(tvEntries[0])
	if err != nil {
		t.Fatalf("LoadRemote: %v", err)
	}
	if loaded.ButtonCount != 3 || !slices.Equal(loaded.Protocols, []string{"NEC", "RAW"}) {
		t.Fatalf("unexpected loaded metadata: count=%d protos=%v", loaded.ButtonCount, loaded.Protocols)
	}
}

func TestSaveUserRemoteAdversarialNames(t *testing.T) {
	store := newTestStore(t)
	valid := sampleNECFile()

	badNames := []struct {
		name    string
		wantErr error
	}{
		{"", ErrInvalidName},
		{"   ", ErrInvalidName},
		{".ir", ErrInvalidName},
		{"..", ErrPathOutsideSandbox},
		{"../databases/tvs/evil.ir", ErrPathOutsideSandbox},
		{"sub/remote.ir", ErrPathOutsideSandbox},
		{`sub\remote.ir`, ErrPathOutsideSandbox},
		{"bad\x00name.ir", ErrPathOutsideSandbox},
		{"bad\nname.ir", ErrInvalidName},
	}
	for _, tc := range badNames {
		t.Run(tc.name, func(t *testing.T) {
			_, err := store.SaveUserRemote(tc.name, valid)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("SaveUserRemote(%q) err=%v, want %v", tc.name, err, tc.wantErr)
			}
		})
	}
	if _, err := store.SaveUserRemote("valid", nil); !errors.Is(err, irfile.ErrInvalidHeader) {
		t.Fatalf("nil IRFile err=%v, want ErrInvalidHeader", err)
	}
}

func TestPromoteToUserAndCollisions(t *testing.T) {
	store := newTestStore(t)
	srcPath := writePackFile(t, store, "acs", "Daikin_ARC480.ir", sampleNECFile())
	srcBytes, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatal(err)
	}

	packEntries, err := store.ListPackRemotes("acs")
	if err != nil || len(packEntries) != 1 {
		t.Fatalf("ListPackRemotes: %v, %v", packEntries, err)
	}

	// Promote 3 times -> Daikin_ARC480.ir, Daikin_ARC480_2.ir, Daikin_ARC480_3.ir.
	wantNames := []string{"Daikin_ARC480.ir", "Daikin_ARC480_2.ir", "Daikin_ARC480_3.ir"}
	for _, want := range wantNames {
		promoted, err := store.PromoteToUser(packEntries[0], "")
		if err != nil {
			t.Fatalf("PromoteToUser(%s): %v", want, err)
		}
		if promoted.RelPath != want || promoted.ButtonCount != 3 || promoted.ReadOnly {
			t.Fatalf("unexpected promoted entry: %+v", promoted)
		}
		dstBytes, err := os.ReadFile(promoted.FullPath)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(srcBytes, dstBytes) {
			t.Fatalf("promoted bytes differ from source for %s", want)
		}
	}

	// Verify source file is completely untouched.
	afterSrc, err := os.ReadFile(srcPath)
	if err != nil || !bytes.Equal(srcBytes, afterSrc) {
		t.Fatalf("source pack file was modified")
	}

	// Promoting an entry already in UserLibrary fails with ErrAlreadyInUserLibrary.
	userEntries, _ := store.ListUserRemotes()
	if _, err := store.PromoteToUser(userEntries[0], ""); !errors.Is(err, ErrAlreadyInUserLibrary) {
		t.Fatalf("expected ErrAlreadyInUserLibrary, got %v", err)
	}

	// Promote single signal helper.
	sigEntry, err := store.PromoteSignalToUser(sampleNECFile().Signals[0], "SinglePower")
	if err != nil || sigEntry.ButtonCount != 1 || sigEntry.RelPath != "SinglePower.ir" {
		t.Fatalf("PromoteSignalToUser: %+v, %v", sigEntry, err)
	}
}

func TestPromoteToUserRejectsCorruptOversizedAndTraversal(t *testing.T) {
	store := newTestStore(t)
	packDir := filepath.Join(store.DatabasesDir, "tvs")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Corrupt .ir file must be rejected and leave zero temp files in UserDir.
	corruptPath := filepath.Join(packDir, "Corrupt.ir")
	if err := os.WriteFile(corruptPath, []byte("<html>404</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PromoteToUser(RemoteEntry{FullPath: corruptPath, ReadOnly: true}, ""); err == nil {
		t.Fatalf("expected error promoting corrupt .ir")
	}
	if entries, _ := os.ReadDir(store.UserDir); len(entries) != 0 {
		t.Fatalf("expected zero leftover files in UserDir, got %d", len(entries))
	}

	// Oversized (> 1 MiB) file must fail with ErrFileTooLarge.
	bigPath := filepath.Join(packDir, "Huge.ir")
	if err := os.WriteFile(bigPath, bytes.Repeat([]byte("A"), MaxRemoteFileBytes+16), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PromoteToUser(RemoteEntry{FullPath: bigPath, ReadOnly: true}, ""); !errors.Is(err, ErrFileTooLarge) {
		t.Fatalf("expected ErrFileTooLarge, got %v", err)
	}

	// Path outside DatabasesDir must fail with ErrPathOutsideSandbox.
	outside := filepath.Join(t.TempDir(), "escape.ir")
	if err := irfile.SaveFileAtomic(outside, sampleNECFile()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PromoteToUser(RemoteEntry{FullPath: outside, ReadOnly: true}, ""); !errors.Is(err, ErrPathOutsideSandbox) {
		t.Fatalf("expected ErrPathOutsideSandbox, got %v", err)
	}
}

func TestDeleteProtectionAndSymlinkEscape(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.SaveUserRemote("KeepMe", sampleNECFile()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveUserRemote("DelA", sampleNECFile()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveUserRemote("DelB", sampleNECFile()); err != nil {
		t.Fatal(err)
	}
	packFile := writePackFile(t, store, "tvs", "Protected.ir", sampleNECFile())

	// DeleteUserRemote refuses direct external pack path.
	if err := store.DeleteUserRemote(packFile); !errors.Is(err, ErrReadOnlyPack) {
		t.Fatalf("DeleteUserRemote(packFile) = %v, want ErrReadOnlyPack", err)
	}
	// DeleteUserRemote refuses traversal.
	if err := store.DeleteUserRemote("../databases/tvs/Protected.ir"); !errors.Is(err, ErrPathOutsideSandbox) {
		t.Fatalf("DeleteUserRemote(traversal) = %v, want ErrPathOutsideSandbox", err)
	}
	// Symlink inside UserDir pointing to external pack must be rejected.
	symPath := filepath.Join(store.UserDir, "EvilLink.ir")
	if err := os.Symlink(packFile, symPath); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteUserRemote("EvilLink.ir"); !errors.Is(err, ErrPathOutsideSandbox) {
		t.Fatalf("DeleteUserRemote(symlink) = %v, want ErrPathOutsideSandbox", err)
	}
	if _, err := os.Stat(packFile); err != nil {
		t.Fatalf("protected pack file disappeared: %v", err)
	}

	// BatchDeleteUserRemotes deletes valid user files and reports errors for invalid ones.
	n, err := store.BatchDeleteUserRemotes([]string{"DelA.ir", "Missing.ir", "DelB.ir", "../databases/tvs/Protected.ir"})
	if n != 2 || err == nil {
		t.Fatalf("BatchDeleteUserRemotes = (%d, %v); want (2, non-nil err)", n, err)
	}

	// Explicit DeleteDatabaseRemote deletes pack file and rejects traversal.
	if err := store.DeleteDatabaseRemote("../remotes", "KeepMe.ir"); !errors.Is(err, ErrPathOutsideSandbox) {
		t.Fatalf("DeleteDatabaseRemote bad pack = %v, want ErrPathOutsideSandbox", err)
	}
	if err := store.DeleteDatabaseRemote("tvs", "../Protected.ir"); !errors.Is(err, ErrPathOutsideSandbox) {
		t.Fatalf("DeleteDatabaseRemote bad relPath = %v, want ErrPathOutsideSandbox", err)
	}
	if err := store.DeleteDatabaseRemote("tvs", "Protected.ir"); err != nil {
		t.Fatalf("DeleteDatabaseRemote valid = %v", err)
	}
	if _, err := os.Stat(packFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected Protected.ir to be deleted, stat err=%v", err)
	}
}

func TestPurgeNoiseRemotesOnlyTouchesUserLibrary(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.SaveUserRemote("GoodTV", sampleNECFile()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveUserRemote("NoiseCapture", sampleNoiseFile()); err != nil {
		t.Fatal(err)
	}
	// Put a noise remote in a DatabasePack — PurgeNoiseRemotes MUST NOT delete it.
	packNoise := writePackFile(t, store, "tvs", "PackNoise.ir", sampleNoiseFile())

	purged, err := store.PurgeNoiseRemotes()
	if err != nil {
		t.Fatalf("PurgeNoiseRemotes: %v", err)
	}
	if purged != 1 {
		t.Fatalf("purged = %d, want 1", purged)
	}

	remaining, err := store.ListUserRemotes()
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].RelPath != "GoodTV.ir" {
		t.Fatalf("unexpected remaining user remotes: %+v", remaining)
	}
	if _, err := os.Stat(packNoise); err != nil {
		t.Fatalf("PurgeNoiseRemotes violated RO invariant on pack file: %v", err)
	}
}

func TestStoreEdgeCasesAndDefaultBaseDir(t *testing.T) {
	if base, err := DefaultBaseDir(); err != nil || !filepath.IsAbs(base) {
		t.Fatalf("DefaultBaseDir() = (%q, %v)", base, err)
	}
	store := newTestStore(t)

	// LoadUserRemote missing and invalid names.
	if _, err := store.LoadUserRemote("Missing.ir"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("LoadUserRemote missing = %v, want ErrNotFound", err)
	}
	if _, err := store.LoadUserRemote("../bad.ir"); !errors.Is(err, ErrPathOutsideSandbox) {
		t.Fatalf("LoadUserRemote traversal = %v, want ErrPathOutsideSandbox", err)
	}
	// ListPackRemotes invalid pack name and missing pack dir.
	if _, err := store.ListPackRemotes("../etc"); !errors.Is(err, ErrPathOutsideSandbox) {
		t.Fatalf("ListPackRemotes traversal = %v", err)
	}
	if got, err := store.ListPackRemotes("nonexistent"); err != nil || len(got) != 0 {
		t.Fatalf("ListPackRemotes missing = (%v, %v)", got, err)
	}
	// DeleteDatabaseRemote missing file.
	if err := store.DeleteDatabaseRemote("tvs", "Nope.ir"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteDatabaseRemote missing = %v, want ErrNotFound", err)
	}
	// PromoteSignalToUser with empty dstName falls back to sig.Name; unusual chars sanitized.
	sig := sampleNECFile().Signals[0]
	if got, err := store.PromoteSignalToUser(sig, ""); err != nil || got.RelPath != "Power.ir" {
		t.Fatalf("PromoteSignalToUser empty dst = (%+v, %v)", got, err)
	}
	if got, err := store.PromoteSignalToUser(sig, "My TV / Bad*Name!"); err != nil || got.RelPath != "BadName.ir" {
		t.Fatalf("PromoteSignalToUser sanitized = (%+v, %v)", got, err)
	}
	// LoadRemote on missing file.
	if _, err := store.LoadRemote(RemoteEntry{FullPath: filepath.Join(store.UserDir, "Ghost.ir")}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("LoadRemote missing = %v, want ErrNotFound", err)
	}
	// SaveUserRemote with extra dot extension appends .ir.
	if got, err := store.SaveUserRemote("my.remote", sampleNECFile()); err != nil || got.RelPath != "my.remote.ir" {
		t.Fatalf("SaveUserRemote non-.ir ext = (%+v, %v)", got, err)
	}
}
