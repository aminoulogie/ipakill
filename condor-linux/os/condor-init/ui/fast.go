package ui

import (
	"image"
	"image/draw"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

// The tablet's 2013 Atom is 10-20x slower than a PC, and a page is 2.2 million pixels, so the
// hot paths here avoid per-pixel interface calls: rows are filled with copy(), and glyphs are
// rasterized once per face and reused.

// CachedFace wraps a font.Face and keeps each glyph's coverage mask after its first use.
// Glyphs are drawn at whole-pixel positions (the faces use full hinting, so advances are
// whole pixels anyway).
type CachedFace struct {
	font.Face
	mu     sync.Mutex
	glyphs map[rune]cachedGlyph
}

type cachedGlyph struct {
	dr      image.Rectangle // relative to the dot
	mask    *image.Alpha    // origin at dr.Min
	advance fixed.Int26_6
	ok      bool
}

// Cache wraps f with a glyph cache.
func Cache(f font.Face) *CachedFace {
	if cf, ok := f.(*CachedFace); ok {
		return cf
	}
	return &CachedFace{Face: f, glyphs: map[rune]cachedGlyph{}}
}

// Glyph implements font.Face.
func (f *CachedFace) Glyph(dot fixed.Point26_6, r rune) (image.Rectangle, image.Image, image.Point, fixed.Int26_6, bool) {
	f.mu.Lock()
	g, hit := f.glyphs[r]
	if !hit {
		dr, mask, mp, adv, ok := f.Face.Glyph(fixed.Point26_6{}, r)
		g = cachedGlyph{dr: dr, advance: adv, ok: ok}
		if ok {
			g.mask = image.NewAlpha(image.Rect(0, 0, dr.Dx(), dr.Dy()))
			draw.Draw(g.mask, g.mask.Rect, mask, mp, draw.Src)
		}
		f.glyphs[r] = g
	}
	f.mu.Unlock()
	if !g.ok {
		return image.Rectangle{}, nil, image.Point{}, g.advance, false
	}
	return g.dr.Add(image.Pt(dot.X.Round(), dot.Y.Round())), g.mask, image.Point{}, g.advance, true
}
