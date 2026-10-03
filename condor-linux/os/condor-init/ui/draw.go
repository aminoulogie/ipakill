// Package ui draws condor's simple interface (status bar, app list, app screens) into an
// ordinary *image.RGBA in logical portrait coordinates. It knows nothing about the
// framebuffer: condor-init blits the finished image onto its Screen with Blit, and the
// ui-preview command writes the same image to a PNG so the layout can be checked on a PC.
package ui

import (
	"image"
	"image/color"
	"math"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// Palette: light, calm, high contrast for reading.
var (
	Background = color.RGBA{242, 242, 247, 255}
	Card       = color.RGBA{255, 255, 255, 255}
	Ink        = color.RGBA{20, 20, 24, 255}
	Muted      = color.RGBA{120, 120, 128, 255}
	Divider    = color.RGBA{220, 220, 226, 255}
	Accent     = color.RGBA{0, 110, 230, 255}
)

// Faces holds the fonts at the sizes the interface uses.
type Faces struct {
	Status, Title, Body, Glyph, Small font.Face
}

// LoadFaces builds the font faces from the embedded Go fonts (BSD licensed, no files needed).
func LoadFaces() (*Faces, error) {
	reg, err := opentype.Parse(goregular.TTF)
	if err != nil {
		return nil, err
	}
	bold, err := opentype.Parse(gobold.TTF)
	if err != nil {
		return nil, err
	}
	face := func(f *opentype.Font, size float64) (font.Face, error) {
		return opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
	}
	var fs Faces
	for _, s := range []struct {
		dst  *font.Face
		f    *opentype.Font
		size float64
	}{
		{&fs.Status, bold, 34}, {&fs.Title, bold, 96}, {&fs.Body, reg, 56},
		{&fs.Glyph, bold, 52}, {&fs.Small, reg, 40},
	} {
		if *s.dst, err = face(s.f, s.size); err != nil {
			return nil, err
		}
	}
	return &fs, nil
}

// TextWidth is the advance width of s in pixels.
func TextWidth(f font.Face, s string) int {
	return font.MeasureString(f, s).Ceil()
}

// DrawText draws s with its baseline at y, starting at x.
func DrawText(dst *image.RGBA, f font.Face, x, y int, c color.Color, s string) {
	d := font.Drawer{Dst: dst, Src: image.NewUniform(c), Face: f, Dot: fixed.P(x, y)}
	d.DrawString(s)
}

// DrawTextCentered draws s centred horizontally on cx and vertically on cy.
func DrawTextCentered(dst *image.RGBA, f font.Face, cx, cy int, c color.Color, s string) {
	m := f.Metrics()
	h := (m.Ascent + m.Descent).Ceil()
	DrawText(dst, f, cx-TextWidth(f, s)/2, cy-h/2+m.Ascent.Ceil(), c, s)
}

// Fill paints the rectangle r.
func Fill(dst *image.RGBA, r image.Rectangle, c color.RGBA) {
	r = r.Intersect(dst.Rect)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			dst.SetRGBA(x, y, c)
		}
	}
}

// blend mixes c over the pixel at (x, y) with coverage a in [0, 1].
func blend(dst *image.RGBA, x, y int, c color.RGBA, a float64) {
	if a <= 0 || !(image.Point{x, y}.In(dst.Rect)) {
		return
	}
	if a >= 1 {
		dst.SetRGBA(x, y, c)
		return
	}
	o := dst.RGBAAt(x, y)
	mix := func(f, b uint8) uint8 { return uint8(float64(f)*a + float64(b)*(1-a) + 0.5) }
	dst.SetRGBA(x, y, color.RGBA{mix(c.R, o.R), mix(c.G, o.G), mix(c.B, o.B), 255})
}

// RoundRect paints r with corners of radius rad, anti-aliased.
func RoundRect(dst *image.RGBA, r image.Rectangle, rad int, c color.RGBA) {
	rad = min(rad, r.Dx()/2, r.Dy()/2)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			// Distance from the pixel centre to the nearest corner circle's centre, only
			// inside the corner squares; everywhere else the rectangle is fully covered.
			cx, cy := float64(x)+0.5, float64(y)+0.5
			ccx := math.Max(float64(r.Min.X+rad), math.Min(cx, float64(r.Max.X-rad)))
			ccy := math.Max(float64(r.Min.Y+rad), math.Min(cy, float64(r.Max.Y-rad)))
			d := math.Hypot(cx-ccx, cy-ccy)
			blend(dst, x, y, c, float64(rad)+0.5-d)
		}
	}
}

// Circle paints a filled, anti-aliased circle.
func Circle(dst *image.RGBA, cx, cy, rad int, c color.RGBA) {
	RoundRect(dst, image.Rect(cx-rad, cy-rad, cx+rad, cy+rad), rad, c)
}

// Blit copies img onto a device through set (e.g. condor-init's Screen.Set).
func Blit(img *image.RGBA, set func(x, y int, r, g, b uint8)) {
	b := img.Rect
	for y := b.Min.Y; y < b.Max.Y; y++ {
		row := img.Pix[img.PixOffset(b.Min.X, y):]
		for x := 0; x < b.Dx(); x++ {
			set(b.Min.X+x, y, row[4*x], row[4*x+1], row[4*x+2])
		}
	}
}
