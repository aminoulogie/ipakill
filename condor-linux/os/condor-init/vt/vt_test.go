package vt

import (
	"fmt"
	"strings"
	"testing"
)

func feed(t *Term, s string) { t.Write([]byte(s)) }

func TestPrintAndNewline(t *testing.T) {
	term := New(10, 3)
	feed(term, "hello\r\nworld")
	if term.Text(0) != "hello" || term.Text(1) != "world" {
		t.Fatalf("rows: %q %q", term.Text(0), term.Text(1))
	}
	if x, y := term.Cursor(); x != 5 || y != 1 {
		t.Fatalf("cursor %d,%d", x, y)
	}
}

func TestWrapAndScroll(t *testing.T) {
	term := New(4, 2)
	feed(term, "abcdefgh") // fills both rows exactly; wrap is pending
	if term.Text(0) != "abcd" || term.Text(1) != "efgh" {
		t.Fatalf("rows: %q %q", term.Text(0), term.Text(1))
	}
	feed(term, "ij") // wraps and scrolls
	if term.Text(0) != "efgh" || term.Text(1) != "ij" {
		t.Fatalf("after scroll: %q %q", term.Text(0), term.Text(1))
	}
}

func TestBackspaceEraseAndCursorMoves(t *testing.T) {
	term := New(10, 3)
	feed(term, "abc\b\bX")
	if term.Text(0) != "aXc" {
		t.Fatalf("got %q", term.Text(0))
	}
	feed(term, "\x1b[2;3Hhi") // row 2, column 3
	if term.Text(1) != "  hi" {
		t.Fatalf("row 2: %q", term.Text(1))
	}
	feed(term, "\x1b[1;2H\x1b[K") // erase to end of line from column 2
	if term.Text(0) != "a" {
		t.Fatalf("after EL: %q", term.Text(0))
	}
	feed(term, "\x1b[2J")
	if term.Text(0) != "" || term.Text(1) != "" {
		t.Fatal("ED 2 should clear the screen")
	}
}

func TestColoursAndUTF8(t *testing.T) {
	term := New(10, 1)
	feed(term, "\x1b[1;32mé\x1b[0mz")
	row, _ := term.Snapshot(0)
	if row[0].Ch != 'é' || row[0].FG != 2 || !row[0].Bold {
		t.Fatalf("cell 0: %+v", row[0])
	}
	if row[1].Ch != 'z' || row[1].FG != Default || row[1].Bold {
		t.Fatalf("cell 1: %+v", row[1])
	}
}

func TestOSCTitleIsIgnored(t *testing.T) {
	term := New(10, 1)
	feed(term, "\x1b]0;my title\x07ok")
	if term.Text(0) != "ok" {
		t.Fatalf("got %q", term.Text(0))
	}
}

func TestDirtyRows(t *testing.T) {
	term := New(5, 4)
	term.TakeDirty()
	feed(term, "\x1b[3;1Hx")
	d := term.TakeDirty()
	if len(d) == 0 || d[len(d)-1] != 2 {
		t.Fatalf("dirty %v", d)
	}
	if len(term.TakeDirty()) != 0 {
		t.Fatal("dirty marks should be cleared")
	}
}

func TestResizeKeepsCursorLine(t *testing.T) {
	term := New(10, 5)
	feed(term, "a\r\nb\r\nc\r\nd\r\ne") // cursor on the last row
	term.Resize(10, 3)
	if term.Text(0) != "c" || term.Text(2) != "e" {
		t.Fatalf("rows after shrink: %q %q %q", term.Text(0), term.Text(1), term.Text(2))
	}
	if x, y := term.Cursor(); x != 1 || y != 2 {
		t.Fatalf("cursor %d,%d", x, y)
	}
	term.Resize(10, 6)
	if term.Text(0) != "c" || term.Text(5) != "" {
		t.Fatalf("rows after grow: %q / %q", term.Text(0), term.Text(5))
	}
	feed(term, "\r\nf") // writing still works at the new size
	if term.Text(3) != "f" {
		t.Fatalf("row 3 = %q", term.Text(3))
	}
}

func TestScrollback(t *testing.T) {
	term := New(10, 3)
	for i := 0; i < 6; i++ {
		feed(term, fmt.Sprintf("line%d\r\n", i))
	}
	// Screen: line4, line5, (empty); scrollback: line0..line3.
	if term.History() != 4 {
		t.Fatalf("history %d", term.History())
	}
	text := func(cells []Cell) string {
		s := ""
		for _, c := range cells {
			s += string(c.Ch)
		}
		return strings.TrimRight(s, " ")
	}
	if got := text(term.View(0, 0)); got != "line4" {
		t.Errorf("live top %q", got)
	}
	if got := text(term.View(0, 2)); got != "line2" {
		t.Errorf("2 back %q", got)
	}
	if got := text(term.View(0, 99)); got != "line0" {
		t.Errorf("far back %q", got)
	}
	feed(term, "\x1b[H\x1b[2J\x1b[3J") // what `clear` sends
	if term.History() != 0 {
		t.Error("clear should forget the scrollback")
	}
}

func TestRichColoursMapToTheSixteen(t *testing.T) {
	term := New(20, 2)
	feed(term, "\x1b[38;5;196mR\x1b[38;5;34mG\x1b[38;2;80;140;255mB\x1b[38;5;245mg\x1b[48;5;226mY\x1b[0m")
	row, _ := term.Snapshot(0)
	want := []struct {
		fg, bg uint8
	}{{9, Default}, {2, Default}, {12, Default}, {8, Default}, {8, 11}}
	for i, w := range want {
		if row[i].FG != w.fg || row[i].BG != w.bg {
			t.Errorf("cell %d (%c): fg %d bg %d, want %d %d", i, row[i].Ch, row[i].FG, row[i].BG, w.fg, w.bg)
		}
	}
}

// Moving the cursor alone (arrow keys, backspace) must redraw its row, or the screen shows
// the cursor where it was.
func TestCursorMoveMarksItsRow(t *testing.T) {
	term := New(20, 3)
	feed(term, "hello")
	term.TakeDirty()
	feed(term, "\b\b")
	if rows := term.TakeDirty(); len(rows) != 1 || rows[0] != 0 {
		t.Fatalf("after backspaces dirty %v", rows)
	}
	feed(term, "\x1b[2;3H") // to another row: both rows
	if rows := term.TakeDirty(); len(rows) != 2 {
		t.Fatalf("after a jump dirty %v", rows)
	}
	if rows := term.TakeDirty(); len(rows) != 0 {
		t.Fatalf("nothing moved, dirty %v", rows)
	}
}
