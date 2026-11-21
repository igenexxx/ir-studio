package library

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func validateBareName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("empty name: %w", ErrInvalidName)
	}
	if name == "." || name == ".." || strings.Contains(name, "..") || strings.ContainsAny(name, "/\\\x00") {
		return fmt.Errorf("%q: %w", name, ErrPathOutsideSandbox)
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%q contains control character: %w", name, ErrInvalidName)
		}
	}
	return nil
}

func normalizeUserFilename(name string) (string, error) {
	if err := validateBareName(name); err != nil {
		return "", err
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	if strings.TrimSpace(stem) == "" {
		return "", fmt.Errorf("%q: %w", name, ErrInvalidName)
	}
	if strings.EqualFold(ext, ".ir") || ext == "" {
		return stem + ".ir", nil
	}
	return name + ".ir", nil
}

func sanitizeFilename(raw string) string {
	base := strings.TrimSpace(filepath.Base(raw))
	var b strings.Builder
	b.Grow(len(base))
	for _, r := range base {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9',
			r == '_', r == '-', r == '.':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), ".")
	if out == "" {
		return "remote.ir"
	}
	return out
}

func sandboxJoin(root, bareName string) (string, error) {
	if err := validateBareName(bareName); err != nil {
		return "", err
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absTarget, err := filepath.Abs(filepath.Join(absRoot, bareName))
	if err != nil {
		return "", err
	}
	if !relInside(absRoot, absTarget) {
		return "", fmt.Errorf("%s: %w", bareName, ErrPathOutsideSandbox)
	}
	return absTarget, nil
}

func verifyInsideRoot(root, target string) (string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	if !relInside(absRoot, absTarget) {
		return "", fmt.Errorf("%s: %w", target, ErrPathOutsideSandbox)
	}
	evalRoot := resolvePathBestEffort(absRoot)
	evalTarget := resolvePathBestEffort(absTarget)
	if !relInside(evalRoot, evalTarget) {
		return "", fmt.Errorf("symlink escape %s: %w", target, ErrPathOutsideSandbox)
	}
	return absTarget, nil
}

func resolvePathBestEffort(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	parent := filepath.Dir(p)
	if resolvedParent, err := filepath.EvalSymlinks(parent); err == nil {
		return filepath.Join(resolvedParent, filepath.Base(p))
	}
	return p
}

func relInside(absRoot, absTarget string) bool {
	rel, err := filepath.Rel(absRoot, absTarget)
	if err != nil {
		return false
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return !filepath.IsAbs(rel)
}

func isInsideDir(dir, target string) bool {
	if dir == "" || target == "" {
		return false
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	return relInside(resolvePathBestEffort(absDir), resolvePathBestEffort(absTarget))
}

func rejectExternalTarget(databasesDir, name string) error {
	if databasesDir == "" || name == "" {
		return nil
	}
	if isInsideDir(databasesDir, name) {
		return ErrReadOnlyPack
	}
	if fi, err := os.Lstat(name); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		if target, err := filepath.EvalSymlinks(name); err == nil && isInsideDir(databasesDir, target) {
			return ErrReadOnlyPack
		}
	}
	return nil
}
