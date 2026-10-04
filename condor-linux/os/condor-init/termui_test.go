package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"condor-init/vt"
)

// Swipe through the scrollback, hold to select, Copy, Paste, and a shortcut.
func TestTerminalScrollSelectCopyPaste(t *testing.T) {
	c := testConsole(t)
	c.mode = modeTerminal
	for i := 0; i < 80; i++ {
		c.t.Write([]byte(fmt.Sprintf("line %d\r\n", i)))
	}
	drawMu.Lock()
	defer drawMu.Unlock()
	c.redrawAll()
	if c.t.History() == 0 {
		t.Fatal("no scrollback kept")
	}
	shot(t, c, "terminal")

	// Swipe down: older lines.
	x, y := c.s.W/2, c.offY+5*c.ch
	c.termTouch(TouchPoint{Down: true, X: x, Y: y})
	c.termTouch(TouchPoint{Moved: true, X: x, Y: y + 10*c.ch})
	c.termTouch(TouchPoint{Up: true, X: x, Y: y + 10*c.ch})
	if c.tu.back != 10 {
		t.Fatalf("scrolled back %d lines, want 10", c.tu.back)
	}
	shot(t, c, "terminal-scrollback")
	top := c.t.View(0, c.tu.back)
	if !strings.HasPrefix(cellsText(top), "line ") {
		t.Fatalf("top line when scrolled back: %q", cellsText(top))
	}

	// Hold on a line, drag to the next: select; Copy.
	c.termTouch(TouchPoint{Down: true, X: c.offX + 2, Y: c.offY + 2*c.ch + 4})
	drawMu.Unlock()
	time.Sleep(450 * time.Millisecond) // the hold
	drawMu.Lock()
	if !c.tu.selOn {
		t.Fatal("holding should start a selection")
	}
	c.termTouch(TouchPoint{Moved: true, X: c.offX + 6*c.cw + 2, Y: c.offY + 3*c.ch + 4})
	c.termTouch(TouchPoint{Up: true, X: c.offX + 6*c.cw + 2, Y: c.offY + 3*c.ch + 4})
	shot(t, c, "terminal-selection")
	want := strings.TrimRight(cellsText(c.t.View(2, c.tu.back)), " ") + "\n" + cellsText(c.t.View(3, c.tu.back))[:7]
	bar := c.accY() + accH/2
	c.termTouch(TouchPoint{Down: true, X: c.offX + 10, Y: bar})
	c.termTouch(TouchPoint{Up: true, X: kbPad + 10, Y: bar}) // Copy is first
	if c.tu.clip != want || c.tu.selOn {
		t.Fatalf("copied %q, want %q (selection still on: %v)", c.tu.clip, want, c.tu.selOn)
	}

	// Paste goes back to the live screen and types the text (newlines as returns).
	pasteX := -1
	for x := kbPad; x < c.s.W; x += 10 {
		if i := c.shortcutAt(x); i >= 0 && termShortcuts[i].label == "Paste" {
			pasteX = x
			break
		}
	}
	r, w, err := os.Pipe() // stands in for the shell
	if err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	c.master = w
	c.mu.Unlock()
	c.tu.back = 3
	c.termTouch(TouchPoint{Up: true, X: pasteX, Y: bar})
	w.Close()
	got, _ := io.ReadAll(r)
	if c.tu.back != 0 || string(got) != strings.ReplaceAll(want, "\n", "\r") {
		t.Fatalf("paste typed %q (back %d)", got, c.tu.back)
	}
}

func cellsText(cells []vt.Cell) string {
	var sb strings.Builder
	for _, c := range cells {
		sb.WriteRune(c.Ch)
	}
	return strings.TrimRight(sb.String(), " ")
}
