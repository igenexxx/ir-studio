package fb

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestRGB565RoundTrip(t *testing.T) {
	cases := []struct {
		name    string
		r, g, b uint8
	}{
		{"black", 0, 0, 0},
		{"white", 255, 255, 255},
		{"cyan", 0x00, 0xd9, 0xff},
		{"emerald", 0x10, 0xe1, 0x91},
		{"amber", 0xf5, 0x9e, 0x0b},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			px := RGB565(tc.r, tc.g, tc.b)
			rgba := RGB565ToRGBA(px)
			if rgba.A != 255 {
				t.Fatalf("alpha = %d, want 255", rgba.A)
			}
			// RGB565 quantization error is at most 7 for R/B and 3 for G.
			if diffU8(rgba.R, tc.r) > 8 || diffU8(rgba.G, tc.g) > 4 || diffU8(rgba.B, tc.b) > 8 {
				t.Fatalf("roundtrip (%d,%d,%d) -> (%d,%d,%d)", tc.r, tc.g, tc.b, rgba.R, rgba.G, rgba.B)
			}
		})
	}
}

func diffU8(a, b uint8) int {
	d := int(a) - int(b)
	if d < 0 {
		return -d
	}
	return d
}

func TestCanvasPrimitivesAndClipping(t *testing.T) {
	c, err := NewCanvas("")
	if err != nil {
		t.Fatalf("NewCanvas headless: %v", err)
	}
	defer c.Close()

	c.Clear(ColBg)
	if got := c.PixelAt(0, 0); got != ColBg {
		t.Fatalf("PixelAt(0,0) = %#x, want %#x", got, ColBg)
	}
	if got := c.PixelAt(-1, 0); got != 0 {
		t.Fatalf("out-of-bounds PixelAt(-1,0) = %#x, want 0", got)
	}
	if got := c.PixelAt(ScreenW, ScreenH); got != 0 {
		t.Fatalf("out-of-bounds PixelAt(W,H) = %#x, want 0", got)
	}

	// Out-of-bounds and zero/negative sizes must clip cleanly without panic.
	c.FillRect(-10, -10, 20, 20, ColCyan)
	c.FillRect(10, 10, 0, 5, ColRed)
	c.FillRect(10, 10, 5, -2, ColRed)
	c.DrawRect(5, 5, 20, 10, ColEmerald)
	c.DrawLine(0, 0, 319, 169, ColAmber)
	c.DrawLine(319, 0, 0, 169, ColMagenta)
	c.DrawText(10, 20, "IR-LAB 38kHz\nOK!", ColFg, 1)
	c.DrawTextCentered(ScreenW/2, 50, "TEST", ColCyan, 2)
	// Non-ASCII fallback to '?' must not panic.
	c.DrawText(10, 90, "\xff\x00", ColMuted, 0)

	if c.PixelAt(0, 0) != ColAmber {
		t.Fatalf("expected diagonal line pixel at (0,0) to be ColAmber")
	}
	if c.PixelAt(5, 5) != ColEmerald {
		t.Fatalf("expected rect border at (5,5) to be ColEmerald")
	}
}

func TestCanvasSavePNGAndToRGBA(t *testing.T) {
	c, err := NewCanvas("")
	if err != nil {
		t.Fatalf("NewCanvas: %v", err)
	}
	c.Clear(ColBg)
	c.FillRect(10, 10, 40, 20, ColCyan)
	c.DrawText(12, 14, "IR", ColBg, 1)

	img1 := c.ToRGBA(1)
	if img1.Bounds().Dx() != ScreenW || img1.Bounds().Dy() != ScreenH {
		t.Fatalf("1x bounds = %v, want 320x170", img1.Bounds())
	}
	img2 := c.ToRGBA(2)
	if img2.Bounds().Dx() != ScreenW*2 || img2.Bounds().Dy() != ScreenH*2 {
		t.Fatalf("2x bounds = %v, want 640x340", img2.Bounds())
	}

	pngPath := filepath.Join(t.TempDir(), "out.png")
	if err := c.SavePNG(pngPath, 2); err != nil {
		t.Fatalf("SavePNG: %v", err)
	}
	f, err := os.Open(pngPath)
	if err != nil {
		t.Fatalf("open png: %v", err)
	}
	defer f.Close()
	decoded, err := png.Decode(f)
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	if decoded.Bounds().Dx() != ScreenW*2 || decoded.Bounds().Dy() != ScreenH*2 {
		t.Fatalf("decoded png bounds = %v, want 640x340", decoded.Bounds())
	}
}

func TestCanvasFlushToFile(t *testing.T) {
	fbPath := filepath.Join(t.TempDir(), "fb0")
	if err := os.WriteFile(fbPath, make([]byte, ScreenW*ScreenH*2), 0o600); err != nil {
		t.Fatalf("seed fb0: %v", err)
	}
	c, err := NewCanvas(fbPath)
	if err != nil {
		t.Fatalf("NewCanvas(fbPath): %v", err)
	}
	c.Clear(ColCyan)
	if err := c.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	data, err := os.ReadFile(fbPath)
	if err != nil {
		t.Fatalf("read fb0: %v", err)
	}
	if len(data) != ScreenW*ScreenH*2 {
		t.Fatalf("fb0 size = %d, want %d", len(data), ScreenW*ScreenH*2)
	}
	if _, err := NewCanvas(filepath.Join(t.TempDir(), "missing-fb0")); err == nil {
		t.Fatalf("expected error opening missing fb device")
	}
}
