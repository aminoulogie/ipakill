package main

import (
	"image"
	"log"
	"math"
	"slices"
	"strconv"
	"time"
)

// Scrolling on the GPU. With the GPU showing the screen (gpu.go), a page taller than the
// screen is uploaded to it once, and scrolling only moves a picture of it: each finger move
// becomes an overlay (the page's window, and the scroll bar) drawn over the screen, 60 frames a
// second, coalesced so the latest position always wins. A sideways row repaints just that row
// (on the CPU) and uploads its strip. Lifting the finger with speed flings the page, slowing
// like iOS's; when it stops, the window is drawn into the screen for real and the overlay goes.

type scrollSample struct {
	t time.Time
	y int
}

const (
	flingTau      = 0.45 // seconds: how quickly a fling slows down
	flingMinSpeed = 250  // pixels a second: slower lifts just stop
	flingStop     = 20   // pixels a second: a fling this slow is over
)

// gpuScrolls: the GPU can scroll the current page.
func (c *console) gpuScrolls() bool {
	return c.disp != nil && c.page != nil && c.page.img.Rect.Dy() <= pageMaxRows &&
		c.page.img.Rect.Dx() == c.s.W
}

// ensureRows puts the slices of the current page holding rows y0..y1-1 on the GPU (the
// first time, and after the page is redrawn), freeing the least recently used beyond
// pageResident. False if the GPU can't: this page then scrolls on the processor. Caller
// holds drawMu.
func (c *console) ensureRows(y0, y1 int) bool {
	if !c.gpuScrolls() || c.sc.texFail == c.pageGen {
		return false
	}
	start := time.Now()
	img := c.page.img
	h := img.Rect.Dy()
	if c.sc.texGen != c.pageGen {
		if err := c.disp.pageNew(h); err != nil {
			return c.texFailed(err)
		}
		c.sc.texGen, c.sc.resident = c.pageGen, nil
	}
	sent := 0
	for k := max(y0, 0) / pageSlice; k*pageSlice < min(y1, h); k++ {
		if i := slices.Index(c.sc.resident, k); i >= 0 {
			c.sc.resident = append(slices.Delete(c.sc.resident, i, i+1), k) // most recent last
			continue
		}
		if len(c.sc.resident) >= pageResident {
			if err := c.disp.pageFree(c.sc.resident[0]); err != nil {
				return c.texFailed(err)
			}
			c.sc.resident = c.sc.resident[1:]
		}
		if err := c.disp.pageRows(img, k*pageSlice, min((k+1)*pageSlice, h)); err != nil {
			return c.texFailed(err)
		}
		c.sc.resident = append(c.sc.resident, k)
		sent++
	}
	if d := time.Since(start); sent > 0 && d > 30*time.Millisecond {
		log.Printf("GPU: %d page slices sent in %v", sent, d.Round(time.Millisecond))
	}
	return true
}

func (c *console) texFailed(err error) bool {
	log.Printf("GPU: page: %v; this page scrolls on the processor", err)
	c.sc.texFail, c.sc.texGen, c.sc.resident = c.pageGen, 0, nil
	if c.disp.failed() != nil {
		c.gpuLost(err)
	}
	return false
}

// uploadStrip sends rows y0..y1-1 again after they were repainted, where they're on the GPU.
func (c *console) uploadStrip(y0, y1 int) bool {
	for _, k := range c.sc.resident {
		a, b := max(y0, k*pageSlice), min(y1, (k+1)*pageSlice)
		if a < b {
			if err := c.disp.pageRows(c.page.img, a, b); err != nil {
				return c.texFailed(err)
			}
		}
	}
	return true
}

// visible: the page's rows on screen at scroll sy (under the fixed header).
func (c *console) visible(sy int) (int, int) {
	return sy + c.page.header, sy + c.viewH()
}

// pageQuad shows the page's rows from srcY on at logical screen rect dst.
func pageQuad(dst frect, srcY int) quad {
	return quad{kind: gpuPage,
		x0: float32(dst.x0), y0: float32(dst.y0), x1: float32(dst.x1), y1: float32(dst.y1),
		u0: float32(dst.x0), v0: float32(srcY), u1: float32(dst.x1), v1: float32(srcY + dst.y1 - dst.y0),
		mul: 1}
}

// colourQuad fills logical rect r with an opaque colour.
func (c *console) colourQuad(r frect, cr, cg, cb uint8) quad {
	h := float32(c.s.fbH)
	return quad{kind: 3, x0: float32(r.y0), y0: h - float32(r.x0), x1: float32(r.y1), y1: h - float32(r.x1),
		a0: 1, a1: 1, r: float32(cr) / 255, g: float32(cg) / 255, b: float32(cb) / 255}
}

// scrollQuads is the overlay for the page scrolled to sy: what blitPage would draw under the
// fixed header, plus the scroll bar while it moves.
func (c *console) scrollQuads(sy int, bar bool) []quad {
	img, vh, hh, w := c.page.img, c.viewH(), c.page.header, c.s.W
	q := []quad{pageQuad(frect{0, c.barH + hh, w, c.barH + vh}, sy+hh)}
	if bar && img.Rect.Dy() > vh {
		track := vh - hh - 40
		thumb := max(track*vh/img.Rect.Dy(), 60)
		ty := c.barH + hh + 20 + (track-thumb)*sy/max(img.Rect.Dy()-vh, 1)
		col := blend(apBG, apLabel, 0.45)
		q = append(q, c.colourQuad(frect{w - scrollbarWide - 6, ty, w - 6, ty + thumb}, col.R, col.G, col.B))
	}
	return q
}

// gpuOverlay is what the GPU draws over the screen now: the page at its scroll, the scroll
// bar if asked, and the row being slid sideways, from its strip.
func (c *console) gpuOverlay(bar bool) []quad {
	sy := c.scrollY()
	q := c.scrollQuads(sy, bar)
	if c.sc.stripOn {
		if r := c.rowByID(c.sc.strip); r != nil {
			if sq, ok := c.stripQuad(r, sy); ok {
				q = append(q, sq)
			}
		}
	}
	return q
}

func (c *console) rowByID(id string) *hrow {
	if c.page == nil {
		return nil
	}
	for i := range c.page.rows {
		if c.page.rows[i].id == id {
			return &c.page.rows[i]
		}
	}
	return nil
}

// stripQuad shows row r from its strip, at its offset, where it is on screen at scroll sy.
func (c *console) stripQuad(r *hrow, sy int) (quad, bool) {
	vis := r.r.Intersect(image.Rect(0, sy+c.page.header, c.s.W, sy+c.viewH()))
	if vis.Empty() {
		return quad{}, false
	}
	off := c.rowOffset(r.id)
	y0 := c.barH + vis.Min.Y - sy
	return quad{kind: gpuStrip,
		x0: 0, y0: float32(y0), x1: float32(c.s.W), y1: float32(y0 + vis.Dy()),
		u0: float32(off), v0: float32(vis.Min.Y - r.r.Min.Y), u1: float32(off + c.s.W), v1: float32(vis.Max.Y - r.r.Min.Y),
		mul: 1}, true
}

// ensureStrip puts row r on the GPU drawn whole (every cover), once per drawing of the page.
// Caller holds drawMu.
func (c *console) ensureStrip(r *hrow) bool {
	if c.sc.strip == r.id && c.sc.stripGen == c.pageGen {
		return true
	}
	if c.sc.stripFail == r.id+"@"+strconv.Itoa(c.pageGen) {
		return false
	}
	if r.contentW > rowStripCols || r.r.Dy() > rowStripRows || r.contentW < c.s.W {
		return false
	}
	start := time.Now()
	img := image.NewRGBA(image.Rect(0, r.r.Min.Y, r.contentW, r.r.Max.Y))
	saved := c.rowOffset(r.id)
	c.sc.x[r.id] = 0
	r.paint(img) // at offset 0 on a picture as wide as the row: every cover
	c.sc.x[r.id] = saved
	if err := c.disp.rowStrip(img); err != nil {
		log.Printf("GPU: row %s: %v; it slides on the processor", r.id, err)
		c.sc.stripFail = r.id + "@" + strconv.Itoa(c.pageGen)
		if c.disp.failed() != nil {
			c.gpuLost(err)
		}
		return false
	}
	c.sc.strip, c.sc.stripGen = r.id, c.pageGen
	if d := time.Since(start); d > 30*time.Millisecond {
		log.Printf("GPU: row %s (%dx%d) drawn and sent in %v", r.id, r.contentW, r.r.Dy(), d.Round(time.Millisecond))
	}
	return true
}

// gpuScrollTo shows the page at y, under the finger or flinging. Caller holds drawMu.
func (c *console) gpuScrollTo(y int) bool {
	y = min(max(y, 0), max(c.page.img.Rect.Dy()-c.viewH(), 0))
	if !c.ensureRows(c.visible(y)) {
		return false
	}
	c.sc.y[c.mode] = y
	c.sc.gpuOn = true
	c.disp.overlay(c.gpuOverlay(true))
	return true
}

// gpuRowTo slides a sideways row to offset off: on the GPU from its strip, every move; or, if
// the row can't go there whole, repainted on the processor (a few times a second). Caller
// holds drawMu.
func (c *console) gpuRowTo(r *hrow, off int) bool {
	if !c.ensureRows(c.visible(c.scrollY())) {
		return false
	}
	if c.ensureStrip(r) {
		c.sc.x[r.id] = off
		c.sc.stripOn, c.sc.gpuOn = true, true
		c.disp.overlay(c.gpuOverlay(false))
		return true
	}
	if time.Since(c.sc.last) < scrollEvery {
		return true
	}
	c.sc.last = time.Now()
	if off != c.rowOffset(r.id) {
		c.sc.x[r.id] = off
		r.paint(c.page.img)
		if !c.uploadStrip(r.r.Min.Y, r.r.Max.Y) {
			return false
		}
	}
	c.sc.gpuOn = true
	c.disp.overlay(c.gpuOverlay(false))
	return true
}

// gpuSettle ends a GPU scroll: a row slid on the GPU is painted into the page where it
// stopped, the page's window is drawn into the screen for real (it looks the same) and the
// overlay goes. Caller holds drawMu.
func (c *console) gpuSettle() {
	if !c.sc.gpuOn {
		return
	}
	c.sc.gpuOn = false
	c.sc.fling++
	if c.disp == nil || c.page == nil {
		c.sc.stripOn = false
		return
	}
	if c.sc.stripOn {
		c.sc.stripOn = false
		if r := c.rowByID(c.sc.strip); r != nil {
			r.paint(c.page.img)
			c.uploadStrip(r.r.Min.Y, r.r.Max.Y)
		}
	}
	c.blitPage()
	c.disp.overlayOff()
	c.s.Flush()
}

// endGPUScroll drops the overlay before something else is drawn. Caller holds drawMu.
func (c *console) endGPUScroll() {
	if !c.sc.gpuOn {
		return
	}
	c.sc.gpuOn, c.sc.stripOn = false, false
	c.sc.fling++
	if c.disp != nil {
		c.disp.overlayOff()
	}
}

// sample notes where the page is, for the fling's speed.
func (c *console) sample(y int) {
	now := time.Now()
	c.sc.samples = append(c.sc.samples, scrollSample{now, y})
	for len(c.sc.samples) > 2 && now.Sub(c.sc.samples[0].t) > 100*time.Millisecond {
		c.sc.samples = c.sc.samples[1:]
	}
}

// speed is how fast the page was moving as the finger lifted, pixels a second.
func (c *console) speed() float64 {
	s := c.sc.samples
	if len(s) < 2 {
		return 0
	}
	a, b := s[0], s[len(s)-1]
	dt := b.t.Sub(a.t).Seconds()
	if dt <= 0 || time.Since(b.t) > 80*time.Millisecond {
		return 0 // the finger stopped before lifting
	}
	return float64(b.y-a.y) / dt
}

// gpuRelease ends a GPU drag, up and down or sideways: fling, or settle. Caller holds drawMu.
func (c *console) gpuRelease() {
	v := c.speed()
	if math.Abs(v) < flingMinSpeed || (!c.sc.vert && !c.sc.stripOn) {
		c.gpuSettle()
		return
	}
	c.sc.fling++
	gen, m, p := c.sc.fling, c.mode, c.page
	if c.sc.vert {
		bottom := float64(max(p.img.Rect.Dy()-c.viewH(), 0))
		go c.fling(gen, m, p, float64(c.scrollY()), v, bottom, c.gpuScrollTo)
		return
	}
	r := c.sc.row
	id := r.id
	end := float64(max(r.contentW-r.r.Dx(), 0))
	go c.fling(gen, m, p, float64(c.rowOffset(id)), v, end, func(off int) bool {
		c.sc.x[id] = off
		c.disp.overlay(c.gpuOverlay(false))
		return true
	})
}

// fling keeps the page (or a row) moving after the finger lifts, slowing down, from at
// v pixels a second, between 0 and end; move shows each position (the GPU paces it: the
// overlay waits for the panel). A touch, a redraw or another screen stops it.
func (c *console) fling(gen int, m mode, p *page, at, v, end float64, move func(int) bool) {
	start := time.Now()
	for {
		time.Sleep(8 * time.Millisecond)
		drawMu.Lock()
		if c.sc.fling != gen || c.mode != m || c.page != p || c.disp == nil {
			drawMu.Unlock()
			return
		}
		t := time.Since(start).Seconds()
		x := at + v*flingTau*(1-math.Exp(-t/flingTau))
		done := math.Abs(v*math.Exp(-t/flingTau)) < flingStop || x <= 0 || x >= end
		x = math.Min(math.Max(x, 0), end)
		if !move(int(x)) || done {
			c.gpuSettle()
			done = true
		}
		drawMu.Unlock()
		if done {
			return
		}
	}
}

// prefetchPage sends the page's slices around the screen to the GPU a moment after it's
// drawn, while nothing else happens, so the first swipe moves at once. Caller holds drawMu.
func (c *console) prefetchPage() {
	if c.disp == nil || !c.gpuScrolls() || c.page.img.Rect.Dy() <= c.viewH() {
		return
	}
	gen := c.pageGen
	time.AfterFunc(300*time.Millisecond, func() {
		drawMu.Lock()
		defer drawMu.Unlock()
		if c.pageGen != gen || c.sc.drag || c.sc.gpuOn || !c.gpuScrolls() {
			return
		}
		y0, y1 := c.visible(c.scrollY())
		c.ensureRows(y0, y1+pageSlice/2)
	})
}
