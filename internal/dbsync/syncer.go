package dbsync

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"ir-studio/internal/irfile"
)

const (
	defaultGitHubAPIURL = "https://api.github.com"
	defaultGitHubRawURL = "https://raw.githubusercontent.com"
	defaultHTTPTimeout  = 30 * time.Second
	maxIndexBytes       = 16 << 20 // 16 MiB cap for GitHub tree / manifest JSON
)

// Syncer performs incremental ETag + SHA delta synchronization into DatabasesDir.
type Syncer struct {
	DatabasesDir string
	Client       *http.Client
	GitHubAPIURL string
	GitHubRawURL string
	mu           sync.Mutex
	syncing      bool
}

// NewSyncer creates a Syncer with optional custom *http.Client.
func NewSyncer(databasesDir string, client *http.Client) *Syncer {
	if client == nil {
		client = &http.Client{Timeout: defaultHTTPTimeout}
	}
	return &Syncer{
		DatabasesDir: filepath.Clean(databasesDir),
		Client:       client,
		GitHubAPIURL: defaultGitHubAPIURL,
		GitHubRawURL: defaultGitHubRawURL,
	}
}

// IsSyncing reports whether a sync operation is currently active.
func (s *Syncer) IsSyncing() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.syncing
}

func (s *Syncer) acquireSync() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.syncing {
		return ErrSyncInProgress
	}
	s.syncing = true
	return nil
}

func (s *Syncer) releaseSync() {
	s.mu.Lock()
	s.syncing = false
	s.mu.Unlock()
}

// SyncAll synchronizes all enabled sources sequentially.
func (s *Syncer) SyncAll(ctx context.Context, sources []DatabaseSource) ([]SyncResult, error) {
	results := make([]SyncResult, 0, len(sources))
	var errs []error
	for _, src := range sources {
		if !src.Enabled {
			continue
		}
		res, err := s.SyncSource(ctx, src)
		results = append(results, res)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", src.ID, err))
			if ctx.Err() != nil {
				break
			}
		}
	}
	return results, errors.Join(errs...)
}

// SyncSource performs an incremental delta sync for a single DatabaseSource.
func (s *Syncer) SyncSource(ctx context.Context, src DatabaseSource) (SyncResult, error) {
	res := SyncResult{PackID: src.ID, Status: "error"}
	if err := src.NormalizeAndValidate(); err != nil {
		return res, err
	}
	res.PackID = src.ID
	if err := s.acquireSync(); err != nil {
		return res, err
	}
	defer s.releaseSync()

	packDir, err := resolvePackDir(s.DatabasesDir, src.ID)
	if err != nil {
		return res, err
	}
	if err := os.MkdirAll(packDir, packDirPerm); err != nil {
		return res, fmt.Errorf("dbsync: mkdir pack %s: %w", src.ID, err)
	}
	state, err := LoadSyncState(packDir)
	if err != nil {
		return res, err
	}
	switch src.Kind {
	case SourceGitHubTree:
		return s.syncGitHubTree(ctx, src, packDir, state)
	case SourceJSONManifest:
		return s.syncJSONManifest(ctx, src, packDir, state)
	default:
		return res, ErrInvalidSource
	}
}

// DeletePackDir removes DatabasesDir/<packID>/ safely after validating packID and sync lock.
func (s *Syncer) DeletePackDir(packID string) error {
	if s.IsSyncing() {
		return ErrSyncInProgress
	}
	target, err := resolvePackDir(s.DatabasesDir, packID)
	if err != nil {
		return err
	}
	return os.RemoveAll(target)
}

// MarkIgnored records relPath in <packID>/.sync-state.json ignored so future syncs skip it.
func (s *Syncer) MarkIgnored(packID, relPath string) error {
	safeName, err := SanitizePackFilename(relPath)
	if err != nil {
		return err
	}
	packDir, err := resolvePackDir(s.DatabasesDir, packID)
	if err != nil {
		return err
	}
	state, err := LoadSyncState(packDir)
	if err != nil {
		return err
	}
	state.Ignored[safeName] = true
	delete(state.Files, safeName)
	return SaveSyncState(packDir, state)
}

type remoteEntry struct {
	safeName  string
	rawURL    string
	hash      string
	verify256 bool
}

func (s *Syncer) applyEntries(
	ctx context.Context,
	src DatabaseSource,
	packDir string,
	state *SyncState,
	newETag string,
	entries []remoteEntry,
	initSkipped int,
	initErrs []string,
) (SyncResult, error) {
	res := SyncResult{PackID: src.ID, Status: "updated", Skipped: initSkipped, Errors: initErrs}
	remoteSet := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		remoteSet[e.safeName] = struct{}{}
	}
	for _, e := range entries {
		if ctx.Err() != nil {
			return s.finishCancelled(packDir, state, res, ctx.Err())
		}
		if s.shouldSkipEntry(packDir, state, e) {
			res.Skipped++
			continue
		}
		if err := s.downloadAndSave(ctx, packDir, state, e); err != nil {
			if ctx.Err() != nil {
				return s.finishCancelled(packDir, state, res, ctx.Err())
			}
			res.Skipped++
			res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", e.safeName, err))
			continue
		}
		res.Downloaded++
	}
	if src.Prune {
		res.Pruned = pruneMissing(packDir, state, remoteSet)
	}
	if newETag != "" {
		state.ETag = newETag
	}
	state.LastSync = time.Now().UTC()
	if err := SaveSyncState(packDir, state); err != nil {
		return res, err
	}
	if res.Downloaded == 0 && res.Pruned == 0 && len(res.Errors) == 0 {
		res.Status = "up-to-date"
	}
	return res, nil
}

func (s *Syncer) finishCancelled(packDir string, state *SyncState, res SyncResult, err error) (SyncResult, error) {
	state.LastSync = time.Now().UTC()
	_ = SaveSyncState(packDir, state)
	res.Status = "cancelled"
	return res, err
}

func (s *Syncer) shouldSkipEntry(packDir string, state *SyncState, e remoteEntry) bool {
	if state.Ignored[e.safeName] {
		return true
	}
	if e.hash != "" && state.Files[e.safeName] == e.hash {
		if _, err := os.Stat(filepath.Join(packDir, e.safeName)); err == nil {
			return true
		}
	}
	return false
}

func (s *Syncer) downloadAndSave(ctx context.Context, packDir string, state *SyncState, e remoteEntry) error {
	data, err := s.fetchBounded(ctx, e.rawURL, MaxIRFileBytes)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	hexSum := hex.EncodeToString(sum[:])
	if e.verify256 && hexSum != e.hash {
		return ErrHashMismatch
	}
	if _, err := irfile.Parse(bytes.NewReader(data)); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidIRPayload, err)
	}
	dst := filepath.Join(packDir, e.safeName)
	if err := writeFileAtomic(dst, data, packDirPerm, packFilePerm); err != nil {
		return err
	}
	storedHash := e.hash
	if storedHash == "" {
		storedHash = hexSum
	}
	state.Files[e.safeName] = storedHash
	return nil
}

func pruneMissing(packDir string, state *SyncState, remoteSet map[string]struct{}) int {
	pruned := 0
	for name := range state.Files {
		if _, exists := remoteSet[name]; exists {
			continue
		}
		if _, err := SanitizePackFilename(name); err != nil {
			continue
		}
		if err := os.Remove(filepath.Join(packDir, name)); err == nil || errors.Is(err, os.ErrNotExist) {
			delete(state.Files, name)
			pruned++
		}
	}
	return pruned
}
