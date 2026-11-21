package runner

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"ir-studio/internal/config"
	"ir-studio/internal/dbsync"
	"ir-studio/internal/fb"
	"ir-studio/internal/input"
	"ir-studio/internal/irfile"
	"ir-studio/internal/library"
	"ir-studio/internal/lirc"
	"ir-studio/internal/mcpserver"
	"ir-studio/internal/ui"
)

const uiFrameInterval = 55 * time.Millisecond

// Options holds runtime flags and dependency-injected paths for ir-studio.
type Options struct {
	SimMode      bool
	ProbeOnly    bool
	SyncOnly     bool
	MCPMode      bool
	FBPath       string
	DevicePath   string
	ConfigPath   string
	SourcesPath  string
	MacrosPath   string
	UserDir      string
	DatabasesDir string
	Stdin        io.Reader
	InputReader  input.Reader
}

// Run dispatches --probe, --sync, --mcp, or the interactive 320x170 UI loop.
func Run(ctx context.Context, opts Options, stdout, stderr io.Writer) error {
	resolved := resolveDefaults(opts)
	switch {
	case resolved.ProbeOnly:
		return runProbe(resolved, stdout)
	case resolved.SyncOnly:
		return runSync(ctx, resolved, stdout)
	case resolved.MCPMode:
		return runMCP(ctx, resolved, stdout, stderr)
	default:
		return runUI(ctx, resolved, stderr)
	}
}

func resolveDefaults(opts Options) Options {
	if opts.ConfigPath == "" {
		opts.ConfigPath, _ = config.ConfigPath()
	}
	if opts.SourcesPath == "" {
		opts.SourcesPath, _ = dbsync.SourcesPath()
	}
	if opts.MacrosPath == "" {
		opts.MacrosPath, _ = config.MacrosPath()
	}
	if opts.UserDir == "" || opts.DatabasesDir == "" {
		base, err := library.DefaultBaseDir()
		if err != nil || base == "" {
			base = filepath.Join(os.TempDir(), "ir-studio")
		}
		if opts.UserDir == "" {
			opts.UserDir = filepath.Join(base, "remotes")
		}
		if opts.DatabasesDir == "" {
			opts.DatabasesDir = filepath.Join(base, "databases")
		}
	}
	if opts.Stdin == nil {
		opts.Stdin = os.Stdin
	}
	return opts
}

func runProbe(opts Options, out io.Writer) error {
	tr, err := lirc.Discover(lirc.DiscoverOptions{
		ForceSim:      opts.SimMode,
		DevicePath:    opts.DevicePath,
		FallbackToSim: true,
	})
	if err != nil {
		return fmt.Errorf("runner: lirc discover: %w", err)
	}
	defer tr.Close()

	feats, err := tr.Features()
	if err != nil {
		return fmt.Errorf("runner: lirc features: %w", err)
	}
	mode := "HW"
	if tr.IsSim() {
		mode = "SIM"
	}
	fmt.Fprintf(out, "ir-studio LIRC probe: mode=%s device=%s features=0x%08x\n", mode, tr.DevicePath(), feats)
	return nil
}

func runSync(ctx context.Context, opts Options, out io.Writer) error {
	sources, err := dbsync.LoadSources(opts.SourcesPath)
	if err != nil {
		return fmt.Errorf("runner: load sources: %w", err)
	}
	if len(sources) == 0 {
		sources = dbsync.DefaultSources()
	}
	syncer := dbsync.NewSyncer(opts.DatabasesDir, nil)
	results, syncErr := syncer.SyncAll(ctx, sources)
	for _, r := range results {
		fmt.Fprintf(out, "pack %-18s status=%-10s downloaded=%d skipped=%d pruned=%d\n",
			r.PackID, r.Status, r.Downloaded, r.Skipped, r.Pruned)
	}
	return syncErr
}

func runMCP(ctx context.Context, opts Options, stdout, stderr io.Writer) error {
	settings, err := config.Load(opts.ConfigPath)
	if err != nil {
		return fmt.Errorf("runner: load settings: %w", err)
	}
	if !settings.MCPEnabled {
		return mcpserver.ErrMCPDisabled
	}
	tr, xcvr, tx, rx, cleanup, err := startTransceiver(ctx, opts, settings, stderr)
	if err != nil {
		return err
	}
	defer cleanup()
	_ = tr

	cfg := mcpserver.ServeConfig{
		Store:       library.NewStore(opts.UserDir, opts.DatabasesDir),
		Syncer:      dbsync.NewSyncer(opts.DatabasesDir, nil),
		Transceiver: xcvr,
		TX:          tx,
		RX:          rx,
		Settings:    settings,
		ConfigPath:  opts.ConfigPath,
		SourcesPath: opts.SourcesPath,
		MacrosPath:  opts.MacrosPath,
	}
	return mcpserver.ServeStdio(ctx, opts.Stdin, stdout, cfg)
}

func runUI(ctx context.Context, opts Options, stderr io.Writer) error {
	settings, err := config.Load(opts.ConfigPath)
	if err != nil {
		fmt.Fprintf(stderr, "ir-studio: settings load error (using defaults): %v\n", err)
		settings = config.DefaultSettings()
	}
	canvas, err := fb.NewCanvas(opts.FBPath)
	if err != nil {
		return fmt.Errorf("runner: open framebuffer: %w", err)
	}
	defer canvas.Close()

	tr, _, tx, rx, cleanup, err := startTransceiver(ctx, opts, settings, stderr)
	if err != nil {
		return err
	}
	defer cleanup()

	reader := opts.InputReader
	if reader == nil {
		reader = input.StartEvdevReader()
	}
	defer reader.Close()

	store := library.NewStore(opts.UserDir, opts.DatabasesDir)
	app := ui.NewApp(ui.Deps{
		Store:       store,
		TX:          tx,
		RX:          rx,
		Syncer:      dbsync.NewSyncer(opts.DatabasesDir, nil),
		MacroRunner: library.NewMacroRunner(store, tx),
		Settings:    settings,
		ConfigPath:  opts.ConfigPath,
		SourcesPath: opts.SourcesPath,
		MacrosPath:  opts.MacrosPath,
		SimMode:     tr.IsSim(),
		DeviceLabel: formatDeviceBadge(tr),
	})
	defer app.Close()

	return runUILoop(ctx, app, canvas, reader.Events())
}

func startTransceiver(
	ctx context.Context,
	opts Options,
	settings config.Settings,
	stderr io.Writer,
) (lirc.Transport, *lirc.Transceiver, *lirc.TXWorker, *lirc.RXWorker, func(), error) {
	devPath := opts.DevicePath
	if devPath == "" {
		devPath = settings.DevicePath
	}
	tr, err := lirc.Discover(lirc.DiscoverOptions{
		ForceSim:      opts.SimMode || settings.ForceSim,
		DevicePath:    devPath,
		FallbackToSim: true,
		RecTimeoutUs:  settings.RecTimeoutUs,
	})
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("runner: discover lirc: %w", err)
	}
	if tr.IsSim() && !opts.SimMode {
		fmt.Fprintln(stderr, "ir-studio: /dev/lirc* not available, running in SIM mode")
	}
	gate := lirc.NewTransceiverGate(lirc.DefaultSettleGuard)
	xcvr := lirc.NewTransceiver(tr, gate)
	reg := irfile.DefaultRegistry()
	tx := lirc.NewTXWorker(xcvr, reg)
	rx := lirc.NewRXWorker(tr, gate, reg)
	tx.Start(ctx)
	rx.Start(ctx)

	cleanup := func() {
		rx.Close()
		tx.Close()
		_ = tr.Close()
	}
	return tr, xcvr, tx, rx, cleanup, nil
}

func formatDeviceBadge(tr lirc.Transport) string {
	if tr.IsSim() {
		return "SIM"
	}
	return filepath.Base(tr.DevicePath())
}

func runUILoop(ctx context.Context, app *ui.App, canvas *fb.Canvas, keys <-chan input.KeyAction) error {
	ticker := time.NewTicker(uiFrameInterval)
	defer ticker.Stop()

	app.Render(canvas)
	_ = canvas.Flush()

	for {
		select {
		case <-ctx.Done():
			return nil
		case act, ok := <-keys:
			if !ok {
				return nil
			}
			if app.HandleKey(act) {
				return nil
			}
			app.Render(canvas)
			_ = canvas.Flush()
		case <-ticker.C:
			app.Step()
			app.Render(canvas)
			_ = canvas.Flush()
		}
	}
}
