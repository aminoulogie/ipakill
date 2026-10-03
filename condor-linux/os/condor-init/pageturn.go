package main

import (
	"image"
	"time"
)

// Page turns, after Apple Books: Slide (the pages move across together), Curl (the page
// lifts from its edge, its back showing the text faintly mirrored, and a shadow falls on
// the page underneath) or None. The animation is time-boxed rather than frame-counted:
// on the tablet's Atom a full-screen frame takes tens of milliseconds, so it draws as many
// frames as fit in the duration and always ends on time.

var turnFrames int // frames drawn, for tests

var turnDuration = map[string]time.Duration{"slide": 280 * time.Millisecond, "curl": 360 * time.Millisecond}

// turnFrom is a copy of the page on screen, taken before a turn. Caller holds drawMu.
func (c *console) turnFrom() *image.RGBA {
	if c.lib.Prefs.PageTurn == "none" || turnDuration[c.lib.Prefs.PageTurn] == 0 || !c.screenOn ||
		c.mode != modeReader || c.page == nil || c.rd.view != "" {
		return nil
	}
	if c.turnOld == nil || c.turnOld.Rect != c.page.img.Rect {
		c.turnOld = image.NewRGBA(c.page.img.Rect)
		c.turnFrame = image.NewRGBA(c.page.img.Rect)
	}
	copy(c.turnOld.Pix, c.page.img.Pix)
	return c.turnOld
}

// animateTurn plays the turn from old to the page now composed (c.page), forward (dir > 0)
// or back. Caller holds drawMu.
func (c *console) animateTurn(old *image.RGBA, dir int) {
	if old == nil {
		return
	}
	style := c.lib.Prefs.PageTurn
	dur := turnDuration[style]
	nu := c.page.img
	start := time.Now()
	for {
		t := float64(time.Since(start)) / float64(dur)
		if t >= 1 {
			return
		}
		e := 1 - (1-t)*(1-t) // ease out
		switch {
		case style == "slide":
			slideFrame(c.turnFrame, old, nu, e, dir)
		case dir > 0:
			curlFrame(c.turnFrame, old, nu, e, c.theme())
		default: // back: the previous page comes down over this one
			curlFrame(c.turnFrame, nu, old, 1-e, c.theme())
		}
		c.s.blitRGBA(c.turnFrame, 0, c.barH)
		c.s.Flush()
		turnFrames++
	}
}

// slideFrame: forward, the new page pushes in from the right; back, from the left.
func slideFrame(dst, old, nu *image.RGBA, t float64, dir int) {
	w := dst.Rect.Dx()
	o := int(float64(w) * t)
	for y := 0; y < dst.Rect.Dy(); y++ {
		row := y * dst.Stride
		d, a, b := dst.Pix[row:row+4*w], old.Pix[row:row+4*w], nu.Pix[row:row+4*w]
		if dir > 0 {
			copy(d[:4*(w-o)], a[4*o:])
			copy(d[4*(w-o):], b[:4*o])
		} else {
			copy(d[:4*o], b[4*(w-o):])
			copy(d[4*o:], a[:4*(w-o)])
		}
	}
}

// curlFrame turns page a over to reveal page b; t from 0 (flat) to 1 (gone). The fold runs
// down the page at F; the part of a beyond it lies folded back over [2F-w, F], showing its
// back: the paper, a little darker, with the text faintly mirrored through it.
func curlFrame(dst, a, b *image.RGBA, t float64, th readerTheme) {
	w, h := dst.Rect.Dx(), dst.Rect.Dy()
	f := int(float64(w) * (1 - t))
	e := max(2*f-w, 0)
	back := blend(th.bg, rgb(0x000000), 0.05)
	if th.dark {
		back = blend(th.bg, rgb(0xffffff), 0.06)
	}
	const shade = 46
	for y := 0; y < h; y++ {
		row := y * dst.Stride
		d := dst.Pix[row : row+4*w]
		copy(d[:4*e], a.Pix[row:row+4*e])
		copy(d[4*f:], b.Pix[row+4*f:row+4*w])
		for x := e; x < f; x++ { // the flap
			src := 2*f - x
			i := 4 * x
			if src >= w {
				d[i], d[i+1], d[i+2], d[i+3] = back.R, back.G, back.B, 255
				continue
			}
			j := row + 4*src
			// The back of the page: its own print shows through at 12%, darker toward the fold.
			k := float64(f-x) / float64(max(f-e, 1))
			pr, pg, pb := float64(a.Pix[j]), float64(a.Pix[j+1]), float64(a.Pix[j+2])
			br, bg, bb := float64(back.R)*(0.94+0.06*k), float64(back.G)*(0.94+0.06*k), float64(back.B)*(0.94+0.06*k)
			d[i], d[i+1], d[i+2], d[i+3] = uint8(br*0.88+pr*0.12), uint8(bg*0.88+pg*0.12), uint8(bb*0.88+pb*0.12), 255
		}
		for x := f; x < min(f+shade, w); x++ { // the shadow the lifted page casts
			i := 4 * x
			s := 0.28 * float64(f+shade-x) / shade
			d[i], d[i+1], d[i+2] = uint8(float64(d[i])*(1-s)), uint8(float64(d[i+1])*(1-s)), uint8(float64(d[i+2])*(1-s))
		}
		if e > 0 { // and a softer one along the flap's edge
			for x := max(e-14, 0); x < e; x++ {
				i := 4 * x
				s := 0.12 * float64(x-(e-14)) / 14
				d[i], d[i+1], d[i+2] = uint8(float64(d[i])*(1-s)), uint8(float64(d[i+1])*(1-s)), uint8(float64(d[i+2])*(1-s))
			}
		}
	}
}
