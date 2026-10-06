package main

import (
	"fmt"
	"image"
	"testing"
	"time"

	"condor-init/ui"
)

// gpuScrollConsole: a tall page with a sideways row and something different on every band
// of rows (so a window shown off by one is caught), on a console whose screen the (software)
// GPU shows. Caller holds drawMu.
func gpuScrollConsole(t *testing.T) (*console, *softGPU) {
	c := testConsole(t)
	c.mode = modeWords
	var items []rowItem
	for k := 0; k < 20; k++ {
		items = append(items, rowItem{it: &storeItem{key: fmt.Sprint(k), title: fmt.Sprint("Book ", k)}, id: fmt.Sprint("b", k), caption: fmt.Sprint("Book ", k)})
	}
	img := canvas(c.s.W, 4000)
	ui.Fill(img, img.Rect, apBG)
	for y := 0; y < 4000; y += 37 {
		ui.Fill(img, image.Rect(y%300, y, y%300+500, y+19), rgb(uint32(0x101010*(y%13)+0x203040)))
	}
	c.page = &page{img: img, header: tabsH}
	c.coverRow(c.page, "row", 600, items, apBG)
	c.pageGen++
	c.blitPage()
	c.s.Flush()
	g := newSoftGPU(c.s)
	c.useGPU(g)
	return c, g
}

// shows reports whether the GPU shows exactly what the processor draws for the page at y.
func shows(t *testing.T, c *console, g *softGPU, what string) {
	t.Helper()
	cpu := append([]byte(nil), g.screen...)
	saved := append([]byte(nil), c.s.buf...)
	drag := c.sc.drag
	c.sc.drag = false // no scroll bar: compare the page itself
	c.blitPage()
	c.sc.drag = drag
	want := append([]byte(nil), c.s.buf...)
	copy(c.s.buf, saved)
	// The scroll bar's strip (the right 12 pixels = the first native rows) is left out.
	skip := (scrollbarWide + 12) * c.s.stride
	if n := differ(cpu[skip:], want[skip:], &Screen{fbW: c.s.fbW, fbH: c.s.fbH - scrollbarWide - 12, stride: c.s.stride}, 0); n != 0 {
		t.Fatalf("%s: the GPU shows %d pixels unlike the page at %d", what, n, c.scrollY())
	}
}

// lift says the finger was moving the page at v pixels a second as it lifted (the tests'
// own pace depends on the machine), then lifts it.
func lift(c *console, v float64, x, y int) {
	now := time.Now()
	c.sc.samples = []scrollSample{{now.Add(-50 * time.Millisecond), c.scrollY() - int(v/20)}, {now, c.scrollY()}}
	c.pageTouch(TouchPoint{Up: true, X: x, Y: y})
}

// With the GPU, the page follows the finger exactly (every move is a frame, no throttle),
// showing what the processor would draw there; lifting it slowly settles the page into the
// screen and drops the overlay.
func TestGPUScrollFollowsFinger(t *testing.T) {
	drawMu.Lock()
	defer drawMu.Unlock()
	c, g := gpuScrollConsole(t)
	c.pageTouch(TouchPoint{Down: true, X: 600, Y: 1500})
	for _, y := range []int{1480, 1400, 1301, 1000, 777} {
		c.pageTouch(TouchPoint{Moved: true, X: 600, Y: y})
		if want := 1500 - y; c.scrollY() != want {
			t.Fatalf("finger at %d: page at %d, want %d", y, c.scrollY(), want)
		}
		if !c.sc.gpuOn || len(g.over) == 0 {
			t.Fatal("no overlay while dragging")
		}
		shows(t, c, g, fmt.Sprint("dragging to ", y))
	}
	if g.uploads > 3 || g.partial != 0 || g.maxSlices > pageResident {
		t.Fatalf("%d slices sent (%d in part), %d on the GPU at once", g.uploads, g.partial, g.maxSlices)
	}
	time.Sleep(100 * time.Millisecond) // the finger rests: no fling
	c.pageTouch(TouchPoint{Up: true, X: 600, Y: 777})
	if c.sc.gpuOn || g.over != nil {
		t.Fatal("the overlay is still up after the finger lifted")
	}
	if c.scrollY() != 723 {
		t.Fatalf("settled at %d, want 723", c.scrollY())
	}
	shows(t, c, g, "settled")
	if differ(g.screen, c.s.buf, c.s, 0) != 0 {
		t.Fatal("the GPU's screen isn't condor's screen after settling")
	}
}

// A quick lift flings the page on, slowing, until it stops (here at the page's end), and
// it ends drawn into the screen.
func TestGPUFling(t *testing.T) {
	drawMu.Lock()
	c, g := gpuScrollConsole(t)
	c.setScroll(c.mode, 1500)
	c.pageTouch(TouchPoint{Down: true, X: 600, Y: 1500})
	for i := 1; i <= 6; i++ {
		c.pageTouch(TouchPoint{Moved: true, X: 600, Y: 1500 - 40*i})
	}
	lift(c, 4000, 600, 1260)
	at := c.scrollY()
	drawMu.Unlock()
	end := 4000 - c.viewH()
	deadline := time.Now().Add(5 * time.Second)
	for {
		drawMu.Lock()
		on, y := c.sc.gpuOn, c.scrollY()
		drawMu.Unlock()
		if !on {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("still flinging at %d", y)
		}
		time.Sleep(20 * time.Millisecond)
	}
	drawMu.Lock()
	defer drawMu.Unlock()
	if c.scrollY() != end {
		t.Fatalf("the fling from %d stopped at %d, the page ends at %d", at, c.scrollY(), end)
	}
	if g.over != nil || differ(g.screen, c.s.buf, c.s, 0) != 0 {
		t.Fatal("the fling didn't end drawn into the screen")
	}
	if g.partial != 0 || g.maxSlices > pageResident {
		t.Fatalf("%d slices sent in part, %d on the GPU at once", g.partial, g.maxSlices)
	}
	shows(t, c, g, "after the fling")
}

// A finger on a flinging page stops it where it is.
func TestGPUFlingCaught(t *testing.T) {
	drawMu.Lock()
	c, _ := gpuScrollConsole(t)
	c.pageTouch(TouchPoint{Down: true, X: 600, Y: 1700})
	for i := 1; i <= 6; i++ {
		c.pageTouch(TouchPoint{Moved: true, X: 600, Y: 1700 - 25*i})
	}
	lift(c, 2500, 600, 1550)
	drawMu.Unlock()
	time.Sleep(60 * time.Millisecond)
	drawMu.Lock()
	defer drawMu.Unlock()
	if !c.sc.gpuOn {
		t.Fatal("no fling")
	}
	c.pageTouch(TouchPoint{Down: true, X: 600, Y: 1000})
	caught := c.scrollY()
	drawMu.Unlock()
	time.Sleep(80 * time.Millisecond)
	drawMu.Lock()
	if c.scrollY() != caught {
		t.Fatalf("the page moved from %d to %d under a resting finger", caught, c.scrollY())
	}
	c.pageTouch(TouchPoint{Up: true, X: 600, Y: 1000}) // a tap: settles first
	if c.sc.gpuOn {
		t.Fatal("still on the GPU after the finger lifted")
	}
}

// A sideways row slides on the GPU from its strip (drawn once, whole): every move shows
// exactly what the processor would paint at that offset, with no repainting; a quick lift
// flings it on; it ends painted into the page.
func TestGPURow(t *testing.T) {
	drawMu.Lock()
	c, g := gpuScrollConsole(t)
	y := c.barH + 600 + rowCH/2
	r := c.rowByID("row")
	expect := func(what string) {
		t.Helper()
		saved := append([]byte(nil), c.page.img.Pix...)
		r.paint(c.page.img) // what the row looks like at its offset now
		shows(t, c, g, what)
		copy(c.page.img.Pix, saved)
	}
	c.pageTouch(TouchPoint{Down: true, X: 900, Y: y})
	for _, x := range []int{870, 500, 433, 120} {
		c.pageTouch(TouchPoint{Moved: true, X: x, Y: y + 5})
		if c.rowOffset("row") != 900-x || !c.sc.stripOn {
			t.Fatalf("finger at %d: row at %d (strip %v), want %d", x, c.rowOffset("row"), c.sc.stripOn, 900-x)
		}
		expect(fmt.Sprint("row slid to ", 900-x))
	}
	if g.strips != 1 {
		t.Fatalf("the row went to the GPU %d times, want once", g.strips)
	}
	lift(c, 3000, 120, y+5)
	at := c.rowOffset("row")
	drawMu.Unlock()
	deadline := time.Now().Add(5 * time.Second)
	for {
		drawMu.Lock()
		on := c.sc.gpuOn
		drawMu.Unlock()
		if !on {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the row is still flinging")
		}
		time.Sleep(20 * time.Millisecond)
	}
	drawMu.Lock()
	defer drawMu.Unlock()
	end := r.contentW - r.r.Dx()
	if c.rowOffset("row") <= at || c.rowOffset("row") > end {
		t.Fatalf("flung from %d to %d (end %d)", at, c.rowOffset("row"), end)
	}
	if g.over != nil || c.sc.stripOn {
		t.Fatal("the overlay is still up")
	}
	shows(t, c, g, "row settled") // painted into the page at its offset
	if id := c.page.hit(48+10, 610); id == "b0" || id == "" {
		t.Fatalf("after the fling the row's buttons are stale: %q", id)
	}
}

// Anything else drawn ends the GPU scroll (no overlay left over another screen), and a page
// redrawn is sent again.
func TestGPUScrollEndsOnRedraw(t *testing.T) {
	drawMu.Lock()
	defer drawMu.Unlock()
	c, g := gpuScrollConsole(t)
	c.pageTouch(TouchPoint{Down: true, X: 600, Y: 1500})
	c.pageTouch(TouchPoint{Moved: true, X: 600, Y: 1200})
	c.redrawAll()
	if c.sc.gpuOn || g.over != nil {
		t.Fatal("the overlay survived a redraw")
	}
	if differ(g.screen, c.s.buf, c.s, 0) != 0 {
		t.Fatal("the GPU doesn't show the redrawn screen")
	}
}

// A drag down the whole page and back: slices come and go (never more than pageResident on
// the GPU, never sent in part) and every frame shows the page exactly.
func TestGPUScrollWholePage(t *testing.T) {
	drawMu.Lock()
	defer drawMu.Unlock()
	c, g := gpuScrollConsole(t)
	c.pageTouch(TouchPoint{Down: true, X: 600, Y: 1800})
	for _, y := range []int{1700, 1000, 300, -300, -900, -1300, -500, 400, 1500} {
		c.pageTouch(TouchPoint{Moved: true, X: 600, Y: y})
		shows(t, c, g, fmt.Sprint("finger at ", y))
	}
	if g.partial != 0 || g.maxSlices > pageResident {
		t.Fatalf("%d slices sent in part, %d on the GPU at once", g.partial, g.maxSlices)
	}
	// Redrawn: the next drag sends the page again.
	before := g.uploads
	c.pageGen++
	c.pageTouch(TouchPoint{Moved: true, X: 600, Y: 1400})
	shows(t, c, g, "after a redraw")
	if g.uploads == before {
		t.Fatal("a redrawn page wasn't sent again")
	}
}
