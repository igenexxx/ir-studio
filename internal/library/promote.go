package library

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"ir-studio/internal/irfile"
)

// PromoteToUser copies a complete .ir remote from a read-only DatabasePack or
// virtual pack into UserDir without modifying the source pack or overwriting
// existing user remotes.
func (s *Store) PromoteToUser(entry RemoteEntry, dstName string) (RemoteEntry, error) {
	if IsVirtualPack(entry.SourcePack) {
		return s.promoteVirtualToUser(entry, dstName)
	}
	if isInsideDir(s.UserDir, entry.FullPath) {
		return RemoteEntry{}, ErrAlreadyInUserLibrary
	}
	srcPath, err := verifyInsideRoot(s.DatabasesDir, entry.FullPath)
	if err != nil {
		return RemoteEntry{}, err
	}
	data, err := readBoundedFile(srcPath)
	if err != nil {
		return RemoteEntry{}, err
	}
	parsed, err := irfile.Parse(bytes.NewReader(data))
	if err != nil {
		return RemoteEntry{}, fmt.Errorf("library: invalid source remote: %w", err)
	}
	if err := os.MkdirAll(s.UserDir, dataDirPerm); err != nil {
		return RemoteEntry{}, fmt.Errorf("library: mkdir user dir: %w", err)
	}
	baseName := dstName
	if strings.TrimSpace(baseName) == "" {
		baseName = filepath.Base(srcPath)
	}
	finalName, dstPath, err := s.pickCollisionSafeName(baseName)
	if err != nil {
		return RemoteEntry{}, err
	}
	if err := writeBytesAtomic(dstPath, data, remoteFilePerm); err != nil {
		return RemoteEntry{}, err
	}
	return buildPromotedEntry(finalName, dstPath, data, parsed), nil
}

func (s *Store) promoteVirtualToUser(entry RemoteEntry, dstName string) (RemoteEntry, error) {
	loaded, err := lookupVirtualRemote(entry)
	if err != nil {
		return RemoteEntry{}, err
	}
	if len(loaded.Signals) == 0 {
		return RemoteEntry{}, fmt.Errorf("library: empty virtual remote %s: %w", loaded.Name, ErrNotFound)
	}
	if err := os.MkdirAll(s.UserDir, dataDirPerm); err != nil {
		return RemoteEntry{}, fmt.Errorf("library: mkdir user dir: %w", err)
	}
	baseName := strings.TrimSpace(dstName)
	if baseName == "" {
		baseName = loaded.RelPath
		if baseName == "" {
			baseName = loaded.Name + ".ir"
		}
	}
	finalName, _, err := s.pickCollisionSafeName(baseName)
	if err != nil {
		return RemoteEntry{}, err
	}
	return s.SaveUserRemote(finalName, &irfile.IRFile{
		Version: irfile.SupportedVersion,
		Signals: loaded.Signals,
	})
}

// PromoteSignalToUser saves a single winning IRSignal as a new user remote.
func (s *Store) PromoteSignalToUser(sig irfile.IRSignal, dstName string) (RemoteEntry, error) {
	if strings.TrimSpace(dstName) == "" {
		dstName = sig.Name
	}
	if err := os.MkdirAll(s.UserDir, dataDirPerm); err != nil {
		return RemoteEntry{}, fmt.Errorf("library: mkdir user dir: %w", err)
	}
	finalName, _, err := s.pickCollisionSafeName(dstName)
	if err != nil {
		return RemoteEntry{}, err
	}
	return s.SaveUserRemote(finalName, &irfile.IRFile{
		Version: irfile.SupportedVersion,
		Signals: []irfile.IRSignal{sig},
	})
}

func (s *Store) pickCollisionSafeName(rawName string) (string, string, error) {
	clean := sanitizeFilename(rawName)
	norm, err := normalizeUserFilename(clean)
	if err != nil {
		return "", "", err
	}
	stem := strings.TrimSuffix(norm, ".ir")
	for i := 1; i <= MaxPromoteCollisions; i++ {
		cand := norm
		if i > 1 {
			cand = fmt.Sprintf("%s_%d.ir", stem, i)
		}
		dst, joinErr := sandboxJoin(s.UserDir, cand)
		if joinErr != nil {
			return "", "", joinErr
		}
		if _, statErr := os.Stat(dst); errors.Is(statErr, os.ErrNotExist) {
			return cand, dst, nil
		}
	}
	return "", "", ErrNameCollisionExhausted
}

func buildPromotedEntry(name, path string, data []byte, parsed *irfile.IRFile) RemoteEntry {
	return RemoteEntry{
		Name:        strings.TrimSuffix(name, filepath.Ext(name)),
		RelPath:     name,
		FullPath:    path,
		SizeBytes:   int64(len(data)),
		ModTime:     time.Now(),
		ButtonCount: len(parsed.Signals),
		Protocols:   collectProtocols(parsed.Signals),
		Signals:     parsed.Signals,
	}
}

func listIRDir(dir, pack string, readOnly bool) ([]RemoteEntry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []RemoteEntry{}, nil
		}
		return nil, fmt.Errorf("library: read dir %s: %w", dir, err)
	}
	out := make([]RemoteEntry, 0, len(entries))
	for _, de := range entries {
		if de.IsDir() || strings.HasPrefix(de.Name(), ".") || !isIRExt(de.Name()) {
			continue
		}
		info, infoErr := de.Info()
		if infoErr != nil || !info.Mode().IsRegular() {
			continue
		}
		out = append(out, RemoteEntry{
			Name:       strings.TrimSuffix(de.Name(), filepath.Ext(de.Name())),
			RelPath:    de.Name(),
			FullPath:   filepath.Join(dir, de.Name()),
			SourcePack: pack,
			ReadOnly:   readOnly,
			SizeBytes:  info.Size(),
			ModTime:    info.ModTime(),
		})
	}
	slices.SortFunc(out, func(a, b RemoteEntry) int {
		return cmp.Compare(strings.ToLower(a.RelPath), strings.ToLower(b.RelPath))
	})
	return out, nil
}

func countIRFiles(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, de := range entries {
		if !de.IsDir() && !strings.HasPrefix(de.Name(), ".") && isIRExt(de.Name()) {
			n++
		}
	}
	return n
}

func isIRExt(name string) bool {
	return strings.EqualFold(filepath.Ext(name), ".ir")
}

func collectProtocols(signals []irfile.IRSignal) []string {
	seen := make(map[string]struct{}, len(signals))
	out := make([]string, 0, 4)
	for _, sig := range signals {
		p := sig.Protocol
		if p == "" {
			if sig.Kind == irfile.SignalRaw {
				p = "RAW"
			} else {
				continue
			}
		}
		if _, ok := seen[p]; !ok {
			seen[p] = struct{}{}
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}

func readBoundedFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%s: %w", filepath.Base(path), ErrNotFound)
		}
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxRemoteFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxRemoteFileBytes {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), ErrFileTooLarge)
	}
	return data, nil
}

func writeBytesAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ir-promote-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(perm); err != nil {
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
