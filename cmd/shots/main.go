package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
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
	"ir-studio/internal/ui"
)

func main() {
	outDir := flag.String("out", "assets/screenshots", "Output directory for PNG screenshots")
	scale := flag.Int("scale", 1, "Scale factor (1=320x170, 2=640x340)")
	iconPath := flag.String("icon", "assets/images/ir-studio-icon.png", "256x256 icon output path")
	syncPortal := flag.String("sync-portal", "", "Optional learning-portal assets dir to copy 320x170 PNGs")
	flag.Parse()

	if err := generateAll(*outDir, *scale, *iconPath, *syncPortal); err != nil {
		fmt.Fprintf(os.Stderr, "shots: %v\n", err)
		os.Exit(1)
	}
}

func generateAll(outDir string, scale int, iconPath, syncPortal string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	tmpRoot, err := os.MkdirTemp("", "irshots-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpRoot)

	app := buildDemoApp(tmpRoot)
	defer app.Close()

	c, err := fb.NewCanvas("")
	if err != nil {
		return err
	}
	defer c.Close()

	names, err := captureScreens(app, c, outDir, scale)
	if err != nil {
		return err
	}
	if syncPortal != "" {
		if err := copyToPortal(outDir, syncPortal, names); err != nil {
			return err
		}
	}
	if iconPath != "" {
		if err := os.MkdirAll(filepath.Dir(iconPath), 0o755); err != nil {
			return err
		}
		return generateIcon(iconPath)
	}
	return nil
}

func buildDemoApp(root string) *ui.App {
	userDir := filepath.Join(root, "remotes")
	dbDir := filepath.Join(root, "databases")
	store := library.NewStore(userDir, dbDir)
	seedDemoRemotes(store, dbDir)

	macrosPath := filepath.Join(root, "macros.json")
	_ = config.SaveMacrosAtomic(macrosPath, &config.MacroFile{
		Version: config.MacroVersion,
		Macros: []config.Macro{
			{
				ID:   "movie_night",
				Name: "Movie Night",
				Steps: []config.MacroStep{
					{Remote: "Samsung_TV.ir", Button: "Power", DelayAfterMs: 800, Repeat: 1},
					{Remote: "Sonos_Bar.ir", Button: "Power", DelayAfterMs: 500, Repeat: 1},
					{Remote: "Samsung_TV.ir", Button: "HDMI_1", DelayAfterMs: 0, Repeat: 1},
				},
			},
		},
	})

	app := ui.NewApp(ui.Deps{
		Store:       store,
		Syncer:      dbsync.NewSyncer(dbDir, nil),
		Settings:    config.DefaultSettings(),
		ConfigPath:  filepath.Join(root, "config.json"),
		SourcesPath: filepath.Join(root, "sources.json"),
		MacrosPath:  macrosPath,
		SimMode:     true,
		DeviceLabel: "SIM",
	})
	return app
}

func seedDemoRemotes(store *library.Store, dbDir string) {
	samsung := &irfile.IRFile{
		Version: 1,
		Signals: []irfile.IRSignal{
			{Name: "Power", Kind: irfile.SignalParsed, Protocol: "Samsung32", Address: [4]byte{0x07}, Command: [4]byte{0x02}},
			{Name: "Mute", Kind: irfile.SignalParsed, Protocol: "Samsung32", Address: [4]byte{0x07}, Command: [4]byte{0x0F}},
			{Name: "Vol_up", Kind: irfile.SignalParsed, Protocol: "Samsung32", Address: [4]byte{0x07}, Command: [4]byte{0x07}},
			{Name: "Vol_dn", Kind: irfile.SignalParsed, Protocol: "Samsung32", Address: [4]byte{0x07}, Command: [4]byte{0x0B}},
			{Name: "Ch_next", Kind: irfile.SignalParsed, Protocol: "Samsung32", Address: [4]byte{0x07}, Command: [4]byte{0x12}},
			{Name: "Ch_prev", Kind: irfile.SignalParsed, Protocol: "Samsung32", Address: [4]byte{0x07}, Command: [4]byte{0x10}},
			{Name: "HDMI_1", Kind: irfile.SignalParsed, Protocol: "Samsung32", Address: [4]byte{0x07}, Command: [4]byte{0xE9}},
			{Name: "Menu", Kind: irfile.SignalParsed, Protocol: "Samsung32", Address: [4]byte{0x07}, Command: [4]byte{0x1A}},
		},
	}
	lg := &irfile.IRFile{
		Version: 1,
		Signals: []irfile.IRSignal{
			{Name: "Power", Kind: irfile.SignalParsed, Protocol: "NEC", Address: [4]byte{0x04}, Command: [4]byte{0x08}},
			{Name: "Vol_up", Kind: irfile.SignalParsed, Protocol: "NEC", Address: [4]byte{0x04}, Command: [4]byte{0x02}},
		},
	}
	sony := &irfile.IRFile{
		Version: 1,
		Signals: []irfile.IRSignal{
			{Name: "Power", Kind: irfile.SignalParsed, Protocol: "SIRC", Address: [4]byte{0x01}, Command: [4]byte{0x15}},
		},
	}
	_, _ = store.SaveUserRemote("Samsung_TV.ir", samsung)
	_, _ = store.SaveUserRemote("LG_LivingRoom.ir", lg)
	_, _ = store.SaveUserRemote("Sonos_Bar.ir", lg)
	_, _ = store.SaveUserRemote("Sony_Bravia.ir", sony)

	tvPack := filepath.Join(dbDir, "TVs")
	_ = os.MkdirAll(tvPack, 0o755)
	_ = irfile.SaveFileAtomic(filepath.Join(tvPack, "LG_AKB7509.ir"), lg)
	_ = irfile.SaveFileAtomic(filepath.Join(tvPack, "Samsung_BN59.ir"), samsung)
	_ = irfile.SaveFileAtomic(filepath.Join(tvPack, "Sony_RM_ED053.ir"), sony)
}

func captureScreens(app *ui.App, c *fb.Canvas, outDir string, scale int) ([]string, error) {
	reg := irfile.DefaultRegistry()
	necPulses, _ := reg.Encode(irfile.IRSignal{
		Name: "Power", Kind: irfile.SignalParsed, Protocol: "NEC",
		Address: [4]byte{0x04}, Command: [4]byte{0x08},
	})
	dec, _ := reg.Decode(necPulses)
	app.InjectCapture(lirc.RXCapture{
		Timestamp:    time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC),
		Pulses:       necPulses,
		Decoded:      &dec,
		CarrierEstHz: 38000,
	})

	shots := []struct {
		name  string
		setup func()
	}{
		{"rx.png", func() { app.SwitchTab(ui.TabRX); app.Chrome.ToastText = "" }},
		{"remote.png", func() { app.SwitchTab(ui.TabRemote); app.Remote.AC.Active = false; app.Chrome.ToastText = "" }},
		{"ac.png", func() {
			app.SwitchTab(ui.TabRemote)
			app.Remote.AC.Active = true
			app.Remote.AC.TempC = 22
			app.Remote.AC.Power = true
			app.Remote.AC.Swing = true
			app.Chrome.ToastText = ""
		}},
		{"probe.png", func() {
			app.SwitchTab(ui.TabProbe)
			app.HandleKey(input.KeyR)
			if app.Probe.Session != nil && len(app.Probe.Candidates) >= 2 {
				app.Probe.Session.RecordSent(0)
				app.Probe.Session.RecordSent(1)
				app.Probe.Snap = app.Probe.Session.Snapshot()
			}
			app.Chrome.ToastText = ""
		}},
		{"saved.png", func() {
			app.SwitchTab(ui.TabSaved)
			app.HandleKey(input.Key1)
			app.HandleKey(input.KeyDown)
			app.HandleKey(input.Key1)
			app.Chrome.ToastText = ""
		}},
		{"macro.png", func() {
			app.SwitchTab(ui.TabMacro)
			app.Macro.Running = true
			app.Macro.Progress = library.MacroProgress{MacroID: "movie_night", StepIndex: 1, StepTotal: 3, Button: "Power"}
			app.Chrome.ToastText = ""
		}},
		{"set.png", func() { app.SwitchTab(ui.TabSet); app.Chrome.ToastText = "" }},
		{"help.png", func() {
			app.SwitchTab(ui.TabRX)
			app.Chrome.HelpOpen = true
			app.Chrome.HelpPage = 0
			app.Chrome.ToastText = ""
		}},
	}

	names := make([]string, 0, len(shots))
	for _, s := range shots {
		s.setup()
		app.Render(c)
		if err := c.SavePNG(filepath.Join(outDir, s.name), scale); err != nil {
			return nil, err
		}
		names = append(names, s.name)
	}
	return names, nil
}

func copyToPortal(srcDir, dstDir string, names []string) error {
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return err
	}
	for _, name := range names {
		if err := copyFile(filepath.Join(srcDir, name), filepath.Join(dstDir, name)); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func generateIcon(path string) error {
	const sz = 256
	img := image.NewRGBA(image.Rect(0, 0, sz, sz))
	bg := color.RGBA{R: 0x0b, G: 0x11, B: 0x17, A: 255}
	cyan := color.RGBA{R: 0x00, G: 0xd9, B: 0xff, A: 255}
	magenta := color.RGBA{R: 0xe8, G: 0x79, B: 0xf9, A: 255}
	red := color.RGBA{R: 0xef, G: 0x44, B: 0x44, A: 255}

	for y := range sz {
		for x := range sz {
			img.SetRGBA(x, y, bg)
			dx, dy := float64(x)-128, float64(y)-158
			d := math.Hypot(dx, dy)
			if dy < 0 && math.Abs(dx) < -dy*1.25 {
				if math.Abs(d-92) < 6 || math.Abs(d-64) < 6 {
					img.SetRGBA(x, y, cyan)
				} else if math.Abs(d-36) < 6 {
					img.SetRGBA(x, y, magenta)
				}
			}
			if math.Hypot(float64(x)-128, float64(y)-168) < 18 {
				img.SetRGBA(x, y, red)
			}
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
