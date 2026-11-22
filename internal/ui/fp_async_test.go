package ui

import (
	"testing"
	"time"

	"ir-studio/internal/config"
	"ir-studio/internal/library"
)

func TestNewAppStartupNonBlockingAndAsyncIndex(t *testing.T) {
	store := library.NewStore(t.TempDir(), t.TempDir())
	start := time.Now()
	app := NewApp(Deps{
		Store:    store,
		Settings: config.DefaultSettings(),
		SimMode:  true,
	})
	defer app.Close()

	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("NewApp blocked startup for %v (must be non-blocking)", elapsed)
	}

	deadline := time.Now().Add(10 * time.Second)
	for app.fpIndexing && time.Now().Before(deadline) {
		app.Step()
		time.Sleep(10 * time.Millisecond)
	}
	if app.fpIndexing {
		t.Fatal("background fingerprint index build did not finish before deadline")
	}
	if app.fpIndex == nil || app.fpIndex.Len() == 0 {
		t.Fatalf("expected non-empty async fingerprint index from built-in packs, got %v", app.fpIndex)
	}
}
