package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"ir-studio/internal/runner"
)

func main() {
	simMode := flag.Bool("sim", false, "Run in simulated LIRC mode without hardware")
	probeOnly := flag.Bool("probe", false, "Probe /dev/lirc* capabilities and exit")
	syncOnly := flag.Bool("sync", false, "Sync enabled Flipper-IRDB database packs and exit")
	mcpMode := flag.Bool("mcp", false, "Run stdio JSON-RPC 2.0 MCP server (requires SET opt-in)")
	fbPath := flag.String("fb", "/dev/fb0", "Framebuffer device path (empty for headless)")
	lircDev := flag.String("lirc", "", "Explicit /dev/lirc* device path")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	opts := runner.Options{
		SimMode:    *simMode,
		ProbeOnly:  *probeOnly,
		SyncOnly:   *syncOnly,
		MCPMode:    *mcpMode,
		FBPath:     *fbPath,
		DevicePath: *lircDev,
	}
	if err := runner.Run(ctx, opts, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "ir-studio: %v\n", err)
		os.Exit(1)
	}
}
