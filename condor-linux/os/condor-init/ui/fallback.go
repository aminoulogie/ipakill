package ui

import (
	"image"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

// FallbackFace draws each rune with the first face whose font has it: a book font for Latin
// text, DejaVu for Arabic (and anything else the book font lacks).
type FallbackFace struct {
	faces []font.Face
	fonts []*sfnt.Font
	mu    sync.Mutex
	buf   sfnt.Buffer
	which map[rune]int
}

// Fallback combines faces made from fonts, in order of preference.
func Fallback(faces []font.Face, fonts []*sfnt.Font) *FallbackFace {
	return &FallbackFace{faces: faces, fonts: fonts, which: map[rune]int{}}
}

func (f *FallbackFace) pick(r rune) font.Face {
	f.mu.Lock()
	defer f.mu.Unlock()
	i, ok := f.which[r]
	if !ok {
		for j, ft := range f.fonts {
			if g, err := ft.GlyphIndex(&f.buf, r); err == nil && g != 0 {
				i, ok = j, true
				break
			}
		}
		f.which[r] = i // 0 when no font has it: the first face draws its "missing" box
	}
	return f.faces[i]
}

func (f *FallbackFace) Close() error { return nil }

func (f *FallbackFace) Glyph(dot fixed.Point26_6, r rune) (image.Rectangle, image.Image, image.Point, fixed.Int26_6, bool) {
	return f.pick(r).Glyph(dot, r)
}

func (f *FallbackFace) GlyphBounds(r rune) (fixed.Rectangle26_6, fixed.Int26_6, bool) {
	return f.pick(r).GlyphBounds(r)
}

func (f *FallbackFace) GlyphAdvance(r rune) (fixed.Int26_6, bool) { return f.pick(r).GlyphAdvance(r) }

func (f *FallbackFace) Kern(r0, r1 rune) fixed.Int26_6 {
	a, b := f.pick(r0), f.pick(r1)
	if a != b {
		return 0
	}
	return a.Kern(r0, r1)
}

func (f *FallbackFace) Metrics() font.Metrics { return f.faces[0].Metrics() }
