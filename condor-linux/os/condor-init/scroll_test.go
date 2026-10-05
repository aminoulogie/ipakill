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
	c.cfg.SmoothScroll = true // following the finger (paged: TestPagedScrolling)
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

// Paged (the default): a swipe jumps a screen down or back, a row by the covers it shows, in
// one redraw, and stops at the ends.
func TestPagedScrolling(t *testing.T) {
	c := testConsole(t)
	drawMu.Lock()
	defer drawMu.Unlock()
	c.mode = modeWords
	var items []rowItem
	for k := 0; k < 20; k++ {
		items = append(items, rowItem{it: &storeItem{key: fmt.Sprint(k)}, id: fmt.Sprint("b", k)})
	}
	c.page = &page{img: canvas(c.s.W, 4000), header: tabsH}
	c.coverRow(c.page, "row", 600, items, apBG)
	c.blitPage()
	swipe := func(x0, y0, x1, y1 int) {
		c.pageTouch(TouchPoint{Down: true, X: x0, Y: y0})
		c.pageTouch(TouchPoint{Moved: true, X: x1, Y: y1})
		c.pageTouch(TouchPoint{Up: true, X: x1, Y: y1})
	}
	step := c.viewH() - tabsH - 160
	swipe(600, 1800, 600, 1500) // a short swipe up is enough
	if c.scrollY() != step {
		t.Fatalf("after a swipe up: %d, want %d", c.scrollY(), step)
	}
	swipe(600, 1800, 600, 1500)
	swipe(600, 1800, 600, 1500)
	if c.scrollY() != 4000-c.viewH() {
		t.Fatalf("should stop at the end: %d", c.scrollY())
	}
	swipe(600, 1000, 600, 1300) // down: back
	if c.scrollY() != 4000-c.viewH()-step {
		t.Fatalf("after a swipe down: %d", c.scrollY())
	}
	c.sc.y[c.mode] = 0
	y := c.barH + 600 + rowCH/2
	swipe(900, y, 700, y)
	if got, want := c.rowOffset("row"), 6*(rowCW+rowGap); got != want {
		t.Fatalf("row after a swipe left: %d, want %d", got, want)
	}
	swipe(700, y, 900, y)
	if c.rowOffset("row") != 0 {
		t.Fatalf("row after a swipe right: %d", c.rowOffset("row"))
	}
}
