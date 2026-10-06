package main

import (
	"image"
	"time"

	"condor-init/ui"
)

// Scrolling, as on Kindle and Apple Books: a page taller than the screen moves up and down
// under the finger, and rows of covers move sideways. The page is drawn once, tall; scrolling
// up and down only copies a different window of it to the screen, so it costs no redraw. A
// sideways row is drawn at its offset, so moving it redraws the page (a few times a second
// while the finger moves, and once when it lifts). The band at the top (the tab bar) stays
// put. A touch that hasn't moved is a tap, as before.

// hrow is a row of a page that scrolls sideways: rect in page coordinates, and the width of
// everything in it.
type hrow struct {
	id       string
	r        image.Rectangle
	contentW int
	paint    func(img *image.RGBA) // redraws the row at its current offset
}

// scrollState: per-mode scroll positions, per-row offsets, and the finger moving them.
type scrollState struct {
	y     map[mode]int
	x     map[string]int
	drag  bool // a finger is down on a page
	moved bool // ...and moving it scrolls (no tap)
	vert  bool
	row   *hrow
	x0    int
	y0    int
	from  int // scroll or row offset when the finger touched down
	last  time.Time

	// The GPU (gpuscroll.go): the page is shown from its copy on the GPU while gpuOn.
	gpuOn   bool
	texGen  int // c.pageGen of the page on the GPU (0: none)
	fling   int // bumped to stop a fling
	samples []scrollSample
}

const (
	scrollSlop    = 20 // pixels a finger moves before it's a scroll, not a tap
	scrollEvery   = 33 * time.Millisecond
	rowDrawEvery  = 70 * time.Millisecond
	scrollbarWide = 6
)

func (c *console) scrollY() int {
	if c.sc.y == nil {
		c.sc.y = map[mode]int{}
	}
	return c.sc.y[c.mode]
}

// setScroll puts a mode's page at y (0 = the top).
func (c *console) setScroll(m mode, y int) {
	c.scrollY()
	c.sc.y[m] = y
}

// rowOffset is how far row id is scrolled sideways (pages read it while drawing).
func (c *console) rowOffset(id string) int {
	if c.sc.x == nil {
		c.sc.x = map[string]int{}
	}
	return c.sc.x[id]
}

// addRow registers a sideways row on p.
func (p *page) addRow(id string, r image.Rectangle, contentW int, paint func(*image.RGBA)) {
	p.rows = append(p.rows, hrow{id, r, contentW, paint})
}

// setRowButtons replaces a row's buttons (they move with it).
func (p *page) setRowButtons(id string, btns []button) {
	if p.rowBtns == nil {
		p.rowBtns = map[string][]button{}
	}
	p.rowBtns[id] = btns
}

// viewH is the height of the page area under the status bar.
func (c *console) viewH() int { return c.s.H - c.barH }

// pageY turns a screen y into page coordinates (the fixed header doesn't scroll).
func (c *console) pageY(y int) int {
	py := y - c.barH
	if c.page != nil && py >= c.page.header {
		py += c.scrollY()
	}
	return py
}

// blitPage copies the visible window of the current page to the screen. Caller holds drawMu.
func (c *console) blitPage() {
	img := c.page.img
	vh := c.viewH()
	if img.Rect.Dy() <= vh {
		c.s.blitRGBA(img, 0, c.barH)
		return
	}
	sy := min(max(c.scrollY(), 0), img.Rect.Dy()-vh)
	c.sc.y[c.mode] = sy
	hh := c.page.header
	c.s.blitRGBA(img.SubImage(image.Rect(0, sy+hh, img.Rect.Dx(), sy+vh)).(*image.RGBA), 0, c.barH+hh)
	if hh > 0 {
		c.s.blitRGBA(img.SubImage(image.Rect(0, 0, img.Rect.Dx(), hh)).(*image.RGBA), 0, c.barH)
	}
	// Where you are on the page: a thin bar on the right, while scrolling.
	if c.sc.drag && c.sc.moved && c.sc.vert {
		track := vh - hh - 40
		thumb := max(track*vh/img.Rect.Dy(), 60)
		ty := c.barH + hh + 20 + (track-thumb)*sy/max(img.Rect.Dy()-vh, 1)
		bar := image.NewRGBA(image.Rect(0, 0, scrollbarWide, thumb))
		ui.Fill(bar, bar.Rect, blend(apBG, apLabel, 0.45))
		c.s.blitRGBA(bar, c.s.W-scrollbarWide-6, ty)
	}
}

// pageTouch handles a finger on a page (not the reader, terminal or a keyboard): scroll the
// page or a row, or tap. Caller holds drawMu.
func (c *console) pageTouch(p TouchPoint) {
	sc := &c.sc
	switch {
	case p.Down:
		if c.page == nil {
			return
		}
		sc.fling++ // a finger on a flinging page catches it (the overlay stays until it lifts)
		sc.drag, sc.moved, sc.row, sc.samples = true, false, nil, nil
		sc.x0, sc.y0, sc.from = p.X, p.Y, c.scrollY()
		py := c.pageY(p.Y)
		for i := range c.page.rows {
			if r := &c.page.rows[i]; (image.Point{p.X, py}).In(r.r) && r.contentW > r.r.Dx() {
				sc.row = r
			}
		}
	case p.Moved && sc.drag:
		dx, dy := p.X-sc.x0, p.Y-sc.y0
		if !sc.moved {
			if abs(dx) < scrollSlop && abs(dy) < scrollSlop {
				return
			}
			sc.moved = true
			sc.vert = sc.row == nil || abs(dy) > abs(dx)
			if !sc.vert {
				sc.from = c.rowOffset(sc.row.id)
			}
		}
		if c.gpuScrolls() { // the GPU follows the finger, every frame
			switch {
			case sc.vert && c.page.img.Rect.Dy() <= c.viewH():
				return
			case sc.vert:
				if c.gpuScrollTo(sc.from - dy) {
					c.sample(c.scrollY())
					return
				}
			case time.Since(sc.last) < scrollEvery:
				return
			default:
				sc.last = time.Now()
				r := sc.row
				if c.gpuRowTo(r, min(max(sc.from-dx, 0), max(r.contentW-r.r.Dx(), 0))) {
					return
				}
			}
		}
		if !c.cfg.SmoothScroll {
			return // paged (the default): the page jumps when the finger lifts
		}
		if time.Since(sc.last) < map[bool]time.Duration{true: scrollEvery, false: rowDrawEvery}[sc.vert] {
			return // the screen can't keep up with every move: draw a few times a second
		}
		c.scrollTo(dx, dy)
	case p.Up:
		if !sc.drag {
			return
		}
		moved := sc.moved
		if sc.gpuOn {
			sc.drag = false
			if moved && sc.vert {
				c.gpuRelease()
				return
			}
			c.gpuSettle()
			if !moved {
				c.pageTap(p.X, p.Y)
			}
			return
		}
		switch {
		case moved && !c.cfg.SmoothScroll:
			sc.drag = false
			c.pageJump(p.X-sc.x0, p.Y-sc.y0)
			return
		case moved:
			c.scrollTo(p.X-sc.x0, p.Y-sc.y0)
		}
		sc.drag = false
		if moved {
			if sc.vert {
				c.blitPage() // without the scroll bar
				c.s.Flush()
			}
			return
		}
		c.pageTap(p.X, p.Y)
	}
}

// scrollTo moves the page (or the row under the finger) by the finger's travel and shows it.
func (c *console) scrollTo(dx, dy int) {
	sc := &c.sc
	sc.last = time.Now()
	if sc.vert {
		if c.page.img.Rect.Dy() <= c.viewH() {
			return
		}
		sc.y[c.mode] = min(max(sc.from-dy, 0), c.page.img.Rect.Dy()-c.viewH())
		c.blitPage()
		c.s.Flush()
		return
	}
	r := sc.row
	off := min(max(sc.from-dx, 0), max(r.contentW-r.r.Dx(), 0))
	if off != c.rowOffset(r.id) {
		c.sc.x[r.id] = off
		r.paint(c.page.img) // just this row, at its new offset
		c.blitPage()
		c.s.Flush()
	}
}

// pageJump is paged scrolling, as on a Kindle: a swipe moves the page by a screen (or a row by
// the covers it shows), in one redraw. Following the finger costs a full-screen copy per step,
// which this tablet's processor can do only a few times a second.
func (c *console) pageJump(dx, dy int) {
	sc := &c.sc
	if sc.vert {
		ih, vh := c.page.img.Rect.Dy(), c.viewH()
		if ih <= vh {
			return
		}
		step := vh - c.page.header - 160 // keep a little of the last screen in view
		if dy > 0 {
			step = -step // finger down: back up the page
		}
		y := min(max(c.scrollY()+step, 0), ih-vh)
		if y == c.scrollY() {
			return
		}
		sc.y[c.mode] = y
		c.blitPage()
		c.s.Flush()
		return
	}
	r := sc.row
	step := (r.r.Dx() - 2*48) / (rowCW + rowGap) * (rowCW + rowGap) // the covers on screen
	if step <= 0 || r.id == "s:topics" {
		step = r.r.Dx() / 2
	}
	if dx > 0 {
		step = -step // finger right: back to the start of the row
	}
	off := min(max(c.rowOffset(r.id)+step, 0), max(r.contentW-r.r.Dx(), 0))
	if off == c.rowOffset(r.id) {
		return
	}
	sc.x[r.id] = off
	r.paint(c.page.img)
	c.blitPage()
	c.s.Flush()
}
