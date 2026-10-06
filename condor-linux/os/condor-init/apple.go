package main

import (
	"fmt"
	"image"
	"image/color"
	"math"

	"github.com/go-fonts/liberation/liberationserifbold"
	"golang.org/x/image/font"

	"condor-init/fonts"
	"condor-init/ui"
)

// The Books app's look, after Apple Books on iPad (iPadOS 18): white pages, system blue,
// serif titles (New York there, Liberation Serif here), San Francisco text (Liberation Sans,
// same metrics as Helvetica), a floating tab bar, the reading-goal ring in the corner,
// covers with soft shadows, sheets and popovers with rounded corners. DejaVu covers Arabic.

// The system colours, set by setAppearance: dark (the default, like a Kindle's dark mode)
// or light (Apple Books' white).
var (
	apBG        color.RGBA // the page
	apGrouped   color.RGBA // behind grouped rows (Settings)
	apBand      color.RGBA // a raised band or card on the page: "Want to Read", Words
	apCard      color.RGBA // cards and grouped rows
	apCard2     color.RGBA // fills: search field, capsules
	apSeparator color.RGBA
	apLabel     color.RGBA
	apSecondary color.RGBA
	apBlue      color.RGBA
	apRingBlue  color.RGBA // the reading goal
	apRingTrack color.RGBA
	apNewBadge  color.RGBA
	apTabBG     color.RGBA
	apTabOn     color.RGBA
	apMenuBG    color.RGBA
	apRed       color.RGBA
	apDark      bool
)

// apOnBlue is text on a blue button, in both looks.
var apOnBlue = rgb(0xffffff)

func init() { setPalette(true) }

// setPalette picks iOS's dark or light system colours.
func setPalette(dark bool) {
	apDark = dark
	if dark {
		apBG, apGrouped, apBand, apCard, apCard2 = rgb(0x000000), rgb(0x000000), rgb(0x1c1c1e), rgb(0x1c1c1e), rgb(0x2c2c2e)
		apSeparator, apLabel, apSecondary = rgb(0x38383a), rgb(0xffffff), rgb(0x8d8d93)
		apBlue, apRingBlue, apRingTrack, apNewBadge = rgb(0x0a84ff), rgb(0x64d2ff), rgb(0x1f3a47), rgb(0x0a84ff)
		apTabBG, apTabOn, apMenuBG, apRed = rgb(0x1c1c1e), rgb(0x3a3a3c), rgb(0x2c2c2e), rgb(0xff453a)
		return
	}
	apBG, apGrouped, apBand, apCard, apCard2 = rgb(0xffffff), rgb(0xf2f2f7), rgb(0xf2f2f7), rgb(0xffffff), rgb(0xefeff0)
	apSeparator, apLabel, apSecondary = rgb(0xd1d1d6), rgb(0x000000), rgb(0x8a8a8e)
	apBlue, apRingBlue, apRingTrack, apNewBadge = rgb(0x007aff), rgb(0x32ade6), rgb(0xd7eef8), rgb(0x0b3d91)
	apTabBG, apTabOn, apMenuBG, apRed = rgb(0xf0f0f2), rgb(0xdcdce0), rgb(0xf9f9f9), rgb(0xff3b30)
}

// setAppearance switches the whole system, books included, to dark or light. Caller holds
// drawMu and redraws.
func (c *console) setAppearance(dark bool) {
	c.cfg.Light = !dark
	setPalette(dark)
	if dark {
		c.lib.Prefs.Theme = themeNight
	} else if c.lib.Prefs.Theme == themeNight {
		c.lib.Prefs.Theme = 0 // Original
	}
	c.lib.save()
	if c.book != nil {
		c.invalidatePage()
		c.relayout()
	}
}

type appleFonts struct {
	largeTitle, title, headline, body, callout, caption, captionBold font.Face
	serifLarge, serifTitle                                           font.Face
}

var apFonts *appleFonts

func apple() *appleFonts {
	if apFonts == nil {
		reg := func(s float64) font.Face { return textFace("inter", fonts.InterRegular, false, s) }
		semi := func(s float64) font.Face { return textFace("inter-semibold", fonts.InterSemiBold, true, s) }
		bold := func(s float64) font.Face { return textFace("inter-bold", fonts.InterBold, true, s) }
		serif := func(s float64) font.Face { return textFace("libserif-bold", liberationserifbold.TTF, true, s) }
		apFonts = &appleFonts{largeTitle: bold(68), title: bold(44), headline: semi(33), body: reg(33),
			callout: reg(30), caption: reg(25), captionBold: semi(25), serifLarge: serif(80), serifTitle: serif(48)}
	}
	return apFonts
}

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

// --- icons, drawn with strokes ---------------------------------------------------------------

func line(img *image.RGBA, x0, y0, x1, y1, w int, c color.RGBA) {
	steps := max(abs(x1-x0), abs(y1-y0), 1)
	for i := 0; i <= steps; i += 2 {
		ui.Circle(img, x0+(x1-x0)*i/steps, y0+(y1-y0)*i/steps, w/2+1, c)
	}
}

func iconBack(img *image.RGBA, x, cy int, c color.RGBA) {
	line(img, x+16, cy-18, x, cy, 5, c)
	line(img, x, cy, x+16, cy+18, 5, c)
}

func iconChevronRight(img *image.RGBA, x, cy int, c color.RGBA) {
	line(img, x, cy-11, x+10, cy, 4, c)
	line(img, x+10, cy, x, cy+11, 4, c)
}

// iconList: three dots and three lines (contents).
func iconList(img *image.RGBA, cx, cy int, c color.RGBA) {
	for i := -1; i <= 1; i++ {
		y := cy + i*14
		ui.Circle(img, cx-20, y, 4, c)
		ui.RoundRect(img, image.Rect(cx-9, y-2, cx+24, y+3), 2, c)
	}
}

// iconPen: a highlighter pen (highlights).
func iconPen(img *image.RGBA, cx, cy int, c color.RGBA) {
	line(img, cx-12, cy+12, cx+14, cy-14, 9, c)
	line(img, cx-18, cy+18, cx-12, cy+12, 4, c)
}

// iconLines: a page of lines with the middle one lit (line by line).
func iconLines(img *image.RGBA, cx, cy int, c, lit color.RGBA) {
	ui.RoundRect(img, image.Rect(cx-22, cy-18, cx+18, cy-14), 2, c)
	ui.RoundRect(img, image.Rect(cx-26, cy-6, cx+26, cy+6), 4, lit)
	ui.RoundRect(img, image.Rect(cx-22, cy+14, cx+12, cy+18), 2, c)
}

func iconAa(img *image.RGBA, cx, cy int, c color.RGBA) {
	f := apple()
	ui.DrawText(img, f.caption, cx-24, cy+11, c, "A")
	ui.DrawText(img, f.headline, cx-6, cy+13, c, "A")
}

// iconMagnifier: search.
func iconMagnifier(img *image.RGBA, cx, cy int, c color.RGBA) {
	ring(img, cx-4, cy-4, 12, 5, 1, c, c)
	line(img, cx+5, cy+5, cx+15, cy+15, 5, c)
}

// iconBookmark: a ribbon, filled or outlined.
func iconBookmark(img *image.RGBA, cx, cy int, c color.RGBA, filled bool) {
	r := image.Rect(cx-13, cy-18, cx+13, cy+18)
	if filled {
		ui.Fill(img, image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Max.Y-10), c)
		for i := 0; i < 12; i++ {
			ui.Fill(img, image.Rect(r.Min.X, r.Max.Y-10+i, r.Min.X+13-i, r.Max.Y-9+i), c)
			ui.Fill(img, image.Rect(r.Max.X-13+i, r.Max.Y-10+i, r.Max.X, r.Max.Y-9+i), c)
		}
		return
	}
	line(img, r.Min.X, r.Min.Y, r.Max.X, r.Min.Y, 4, c)
	line(img, r.Min.X, r.Min.Y, r.Min.X, r.Max.Y, 4, c)
	line(img, r.Max.X, r.Min.Y, r.Max.X, r.Max.Y, 4, c)
	line(img, r.Min.X, r.Max.Y, cx, r.Max.Y-12, 4, c)
	line(img, cx, r.Max.Y-12, r.Max.X, r.Max.Y, 4, c)
}

// iconBook: an open book (look up).
func iconBook(img *image.RGBA, cx, cy int, c color.RGBA) {
	line(img, cx-20, cy-12, cx-2, cy-8, 4, c)
	line(img, cx+20, cy-12, cx+2, cy-8, 4, c)
	line(img, cx-20, cy-12, cx-20, cy+12, 4, c)
	line(img, cx+20, cy-12, cx+20, cy+12, 4, c)
	line(img, cx-20, cy+12, cx, cy+16, 4, c)
	line(img, cx+20, cy+12, cx, cy+16, 4, c)
	line(img, cx, cy-8, cx, cy+16, 3, c)
}

// iconGlobe: translate.
func iconGlobe(img *image.RGBA, cx, cy int, c color.RGBA) {
	ring(img, cx, cy, 17, 4, 1, c, c)
	line(img, cx-17, cy, cx+17, cy, 3, c)
	for _, dx := range []int{-7, 7} {
		line(img, cx, cy-17, cx+dx, cy, 3, c)
		line(img, cx+dx, cy, cx, cy+17, 3, c)
	}
}

// iconTrash: remove.
func iconTrash(img *image.RGBA, cx, cy int, c color.RGBA) {
	line(img, cx-16, cy-12, cx+16, cy-12, 4, c)
	line(img, cx-6, cy-17, cx+6, cy-17, 4, c)
	line(img, cx-12, cy-10, cx-9, cy+16, 4, c)
	line(img, cx+12, cy-10, cx+9, cy+16, 4, c)
	line(img, cx-9, cy+16, cx+9, cy+16, 4, c)
}

func iconCheck(img *image.RGBA, x, cy int, c color.RGBA) {
	line(img, x, cy, x+10, cy+11, 5, c)
	line(img, x+10, cy+11, x+28, cy-12, 5, c)
}

// iconHalfMoon: the light/dark appearance button.
func iconHalfMoon(img *image.RGBA, cx, cy int, c color.RGBA) {
	ring(img, cx, cy, 16, 4, 1, c, c)
	for y := -16; y <= 16; y++ {
		w := int(math.Sqrt(float64(256 - y*y)))
		ui.Fill(img, image.Rect(cx, cy+y, cx+w, cy+y+1), c)
	}
}

func iconDots(img *image.RGBA, cx, cy int, c color.RGBA) {
	for i := -1; i <= 1; i++ {
		ui.Circle(img, cx+i*13, cy, 4, c)
	}
}

// --- surfaces --------------------------------------------------------------------------------

// shadow draws a soft drop shadow under a card (before the card itself).
func shadow(img *image.RGBA, r image.Rectangle, rad int, strength float64) {
	for i := 1; i <= 14; i += 2 {
		blendRect(img, image.Rect(r.Min.X+rad/2-i/2, r.Max.Y-rad/2+i, r.Max.X-rad/2+i/2, r.Max.Y-rad/2+i+2), rgb(0x000000), strength/2)
		blendRect(img, image.Rect(r.Min.X-i/2, r.Min.Y+rad/2, r.Min.X-i/2+1, r.Max.Y-rad/2), rgb(0x000000), strength/3)
		blendRect(img, image.Rect(r.Max.X+i/2-1, r.Min.Y+rad/2, r.Max.X+i/2, r.Max.Y-rad/2), rgb(0x000000), strength/3)
	}
}

// shadowRect is a cover's shadow.
func shadowRect(img *image.RGBA, r image.Rectangle) {
	for i := 1; i <= 12; i++ {
		blendRect(img, image.Rect(r.Min.X-i/2+4, r.Max.Y+i-2, r.Max.X+i/2-4, r.Max.Y+i-1), rgb(0x000000), 0.06)
		blendRect(img, image.Rect(r.Max.X+i-1, r.Min.Y+8, r.Max.X+i, r.Max.Y), rgb(0x000000), 0.035)
	}
}

// sheet dims what's behind and draws a sheet rising from the bottom to top, with rounded top
// corners and a grabber; it returns the sheet's rectangle.
func sheet(img *image.RGBA, top int, bg color.RGBA) image.Rectangle {
	b := img.Rect
	blendRect(img, image.Rect(0, 0, b.Dx(), top), rgb(0x000000), 0.25)
	r := image.Rect(0, top, b.Dx(), b.Max.Y)
	ui.RoundRect(img, image.Rect(0, top, b.Dx(), b.Max.Y+40), 36, bg)
	ui.RoundRect(img, image.Rect(b.Dx()/2-36, top+14, b.Dx()/2+36, top+22), 4, blend(bg, apSecondary, 0.6))
	return r
}

// segmented draws an iOS segmented control and registers its segments.
func segmented(p *page, r image.Rectangle, ids, labels []string, on string, bg, fg color.RGBA) {
	f := apple()
	ui.RoundRect(p.img, r, 20, blend(bg, fg, 0.07))
	w := r.Dx() / len(ids)
	for i, id := range ids {
		sr := image.Rect(r.Min.X+i*w+4, r.Min.Y+4, r.Min.X+(i+1)*w-4, r.Max.Y-4)
		face := f.callout
		if id == on {
			knob := rgb(0xffffff)
			if bg.R < 128 {
				knob = blend(bg, fg, 0.25)
			}
			ui.RoundRect(p.img, sr, 16, knob)
			face = f.captionBold
		}
		apTextCenter(p.img, face, (sr.Min.X+sr.Max.X)/2, (sr.Min.Y+sr.Max.Y)/2, fg, labels[i])
		p.buttons = append(p.buttons, button{id, sr})
	}
}

// ring draws a progress ring (the reading goal).
func ring(img *image.RGBA, cx, cy, rad, width int, frac float64, track, c color.RGBA) {
	for a := 0.0; a < 360; a += 1.2 {
		col := track
		if a/360 < frac {
			col = c
		}
		t := (a - 90) * math.Pi / 180
		ui.Circle(img, cx+int(float64(rad)*math.Cos(t)), cy+int(float64(rad)*math.Sin(t)), width/2, col)
	}
}

// --- the Books app's tab bar --------------------------------------------------------------

// booksTabs is the floating tab bar of Apple Books on iPad, with the reading goal ring.
func (c *console) booksTabs(p *page, on string) {
	f := apple()
	img := p.img
	tabs := []struct{ id, label string }{{"tab:home", "Home"}, {"tab:library", "Library"}, {"tab:store", "Store"},
		{"tab:words", "Words"}, {"tab:soma", "Soma"}, {"tab:terminal", "Terminal"}, {"tab:settings", "Settings"}}
	widths := make([]int, len(tabs))
	total := 80 // the magnifier
	for i, t := range tabs {
		widths[i] = ui.TextWidth(f.callout, t.label) + 40
		total += widths[i]
	}
	x := (c.s.W - total) / 2
	bar := image.Rect(x-10, 26, x+total+10, 106)
	shadow(img, bar, 40, 0.10)
	ui.RoundRect(img, bar, 40, apTabBG)
	for i, t := range tabs {
		r := image.Rect(x, bar.Min.Y+8, x+widths[i], bar.Max.Y-8)
		if t.id == on {
			ui.RoundRect(img, r, 32, apTabOn)
		}
		apTextCenter(img, f.callout, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, apLabel, t.label)
		p.buttons = append(p.buttons, button{t.id, r})
		x += widths[i]
	}
	iconMagnifier(img, x+40, (bar.Min.Y+bar.Max.Y)/2, apLabel)
	p.buttons = append(p.buttons, button{"tab:search", image.Rect(x, bar.Min.Y, x+80, bar.Max.Y)})

	// The reading goal, top right: minutes today in a ring, the goal under it.
	mins, goal := c.lib.readingToday(), c.lib.Prefs.GoalMinutes
	cx, cy := c.s.W-70, 66
	ring(img, cx, cy, 30, 7, float64(mins)/float64(max(goal, 1)), apRingTrack, apRingBlue)
	apTextCenter(img, f.captionBold, cx, cy-4, apRingBlue, fmt.Sprint(mins))
	apTextCenter(img, textFace("inter", fonts.InterRegular, false, 15), cx, cy+16, apSecondary, fmt.Sprint(goal))
	p.buttons = append(p.buttons, button{"r:goal", image.Rect(cx-50, 10, cx+60, 120)})
}

// booksTap handles the tab bar. Caller holds drawMu.
func (c *console) booksTap(id string) bool {
	switch id {
	case "books": // from the home screen
		c.transition("push", image.Rectangle{}, func() { c.setMode(modeBooksHome) })
	case "tab:home":
		c.setMode(modeBooksHome)
	case "tab:library":
		c.setMode(modeBooks)
	case "tab:store":
		c.store.sel = nil
		c.setMode(modeStore)
	case "tab:search":
		c.store.sel = nil
		c.mode = modeStore
		c.storeTap("s:search")
		return true
	case "tab:words":
		c.wui = wordsUI{}
		c.setMode(modeWords)
	case "tab:soma":
		c.setMode(modeSoma)
		go soma.sync()
	case "tab:terminal":
		c.setMode(modeTerminal)
	case "tab:settings":
		c.setMode(modeSettings)
	default:
		return false
	}
	return true
}
