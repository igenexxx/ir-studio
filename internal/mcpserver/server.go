package mcpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"ir-studio/internal/config"
	"ir-studio/internal/dbsync"
	"ir-studio/internal/irfile"
	"ir-studio/internal/library"
)

const (
	protocolVersion = "2024-11-05"
	serverName      = "ir-studio"
	serverVersion   = "0.1.0"

	errCodeParseError     = -32700
	errCodeMethodNotFound = -32601
	errCodeInvalidParams  = -32602

	stdioInitBufSize  = 64 * 1024
	stdioMaxBufSize   = 2 * 1024 * 1024
	maxTransmitRepeat = 5
	maxMacroDuration  = 30 * time.Second
)

type toolHandler func(ctx context.Context, args json.RawMessage) (toolResult, *rpcError)

// Server implements a pure-Go MCP JSON-RPC 2.0 server over stdio.
type Server struct {
	backend     IRBackend
	store       *library.Store
	syncer      *dbsync.Syncer
	registry    *irfile.ProtocolRegistry
	settings    config.Settings
	sourcesPath string
	macrosPath  string
	handlers    map[string]toolHandler
	writeMu     sync.Mutex
	outMu       sync.Mutex
}

// NewServerFromConfig builds a Server backed by the injected ServeConfig dependencies.
func NewServerFromConfig(cfg ServeConfig) *Server {
	reg := irfile.DefaultRegistry()
	backend := NewHardwareBackend(cfg, reg)
	return NewServer(backend, cfg)
}

// NewServer creates a Server with an explicit IRBackend (or HardwareBackend when nil).
func NewServer(backend IRBackend, cfg ServeConfig) *Server {
	reg := irfile.DefaultRegistry()
	if backend == nil {
		backend = NewHardwareBackend(cfg, reg)
	}
	store := cfg.Store
	if store == nil {
		store = defaultStore()
	}
	syncer := cfg.Syncer
	if syncer == nil {
		syncer = dbsync.NewSyncer(store.DatabasesDir, nil)
	}
	s := &Server{
		backend:     backend,
		store:       store,
		syncer:      syncer,
		registry:    reg,
		settings:    cfg.Settings,
		sourcesPath: cfg.SourcesPath,
		macrosPath:  cfg.MacrosPath,
		handlers:    make(map[string]toolHandler, 23),
	}
	s.registerTools()
	return s
}

func defaultStore() *library.Store {
	base, err := library.DefaultBaseDir()
	if err != nil {
		return library.NewStore("", "")
	}
	return library.NewStore(base+"/remotes", base+"/databases")
}

func (s *Server) registerTools() {
	s.handlers["ir_get_status"] = s.toolGetStatus
	s.handlers["ir_learn_signal"] = s.toolLearnSignal
	s.handlers["ir_transmit_button"] = s.toolTransmitButton
	s.handlers["ir_run_macro"] = s.toolRunMacro
	s.handlers["ir_list_macros"] = s.toolListMacros
	s.handlers["ir_analyze_pulses"] = s.toolAnalyzePulses
	s.handlers["ir_classify_signal"] = s.toolClassifySignal
	s.handlers["ir_match_fingerprint"] = s.toolMatchFingerprint
	s.handlers["ir_rank_probe_pack"] = s.toolRankProbePack
	s.handlers["ir_purge_noise"] = s.toolPurgeNoise
	s.handlers["ir_list_remotes"] = s.toolListRemotes
	s.handlers["ir_read_remote"] = s.toolReadRemote
	s.handlers["ir_save_remote"] = s.toolSaveRemote
	s.handlers["ir_delete_remote"] = s.toolDeleteRemote
	s.handlers["ir_list_databases"] = s.toolListDatabases
	s.handlers["ir_promote_remote"] = s.toolPromoteRemote
	s.handlers["ir_list_sources"] = s.toolListSources
	s.handlers["ir_add_source"] = s.toolAddSource
	s.handlers["ir_remove_source"] = s.toolRemoveSource
	s.handlers["ir_sync_sources"] = s.toolSyncSources
	s.handlers["ir_delete_pack_files"] = s.toolDeletePackFiles
	s.handlers["ir_send_ac_state"] = s.toolSendACState
	s.handlers["ir_sweep_carrier"] = s.toolSweepCarrier
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type toolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type toolResult struct {
	Content []toolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// ServeStdio reads newline-delimited JSON-RPC 2.0 frames from r and writes responses to w.
func (s *Server) ServeStdio(ctx context.Context, r io.Reader, w io.Writer) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, stdioInitBufSize), stdioMaxBufSize)

	for scanner.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		resp := s.handleRequest(ctx, []byte(line))
		if resp == nil {
			continue
		}
		if err := s.writeResponse(w, resp); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			_ = s.writeResponse(w, rpcFail(json.RawMessage("null"), errCodeParseError, "Parse error: request exceeds 2 MiB limit"))
		}
		return err
	}
	return ctx.Err()
}

func (s *Server) writeResponse(w io.Writer, resp *rpcResponse) error {
	s.outMu.Lock()
	defer s.outMu.Unlock()
	raw, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	_, err = w.Write(raw)
	return err
}

func (s *Server) handleRequest(ctx context.Context, line []byte) *rpcResponse {
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		return rpcFail(json.RawMessage("null"), errCodeParseError, "Parse error: "+err.Error())
	}
	if isNotification(req) {
		return nil
	}
	id := req.ID
	if len(id) == 0 {
		id = json.RawMessage("null")
	}

	switch req.Method {
	case "initialize":
		return rpcOK(id, initializeResult())
	case "ping":
		return rpcOK(id, map[string]any{})
	case "tools/list":
		return rpcOK(id, map[string]any{"tools": toolDefinitions()})
	case "tools/call":
		return s.dispatchTool(ctx, id, req.Params)
	default:
		return rpcFail(id, errCodeMethodNotFound, fmt.Sprintf("Method not found: %s", req.Method))
	}
}

func isNotification(req rpcRequest) bool {
	if req.Method == "notifications/initialized" {
		return true
	}
	return len(req.ID) == 0 && strings.HasPrefix(req.Method, "notifications/")
}

func initializeResult() map[string]any {
	return map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo": map[string]any{
			"name":    serverName,
			"version": serverVersion,
		},
	}
}

func (s *Server) dispatchTool(ctx context.Context, id json.RawMessage, rawParams json.RawMessage) *rpcResponse {
	if len(rawParams) == 0 {
		return rpcFail(id, errCodeInvalidParams, "Invalid params: missing params object")
	}
	var params toolCallParams
	if err := json.Unmarshal(rawParams, &params); err != nil || params.Name == "" {
		return rpcFail(id, errCodeInvalidParams, "Invalid params: expected {name, arguments}")
	}
	args := params.Arguments
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	handler, ok := s.handlers[params.Name]
	if !ok {
		return rpcFail(id, errCodeInvalidParams, fmt.Sprintf("Unknown tool: %s", params.Name))
	}
	res, rpcErr := handler(ctx, args)
	if rpcErr != nil {
		return &rpcResponse{JSONRPC: "2.0", ID: id, Error: rpcErr}
	}
	return rpcOK(id, res)
}

func decodeArgs(raw json.RawMessage, dst any) *rpcError {
	if err := json.Unmarshal(raw, dst); err != nil {
		return &rpcError{Code: errCodeInvalidParams, Message: "Invalid arguments: " + err.Error()}
	}
	return nil
}

func rpcOK(id json.RawMessage, result any) *rpcResponse {
	return &rpcResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func rpcFail(id json.RawMessage, code int, msg string) *rpcResponse {
	return &rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: code, Message: msg},
	}
}

func okJSON(v any) toolResult {
	b, err := json.Marshal(v)
	if err != nil {
		return errResult("marshal output: " + err.Error())
	}
	return toolResult{
		Content: []toolContent{{Type: "text", Text: string(b)}},
	}
}

func errResult(msg string) toolResult {
	b, _ := json.Marshal(map[string]string{"error": msg})
	return toolResult{
		Content: []toolContent{{Type: "text", Text: string(b)}},
		IsError: true,
	}
}
