package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"ir-studio/internal/dbsync"
)

func (s *Server) toolListSources(_ context.Context, _ json.RawMessage) (toolResult, *rpcError) {
	sources, err := s.loadSources()
	if err != nil {
		return errResult("load sources: " + err.Error()), nil
	}
	items := make([]map[string]any, 0, len(sources))
	for _, src := range sources {
		lastSynced := ""
		packDir := filepath.Join(s.store.DatabasesDir, src.ID)
		if st, err := dbsync.LoadSyncState(packDir); err == nil && !st.LastSync.IsZero() {
			lastSynced = st.LastSync.UTC().Format(time.RFC3339)
		}
		urlVal := src.URL
		if urlVal == "" && src.Repo != "" {
			urlVal = "https://github.com/" + src.Repo
		}
		items = append(items, map[string]any{
			"id":          src.ID,
			"kind":        string(src.Kind),
			"url":         urlVal,
			"branch":      src.Branch,
			"subpath":     src.PathPrefix,
			"enabled":     src.Enabled,
			"prune":       src.Prune,
			"last_synced": lastSynced,
		})
	}
	return okJSON(map[string]any{"sources": items}), nil
}

func (s *Server) toolAddSource(_ context.Context, raw json.RawMessage) (toolResult, *rpcError) {
	var args struct {
		ID      string `json:"id"`
		Kind    string `json:"kind"`
		URL     string `json:"url"`
		Branch  string `json:"branch"`
		Subpath string `json:"subpath"`
		Prune   bool   `json:"prune"`
	}
	if rpcErr := decodeArgs(raw, &args); rpcErr != nil {
		return toolResult{}, rpcErr
	}
	if err := dbsync.ValidateSourceID(args.ID); err != nil {
		return errResult(err.Error()), nil
	}
	if err := ValidateHTTPSURL(args.URL); err != nil {
		return errResult(err.Error()), nil
	}
	src, err := buildDatabaseSource(args.ID, args.Kind, args.URL, args.Branch, args.Subpath, args.Prune)
	if err != nil {
		return errResult(err.Error()), nil
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if _, err := dbsync.UpsertSource(s.resolvedSourcesPath(), src); err != nil {
		return errResult("add source: " + err.Error()), nil
	}
	return okJSON(map[string]any{
		"added":  true,
		"source": src,
	}), nil
}

func buildDatabaseSource(id, kindRaw, rawURL, branch, subpath string, prune bool) (dbsync.DatabaseSource, error) {
	src := dbsync.DatabaseSource{
		ID:         id,
		Enabled:    true,
		Prune:      prune,
		Branch:     strings.TrimSpace(branch),
		PathPrefix: strings.TrimSpace(subpath),
	}
	switch strings.ToLower(strings.TrimSpace(kindRaw)) {
	case "github_tree", "github":
		src.Kind = dbsync.SourceGitHubTree
		repo, parsedBranch, parsedPrefix, err := dbsync.ParseGitHubURL(rawURL)
		if err != nil {
			return src, err
		}
		src.Repo = repo
		if src.Branch == "" {
			src.Branch = parsedBranch
		}
		if src.PathPrefix == "" {
			src.PathPrefix = parsedPrefix
		}
	case "json_manifest", "manifest":
		src.Kind = dbsync.SourceJSONManifest
		src.URL = strings.TrimSpace(rawURL)
	default:
		return src, fmt.Errorf("unsupported source kind %q", kindRaw)
	}
	if err := src.NormalizeAndValidate(); err != nil {
		return src, err
	}
	return src, nil
}

func (s *Server) toolRemoveSource(_ context.Context, raw json.RawMessage) (toolResult, *rpcError) {
	var args struct {
		IDs         []string `json:"ids"`
		DeleteFiles bool     `json:"delete_files"`
		Confirm     bool     `json:"confirm"`
	}
	if rpcErr := decodeArgs(raw, &args); rpcErr != nil {
		return toolResult{}, rpcErr
	}
	if !args.Confirm {
		return errResult("confirm: true is required to remove sources"), nil
	}
	if len(args.IDs) == 0 {
		return errResult("ids must be a non-empty list"), nil
	}
	for _, id := range args.IDs {
		if err := ValidateSafeName(id); err != nil {
			return errResult(err.Error()), nil
		}
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	existing, err := s.loadSources()
	if err != nil {
		return errResult("load sources: " + err.Error()), nil
	}
	existingSet := make(map[string]bool, len(existing))
	for _, src := range existing {
		existingSet[src.ID] = true
	}

	removed := make([]string, 0, len(args.IDs))
	notFound := make([]string, 0)
	deletedPacks := make([]string, 0)

	for _, id := range args.IDs {
		if !existingSet[id] {
			notFound = append(notFound, id)
			continue
		}
		if _, err := dbsync.RemoveSource(s.resolvedSourcesPath(), id); err != nil {
			return errResult("remove source: " + err.Error()), nil
		}
		delete(existingSet, id)
		removed = append(removed, id)
		if args.DeleteFiles && s.syncer != nil {
			if err := s.syncer.DeletePackDir(id); err == nil {
				deletedPacks = append(deletedPacks, id)
			}
		}
	}

	return okJSON(map[string]any{
		"removed_ids":         removed,
		"not_found_ids":       notFound,
		"files_deleted_packs": deletedPacks,
	}), nil
}

func (s *Server) toolSyncSources(ctx context.Context, raw json.RawMessage) (toolResult, *rpcError) {
	var args struct {
		IDs []string `json:"ids"`
	}
	if rpcErr := decodeArgs(raw, &args); rpcErr != nil {
		return toolResult{}, rpcErr
	}
	sources, err := s.loadSources()
	if err != nil {
		return errResult("load sources: " + err.Error()), nil
	}
	selected, err := selectSyncSources(sources, args.IDs)
	if err != nil {
		return errResult(err.Error()), nil
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	results, err := s.syncer.SyncAll(ctx, selected)
	if err != nil {
		return errResult("sync sources: " + err.Error()), nil
	}
	out := make([]map[string]any, 0, len(results))
	for _, r := range results {
		out = append(out, map[string]any{
			"id":      r.PackID,
			"status":  r.Status,
			"added":   r.Downloaded,
			"updated": 0,
			"skipped": r.Skipped,
			"pruned":  r.Pruned,
			"errors":  r.Errors,
		})
	}
	return okJSON(map[string]any{"results": out}), nil
}

func selectSyncSources(all []dbsync.DatabaseSource, ids []string) ([]dbsync.DatabaseSource, error) {
	if len(ids) == 0 {
		out := make([]dbsync.DatabaseSource, 0, len(all))
		for _, src := range all {
			if src.Enabled {
				out = append(out, src)
			}
		}
		return out, nil
	}
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		if err := ValidateSafeName(id); err != nil {
			return nil, err
		}
		want[id] = true
	}
	out := make([]dbsync.DatabaseSource, 0, len(ids))
	for _, src := range all {
		if want[src.ID] {
			src.Enabled = true
			out = append(out, src)
		}
	}
	return out, nil
}

func (s *Server) toolDeletePackFiles(_ context.Context, raw json.RawMessage) (toolResult, *rpcError) {
	var args struct {
		Pack    string   `json:"pack"`
		Files   []string `json:"files"`
		Confirm bool     `json:"confirm"`
	}
	if rpcErr := decodeArgs(raw, &args); rpcErr != nil {
		return toolResult{}, rpcErr
	}
	if !args.Confirm {
		return errResult("confirm: true is required to delete pack files"), nil
	}
	if len(args.Files) == 0 {
		return errResult("files must be a non-empty list"), nil
	}
	packDir, err := s.resolvePackDir(args.Pack)
	if err != nil {
		return errResult(err.Error()), nil
	}
	for _, f := range args.Files {
		if _, err := ResolveSandboxedPath(packDir, f); err != nil {
			return errResult(err.Error()), nil
		}
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	deleted := make([]string, 0, len(args.Files))
	notFound := make([]string, 0)
	for _, f := range args.Files {
		if !packFileExists(packDir, f) {
			notFound = append(notFound, f)
			continue
		}
		if err := s.store.DeleteDatabaseRemote(args.Pack, f); err != nil {
			return errResult("delete pack file: " + err.Error()), nil
		}
		if s.syncer != nil {
			_ = s.syncer.MarkIgnored(args.Pack, f)
		}
		deleted = append(deleted, f)
	}
	return okJSON(map[string]any{
		"pack":      args.Pack,
		"deleted":   deleted,
		"not_found": notFound,
	}), nil
}

func (s *Server) resolvedSourcesPath() string {
	if s.sourcesPath != "" {
		return s.sourcesPath
	}
	p, _ := dbsync.SourcesPath()
	return p
}

func (s *Server) loadSources() ([]dbsync.DatabaseSource, error) {
	return dbsync.LoadSources(s.resolvedSourcesPath())
}
