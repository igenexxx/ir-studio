package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ir-studio/internal/dbsync"
	"ir-studio/internal/irfile"
)

func (s *Server) toolListRemotes(_ context.Context, raw json.RawMessage) (toolResult, *rpcError) {
	var args struct {
		Filter string `json:"filter"`
	}
	if rpcErr := decodeArgs(raw, &args); rpcErr != nil {
		return toolResult{}, rpcErr
	}
	remotes, err := s.store.ListUserRemotes()
	if err != nil {
		return errResult("list remotes: " + err.Error()), nil
	}
	filter := strings.ToLower(strings.TrimSpace(args.Filter))
	items := make([]map[string]any, 0, len(remotes))
	for _, r := range remotes {
		if filter != "" && !strings.Contains(strings.ToLower(r.Name), filter) {
			continue
		}
		loaded, loadErr := s.store.LoadRemote(r)
		btnCount := r.ButtonCount
		protos := r.Protocols
		if loadErr == nil {
			btnCount = len(loaded.Signals)
			protos = uniqueProtocols(loaded.Signals)
		}
		items = append(items, map[string]any{
			"name":         r.Name,
			"button_count": btnCount,
			"protocols":    protos,
			"size_bytes":   r.SizeBytes,
			"modified_at":  r.ModTime.UTC().Format(time.RFC3339),
		})
	}
	return okJSON(map[string]any{"remotes": items}), nil
}

func (s *Server) toolReadRemote(_ context.Context, raw json.RawMessage) (toolResult, *rpcError) {
	var args struct {
		Name string `json:"name"`
		Pack string `json:"pack"`
	}
	if rpcErr := decodeArgs(raw, &args); rpcErr != nil {
		return toolResult{}, rpcErr
	}
	entry, err := s.loadRemoteEntry(args.Name, args.Pack)
	if err != nil {
		return errResult(err.Error()), nil
	}
	buttons := make([]signalDTO, 0, len(entry.Signals))
	for _, sig := range entry.Signals {
		buttons = append(buttons, signalToDTO(sig))
	}
	return okJSON(map[string]any{
		"name":     entry.Name,
		"pack":     args.Pack,
		"filetype": irfile.FiletypeHeader,
		"version":  irfile.SupportedVersion,
		"buttons":  buttons,
	}), nil
}

func (s *Server) toolSaveRemote(_ context.Context, raw json.RawMessage) (toolResult, *rpcError) {
	var args struct {
		Name      string      `json:"name"`
		Buttons   []signalDTO `json:"buttons"`
		Overwrite bool        `json:"overwrite"`
	}
	if rpcErr := decodeArgs(raw, &args); rpcErr != nil {
		return toolResult{}, rpcErr
	}
	if err := ValidateSafeName(args.Name); err != nil {
		return errResult(err.Error()), nil
	}
	if len(args.Buttons) == 0 {
		return errResult("buttons must be a non-empty array"), nil
	}
	name := args.Name
	if !strings.HasSuffix(strings.ToLower(name), ".ir") {
		name += ".ir"
	}
	target, err := ResolveSandboxedPath(s.store.UserDir, name)
	if err != nil {
		return errResult(err.Error()), nil
	}
	f, err := buildIRFile(args.Buttons)
	if err != nil {
		return errResult(err.Error()), nil
	}
	var buf bytes.Buffer
	if err := irfile.Write(&buf, f); err != nil {
		return errResult("serialize .ir: " + err.Error()), nil
	}
	if buf.Len() > irfile.MaxIRFileBytes {
		return errResult(ErrFileTooLarge.Error()), nil
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if !args.Overwrite {
		if _, err := os.Stat(target); err == nil {
			return errResult(fmt.Sprintf("remote %q already exists; set overwrite: true to replace", name)), nil
		}
	}
	saved, err := s.store.SaveUserRemote(name, f)
	if err != nil {
		return errResult("save remote: " + err.Error()), nil
	}
	return okJSON(map[string]any{
		"saved":        true,
		"name":         saved.Name,
		"path":         saved.FullPath,
		"button_count": len(f.Signals),
		"size_bytes":   saved.SizeBytes,
	}), nil
}

func buildIRFile(dtos []signalDTO) (*irfile.IRFile, error) {
	signals := make([]irfile.IRSignal, 0, len(dtos))
	for _, dto := range dtos {
		sig, err := dtoToSignal(dto)
		if err != nil {
			return nil, err
		}
		signals = append(signals, sig)
	}
	return &irfile.IRFile{
		Version: irfile.SupportedVersion,
		Signals: signals,
	}, nil
}

func (s *Server) toolDeleteRemote(_ context.Context, raw json.RawMessage) (toolResult, *rpcError) {
	var args struct {
		Name    string `json:"name"`
		Confirm bool   `json:"confirm"`
	}
	if rpcErr := decodeArgs(raw, &args); rpcErr != nil {
		return toolResult{}, rpcErr
	}
	if !args.Confirm {
		return errResult("confirm: true is required to delete a remote"), nil
	}
	if err := ValidateSafeName(args.Name); err != nil {
		return errResult(err.Error()), nil
	}
	if _, err := ResolveSandboxedPath(s.store.UserDir, args.Name); err != nil {
		return errResult(err.Error()), nil
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if err := s.store.DeleteUserRemote(args.Name); err != nil {
		return errResult("delete remote: " + err.Error()), nil
	}
	return okJSON(map[string]any{
		"deleted": true,
		"name":    args.Name,
	}), nil
}

func (s *Server) toolListDatabases(_ context.Context, _ json.RawMessage) (toolResult, *rpcError) {
	packs, err := s.store.ListDatabasePacks()
	if err != nil {
		return errResult("list databases: " + err.Error()), nil
	}
	sourceURLs := s.sourceURLMap()
	items := make([]map[string]any, 0, len(packs))
	for _, p := range packs {
		if p.Virtual {
			items = append(items, map[string]any{
				"id":           p.ID,
				"remote_count": p.FileCount,
				"virtual":      true,
				"last_synced":  "",
				"source_url":   "embedded://tv-b-gone",
			})
			continue
		}
		lastSynced := ""
		if st, err := dbsync.LoadSyncState(p.Dir); err == nil && !st.LastSync.IsZero() {
			lastSynced = st.LastSync.UTC().Format(time.RFC3339)
		}
		items = append(items, map[string]any{
			"id":           p.ID,
			"remote_count": p.FileCount,
			"last_synced":  lastSynced,
			"source_url":   sourceURLs[p.ID],
		})
	}
	return okJSON(map[string]any{"packs": items}), nil
}

func (s *Server) toolPromoteRemote(_ context.Context, raw json.RawMessage) (toolResult, *rpcError) {
	var args struct {
		Pack       string `json:"pack"`
		RemoteName string `json:"remote_name"`
		TargetName string `json:"target_name"`
	}
	if rpcErr := decodeArgs(raw, &args); rpcErr != nil {
		return toolResult{}, rpcErr
	}
	if args.TargetName != "" {
		if err := ValidateSafeName(args.TargetName); err != nil {
			return errResult(err.Error()), nil
		}
	}
	entry, err := s.loadRemoteEntry(args.RemoteName, args.Pack)
	if err != nil {
		return errResult(err.Error()), nil
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	promoted, err := s.store.PromoteToUser(entry, args.TargetName)
	if err != nil {
		return errResult("promote remote: " + err.Error()), nil
	}
	return okJSON(map[string]any{
		"promoted":      true,
		"source_pack":   args.Pack,
		"source_remote": entry.RelPath,
		"saved_as":      promoted.Name,
		"button_count":  len(entry.Signals),
	}), nil
}

func (s *Server) sourceURLMap() map[string]string {
	urls := make(map[string]string)
	if s.sourcesPath == "" {
		return urls
	}
	sources, err := dbsync.LoadSources(s.sourcesPath)
	if err != nil {
		return urls
	}
	for _, src := range sources {
		if src.URL != "" {
			urls[src.ID] = src.URL
		} else if src.Repo != "" {
			urls[src.ID] = "https://github.com/" + src.Repo
		}
	}
	return urls
}

func uniqueProtocols(signals []irfile.IRSignal) []string {
	seen := make(map[string]struct{}, len(signals))
	out := make([]string, 0, len(signals))
	for _, sig := range signals {
		p := sig.Protocol
		if p == "" {
			p = string(sig.Kind)
		}
		if _, ok := seen[p]; !ok {
			seen[p] = struct{}{}
			out = append(out, p)
		}
	}
	return out
}

func (s *Server) resolvePackDir(pack string) (string, error) {
	if err := ValidateSafeName(pack); err != nil {
		return "", err
	}
	return ResolveSandboxedPath(s.store.DatabasesDir, pack)
}

func packFileExists(packDir, rel string) bool {
	target, err := ResolveSandboxedPath(packDir, rel)
	if err != nil {
		return false
	}
	_, err = os.Lstat(filepath.Clean(target))
	return err == nil
}
