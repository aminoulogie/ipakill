package main

import (
	"image"
	"image/color"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

// Rasterizing a character from the font is the slow part of drawing the console, and a
// screen uses only a few hundred distinct (character, colours, weight) combinations. Each is
// drawn once into a cell-sized bitmap and copied after that.

type glyphKey struct {
	r      rune
	fg, bg color.RGBA
	bold   bool
}

const maxGlyphs = 4096 // a colourful program could create many; start over past this

// putGlyph copies the cell for (r, fg, bg, bold) into dst with its top-left at (x, 0).
func (c *console) putGlyph(dst *image.RGBA, x int, r rune, fg, bg color.RGBA, bold bool) {
	if r == 0 {
		r = ' '
	}
	k := glyphKey{r, fg, bg, bold}
	g := c.glyphs[k]
	if g == nil {
		if len(c.glyphs) >= maxGlyphs {
			clear(c.glyphs)
		}
		g = image.NewRGBA(image.Rect(0, 0, c.cw, c.ch))
		for i := 0; i < len(g.Pix); i += 4 {
			g.Pix[i], g.Pix[i+1], g.Pix[i+2], g.Pix[i+3] = bg.R, bg.G, bg.B, 255
		}
		if r != ' ' {
			face := c.reg
			if bold {
				face = c.bold
			}
			d := font.Drawer{Dst: g, Src: image.NewUniform(fg), Face: face, Dot: fixed.P(0, c.asc)}
			d.DrawString(string(r))
		}
		c.glyphs[k] = g
	}
	for y := 0; y < c.ch; y++ {
		copy(dst.Pix[dst.PixOffset(x, y):dst.PixOffset(x+c.cw, y)], g.Pix[y*g.Stride:y*g.Stride+c.cw*4])
	}
}
