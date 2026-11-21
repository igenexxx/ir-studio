package fb

import (
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
)

const (
	// ScreenW and ScreenH are the physical CardputerZero LCD dimensions.
	ScreenW = 320
	ScreenH = 170
)

// RGB565 converts 8-bit R, G, B into 16-bit little-endian RGB565.
func RGB565(r, g, b uint8) uint16 {
	return uint16(r&0xF8)<<8 | uint16(g&0xFC)<<3 | uint16(b)>>3
}

// RGB565ToRGBA expands a 16-bit RGB565 pixel to 8-bit RGBA.
func RGB565ToRGBA(px uint16) color.RGBA {
	r := uint8((px >> 11) & 0x1F)
	g := uint8((px >> 5) & 0x3F)
	b := uint8(px & 0x1F)
	return color.RGBA{
		R: (r << 3) | (r >> 2),
		G: (g << 2) | (g >> 4),
		B: (b << 3) | (b >> 2),
		A: 255,
	}
}

// Canonical CardputerZero IR Studio dark-mode RGB565 palette (SSOT from DESIGN.md).
var (
	ColBg         = RGB565(0x0b, 0x11, 0x17)
	ColSurface    = RGB565(0x12, 0x1a, 0x23)
	ColSurfaceAlt = RGB565(0x18, 0x22, 0x30)
	ColBorder     = RGB565(0x1e, 0x29, 0x3b)
	ColFg         = RGB565(0xe2, 0xe8, 0xf0)
	ColMuted      = RGB565(0x94, 0xa3, 0xb8)
	ColCyan       = RGB565(0x00, 0xd9, 0xff)
	ColEmerald    = RGB565(0x10, 0xe1, 0x91)
	ColAmber      = RGB565(0xf5, 0x9e, 0x0b)
	ColMagenta    = RGB565(0xe8, 0x79, 0xf9)
	ColRed        = RGB565(0xef, 0x44, 0x44)
	ColNavBg      = RGB565(0x09, 0x0f, 0x15)
	ColNavActive  = RGB565(0x0d, 0x3b, 0x4c)
)

// Canvas is a double-buffered 320x170 RGB565 drawing surface optionally backed
// by a Linux /dev/fb0 device.
type Canvas struct {
	pix    [ScreenW * ScreenH]uint16
	rawBuf [ScreenW * ScreenH * 2]byte
	fbFile *os.File
}

// NewCanvas creates an offscreen Canvas. If fbPath is non-empty, it opens the
// framebuffer device for Flush writes.
func NewCanvas(fbPath string) (*Canvas, error) {
	c := &Canvas{}
	if fbPath != "" {
		f, err := os.OpenFile(fbPath, os.O_RDWR, 0)
		if err != nil {
			return nil, fmt.Errorf("fb: open %s: %w", fbPath, err)
		}
		c.fbFile = f
	}
	return c, nil
}

// Close closes the underlying framebuffer file if one was opened.
func (c *Canvas) Close() error {
	if c.fbFile != nil {
		err := c.fbFile.Close()
		c.fbFile = nil
		return err
	}
	return nil
}

// Flush writes the RGB565 pixel buffer to /dev/fb0 (no-op in headless/SIM mode).
func (c *Canvas) Flush() error {
	if c.fbFile == nil {
		return nil
	}
	for i, px := range c.pix {
		binary.LittleEndian.PutUint16(c.rawBuf[i*2:i*2+2], px)
	}
	if _, err := c.fbFile.Seek(0, 0); err != nil {
		return fmt.Errorf("fb: seek: %w", err)
	}
	if _, err := c.fbFile.Write(c.rawBuf[:]); err != nil {
		return fmt.Errorf("fb: write: %w", err)
	}
	return nil
}

// Clear fills the entire 320x170 canvas with col.
func (c *Canvas) Clear(col uint16) {
	for i := range c.pix {
		c.pix[i] = col
	}
}

// SetPixel sets (x, y) to col if within [0, ScreenW) x [0, ScreenH).
func (c *Canvas) SetPixel(x, y int, col uint16) {
	if x < 0 || x >= ScreenW || y < 0 || y >= ScreenH {
		return
	}
	c.pix[y*ScreenW+x] = col
}

// PixelAt returns the RGB565 pixel at (x, y) or 0 if out of bounds.
func (c *Canvas) PixelAt(x, y int) uint16 {
	if x < 0 || x >= ScreenW || y < 0 || y >= ScreenH {
		return 0
	}
	return c.pix[y*ScreenW+x]
}

// FillRect fills a clipped rectangle [x, x+w) x [y, y+h) with col.
func (c *Canvas) FillRect(x, y, w, h int, col uint16) {
	if w <= 0 || h <= 0 {
		return
	}
	x0, y0 := max(0, x), max(0, y)
	x1, y1 := min(ScreenW, x+w), min(ScreenH, y+h)
	for py := y0; py < y1; py++ {
		row := py * ScreenW
		for px := x0; px < x1; px++ {
			c.pix[row+px] = col
		}
	}
}

// DrawRect draws a 1 px rectangle outline around [x, x+w) x [y, y+h).
func (c *Canvas) DrawRect(x, y, w, h int, col uint16) {
	if w <= 0 || h <= 0 {
		return
	}
	c.HLine(x, y, w, col)
	c.HLine(x, y+h-1, w, col)
	c.VLine(x, y, h, col)
	c.VLine(x+w-1, y, h, col)
}

// HLine draws a horizontal line starting at (x, y) with length w.
func (c *Canvas) HLine(x, y, w int, col uint16) {
	if y < 0 || y >= ScreenH || w <= 0 {
		return
	}
	x0, x1 := max(0, x), min(ScreenW, x+w)
	row := y * ScreenW
	for px := x0; px < x1; px++ {
		c.pix[row+px] = col
	}
}

// VLine draws a vertical line starting at (x, y) with height h.
func (c *Canvas) VLine(x, y, h int, col uint16) {
	if x < 0 || x >= ScreenW || h <= 0 {
		return
	}
	y0, y1 := max(0, y), min(ScreenH, y+h)
	for py := y0; py < y1; py++ {
		c.pix[py*ScreenW+x] = col
	}
}

// DrawLine draws a 1 px line between (x0, y0) and (x1, y1) using Bresenham's algorithm.
func (c *Canvas) DrawLine(x0, y0, x1, y1 int, col uint16) {
	dx, sx := x1-x0, 1
	if dx < 0 {
		dx, sx = -dx, -1
	}
	dy, sy := y1-y0, 1
	if dy < 0 {
		dy, sy = -dy, -1
	}
	err := dx - dy
	for {
		c.SetPixel(x0, y0, col)
		if x0 == x1 && y0 == y1 {
			break
		}
		e2 := 2 * err
		if e2 > -dy {
			err -= dy
			x0 += sx
		}
		if e2 < dx {
			err += dx
			y0 += sy
		}
	}
}

// DrawText renders ASCII text at (x, y) using the 5x7 bitmap font.
func (c *Canvas) DrawText(x, y int, text string, col uint16, scale int) {
	if scale < 1 {
		scale = 1
	}
	cx := x
	for i := range len(text) {
		ch := text[i]
		if ch == '\n' {
			cx = x
			y += 9 * scale
			continue
		}
		c.drawGlyph(cx, y, Glyph5x7(ch), col, scale)
		cx += 6 * scale
	}
}

func (c *Canvas) drawGlyph(x, y int, glyph [5]uint8, col uint16, scale int) {
	for colIdx := range 5 {
		bits := glyph[colIdx]
		for rowIdx := range 7 {
			if (bits>>uint(rowIdx))&1 == 0 {
				continue
			}
			if scale == 1 {
				c.SetPixel(x+colIdx, y+rowIdx, col)
			} else {
				c.FillRect(x+colIdx*scale, y+rowIdx*scale, scale, scale, col)
			}
		}
	}
}

// DrawTextCentered renders text horizontally centered at centerX.
func (c *Canvas) DrawTextCentered(centerX, y int, text string, col uint16, scale int) {
	if scale < 1 {
		scale = 1
	}
	w := len(text) * 6 * scale
	c.DrawText(centerX-w/2, y, text, col, scale)
}

// ToRGBA converts the RGB565 canvas into a crisp nearest-neighbor RGBA image.
func (c *Canvas) ToRGBA(scale int) *image.RGBA {
	if scale < 1 {
		scale = 1
	}
	img := image.NewRGBA(image.Rect(0, 0, ScreenW*scale, ScreenH*scale))
	for y := range ScreenH {
		for x := range ScreenW {
			rgb := RGB565ToRGBA(c.pix[y*ScreenW+x])
			for dy := range scale {
				for dx := range scale {
					img.SetRGBA(x*scale+dx, y*scale+dy, rgb)
				}
			}
		}
	}
	return img
}

// SavePNG encodes the canvas at integer nearest-neighbor scale to a PNG file.
func (c *Canvas) SavePNG(path string, scale int) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("fb: create png %s: %w", path, err)
	}
	if err := png.Encode(f, c.ToRGBA(scale)); err != nil {
		_ = f.Close()
		return fmt.Errorf("fb: encode png %s: %w", path, err)
	}
	return f.Close()
}
