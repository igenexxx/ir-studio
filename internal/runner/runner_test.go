package runner

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ir-studio/internal/config"
	"ir-studio/internal/dbsync"
	"ir-studio/internal/input"
	"ir-studio/internal/mcpserver"
)

func TestRunProbeSim(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), Options{
		SimMode:   true,
		ProbeOnly: true,
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run --probe: %v", err)
	}
	if !strings.Contains(stdout.String(), "mode=SIM") {
		t.Fatalf("unexpected probe output: %q", stdout.String())
	}
}

func TestRunMCPDefaultDeny(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.json")
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), Options{
		SimMode:    true,
		MCPMode:    true,
		ConfigPath: cfgPath,
		Stdin:      bytes.NewBuffer(nil),
	}, &stdout, &stderr)
	if !errors.Is(err, mcpserver.ErrMCPDisabled) {
		t.Fatalf("err = %v, want ErrMCPDisabled", err)
	}
}

func TestRunSyncWithDisabledSources(t *testing.T) {
	tmp := t.TempDir()
	srcPath := filepath.Join(tmp, "sources.json")
	srcs := []dbsync.DatabaseSource{
		{
			ID:      "flipper-tvs",
			Kind:    dbsync.SourceGitHubTree,
			Repo:    "Lucaslhm/Flipper-IRDB",
			Branch:  "main",
			Enabled: false,
		},
	}
	if err := dbsync.SaveSourcesAtomic(srcPath, srcs); err != nil {
		t.Fatalf("SaveSourcesAtomic: %v", err)
	}
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), Options{
		SyncOnly:     true,
		SourcesPath:  srcPath,
		DatabasesDir: filepath.Join(tmp, "databases"),
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run --sync: %v", err)
	}
}

func TestRunUIHeadlessGracefulExit(t *testing.T) {
	tmp := t.TempDir()
	mr := input.NewMockReader()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		time.Sleep(80 * time.Millisecond)
		mr.Send(input.KeyTab)
		mr.Send(input.KeyQ)
	}()

	var stdout, stderr bytes.Buffer
	err := Run(ctx, Options{
		SimMode:      true,
		ConfigPath:   filepath.Join(tmp, "config.json"),
		SourcesPath:  filepath.Join(tmp, "sources.json"),
		MacrosPath:   filepath.Join(tmp, "macros.json"),
		UserDir:      filepath.Join(tmp, "remotes"),
		DatabasesDir: filepath.Join(tmp, "databases"),
		InputReader:  mr,
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run UI: %v", err)
	}
	// Verify default settings remained safe.
	s, err := config.Load(filepath.Join(tmp, "config.json"))
	if err != nil || s.MCPEnabled {
		t.Fatalf("settings = %+v, err = %v", s, err)
	}
}
