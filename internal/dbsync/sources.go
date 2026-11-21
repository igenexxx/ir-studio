package dbsync

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const (
	configDirPerm  os.FileMode = 0o700
	configFilePerm os.FileMode = 0o600
	maxSourceIDLen             = 32
)

// SourceKind distinguishes GitHub Trees API packs from HTTPS JSON manifest packs.
type SourceKind string

const (
	SourceGitHubTree   SourceKind = "github"
	SourceJSONManifest SourceKind = "manifest"
)

var (
	ErrInvalidSource  = errors.New("dbsync: invalid database source")
	ErrSourceNotFound = errors.New("dbsync: database source not found")
)

// DatabaseSource configures an external remote .ir pack source persisted in sources.json.
type DatabaseSource struct {
	ID         string     `json:"id"`
	Name       string     `json:"name,omitempty"`
	Kind       SourceKind `json:"kind"`
	Repo       string     `json:"repo,omitempty"`
	Branch     string     `json:"branch,omitempty"`
	PathPrefix string     `json:"path_prefix,omitempty"`
	URL        string     `json:"url,omitempty"`
	Enabled    bool       `json:"enabled"`
	Prune      bool       `json:"prune"`
}

// DefaultSources returns curated Flipper-IRDB packs with safe HTTPS defaults.
func DefaultSources() []DatabaseSource {
	return []DatabaseSource{
		{
			ID:         "flipper-tvs",
			Name:       "Flipper-IRDB TVs",
			Kind:       SourceGitHubTree,
			Repo:       "Lucaslhm/Flipper-IRDB",
			Branch:     "main",
			PathPrefix: "TVs",
			Enabled:    true,
			Prune:      false,
		},
		{
			ID:         "flipper-acs",
			Name:       "Flipper-IRDB ACs",
			Kind:       SourceGitHubTree,
			Repo:       "Lucaslhm/Flipper-IRDB",
			Branch:     "main",
			PathPrefix: "ACs",
			Enabled:    true,
			Prune:      false,
		},
		{
			ID:         "flipper-audio",
			Name:       "Flipper-IRDB Audio",
			Kind:       SourceGitHubTree,
			Repo:       "Lucaslhm/Flipper-IRDB",
			Branch:     "main",
			PathPrefix: "Audio_and_Video_Receivers",
			Enabled:    false,
			Prune:      false,
		},
		{
			ID:         "flipper-projectors",
			Name:       "Flipper-IRDB Projectors",
			Kind:       SourceGitHubTree,
			Repo:       "Lucaslhm/Flipper-IRDB",
			Branch:     "main",
			PathPrefix: "Projectors",
			Enabled:    false,
			Prune:      false,
		},
	}
}

// SourcesPath returns $HOME/.config/ir-studio/sources.json.
func SourcesPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("dbsync: resolve home dir: %w", err)
	}
	return filepath.Join(home, ".config", "ir-studio", "sources.json"), nil
}

// ValidateSourceID enforces ^[a-z0-9][a-z0-9_-]{0,31}$ and rejects reserved IDs.
func ValidateSourceID(id string) error {
	if len(id) == 0 || len(id) > maxSourceIDLen {
		return fmt.Errorf("id %q length invalid: %w", id, ErrInvalidSource)
	}
	if id == "user" || id == "root" {
		return fmt.Errorf("id %q is reserved: %w", id, ErrInvalidSource)
	}
	for i := range len(id) {
		c := id[i]
		isLower := c >= 'a' && c <= 'z'
		isDigit := c >= '0' && c <= '9'
		if i == 0 && !isLower && !isDigit {
			return fmt.Errorf("id %q must start with lowercase alphanumeric: %w", id, ErrInvalidSource)
		}
		if !isLower && !isDigit && c != '-' && c != '_' {
			return fmt.Errorf("id %q contains invalid character %q: %w", id, c, ErrInvalidSource)
		}
	}
	return nil
}

// NormalizeAndValidate normalizes src fields and enforces all security invariants.
func (s *DatabaseSource) NormalizeAndValidate() error {
	s.ID = strings.ToLower(strings.TrimSpace(s.ID))
	if err := ValidateSourceID(s.ID); err != nil {
		return err
	}
	s.Name = strings.TrimSpace(s.Name)
	switch s.Kind {
	case SourceGitHubTree:
		return s.validateGitHub()
	case SourceJSONManifest:
		return s.validateManifest()
	default:
		return fmt.Errorf("unsupported kind %q: %w", s.Kind, ErrInvalidSource)
	}
}

func (s *DatabaseSource) validateGitHub() error {
	s.Repo = strings.Trim(strings.TrimSpace(s.Repo), "/")
	owner, repo, ok := strings.Cut(s.Repo, "/")
	if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") ||
		strings.Contains(s.Repo, "..") || strings.ContainsAny(s.Repo, "\\ \t") {
		return fmt.Errorf("invalid github repo %q: %w", s.Repo, ErrInvalidSource)
	}
	s.Branch = strings.TrimSpace(s.Branch)
	if s.Branch == "" {
		s.Branch = "main"
	}
	if strings.Contains(s.Branch, "..") || strings.ContainsAny(s.Branch, "\\\x00") {
		return fmt.Errorf("invalid branch %q: %w", s.Branch, ErrInvalidSource)
	}
	s.PathPrefix = strings.Trim(strings.TrimSpace(s.PathPrefix), "/")
	if strings.Contains(s.PathPrefix, "..") || strings.ContainsAny(s.PathPrefix, "\\\x00") {
		return fmt.Errorf("invalid path_prefix %q: %w", s.PathPrefix, ErrInvalidSource)
	}
	return nil
}

func (s *DatabaseSource) validateManifest() error {
	s.URL = strings.TrimSpace(s.URL)
	return validateSafeRemoteURL(s.URL)
}

func validateSafeRemoteURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("invalid url %q: %w", raw, ErrInvalidSource)
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return nil
	case "http":
		host := strings.ToLower(u.Hostname())
		if host == "127.0.0.1" || host == "localhost" || host == "::1" {
			return nil
		}
		return fmt.Errorf("non-loopback http url %q rejected: %w", raw, ErrInvalidSource)
	default:
		return fmt.Errorf("unsupported scheme %q: %w", u.Scheme, ErrInvalidSource)
	}
}

// ParseGitHubURL parses https://github.com/<owner>/<repo>[/tree/<branch>[/<prefix>]].
func ParseGitHubURL(rawURL string) (repo, branch, pathPrefix string, err error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || !strings.EqualFold(u.Scheme, "https") || !strings.EqualFold(u.Hostname(), "github.com") {
		return "", "", "", fmt.Errorf("not a github.com https url: %w", ErrInvalidSource)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", "", fmt.Errorf("missing owner/repo in url: %w", ErrInvalidSource)
	}
	owner := parts[0]
	repoName := strings.TrimSuffix(parts[1], ".git")
	if repoName == "" {
		return "", "", "", fmt.Errorf("empty repo name: %w", ErrInvalidSource)
	}
	repo = owner + "/" + repoName
	branch = "main"
	if len(parts) >= 4 && parts[2] == "tree" {
		branch = parts[3]
		if len(parts) > 4 {
			pathPrefix = strings.Join(parts[4:], "/")
		}
	}
	if strings.Contains(pathPrefix, "..") || strings.Contains(branch, "..") {
		return "", "", "", fmt.Errorf("traversal in github url: %w", ErrInvalidSource)
	}
	return repo, branch, pathPrefix, nil
}

// LoadSources reads and validates sources.json. Missing files return nil, nil.
func LoadSources(path string) ([]DatabaseSource, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("dbsync: read %s: %w", path, err)
	}
	var sources []DatabaseSource
	if err := json.Unmarshal(data, &sources); err != nil {
		return nil, fmt.Errorf("dbsync: parse %s: %w", path, err)
	}
	if err := validateSourcesSlice(sources); err != nil {
		return nil, err
	}
	return sources, nil
}

// SaveSourcesAtomic validates sources and writes them atomically with 0600 permissions.
func SaveSourcesAtomic(path string, sources []DatabaseSource) error {
	cp := make([]DatabaseSource, len(sources))
	copy(cp, sources)
	if err := validateSourcesSlice(cp); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return fmt.Errorf("dbsync: marshal sources: %w", err)
	}
	data = append(data, '\n')
	return writeFileAtomic(path, data, configDirPerm, configFilePerm)
}

// UpsertSource adds or replaces a source by normalized ID and saves atomically.
func UpsertSource(path string, src DatabaseSource) ([]DatabaseSource, error) {
	if err := src.NormalizeAndValidate(); err != nil {
		return nil, err
	}
	sources, err := LoadSources(path)
	if err != nil {
		return nil, err
	}
	replaced := false
	for i := range sources {
		if sources[i].ID == src.ID {
			sources[i] = src
			replaced = true
			break
		}
	}
	if !replaced {
		sources = append(sources, src)
	}
	if err := SaveSourcesAtomic(path, sources); err != nil {
		return nil, err
	}
	return sources, nil
}

// RemoveSource removes the source with normalized id and saves atomically.
func RemoveSource(path, id string) ([]DatabaseSource, error) {
	normID := strings.ToLower(strings.TrimSpace(id))
	sources, err := LoadSources(path)
	if err != nil {
		return nil, err
	}
	idx := -1
	for i := range sources {
		if sources[i].ID == normID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, fmt.Errorf("%s: %w", normID, ErrSourceNotFound)
	}
	sources = append(sources[:idx], sources[idx+1:]...)
	if err := SaveSourcesAtomic(path, sources); err != nil {
		return nil, err
	}
	return sources, nil
}

func validateSourcesSlice(sources []DatabaseSource) error {
	seen := make(map[string]struct{}, len(sources))
	for i := range sources {
		if err := sources[i].NormalizeAndValidate(); err != nil {
			return err
		}
		if _, dup := seen[sources[i].ID]; dup {
			return fmt.Errorf("duplicate source id %q: %w", sources[i].ID, ErrInvalidSource)
		}
		seen[sources[i].ID] = struct{}{}
	}
	return nil
}
