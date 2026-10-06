package main

import (
	"log"
	"math"
	"slices"
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

// gpuScrollTo shows the page at y, under the finger or flinging. Caller holds drawMu.
func (c *console) gpuScrollTo(y int) bool {
	y = min(max(y, 0), max(c.page.img.Rect.Dy()-c.viewH(), 0))
	if !c.ensureRows(c.visible(y)) {
		return false
	}
	c.sc.y[c.mode] = y
	c.sc.gpuOn = true
	c.disp.overlay(c.scrollQuads(y, true))
	return true
}

// gpuRowTo moves a sideways row: repaint it, upload its strip, show it. Caller holds drawMu.
func (c *console) gpuRowTo(r *hrow, off int) bool {
	if !c.ensureRows(c.visible(c.scrollY())) {
		return false
	}
	if off != c.rowOffset(r.id) {
		c.sc.x[r.id] = off
		r.paint(c.page.img)
		if !c.uploadStrip(r.r.Min.Y, r.r.Max.Y) {
			return false
		}
	}
	c.sc.gpuOn = true
	c.disp.overlay(c.scrollQuads(c.scrollY(), false))
	return true
}

// gpuSettle ends a GPU scroll: the page's window is drawn into the screen for real (it looks
// the same) and the overlay goes. Caller holds drawMu.
func (c *console) gpuSettle() {
	if !c.sc.gpuOn {
		return
	}
	c.sc.gpuOn = false
	c.sc.fling++
	if c.disp == nil || c.page == nil {
		return
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
	c.sc.gpuOn = false
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

// gpuRelease ends a vertical GPU drag: fling, or settle. Caller holds drawMu.
func (c *console) gpuRelease() {
	v := c.speed()
	if math.Abs(v) < flingMinSpeed {
		c.gpuSettle()
		return
	}
	c.sc.fling++
	go c.fling(c.sc.fling, c.mode, c.page, float64(c.scrollY()), v)
}

// fling keeps the page moving after the finger lifts, slowing down, one position per frame
// (the GPU paces it: the overlay waits for the panel). A touch, a redraw or another screen
// stops it.
func (c *console) fling(gen int, m mode, p *page, y0, v float64) {
	start := time.Now()
	for {
		time.Sleep(8 * time.Millisecond)
		drawMu.Lock()
		if c.sc.fling != gen || c.mode != m || c.page != p || c.disp == nil {
			drawMu.Unlock()
			return
		}
		t := time.Since(start).Seconds()
		y := y0 + v*flingTau*(1-math.Exp(-t/flingTau))
		top, bottom := 0.0, float64(max(p.img.Rect.Dy()-c.viewH(), 0))
		done := math.Abs(v*math.Exp(-t/flingTau)) < flingStop || y <= top || y >= bottom
		y = math.Min(math.Max(y, top), bottom)
		if done {
			c.sc.y[m] = int(y)
			c.gpuSettle()
		} else if !c.gpuScrollTo(int(y)) {
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
