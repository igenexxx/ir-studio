package mcpserver

import (
	"context"
	"errors"
	"io"

	"ir-studio/internal/config"
	"ir-studio/internal/dbsync"
	"ir-studio/internal/library"
	"ir-studio/internal/lirc"
)

// ErrMCPDisabled is returned when MCP mode is invoked while opt-in is disabled.
var ErrMCPDisabled = errors.New("MCP server is disabled: enable it in SET -> MCP Server inside ir-studio")

// ServeConfig bundles the domain dependencies injected into the stdio MCP server.
type ServeConfig struct {
	Store       *library.Store
	Syncer      *dbsync.Syncer
	Transceiver *lirc.Transceiver
	TX          *lirc.TXWorker
	RX          *lirc.RXWorker
	Settings    config.Settings
	ConfigPath  string
	SourcesPath string
	MacrosPath  string
}

// ServeStdio starts the JSON-RPC 2.0 MCP server over r/w until ctx is cancelled or EOF.
func ServeStdio(ctx context.Context, r io.Reader, w io.Writer, cfg ServeConfig) error {
	if !cfg.Settings.MCPEnabled {
		return ErrMCPDisabled
	}
	return runStdioServer(ctx, r, w, cfg)
}

func runStdioServer(ctx context.Context, r io.Reader, w io.Writer, cfg ServeConfig) error {
	srv := NewServerFromConfig(cfg)
	return srv.ServeStdio(ctx, r, w)
}
