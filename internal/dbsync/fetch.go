package dbsync

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

type githubTreeResp struct {
	Tree []struct {
		Path string `json:"path"`
		Type string `json:"type"`
		SHA  string `json:"sha"`
		Size int64  `json:"size"`
	} `json:"tree"`
}

type manifestDoc struct {
	Version int `json:"version"`
	Files   []struct {
		Name   string `json:"name"`
		URL    string `json:"url"`
		SHA256 string `json:"sha256"`
	} `json:"files"`
}

func (s *Syncer) syncGitHubTree(ctx context.Context, src DatabaseSource, packDir string, state *SyncState) (SyncResult, error) {
	apiBase := strings.TrimRight(s.GitHubAPIURL, "/")
	treeURL := fmt.Sprintf("%s/repos/%s/git/trees/%s?recursive=1",
		apiBase, src.Repo, url.PathEscape(src.Branch))
	body, etag, notModified, err := s.fetchIndex(ctx, treeURL, state.ETag)
	if err != nil {
		return SyncResult{PackID: src.ID, Status: "error"}, err
	}
	if notModified {
		state.LastSync = time.Now().UTC()
		_ = SaveSyncState(packDir, state)
		return SyncResult{PackID: src.ID, Status: "up-to-date"}, nil
	}
	var doc githubTreeResp
	if err := json.Unmarshal(body, &doc); err != nil {
		return SyncResult{PackID: src.ID, Status: "error"}, fmt.Errorf("dbsync: parse github tree: %w", err)
	}
	entries, skipped, errs := s.extractGitHubEntries(src, doc)
	return s.applyEntries(ctx, src, packDir, state, etag, entries, skipped, errs)
}

func (s *Syncer) extractGitHubEntries(src DatabaseSource, doc githubTreeResp) ([]remoteEntry, int, []string) {
	rawBase := strings.TrimRight(s.GitHubRawURL, "/")
	prefix := src.PathPrefix
	if prefix != "" {
		prefix += "/"
	}
	entries := make([]remoteEntry, 0, len(doc.Tree))
	skipped := 0
	var errs []string
	for _, item := range doc.Tree {
		if item.Type != "blob" || !strings.EqualFold(filepath.Ext(item.Path), ".ir") {
			continue
		}
		rel := item.Path
		if prefix != "" {
			if !strings.HasPrefix(rel, prefix) {
				continue
			}
			rel = strings.TrimPrefix(rel, prefix)
		}
		safeName, err := SanitizePackFilename(rel)
		if err != nil {
			skipped++
			errs = append(errs, err.Error())
			continue
		}
		entries = append(entries, remoteEntry{
			safeName: safeName,
			rawURL:   fmt.Sprintf("%s/%s/%s/%s", rawBase, src.Repo, src.Branch, item.Path),
			hash:     item.SHA,
		})
	}
	return entries, skipped, errs
}

func (s *Syncer) syncJSONManifest(ctx context.Context, src DatabaseSource, packDir string, state *SyncState) (SyncResult, error) {
	body, etag, notModified, err := s.fetchIndex(ctx, src.URL, state.ETag)
	if err != nil {
		return SyncResult{PackID: src.ID, Status: "error"}, err
	}
	if notModified {
		state.LastSync = time.Now().UTC()
		_ = SaveSyncState(packDir, state)
		return SyncResult{PackID: src.ID, Status: "up-to-date"}, nil
	}
	var doc manifestDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return SyncResult{PackID: src.ID, Status: "error"}, fmt.Errorf("dbsync: parse manifest: %w", err)
	}
	entries := make([]remoteEntry, 0, len(doc.Files))
	skipped := 0
	var errs []string
	for _, f := range doc.Files {
		safeName, nameErr := SanitizePackFilename(f.Name)
		if nameErr != nil {
			skipped++
			errs = append(errs, nameErr.Error())
			continue
		}
		if urlErr := validateSafeRemoteURL(f.URL); urlErr != nil {
			skipped++
			errs = append(errs, urlErr.Error())
			continue
		}
		entries = append(entries, remoteEntry{
			safeName:  safeName,
			rawURL:    f.URL,
			hash:      strings.ToLower(strings.TrimSpace(f.SHA256)),
			verify256: strings.TrimSpace(f.SHA256) != "",
		})
	}
	return s.applyEntries(ctx, src, packDir, state, etag, entries, skipped, errs)
}

func (s *Syncer) fetchIndex(ctx context.Context, rawURL, etag string) ([]byte, string, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", false, err
	}
	req.Header.Set("User-Agent", "ir-studio/1.0")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := s.Client.Do(req)
	if err != nil {
		return nil, "", false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		return nil, etag, true, nil
	}
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		return nil, "", false, fmt.Errorf("HTTP %d: %w", resp.StatusCode, ErrRateLimited)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", false, fmt.Errorf("dbsync: unexpected HTTP %d for %s", resp.StatusCode, rawURL)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxIndexBytes+1))
	if err != nil {
		return nil, "", false, err
	}
	if len(data) > maxIndexBytes {
		return nil, "", false, fmt.Errorf("index exceeds limit: %w", ErrFileTooLarge)
	}
	return data, resp.Header.Get("ETag"), false, nil
}

func (s *Syncer) fetchBounded(ctx context.Context, rawURL string, maxBytes int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "ir-studio/1.0")
	resp, err := s.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("HTTP %d: %w", resp.StatusCode, ErrRateLimited)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, ErrFileTooLarge
	}
	return data, nil
}
