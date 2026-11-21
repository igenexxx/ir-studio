package main

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"ir-studio/internal/fb"
)

func TestGenerateAllScreenshotsAndIcon(t *testing.T) {
	tmp := t.TempDir()
	outDir := filepath.Join(tmp, "screenshots")
	iconPath := filepath.Join(tmp, "icon.png")

	if err := generateAll(outDir, 1, iconPath, ""); err != nil {
		t.Fatalf("generateAll: %v", err)
	}
	expected := []string{"rx.png", "remote.png", "probe.png", "saved.png", "macro.png", "set.png", "help.png"}
	for _, name := range expected {
		f, err := os.Open(filepath.Join(outDir, name))
		if err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
		img, err := png.Decode(f)
		_ = f.Close()
		if err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		if img.Bounds().Dx() != fb.ScreenW || img.Bounds().Dy() != fb.ScreenH {
			t.Fatalf("%s bounds = %v, want 320x170", name, img.Bounds())
		}
	}

	f, err := os.Open(iconPath)
	if err != nil {
		t.Fatalf("missing icon: %v", err)
	}
	icon, err := png.Decode(f)
	_ = f.Close()
	if err != nil {
		t.Fatalf("decode icon: %v", err)
	}
	if icon.Bounds().Dx() != 256 || icon.Bounds().Dy() != 256 {
		t.Fatalf("icon bounds = %v, want 256x256", icon.Bounds())
	}
}
