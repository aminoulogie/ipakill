//go:build linux

package main

import (
	"image"
	"image/png"
	"os"
	"strings"
	"testing"
	"time"
)

// TestConsoleRunsAShell runs the real console (pty + shell + rendering) on a fake screen.
// Set CONSOLE_PNG=path to also save what the tablet would show.
func TestConsoleRunsAShell(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	shellPath = "/bin/sh"
	s := newScreen(nil, 1920, 1200, 7680, 32, bitfield{16, 8, 0}, bitfield{8, 8, 0}, bitfield{0, 8, 0}, Rot90)
	c, err := newConsole(s)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("grid %dx%d, cell %dx%d", c.t.Cols, c.t.Rows, c.cw, c.ch)
	if c.t.Cols < 60 || c.t.Rows < 40 {
		t.Fatalf("grid too small: %dx%d", c.t.Cols, c.t.Rows)
	}
	go c.run()
	deadline := time.Now().Add(5 * time.Second)
	for c.master == nil && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	c.input([]byte("echo hello from condor; printf '\\033[32mgreen\\033[0m\\n'; stty size\n"))
	var screen string
	for time.Now().Before(deadline) {
		screen = ""
		for y := 0; y < c.t.Rows; y++ {
			screen += c.t.Text(y) + "\n"
		}
		if strings.Contains(screen, "hello from condor\n") && strings.Contains(screen, "green") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(screen, "hello from condor\n") || !strings.Contains(screen, "condor login: root") {
		t.Fatalf("screen:\n%s", screen)
	}
	if out := os.Getenv("CONSOLE_PNG"); out != "" {
		img := image.NewRGBA(image.Rect(0, 0, s.W, s.H))
		for y := 0; y < s.H; y++ {
			for x := 0; x < s.W; x++ {
				fx, fy := s.rot.toFB(x, y, s.fbW, s.fbH)
				o := fy*s.stride + fx*4
				img.Pix[img.PixOffset(x, y)+0] = s.buf[o+2]
				img.Pix[img.PixOffset(x, y)+1] = s.buf[o+1]
				img.Pix[img.PixOffset(x, y)+2] = s.buf[o]
				img.Pix[img.PixOffset(x, y)+3] = 255
			}
		}
		f, _ := os.Create(out)
		png.Encode(f, img)
		f.Close()
	}
}
