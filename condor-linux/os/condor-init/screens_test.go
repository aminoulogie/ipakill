package main

import (
	"image"
	"image/png"
	"os"
	"testing"
)

// screenPNG saves what the tablet would show (logical orientation).
func screenPNG(t *testing.T, s *Screen, path string) {
	img := image.NewRGBA(image.Rect(0, 0, s.W, s.H))
	for y := 0; y < s.H; y++ {
		for x := 0; x < s.W; x++ {
			fx, fy := s.rot.toFB(x, y, s.fbW, s.fbH)
			o := fy*s.stride + fx*4
			i := img.PixOffset(x, y)
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = s.buf[o+2], s.buf[o+1], s.buf[o], 255
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	png.Encode(f, img)
	f.Close()
}

// tapButton lifts a finger on the centre of a page button.
func tapButton(t *testing.T, c *console, id string) {
	t.Helper()
	for _, b := range c.page.buttons {
		if b.id == id {
			p := b.r.Min.Add(b.r.Max).Div(2)
			c.pageTap(p.X, p.Y+c.barH)
			return
		}
	}
	t.Fatalf("no button %q on this page", id)
}

func TestLauncherAndSettings(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	c := testConsole(t)
	c.cfg = savedSettings{Brightness: 80, ScreenOff: 5}
	c.redrawAll()
	if c.mode != modeLauncher || c.page == nil {
		t.Fatal("should start on the launcher")
	}
	shot(t, c, "system-home")
	tapButton(t, c, "terminal")
	if c.mode != modeTerminal {
		t.Fatal("terminal icon should open the terminal")
	}
	c.setMode(modeLauncher)
	tapButton(t, c, "settings")
	if c.mode != modeSettings {
		t.Fatal("settings icon should open settings")
	}
	shot(t, c, "system-settings-wifi")
	tapButton(t, c, "set:pane:display")
	tapButton(t, c, "bright+")
	tapButton(t, c, "off1")
	if c.cfg.Brightness != 90 || c.cfg.ScreenOff != 1 {
		t.Fatalf("settings: %+v", c.cfg)
	}
	shot(t, c, "system-settings-display")
	tapButton(t, c, "set:pane:general")
	shot(t, c, "system-settings-general")
	tapButton(t, c, "set:pane:battery")
	shot(t, c, "system-settings-battery")
	tapButton(t, c, "set:pane:power")
	tapButton(t, c, "restart") // first tap only asks for confirmation
	if c.confirm != "restart" {
		t.Fatalf("confirm = %q", c.confirm)
	}
	shot(t, c, "system-settings-power")
	tapButton(t, c, "home")
	if c.mode != modeLauncher || c.confirm != "" {
		t.Fatalf("home: mode %v confirm %q", c.mode, c.confirm)
	}
}
