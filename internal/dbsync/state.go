package dbsync

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ir-studio/internal/irfile"
)

const (
	// SyncStateFilename is the per-pack incremental sync metadata file.
	SyncStateFilename = ".sync-state.json"

	// MaxIRFileBytes is the SSOT 1 MiB cap for downloaded .ir payloads.
	MaxIRFileBytes = irfile.MaxIRFileBytes

	packDirPerm  os.FileMode = 0o755
	packFilePerm os.FileMode = 0o644
)

var (
	ErrFileTooLarge       = errors.New("dbsync: downloaded file exceeds 1 MiB limit")
	ErrHashMismatch       = errors.New("dbsync: sha256 checksum mismatch")
	ErrPathTraversal      = errors.New("dbsync: unsafe filename or path traversal")
	ErrRateLimited        = errors.New("dbsync: remote rate limit exceeded")
	ErrSyncInProgress     = errors.New("dbsync: sync already in progress")
	ErrInvalidIRPayload   = errors.New("dbsync: downloaded payload failed .ir validation")
)

// SyncState stores ETag, per-file hashes, ignored files, and last sync time in .sync-state.json.
type SyncState struct {
	ETag     string            `json:"etag,omitempty"`
	Files    map[string]string `json:"files"`
	Ignored  map[string]bool   `json:"ignored,omitempty"`
	LastSync time.Time         `json:"last_sync,omitempty"`
}

// SyncResult summarizes one pack sync run.
type SyncResult struct {
	PackID     string
	Status     string
	Downloaded int
	Skipped    int
	Pruned     int
	Errors     []string
}

// NewSyncState returns an initialized SyncState with non-nil maps.
func NewSyncState() *SyncState {
	return &SyncState{
		Files:   make(map[string]string),
		Ignored: make(map[string]bool),
	}
}

// LoadSyncState reads <packDir>/.sync-state.json. Missing files return a fresh SyncState.
func LoadSyncState(packDir string) (*SyncState, error) {
	path := filepath.Join(packDir, SyncStateFilename)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return NewSyncState(), nil
		}
		return nil, fmt.Errorf("dbsync: read state %s: %w", path, err)
	}
	st := NewSyncState()
	if err := json.Unmarshal(data, st); err != nil {
		return nil, fmt.Errorf("dbsync: parse state %s: %w", path, err)
	}
	if st.Files == nil {
		st.Files = make(map[string]string)
	}
	if st.Ignored == nil {
		st.Ignored = make(map[string]bool)
	}
	return st, nil
}

// SaveSyncState writes st atomically to <packDir>/.sync-state.json.
func SaveSyncState(packDir string, st *SyncState) error {
	if st == nil {
		st = NewSyncState()
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("dbsync: marshal state: %w", err)
	}
	data = append(data, '\n')
	return writeFileAtomic(filepath.Join(packDir, SyncStateFilename), data, packDirPerm, packFilePerm)
}

// SanitizePackFilename validates a remote relative path and flattens nested
// subpaths (e.g. "Daikin/ARC480.ir" -> "Daikin__ARC480.ir") into a flat,
// sandbox-safe .ir filename.
func SanitizePackFilename(rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" || strings.HasPrefix(rel, "/") || strings.ContainsAny(rel, "\\\x00") {
		return "", fmt.Errorf("%q: %w", rel, ErrPathTraversal)
	}
	parts := strings.Split(rel, "/")
	cleaned := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" || p == "." || p == ".." || strings.Contains(p, "..") {
			return "", fmt.Errorf("%q: %w", rel, ErrPathTraversal)
		}
		cleaned = append(cleaned, p)
	}
	flat := strings.Join(cleaned, "__")
	if !strings.EqualFold(filepath.Ext(flat), ".ir") {
		return "", fmt.Errorf("%q not an .ir file: %w", rel, ErrPathTraversal)
	}
	if strings.HasPrefix(flat, ".") {
		return "", fmt.Errorf("%q hidden filename rejected: %w", rel, ErrPathTraversal)
	}
	return flat, nil
}

func resolvePackDir(databasesDir, packID string) (string, error) {
	if err := ValidateSourceID(packID); err != nil {
		return "", err
	}
	absBase, err := filepath.Abs(databasesDir)
	if err != nil {
		return "", err
	}
	target, err := filepath.Abs(filepath.Join(absBase, packID))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absBase, target)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("pack %q escapes %s: %w", packID, databasesDir, ErrPathTraversal)
	}
	return target, nil
}

func writeFileAtomic(path string, data []byte, dirPerm, filePerm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("dbsync: mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".dbsync-*.tmp")
	if err != nil {
		return fmt.Errorf("dbsync: create temp: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(filePerm); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}
