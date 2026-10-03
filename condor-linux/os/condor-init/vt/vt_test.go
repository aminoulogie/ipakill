package vt

import "testing"

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
