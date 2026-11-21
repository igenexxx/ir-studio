package library

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"ir-studio/internal/irfile"
	"ir-studio/internal/irml"
)

const (
	// MaxRemoteFileBytes is the SSOT 1 MiB ceiling for any loaded or promoted .ir file.
	MaxRemoteFileBytes = irfile.MaxIRFileBytes

	// MaxPromoteCollisions bounds collision-safe filename suffix searches (_2.._999).
	MaxPromoteCollisions = 999

	dataDirPerm    os.FileMode = 0o755
	remoteFilePerm os.FileMode = 0o644
)

var (
	ErrReadOnlyPack           = errors.New("library: external database pack is read-only")
	ErrPathOutsideSandbox     = errors.New("library: path escapes storage sandbox")
	ErrAlreadyInUserLibrary   = errors.New("library: remote is already in user library")
	ErrNameCollisionExhausted = errors.New("library: collision-safe filename limit reached")
	ErrFileTooLarge           = errors.New("library: remote file exceeds 1 MiB limit")
	ErrNotFound               = errors.New("library: remote not found")
	ErrInvalidName            = errors.New("library: invalid remote or pack name")
)

// RemoteEntry describes a Flipper .ir file in UserLibrary or a DatabasePack.
// Signals remain nil until LoadRemote is called (lazy parsing for 512 MB RAM).
type RemoteEntry struct {
	Name        string
	RelPath     string
	FullPath    string
	SourcePack  string
	ReadOnly    bool
	SizeBytes   int64
	ModTime     time.Time
	ButtonCount int
	Protocols   []string
	Signals     []irfile.IRSignal
}

// DatabasePack describes a read-only external or compiled-in virtual .ir pack.
type DatabasePack struct {
	ID        string
	Dir       string
	FileCount int
	Virtual   bool
}

// Store manages strict storage separation between UserLibrary (RW) and DatabasePacks (RO).
type Store struct {
	UserDir      string
	DatabasesDir string
	registry     *irfile.ProtocolRegistry
}

// NewStore creates a Store with explicit UserDir and DatabasesDir paths.
func NewStore(userDir, databasesDir string) *Store {
	return &Store{
		UserDir:      filepath.Clean(userDir),
		DatabasesDir: filepath.Clean(databasesDir),
		registry:     irfile.DefaultRegistry(),
	}
}

// DefaultBaseDir returns ~/ir-studio.
func DefaultBaseDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("library: resolve home dir: %w", err)
	}
	return filepath.Join(home, "ir-studio"), nil
}

// ListUserRemotes enumerates .ir files in UserDir without parsing pulse arrays.
func (s *Store) ListUserRemotes() ([]RemoteEntry, error) {
	return listIRDir(s.UserDir, "", false)
}

// ListDatabasePacks enumerates compiled-in virtual packs followed by pack
// subdirectories inside DatabasesDir.
func (s *Store) ListDatabasePacks() ([]DatabasePack, error) {
	packs := []DatabasePack{{
		ID:        VirtualPackTVBGone,
		FileCount: TVBGoneCodeCount,
		Virtual:   true,
	}}
	entries, err := os.ReadDir(s.DatabasesDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return packs, nil
		}
		return nil, fmt.Errorf("library: list packs: %w", err)
	}
	diskPacks := make([]DatabasePack, 0, len(entries))
	for _, de := range entries {
		if !de.IsDir() || strings.HasPrefix(de.Name(), ".") {
			continue
		}
		packDir := filepath.Join(s.DatabasesDir, de.Name())
		diskPacks = append(diskPacks, DatabasePack{
			ID:        de.Name(),
			Dir:       packDir,
			FileCount: countIRFiles(packDir),
		})
	}
	slices.SortFunc(diskPacks, func(a, b DatabasePack) int {
		return cmp.Compare(strings.ToLower(a.ID), strings.ToLower(b.ID))
	})
	return append(packs, diskPacks...), nil
}

// ListPackRemotes enumerates .ir files inside DatabasesDir/<pack> lazily,
// or returns embedded entries for compiled-in virtual packs.
func (s *Store) ListPackRemotes(pack string) ([]RemoteEntry, error) {
	if IsVirtualPack(pack) {
		return TVBGoneRemotes(), nil
	}
	if err := validateBareName(pack); err != nil {
		return nil, err
	}
	if strings.HasPrefix(pack, VirtualPackPrefix) {
		return nil, fmt.Errorf("%q: %w", pack, ErrInvalidName)
	}
	packDir, err := sandboxJoin(s.DatabasesDir, pack)
	if err != nil {
		return nil, err
	}
	return listIRDir(packDir, pack, true)
}

// LoadRemote lazily parses entry's .ir file and populates Signals, ButtonCount, and Protocols.
func (s *Store) LoadRemote(entry RemoteEntry) (RemoteEntry, error) {
	if IsVirtualPack(entry.SourcePack) {
		return lookupVirtualRemote(entry)
	}
	root := s.UserDir
	if entry.ReadOnly || entry.SourcePack != "" {
		root = s.DatabasesDir
	}
	safePath, err := verifyInsideRoot(root, entry.FullPath)
	if err != nil {
		return entry, err
	}
	data, err := readBoundedFile(safePath)
	if err != nil {
		return entry, err
	}
	parsed, err := irfile.Parse(bytes.NewReader(data))
	if err != nil {
		return entry, fmt.Errorf("library: parse %s: %w", entry.Name, err)
	}
	entry.Signals = parsed.Signals
	entry.ButtonCount = len(parsed.Signals)
	entry.Protocols = collectProtocols(parsed.Signals)
	entry.SizeBytes = int64(len(data))
	return entry, nil
}

// LoadUserRemote loads a single named remote strictly from UserDir.
func (s *Store) LoadUserRemote(name string) (RemoteEntry, error) {
	norm, err := normalizeUserFilename(name)
	if err != nil {
		return RemoteEntry{}, err
	}
	full, err := sandboxJoin(s.UserDir, norm)
	if err != nil {
		return RemoteEntry{}, err
	}
	info, err := os.Stat(full)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return RemoteEntry{}, fmt.Errorf("%s: %w", norm, ErrNotFound)
		}
		return RemoteEntry{}, err
	}
	return s.LoadRemote(RemoteEntry{
		Name:      strings.TrimSuffix(norm, filepath.Ext(norm)),
		RelPath:   norm,
		FullPath:  full,
		SizeBytes: info.Size(),
		ModTime:   info.ModTime(),
	})
}

// SaveUserRemote atomically writes f to UserDir/<name>.ir with strict path sandboxing.
func (s *Store) SaveUserRemote(name string, f *irfile.IRFile) (RemoteEntry, error) {
	if f == nil {
		return RemoteEntry{}, fmt.Errorf("library: nil IRFile: %w", irfile.ErrInvalidHeader)
	}
	norm, err := normalizeUserFilename(name)
	if err != nil {
		return RemoteEntry{}, err
	}
	if err := os.MkdirAll(s.UserDir, dataDirPerm); err != nil {
		return RemoteEntry{}, fmt.Errorf("library: mkdir user dir: %w", err)
	}
	dst, err := sandboxJoin(s.UserDir, norm)
	if err != nil {
		return RemoteEntry{}, err
	}
	if err := irfile.SaveFileAtomic(dst, f); err != nil {
		return RemoteEntry{}, err
	}
	info, err := os.Stat(dst)
	if err != nil {
		return RemoteEntry{}, err
	}
	return RemoteEntry{
		Name:        strings.TrimSuffix(norm, filepath.Ext(norm)),
		RelPath:     norm,
		FullPath:    dst,
		SizeBytes:   info.Size(),
		ModTime:     info.ModTime(),
		ButtonCount: len(f.Signals),
		Protocols:   collectProtocols(f.Signals),
		Signals:     slices.Clone(f.Signals),
	}, nil
}

// DeleteUserRemote deletes a single .ir file strictly inside UserDir.
func (s *Store) DeleteUserRemote(name string) error {
	if err := rejectExternalTarget(s.DatabasesDir, name); err != nil {
		return err
	}
	norm, err := normalizeUserFilename(name)
	if err != nil {
		return err
	}
	target, err := verifyInsideRoot(s.UserDir, filepath.Join(s.UserDir, norm))
	if err != nil {
		return err
	}
	if err := os.Remove(target); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%s: %w", norm, ErrNotFound)
		}
		return err
	}
	return nil
}

// BatchDeleteUserRemotes deletes multiple user remotes strictly inside UserDir.
func (s *Store) BatchDeleteUserRemotes(names []string) (int, error) {
	deleted := 0
	var errs []error
	for _, name := range names {
		if err := s.DeleteUserRemote(name); err != nil {
			errs = append(errs, err)
			continue
		}
		deleted++
	}
	return deleted, errors.Join(errs...)
}

// DeleteDatabaseRemote removes a single .ir file inside DatabasesDir/<pack>/ when
// explicitly requested by Manage DB Sources / DBSync / PROBE batch deletion.
func (s *Store) DeleteDatabaseRemote(pack, relPath string) error {
	if IsVirtualPack(pack) {
		return ErrReadOnlyPack
	}
	if err := validateBareName(pack); err != nil {
		return err
	}
	if err := validateBareName(relPath); err != nil {
		return err
	}
	packDir, err := sandboxJoin(s.DatabasesDir, pack)
	if err != nil {
		return err
	}
	target, err := verifyInsideRoot(packDir, filepath.Join(packDir, relPath))
	if err != nil {
		return err
	}
	if err := os.Remove(target); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%s/%s: %w", pack, relPath, ErrNotFound)
		}
		return err
	}
	return nil
}

// PurgeNoiseRemotes scans UserDir ONLY using irml.ClassifySignal and removes
// remotes whose signals are all classified as ambient noise.
func (s *Store) PurgeNoiseRemotes() (int, error) {
	entries, err := s.ListUserRemotes()
	if err != nil {
		return 0, err
	}
	purged := 0
	for _, entry := range entries {
		loaded, loadErr := s.LoadRemote(entry)
		if loadErr != nil || len(loaded.Signals) == 0 {
			continue
		}
		if s.isAllNoise(loaded.Signals) {
			if delErr := s.DeleteUserRemote(entry.RelPath); delErr == nil {
				purged++
			}
		}
	}
	return purged, nil
}

func (s *Store) isAllNoise(signals []irfile.IRSignal) bool {
	for _, sig := range signals {
		pulses, carrierHz, ok := s.signalPulses(sig)
		if !ok {
			return false
		}
		if !irml.ClassifySignal(pulses, carrierHz).IsNoise {
			return false
		}
	}
	return true
}

func (s *Store) signalPulses(sig irfile.IRSignal) (irfile.PulseTrain, uint32, bool) {
	carrier := sig.CarrierSpec().FreqHz
	if sig.Kind == irfile.SignalRaw {
		p, err := irfile.FromRawData(sig.RawData)
		if err != nil {
			return nil, 0, false
		}
		return p, carrier, true
	}
	p, err := s.registry.Encode(sig)
	if err != nil {
		return nil, 0, false
	}
	return p, carrier, true
}
