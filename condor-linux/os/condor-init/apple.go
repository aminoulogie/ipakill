package main

import (
	"image"
	"image/color"
	"math"

	"github.com/go-fonts/liberation/liberationsansbold"
	"github.com/go-fonts/liberation/liberationsansregular"
	"golang.org/x/image/font"

	"condor-init/ui"
)

// The Books app's look, after Apple Books: large bold titles, covers first, an orange
// accent, sheets that slide up from the bottom with a grabber, a dark callout over a
// selection, controls that stay out of the way until the middle of the page is tapped.
// Liberation Sans stands in for San Francisco (Helvetica metrics); DejaVu covers Arabic.

// Dark-mode system colours.
var (
	apBG        = rgb(0x000000)
	apCard      = rgb(0x1c1c1e)
	apCard2     = rgb(0x2c2c2e)
	apSeparator = rgb(0x38383a)
	apLabel     = rgb(0xffffff)
	apSecondary = rgb(0x8e8e93)
	apOrange    = rgb(0xff9f0a)
	apCallout   = rgb(0x2c2c2e)
)

type appleFonts struct {
	largeTitle, title, headline, body, callout, caption, captionBold font.Face
}

var apFonts *appleFonts

func apple() *appleFonts {
	if apFonts == nil {
		reg := func(s float64) font.Face { return textFace("libsans", liberationsansregular.TTF, false, s) }
		bold := func(s float64) font.Face { return textFace("libsans-bold", liberationsansbold.TTF, true, s) }
		apFonts = &appleFonts{largeTitle: bold(72), title: bold(46), headline: bold(36), body: reg(34),
			callout: reg(31), caption: reg(27), captionBold: bold(27)}
	}
	return apFonts
}

// text draws s (shaped if Arabic) with its baseline at y.
func apText(img *image.RGBA, f font.Face, x, y int, c color.Color, s string) {
	ui.DrawText(img, f, x, y, c, visual(s))
}

func apTextRight(img *image.RGBA, f font.Face, right, y int, c color.Color, s string) {
	s = visual(s)
	ui.DrawText(img, f, right-ui.TextWidth(f, s), y, c, s)
}

func apTextCenter(img *image.RGBA, f font.Face, cx, cy int, c color.Color, s string) {
	ui.DrawTextCentered(img, f, cx, cy, c, visual(s))
}

// --- icons, drawn with rectangles and circles (stroke ~5 px at this density) ---------------

func line(img *image.RGBA, x0, y0, x1, y1, w int, c color.RGBA) {
	steps := max(abs(x1-x0), abs(y1-y0), 1)
	for i := 0; i <= steps; i += 2 {
		x := x0 + (x1-x0)*i/steps
		y := y0 + (y1-y0)*i/steps
		ui.Circle(img, x, y, w/2+1, c)
	}
}

// iconBack is a chevron "<".
func iconBack(img *image.RGBA, x, cy int, c color.RGBA) {
	line(img, x+18, cy-20, x, cy, 6, c)
	line(img, x, cy, x+18, cy+20, 6, c)
}

// iconList: three dots and three lines (contents).
func iconList(img *image.RGBA, cx, cy int, c color.RGBA) {
	for i := -1; i <= 1; i++ {
		y := cy + i*16
		ui.Circle(img, cx-22, y, 4, c)
		ui.RoundRect(img, image.Rect(cx-10, y-3, cx+26, y+3), 3, c)
	}
}

// iconMarker: a highlighter nib over a stroke (highlights).
func iconMarker(img *image.RGBA, cx, cy int, c color.RGBA) {
	line(img, cx-14, cy+10, cx+12, cy-16, 10, c)
	line(img, cx-20, cy+16, cx-14, cy+10, 5, c)
	ui.RoundRect(img, image.Rect(cx-26, cy+20, cx+26, cy+24), 2, c)
}

// iconLines: a page of lines with the middle one lit (line by line).
func iconLines(img *image.RGBA, cx, cy int, c, lit color.RGBA) {
	ui.RoundRect(img, image.Rect(cx-26, cy-20, cx+20, cy-15), 2, c)
	ui.RoundRect(img, image.Rect(cx-30, cy-7, cx+30, cy+7), 4, lit)
	ui.RoundRect(img, image.Rect(cx-26, cy+15, cx+14, cy+20), 2, c)
}

// iconAa: the type settings button.
func iconAa(img *image.RGBA, cx, cy int, c color.RGBA) {
	f := apple()
	ui.DrawText(img, f.caption, cx-26, cy+12, c, "A")
	ui.DrawText(img, f.headline, cx-6, cy+14, c, "A")
}

// iconCheck: a checkmark.
func iconCheck(img *image.RGBA, x, cy int, c color.RGBA) {
	line(img, x, cy, x+10, cy+11, 5, c)
	line(img, x+10, cy+11, x+28, cy-12, 5, c)
}

// --- surfaces --------------------------------------------------------------------------------

// sheet dims what's behind and draws a sheet rising from the bottom to top, with rounded top
// corners and a grabber; it returns the sheet's rectangle.
func sheet(img *image.RGBA, top int, bg color.RGBA) image.Rectangle {
	b := img.Rect
	blendRect(img, image.Rect(0, 0, b.Dx(), top), rgb(0x000000), 0.35)
	r := image.Rect(0, top, b.Dx(), b.Max.Y)
	ui.RoundRect(img, image.Rect(0, top, b.Dx(), b.Max.Y+40), 36, bg)
	ui.RoundRect(img, image.Rect(b.Dx()/2-36, top+14, b.Dx()/2+36, top+22), 4, blend(bg, apSecondary, 0.6))
	return r
}

// segmented draws an iOS segmented control and registers its segments.
func segmented(p *page, r image.Rectangle, ids, labels []string, on string, bg, fg color.RGBA) {
	f := apple()
	track := blend(bg, fg, 0.12)
	ui.RoundRect(p.img, r, 20, track)
	w := r.Dx() / len(ids)
	for i, id := range ids {
		sr := image.Rect(r.Min.X+i*w+4, r.Min.Y+4, r.Min.X+(i+1)*w-4, r.Max.Y-4)
		face := f.callout
		if id == on {
			ui.RoundRect(p.img, sr, 16, blend(bg, fg, 0.28))
			face = f.captionBold
		}
		apTextCenter(p.img, face, (sr.Min.X+sr.Max.X)/2, (sr.Min.Y+sr.Max.Y)/2, fg, labels[i])
		p.buttons = append(p.buttons, button{id, sr})
	}
}

// ring draws an activity-style progress ring (the reading goal).
func ring(img *image.RGBA, cx, cy, rad, width int, frac float64, track, c color.RGBA) {
	for a := 0.0; a < 360; a += 1.5 {
		col := track
		if a/360 < frac {
			col = c
		}
		t := (a - 90) * math.Pi / 180
		ui.Circle(img, cx+int(float64(rad)*math.Cos(t)), cy+int(float64(rad)*math.Sin(t)), width/2, col)
	}
}

// shadowRect draws a soft shadow under a cover.
func shadowRect(img *image.RGBA, r image.Rectangle) {
	for i := 1; i <= 10; i++ {
		blendRect(img, image.Rect(r.Min.X-i+6, r.Max.Y, r.Max.X+i-6, r.Max.Y+i), rgb(0x000000), 0.10)
	}
}
