package main

import (
	"fmt"
	"testing"
)

// A row of covers wider than the screen moves sideways under the finger (and its covers'
// buttons with it); the page moves up and down, stopping at its ends; a touch that doesn't
// move is a tap.
func TestScrollPageAndRow(t *testing.T) {
	c := testConsole(t)
	drawMu.Lock()
	defer drawMu.Unlock()
	c.mode = modeWords
	var items []rowItem
	for k := 0; k < 20; k++ {
		items = append(items, rowItem{it: &storeItem{key: fmt.Sprint(k), title: fmt.Sprint("Book ", k)}, id: fmt.Sprint("b", k), caption: fmt.Sprint("Book ", k)})
	}
	c.page = &page{img: canvas(c.s.W, 4000), header: tabsH}
	c.coverRow(c.page, "row", 600, items, apBG)
	c.blitPage()

	y := c.barH + 600 + rowCH/2 // on the row
	if id := c.page.hit(48+10, 610); id != "b0" {
		t.Fatalf("before: %q under the first cover", id)
	}
	c.pageTouch(TouchPoint{Down: true, X: 900, Y: y})
	c.pageTouch(TouchPoint{Moved: true, X: 500, Y: y + 5})
	c.pageTouch(TouchPoint{Up: true, X: 500, Y: y + 5})
	if got := c.rowOffset("row"); got != 400 {
		t.Fatalf("row moved %d, want 400", got)
	}
	if c.scrollY() != 0 {
		t.Fatal("a sideways move shouldn't scroll the page")
	}
	if id := c.page.hit(48+10, 610); id == "b0" || id == "" {
		t.Fatalf("after: %q at the row's left, want a later book", id)
	}
	// Far past the end: it stops at the last cover.
	c.pageTouch(TouchPoint{Down: true, X: 1100, Y: y})
	c.pageTouch(TouchPoint{Moved: true, X: -9000, Y: y})
	c.pageTouch(TouchPoint{Up: true, X: -9000, Y: y})
	if max := 2*48 + 20*(rowCW+rowGap) - rowGap - c.s.W; c.rowOffset("row") != max {
		t.Fatalf("row at %d, its end is %d", c.rowOffset("row"), max)
	}

	// Up and down: clamped to the page.
	c.pageTouch(TouchPoint{Down: true, X: 600, Y: 1800})
	c.pageTouch(TouchPoint{Moved: true, X: 600, Y: -8000})
	c.pageTouch(TouchPoint{Up: true, X: 600, Y: -8000})
	if c.scrollY() != 4000-c.viewH() {
		t.Fatalf("scrolled to %d, the end is %d", c.scrollY(), 4000-c.viewH())
	}
	// Taps below the header map through the scroll; taps on the header don't.
	if got := c.pageY(c.barH + tabsH + 10); got != tabsH+10+c.scrollY() {
		t.Fatalf("pageY below the header %d", got)
	}
	if got := c.pageY(c.barH + 50); got != 50 {
		t.Fatalf("pageY on the header %d", got)
	}
}
