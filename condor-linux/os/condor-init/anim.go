package main

import (
	"image"
	"time"
)

// Animations, iOS's: a pushed screen slides in from the right over the one before (which
// drifts left a third as far, in shadow) and slides back out on the way back; sheets rise
// from the bottom; pages slide or curl; the lit line glides to the next one.
//
// The panel refreshes at most 60 times a second (its timings are logged at start; it can't
// do 120), and on this Atom a frame redrawn from scratch takes tens of milliseconds. So
// transitions don't redraw: the screen before and after are each drawn once, and every
// frame is put together from the two in the framebuffer's own layout, where moving
// sideways on the portrait screen is moving whole native rows (a memmove) and moving up or
// down is moving bytes within them. Frames wait for the vertical blank when the driver
// offers it, and every animation is time-boxed: it ends on time even if frames are slow.

var animScale = 1.0 // tests set 0: no frames, straight to the end

const (
	durPush  = 330 * time.Millisecond
	durSheet = 300 * time.Millisecond
	durGlide = 110 * time.Millisecond
)

var turnDuration = map[string]time.Duration{"slide": 300 * time.Millisecond, "curl": 380 * time.Millisecond}

var animFrames int // frames drawn, for tests

// animOK: frames can be composed natively (portrait on a landscape 32 bpp framebuffer).
func (c *console) animOK() bool {
	return animScale > 0 && c.screenOn && c.s.rot == Rot90 && c.s.bpp == 32
}

// spring is iOS's ease-out: quick start, soft landing.
func spring(t float64) float64 { u := 1 - t; return 1 - u*u*u }

// frames runs draw(t) for t in (0, 1) over d, one frame per vertical blank, then stops.
func (c *console) frames(d time.Duration, draw func(t float64)) {
	d = time.Duration(float64(d) * animScale)
	start := time.Now()
	for {
		t := float64(time.Since(start)) / float64(d)
		if d <= 0 || t >= 1 {
			return
		}
		draw(spring(t)) // marks what it changed
		c.s.Flush()
		animFrames++
		if !waitVsync(c.s) {
			time.Sleep(4 * time.Millisecond) // let touches in between frames
		}
	}
}

// animBufs are the screen before and after a transition, in native layout.
func (c *console) animBufs() (a, b []byte) {
	if len(c.animA) != len(c.s.buf) {
		c.animA, c.animB = make([]byte, len(c.s.buf)), make([]byte, len(c.s.buf))
	}
	return c.animA, c.animB
}

// transition runs change (which redraws the screen) and animates from the old screen to the
// new one: "push" (forward), "pop" (back), "rise" (a sheet in rect r rising by its height).
// Caller holds drawMu.
func (c *console) transition(kind string, r image.Rectangle, change func()) {
	if !c.animOK() {
		change()
		return
	}
	s := c.s
	old, nu := c.animBufs()
	copy(old, s.buf)
	s.hold = true
	change()
	s.hold = false
	copy(nu, s.buf)
	switch kind {
	case "push", "pop":
		c.frames(durPush, func(t float64) { c.pushFrame(old, nu, t, kind == "push"); s.markRows(0, s.fbH-1) })
	case "rise":
		c.frames(durSheet, func(t float64) { c.riseFrame(old, nu, r, t); s.markRows(0, s.fbH-1) })
	case "fall": // a sheet dropping away: rise played backwards
		c.frames(durSheet*4/5, func(t float64) { c.riseFrame(nu, old, r, 1-t); s.markRows(0, s.fbH-1) })
	}
	copy(s.buf, nu)
	s.markRows(0, s.fbH-1)
	s.Flush()
}

// fullSheet is the area under the status bar (a sheet covering the screen).
func (c *console) fullSheet() image.Rectangle { return image.Rect(0, c.barH, c.s.W, c.s.H) }

// lookupSheet is where the Look Up / Translate sheet sits (logical screen coordinates).
func (c *console) lookupSheet() image.Rectangle {
	return image.Rect(0, c.barH+c.s.H-c.barH-860, c.s.W, c.s.H)
}

// row is native row fy of buf (logical column fbH-1-fy, top to bottom).
func (c *console) row(buf []byte, logicalX int) []byte {
	fy := c.s.fbH - 1 - logicalX
	return buf[fy*c.s.stride : fy*c.s.stride+4*c.s.fbW]
}

// shadeRow darkens logical rows [y0, y1) of a native row by k (0..1).
func shadeRow(r []byte, y0, y1 int, k float64) {
	m := int((1 - k) * 256)
	for i := 4 * y0; i < 4*y1 && i+2 < len(r); i += 4 {
		r[i] = uint8(int(r[i]) * m >> 8)
		r[i+1] = uint8(int(r[i+1]) * m >> 8)
		r[i+2] = uint8(int(r[i+2]) * m >> 8)
	}
}

// pushFrame: the new screen slides in from the right over the old one, which drifts left
// a third as far under the new screen's shadow; pop plays it backwards. The status bar stays
// put. Only memmoves and a narrow shadow, so a frame costs little more than a copy.
func (c *console) pushFrame(old, nu []byte, t float64, forward bool) {
	w, bar := c.s.W, c.barH
	top, under := nu, old // the screen sliding over, and the one beneath
	if !forward {
		top, under, t = old, nu, 1-t
	}
	edge := int(float64(w) * (1 - t)) // where the top screen's left edge is
	drift := int(float64(w) * t / 3)
	for x := 0; x < w; x++ {
		dst := c.row(c.s.buf, x)
		if x >= edge {
			copy(dst[4*bar:], c.row(top, x-edge)[4*bar:])
		} else {
			copy(dst[4*bar:], c.row(under, min(x+drift, w-1))[4*bar:])
			if edge-x < 30 { // the top screen's shadow on its left
				shadeRow(dst, bar, len(dst)/4, 0.16*float64(30-(edge-x))/30)
			}
		}
		copy(dst[:4*bar], c.row(nu, x)[:4*bar])
	}
}

// riseFrame: the area r of the new screen (a sheet) rises from below its place. Above it
// the old screen darkens as the new one's backdrop (sheet() dims by 25%); outside r the new
// screen is already in place.
func (c *console) riseFrame(old, nu []byte, r image.Rectangle, t float64) {
	h := c.s.H
	off := int(float64(h-r.Min.Y) * (1 - t))
	y0 := min(r.Min.Y+off, h)
	for x := 0; x < c.s.W; x++ {
		dst, src := c.row(c.s.buf, x), c.row(nu, x)
		if x < r.Min.X || x >= r.Max.X {
			copy(dst, src)
			continue
		}
		copy(dst[:4*r.Min.Y], src[:4*r.Min.Y])
		copy(dst[4*r.Min.Y:4*y0], c.row(old, x)[4*r.Min.Y:4*y0])
		shadeRow(dst, r.Min.Y, y0, 0.25*t)
		copy(dst[4*y0:4*h], src[4*r.Min.Y:4*(h-off)])
	}
}

// --- page turns ----------------------------------------------------------------------------

// turnFrom keeps the screen as it is, before a page turn. Caller holds drawMu.
func (c *console) turnFrom() []byte {
	if c.lib.Prefs.PageTurn == "none" || turnDuration[c.lib.Prefs.PageTurn] == 0 || !c.animOK() ||
		c.mode != modeReader || c.rd.view != "" {
		return nil
	}
	old, _ := c.animBufs()
	copy(old, c.s.buf)
	return old
}

// animateTurn plays the turn from old to the screen now drawn. Caller holds drawMu.
func (c *console) animateTurn(old []byte, dir int) {
	if old == nil {
		return
	}
	_, nu := c.animBufs()
	copy(nu, c.s.buf)
	style := c.lib.Prefs.PageTurn
	c.frames(turnDuration[style], func(t float64) {
		c.s.markRows(0, c.s.fbH-1)
		switch {
		case style == "slide":
			c.slideFrame(old, nu, t, dir)
		case dir > 0:
			c.curlFrame(old, nu, t)
		default: // back: the previous page comes down over this one
			c.curlFrame(nu, old, 1-t)
		}
	})
	copy(c.s.buf, nu)
	c.s.markRows(0, c.s.fbH-1)
	c.s.Flush()
}

// slideFrame: both pages move across together (forward: to the left).
func (c *console) slideFrame(old, nu []byte, t float64, dir int) {
	w, bar := c.s.W, c.barH
	o := int(float64(w) * t)
	for x := 0; x < w; x++ {
		dst := c.row(c.s.buf, x)
		var src []byte
		if dir > 0 {
			if x < w-o {
				src = c.row(old, x+o)
			} else {
				src = c.row(nu, x-(w-o))
			}
		} else {
			if x < o {
				src = c.row(nu, x+w-o)
			} else {
				src = c.row(old, x-o)
			}
		}
		copy(dst[4*bar:], src[4*bar:])
		copy(dst[:4*bar], c.row(nu, x)[:4*bar])
	}
}

// curlFrame turns page a over to reveal page b, t from 0 (flat) to 1 (gone): the fold at F,
// the part of a beyond it lying folded back over [2F-w, F] and showing the paper's back with
// the print faintly mirrored through it, a shadow on b along the fold.
func (c *console) curlFrame(a, b []byte, t float64) {
	w, bar := c.s.W, c.barH
	f := int(float64(w) * (1 - t))
	e := max(2*f-w, 0)
	th := c.theme()
	back := blend(th.bg, rgb(0x000000), 0.05)
	if th.dark {
		back = blend(th.bg, rgb(0xffffff), 0.06)
	}
	const shade = 46
	for x := 0; x < w; x++ {
		dst := c.row(c.s.buf, x)
		switch {
		case x < e:
			copy(dst[4*bar:], c.row(a, x)[4*bar:])
			if x >= e-14 {
				shadeRow(dst, bar, len(dst)/4, 0.12*float64(x-(e-14))/14)
			}
		case x < f: // the flap: the back of the page
			src := 2*f - x
			k := 240 + 16*(f-x)/max(f-e, 1) // darker toward the fold, in 256ths
			bb, bg, br := int(back.B)*k>>8, int(back.G)*k>>8, int(back.R)*k>>8
			if src >= w {
				for i := 4 * bar; i+3 < len(dst); i += 4 {
					dst[i], dst[i+1], dst[i+2] = uint8(bb), uint8(bg), uint8(br) // XRGB: b, g, r
				}
				break
			}
			// The paper's back, 88%, with its print showing through at 12% (in 256ths: 225/31).
			pb, pg, pr := bb*225, bg*225, br*225
			s := c.row(a, src)
			for i := 4 * bar; i+3 < len(dst); i += 4 {
				dst[i] = uint8((pb + int(s[i])*31) >> 8)
				dst[i+1] = uint8((pg + int(s[i+1])*31) >> 8)
				dst[i+2] = uint8((pr + int(s[i+2])*31) >> 8)
			}
		default:
			copy(dst[4*bar:], c.row(b, x)[4*bar:])
			if x < f+shade {
				shadeRow(dst, bar, len(dst)/4, 0.28*float64(f+shade-x)/shade)
			}
		}
		copy(dst[:4*bar], c.row(b, x)[:4*bar])
	}
}

// --- the lit line gliding -------------------------------------------------------------------

// glideLines moves the lit band from line a to line b over a few frames, redrawing only the
// strip between them. Caller holds drawMu.
func (c *console) glideLines(a, b int) {
	ra, rb := c.litRect(a), c.litRect(b)
	if !c.animOK() || ra.Empty() || rb.Empty() || c.page == nil || c.pcache.dim == nil {
		return
	}
	img := c.page.img
	th := c.theme()
	area := ra.Union(rb)
	ba, bb := c.band(c.book.pages[c.book.page][a]), c.band(c.book.pages[c.book.page][b])
	c.frames(durGlide, func(t float64) {
		y0 := ba.Min.Y + int(float64(bb.Min.Y-ba.Min.Y)*t)
		h := ba.Dy() + int(float64(bb.Dy()-ba.Dy())*t)
		band := image.Rect(ba.Min.X-16, y0, ba.Max.X+16, y0+h)
		copyRows(img, c.pcache.dim, area)
		copyRows(img, c.pcache.normal, image.Rect(0, band.Min.Y, c.s.W, band.Max.Y))
		blendRect(img, band, th.mark, th.markA)
		c.s.hold = true
		c.s.blitRGBA(img.SubImage(area).(*image.RGBA), 0, c.barH+area.Min.Y)
		c.s.hold = false
	})
}
