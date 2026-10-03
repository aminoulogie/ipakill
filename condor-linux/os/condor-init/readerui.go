package main

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"math"
	"strings"
	"time"

	"condor-init/ui"
)

// What happens ON the page: gestures, the selection and its word menu, the answer card,
// the settings sheet, and the contents and highlights lists.

type selection struct{ anchor, from, to int } // chapter word indices

type menuState int

const (
	menuNone menuState = iota
	menuMain           // highlight · look up · <language> · keep
	menuInk            // the five colours (and remove)
)

type lookupPanel struct {
	kind    string // "meaning" or "translate"
	query   string
	isWord  bool
	loading bool
	err     string
	look    *wordLookup
	tr      *translation
	lang    string
	gen     int
}

type gesture struct {
	active, moved, held, selecting bool
	slot, x0, y0, x, y, id         int
	lastDraw                       time.Time
}

type readerUI struct {
	sel        *selection
	menu       menuState
	panel      *lookupPanel
	settings   bool
	chrome     bool   // Apple Books' controls, shown by a tap in the middle of the page
	view       string // "", "contents", "marks"
	listFrom   int
	toast      string
	toastUntil time.Time
	g          gesture
	gen        int
}

const holdDelay = 350 * time.Millisecond // Soma: a third of a second, what a thumb knows from iOS

// --- hit testing ---------------------------------------------------------------------------

// wordAt is the line and word under a point (page coordinates), or -1s.
func (c *console) wordAt(x, y int) (li, word int) {
	ob := c.book
	tr := c.textRect()
	for i, l := range ob.pages[ob.page] {
		top, bot := tr.Min.Y+l.y, tr.Min.Y+l.y+l.h
		if y < top || y >= bot {
			continue
		}
		best, bestD := -1, math.MaxInt
		for _, w := range l.words {
			x0, x1 := tr.Min.X+w.x, tr.Min.X+w.x+w.w
			d := 0
			if x < x0 {
				d = x0 - x
			} else if x > x1 {
				d = x - x1
			}
			if d < bestD {
				best, bestD = w.idx, d
			}
		}
		return i, best
	}
	return -1, -1
}

func (c *console) selText() string {
	s, ob := c.rd.sel, c.book
	if s == nil || s.from < 0 || s.to >= len(ob.words) {
		return ""
	}
	return strings.Join(ob.words[s.from:s.to+1], " ")
}

func (c *console) closeOverlays() {
	c.rd.sel, c.rd.menu, c.rd.panel, c.rd.settings = nil, menuNone, nil, false
}

func (c *console) overlayOpen() bool {
	return c.rd.sel != nil || c.rd.menu != menuNone || c.rd.panel != nil || c.rd.settings
}

// --- gestures --------------------------------------------------------------------------------

// readerTouch takes the finger on a book page. Caller holds drawMu.
func (c *console) readerTouch(p TouchPoint) {
	g := &c.rd.g
	switch {
	case p.Down: // a new finger: a new gesture (a touch the bar took may have left one open)
		g.id++
		*g = gesture{active: true, slot: p.Slot, x0: p.X, y0: p.Y, x: p.X, y: p.Y, id: g.id}
		id := g.id
		if c.rd.view == "" {
			time.AfterFunc(holdDelay, func() {
				drawMu.Lock()
				defer drawMu.Unlock()
				if g.active && g.id == id && !g.moved && c.mode == modeReader {
					g.held = true
					c.longPress(g.x0, g.y0)
				}
			})
		}
	case !g.active || p.Slot != g.slot:
		return
	case p.Moved && !p.Up:
		g.x, g.y = p.X, p.Y
		if abs(g.x-g.x0) > 14 || abs(g.y-g.y0) > 14 {
			g.moved = true
		}
		if g.selecting && time.Since(g.lastDraw) > 90*time.Millisecond {
			c.extendSelection(p.X, p.Y)
			g.lastDraw = time.Now()
		}
	case p.Up:
		g.active = false
		g.x, g.y = p.X, p.Y
		switch {
		case g.selecting:
			g.selecting = false
			c.extendSelection(p.X, p.Y)
			if !isSelectable(c.selText()) {
				c.closeOverlays()
			}
			c.showPage()
		case g.held:
		case g.moved:
			dx, dy := g.x-g.x0, g.y-g.y0
			if c.rd.view == "" && abs(dx) > 100 && abs(dx) > 2*abs(dy) {
				if c.overlayOpen() {
					c.closeOverlays()
					c.showPage()
				} else if dx < 0 {
					c.turn(1)
				} else {
					c.turn(-1)
				}
			}
		default:
			c.readerTapAt(p.X, p.Y)
		}
	}
}

// readerTapAt is a plain tap: a button, or (with something open) closing it.
func (c *console) readerTapAt(x, y int) {
	if c.page == nil {
		return
	}
	if c.page.hit(x, y-c.barH) == "" {
		if c.overlayOpen() {
			c.closeOverlays()
			c.showPage()
		}
		return
	}
	c.pageTap(x, y)
}

// longPress selects the word under the finger (the whole highlight if it's in one) and, in
// line-by-line mode, moves the lit line there.
func (c *console) longPress(x, y int) {
	ob := c.book
	if ob == nil || c.rd.view != "" || c.rd.settings {
		return
	}
	li, w := c.wordAt(x, y-c.barH)
	if w < 0 {
		return
	}
	c.readTick()
	if c.lib.Prefs.LineFocus {
		ob.line = li
		c.saveProgress()
	}
	c.rd.panel = nil
	c.rd.sel = &selection{w, w, w}
	if m := markAt(c.lib.Marks[ob.path], ob.chapter, w); m != nil {
		c.rd.sel = &selection{m.Start, m.Start, m.End}
	}
	c.rd.menu = menuMain
	c.rd.g.selecting = true
	c.showPage()
}

func (c *console) extendSelection(x, y int) {
	s := c.rd.sel
	if s == nil {
		return
	}
	_, w := c.wordAt(x, y-c.barH)
	if w < 0 {
		return
	}
	from, to := min(s.anchor, w), max(s.anchor, w)
	if s.from == from && s.to == to {
		return
	}
	if len([]rune(strings.Join(c.book.words[from:to+1], " "))) > maxPassage {
		return // Soma: beyond MAX_PASSAGE it isn't a passage, it's a chapter
	}
	s.from, s.to = from, to
	c.showPage()
}

// --- overlays ------------------------------------------------------------------------------

func (c *console) selBands() (first, last image.Rectangle, ok bool) {
	s := c.rd.sel
	if s == nil {
		return
	}
	for _, l := range c.book.pages[c.book.page] {
		if r, in := c.wordsRect(l, s.from, s.to); in {
			if !ok {
				first, ok = r, true
			}
			last = r
		}
	}
	return
}

// sheetColours are an Apple sheet's background, text and secondary text on a theme.
func sheetColours(th readerTheme) (bg, label, secondary, sep color.RGBA) {
	if th.dark {
		return apCard, apLabel, apSecondary, apSeparator
	}
	return rgb(0xf2f2f7), rgb(0x000000), rgb(0x6c6c70), rgb(0xd1d1d6)
}

// drawOverlays draws the selection's callout menu, the Look Up / Translate sheet, the Themes
// & Settings sheet and a toast, registering their buttons first.
func (c *console) drawOverlays(p *page, th readerTheme) {
	if c.rd.settings {
		c.drawSettings(p, th)
		return
	}
	first, last, ok := c.selBands()
	if c.rd.menu != menuNone && ok {
		c.drawCallout(p, first, last)
	}
	if pn := c.rd.panel; pn != nil {
		c.drawPanel(p, th, pn)
	}
	if c.rd.toast != "" && time.Now().Before(c.rd.toastUntil) {
		f := apple()
		w := ui.TextWidth(f.callout, c.rd.toast) + 80
		r := image.Rect((c.s.W-w)/2, 120, (c.s.W+w)/2, 196)
		ui.RoundRect(p.img, r, 38, apCallout)
		apTextCenter(p.img, f.callout, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, apLabel, c.rd.toast)
	}
}

// drawCallout is the dark menu over a selection, with its arrow pointing at the words.
func (c *console) drawCallout(p *page, first, last image.Rectangle) {
	f := apple()
	ph := c.s.H - c.barH
	const mh = 104
	text := c.selText()
	onMark := markAt(c.lib.Marks[c.book.path], c.book.chapter, c.rd.sel.from) != nil
	type item struct{ id, label string }
	var items []item
	if c.rd.menu == menuInk {
		for _, m := range markColours {
			items = append(items, item{"r:ink:" + m.id, ""})
		}
		if onMark {
			items = append(items, item{"r:unmark", "Remove"})
		}
	} else {
		items = append(items, item{"r:hl", "Highlight"})
		if isCapturable(text) {
			items = append(items, item{"r:lookup", "Look Up"})
		}
		items = append(items, item{"r:translate", "Translate"})
		if isCapturable(text) {
			items = append(items, item{"r:keep", "Keep"})
		}
	}
	widths := make([]int, len(items))
	total := 0
	for i, it := range items {
		widths[i] = 96
		if it.label != "" {
			widths[i] = ui.TextWidth(f.callout, it.label) + 64
		}
		total += widths[i]
	}
	cx := (first.Min.X + first.Max.X) / 2
	x0 := min(max(cx-total/2, 24), c.s.W-24-total)
	above := first.Min.Y-mh-30 > 150
	y := first.Min.Y - mh - 24
	if !above {
		y = min(last.Max.Y+24, ph-mh-20)
	}
	r := image.Rect(x0, y, x0+total, y+mh)
	ui.RoundRect(p.img, r, 22, apCallout)
	// The arrow.
	ax := min(max(cx, r.Min.X+40), r.Max.X-40)
	for i := 0; i < 18; i++ {
		if above {
			ui.Fill(p.img, image.Rect(ax-18+i, r.Max.Y+i, ax+18-i, r.Max.Y+i+1), apCallout)
		} else {
			ui.Fill(p.img, image.Rect(ax-18+i, r.Min.Y-i-1, ax+18-i, r.Min.Y-i), apCallout)
		}
	}
	x := r.Min.X
	for i, it := range items {
		ir := image.Rect(x, r.Min.Y, x+widths[i], r.Max.Y)
		if it.label == "" { // an ink
			mc := markColour(strings.TrimPrefix(it.id, "r:ink:"))
			ui.Circle(p.img, (ir.Min.X+ir.Max.X)/2, (ir.Min.Y+ir.Max.Y)/2, 28, mc.chip)
		} else {
			col := apLabel
			if it.id == "r:unmark" {
				col = rgb(0xff453a)
			}
			apTextCenter(p.img, f.callout, (ir.Min.X+ir.Max.X)/2, (ir.Min.Y+ir.Max.Y)/2, col, it.label)
		}
		if i > 0 && it.label != "" || i > 0 && items[i-1].label != "" {
			ui.Fill(p.img, image.Rect(x, r.Min.Y+20, x+2, r.Max.Y-20), rgb(0x48484a))
		}
		p.buttons = append(p.buttons, button{it.id, ir})
		x += widths[i]
	}
}

// drawPanel is the Look Up / Translate sheet.
func (c *console) drawPanel(p *page, th readerTheme, pn *lookupPanel) {
	f := apple()
	ph := c.s.H - c.barH
	bg, label, secondary, sep := sheetColours(th)
	acc := th.accent()
	r := sheet(p.img, ph-860, bg)
	p.buttons = append(p.buttons, button{"r:close", image.Rect(0, 0, c.s.W, r.Min.Y)}) // tap above: close
	x, w := 48, c.s.W-96
	y := r.Min.Y + 90
	apTextRight(p.img, f.headline, c.s.W-48, y, acc, "Done")
	p.buttons = append(p.buttons, button{"r:close", image.Rect(c.s.W-220, r.Min.Y, c.s.W, r.Min.Y+130)})
	title := "Look Up"
	if pn.kind == "translate" {
		title = "Translate"
	}
	apText(p.img, f.captionBold, x, y-6, secondary, strings.ToUpper(title))
	y += 20
	head := pn.query
	if len([]rune(head)) > 120 {
		head = string([]rune(head)[:119]) + "…"
	}
	for i, l := range layoutWords(f.title, strings.Fields(head), 0, w, false) {
		if i == 2 {
			break
		}
		y += 56
		drawWords(p.img, f.title, l, x, y, label)
	}
	y += 60
	bottom := r.Max.Y - 150
	if pn.kind == "translate" {
		apText(p.img, f.caption, x, y+10, secondary, languageLabel(c.bookLang())+"  →  "+languageLabel(pn.lang))
		y += 40
		px := x
		for _, l := range languages {
			lw := ui.TextWidth(f.captionBold, strings.ToUpper(l.code)) + 44
			pr := image.Rect(px, y, px+lw, y+64)
			bgc, fg := blend(bg, label, 0.08), label
			if l.code == pn.lang {
				bgc, fg = acc, rgb(0xffffff)
			}
			ui.RoundRect(p.img, pr, 32, bgc)
			apTextCenter(p.img, f.captionBold, (pr.Min.X+pr.Max.X)/2, (pr.Min.Y+pr.Max.Y)/2, fg, strings.ToUpper(l.code))
			p.buttons = append(p.buttons, button{"r:lang:" + l.code, pr})
			px += lw + 12
		}
		y += 100
	}
	ui.Fill(p.img, image.Rect(x, y-20, x+w, y-18), sep)
	switch {
	case pn.loading:
		msg := "Looking up…"
		if pn.kind == "translate" {
			msg = "Translating…"
		}
		apText(p.img, f.body, x, y+30, secondary, msg)
	case pn.err != "":
		drawParagraphs(p.img, f.body, strings.ToUpper(pn.err[:1])+pn.err[1:], x, y, w, 48, bottom, secondary)
	case pn.look != nil:
		apText(p.img, f.captionBold, x, y+20, secondary, "DICTIONARY · "+strings.ToUpper(pn.look.source))
		y += 46
		for i, s := range pn.look.senses {
			if s.pos != "" {
				apText(p.img, f.captionBold, x, y+30, acc, fmt.Sprintf("%d  %s", i+1, strings.ToLower(s.pos)))
				y += 44
			}
			y = drawParagraphs(p.img, f.body, s.definition, x, y, w, 46, bottom, label)
			if s.example != "" {
				y = drawParagraphs(p.img, f.callout, "“"+s.example+"”", x+24, y, w-24, 42, bottom, secondary)
			}
			y += 16
		}
	case pn.tr != nil:
		y = drawParagraphs(p.img, f.headline, pn.tr.text, x, y, w, 52, bottom, label)
		if pn.tr.quality >= 0 && pn.tr.quality < 0.7 {
			apText(p.img, f.caption, x, min(y+40, bottom), secondary, "Machine translation, may be loose")
		}
	}
	// Actions.
	type act struct{ id, label string }
	var acts []act
	if pn.isWord {
		acts = append(acts, act{"r:keep", "Keep Word"})
	}
	if pn.kind == "meaning" {
		acts = append(acts, act{"r:translate", "Translate"})
	} else if pn.isWord {
		acts = append(acts, act{"r:lookup", "Look Up"})
	}
	if len(acts) > 0 {
		gap := 20
		bw := (w - gap*(len(acts)-1)) / len(acts)
		for i, a := range acts {
			br := image.Rect(x+i*(bw+gap), r.Max.Y-130, x+i*(bw+gap)+bw, r.Max.Y-36)
			ui.RoundRect(p.img, br, 24, blend(bg, label, 0.08))
			apTextCenter(p.img, f.headline, (br.Min.X+br.Max.X)/2, (br.Min.Y+br.Max.Y)/2, acc, a.label)
			p.buttons = append(p.buttons, button{a.id, br})
		}
	}
	p.buttons = append(p.buttons, button{"r:panel", r})
}

// readerThemeNames: Apple Books' names for the three papers.
var readerThemeNames = []string{"Night", "Original", "Calm"}

// iosSwitch draws an on/off switch with its right edge at right.
func iosSwitch(img *image.RGBA, right, cy int, on, dark bool) image.Rectangle {
	r := image.Rect(right-102, cy-31, right, cy+31)
	track := rgb(0xe9e9eb)
	if dark {
		track = rgb(0x39393d)
	}
	if on {
		track = rgb(0x30d158)
	}
	ui.RoundRect(img, r, 31, track)
	kx := r.Min.X + 31
	if on {
		kx = r.Max.X - 31
	}
	ui.Circle(img, kx, cy, 27, rgb(0xffffff))
	return r
}

// drawSettings is Apple Books' Themes & Settings sheet, with Soma's extra controls.
func (c *console) drawSettings(p *page, th readerTheme) {
	f := apple()
	ph := c.s.H - c.barH
	pr := c.lib.Prefs
	bg, label, secondary, sep := sheetColours(th)
	acc := th.accent()
	r := sheet(p.img, ph-1500, bg)
	p.buttons = append(p.buttons, button{"settings", image.Rect(0, 0, c.s.W, r.Min.Y)}) // tap above: close
	x, w := 40, c.s.W-80
	y := r.Min.Y + 90
	apText(p.img, f.headline, x, y, label, "Themes & Settings")
	apTextRight(p.img, f.headline, x+w, y, acc, "Done")
	p.buttons = append(p.buttons, button{"settings", image.Rect(c.s.W-220, r.Min.Y, c.s.W, r.Min.Y+130)})
	y += 40
	pill := blend(bg, label, 0.08)

	// Text size and brightness: two capsules split down the middle.
	capsule := func(cr image.Rectangle, lid, rid string, drawL, drawR func(cx, cy int)) {
		ui.RoundRect(p.img, cr, 26, pill)
		mid := (cr.Min.X + cr.Max.X) / 2
		ui.Fill(p.img, image.Rect(mid-1, cr.Min.Y+18, mid+1, cr.Max.Y-18), sep)
		drawL((cr.Min.X+mid)/2, (cr.Min.Y+cr.Max.Y)/2)
		drawR((mid+cr.Max.X)/2, (cr.Min.Y+cr.Max.Y)/2)
		p.buttons = append(p.buttons, button{lid, image.Rect(cr.Min.X, cr.Min.Y, mid, cr.Max.Y)},
			button{rid, image.Rect(mid, cr.Min.Y, cr.Max.X, cr.Max.Y)})
	}
	half := (w - 24) / 2
	capsule(image.Rect(x, y, x+half, y+100), "r:set:size:-", "r:set:size:+",
		func(cx, cy int) { apTextCenter(p.img, f.caption, cx, cy, label, "A") },
		func(cx, cy int) { apTextCenter(p.img, f.title, cx, cy, label, "A") })
	sun := func(rad int) func(cx, cy int) {
		return func(cx, cy int) {
			ui.Circle(p.img, cx, cy, rad, label)
			for a := 0; a < 8; a++ {
				t := float64(a) * math.Pi / 4
				line(p.img, cx+int(float64(rad+6)*math.Cos(t)), cy+int(float64(rad+6)*math.Sin(t)),
					cx+int(float64(rad+12)*math.Cos(t)), cy+int(float64(rad+12)*math.Sin(t)), 4, label)
			}
		}
	}
	capsule(image.Rect(x+half+24, y, x+w, y+100), "r:set:bright:-", "r:set:bright:+", sun(6), sun(11))
	y += 130

	// Themes as tiles of their own paper and ink.
	tw := (w - 2*24) / 3
	for i, t := range readerThemes {
		tr := image.Rect(x+i*(tw+24), y, x+i*(tw+24)+tw, y+200)
		if i == pr.Theme {
			ui.RoundRect(p.img, tr.Inset(-6), 30, acc)
		} else {
			ui.RoundRect(p.img, tr.Inset(-2), 26, sep)
		}
		ui.RoundRect(p.img, tr, 24, t.bg)
		ui.DrawTextCentered(p.img, newReaderFonts(pr.Font, 64, 1.4).body, (tr.Min.X+tr.Max.X)/2, (tr.Min.Y+tr.Max.Y)/2, t.fg, "Aa")
		apTextCenter(p.img, f.callout, (tr.Min.X+tr.Max.X)/2, tr.Max.Y+36, label, readerThemeNames[i])
		p.buttons = append(p.buttons, button{fmt.Sprintf("r:set:theme:%d", i), image.Rect(tr.Min.X, tr.Min.Y, tr.Max.X, tr.Max.Y+56)})
	}
	y += 280

	// Fonts: each name in its own face, a check on the one in use.
	group := image.Rect(x, y, x+w, y+len(readerFontList)*84)
	ui.RoundRect(p.img, group, 24, pill)
	for i, rf := range readerFontList {
		rr := image.Rect(x, y+i*84, x+w, y+(i+1)*84)
		if i > 0 {
			ui.Fill(p.img, image.Rect(x+30, rr.Min.Y, x+w, rr.Min.Y+2), sep)
		}
		face := newReaderFonts(rf.id, 36, 1.4).body
		ui.DrawText(p.img, face, x+30, rr.Min.Y+56, label, rf.label)
		if rf.id == pr.Font {
			iconCheck(p.img, x+w-70, (rr.Min.Y+rr.Max.Y)/2, acc)
		}
		p.buttons = append(p.buttons, button{"r:set:font:" + rf.id, rr})
	}
	y = group.Max.Y + 50

	// Soma's controls.
	apText(p.img, f.captionBold, x+10, y, secondary, "CUSTOMIZE")
	y += 20
	rows := 5
	group = image.Rect(x, y, x+w, y+rows*92)
	ui.RoundRect(p.img, group, 24, pill)
	row := func(i int, name string) (int, int) { // returns the row's centre y and right edge
		top := y + i*92
		if i > 0 {
			ui.Fill(p.img, image.Rect(x+30, top, x+w, top+2), sep)
		}
		apText(p.img, f.body, x+30, top+60, label, name)
		return top + 46, x + w - 24
	}
	stepper := func(i int, name, id, value string) {
		cy, right := row(i, name)
		sr := image.Rect(right-320, cy-34, right, cy+34)
		ui.RoundRect(p.img, sr, 20, blend(pill, label, 0.08))
		apTextCenter(p.img, f.headline, sr.Min.X+50, cy, label, "−")
		apTextCenter(p.img, f.callout, (sr.Min.X+sr.Max.X)/2, cy, label, value)
		apTextCenter(p.img, f.headline, sr.Max.X-50, cy, label, "+")
		p.buttons = append(p.buttons, button{id + ":-", image.Rect(sr.Min.X, sr.Min.Y-12, sr.Min.X+110, sr.Max.Y+12)},
			button{id + ":+", image.Rect(sr.Max.X-110, sr.Min.Y-12, sr.Max.X, sr.Max.Y+12)})
	}
	stepper(0, "Line Spacing", "r:set:lh", fmt.Sprintf("%.1f", pr.LineHeight))
	stepper(1, "Margins", "r:set:margin", fmt.Sprintf("%d", pr.Margin))
	cy, right := row(2, "Line by Line")
	sw := iosSwitch(p.img, right, cy, pr.LineFocus, th.dark)
	p.buttons = append(p.buttons, button{map[bool]string{true: "r:set:line:off", false: "r:set:line:on"}[pr.LineFocus], sw.Inset(-14)})
	cy, right = row(3, "Translate To")
	apTextRight(p.img, f.body, right-30, cy+12, acc, languageLabel(pr.TranslateTo))
	line(p.img, right-14, cy-12, right-2, cy, 4, secondary)
	line(p.img, right-2, cy, right-14, cy+12, 4, secondary)
	p.buttons = append(p.buttons, button{"r:set:trnext", image.Rect(x+w/2, cy-46, x+w, cy+46)})
	stepper(4, fmt.Sprintf("Daily Goal  (today %d)", c.lib.readingToday()), "r:set:goal", fmt.Sprintf("%d min", pr.GoalMinutes))
	p.buttons = append(p.buttons, button{"r:panel", r})
}

func (c *console) toastMsg(s string) {
	c.rd.toast, c.rd.toastUntil = s, time.Now().Add(2*time.Second)
	time.AfterFunc(2100*time.Millisecond, func() {
		drawMu.Lock()
		defer drawMu.Unlock()
		if c.mode == modeReader && c.rd.toast == s && time.Now().After(c.rd.toastUntil) {
			c.rd.toast = ""
			c.showPage()
		}
	})
}

// --- lookups ------------------------------------------------------------------------------

func (c *console) bookLang() string {
	if c.book != nil && c.book.b.Language != "" {
		return c.book.b.Language
	}
	return "en"
}

// ask opens the card and fetches a meaning or a translation in the background.
func (c *console) ask(kind string) {
	text := c.selText()
	if text == "" {
		return
	}
	c.rd.gen++
	pn := &lookupPanel{kind: kind, query: cleanSelection(text), isWord: isCapturable(text), loading: true,
		lang: c.lib.Prefs.TranslateTo, gen: c.rd.gen}
	if kind == "translate" {
		pn.query = strings.Join(strings.Fields(text), " ")
	}
	c.rd.panel, c.rd.menu = pn, menuNone
	from := c.bookLang()
	go func() {
		ctx := context.Background()
		var look *wordLookup
		var tr *translation
		var err error
		if kind == "meaning" {
			look, err = lookupWord(ctx, pn.query, from)
		} else {
			tr, err = translateText(ctx, pn.query, from, pn.lang)
		}
		drawMu.Lock()
		defer drawMu.Unlock()
		if c.rd.panel != pn || pn.gen != c.rd.gen {
			return
		}
		pn.loading, pn.look, pn.tr = false, look, tr
		if err != nil {
			pn.err = err.Error()
		}
		if c.mode == modeReader {
			c.showPage()
		}
	}()
}

// keepWord files the selection in the word book, with the meaning if one is on screen; if
// not, a lookup fills it in afterwards.
func (c *console) keepWord() {
	ob := c.book
	text := c.selText()
	if !isCapturable(text) {
		return
	}
	block := ""
	if s := c.rd.sel; s != nil && s.from < len(ob.blockOf) {
		block = ob.blocks[ob.blockOf[s.from]].Text
	}
	e := wordEntry{Word: cleanSelection(text), Sentence: sentenceAround(block, text), Book: ob.b.Title, Lang: c.bookLang()}
	if pn := c.rd.panel; pn != nil {
		switch {
		case pn.look != nil:
			var parts []string
			for _, s := range pn.look.senses[:min(2, len(pn.look.senses))] {
				parts = append(parts, strings.TrimSpace(s.pos+" "+s.definition))
			}
			e.Meaning = strings.Join(parts, "; ")
		case pn.tr != nil:
			e.Meaning = pn.tr.text
		}
	}
	isNew := c.words.keep(e)
	msg := "kept in words"
	if !isNew {
		msg = "already in words"
	}
	c.closeOverlays()
	c.toastMsg(msg)
	if e.Meaning == "" {
		word, lang := e.Word, e.Lang
		go func() {
			look, err := lookupWord(context.Background(), word, lang)
			if err != nil || look == nil {
				return
			}
			var parts []string
			for _, s := range look.senses[:min(2, len(look.senses))] {
				parts = append(parts, strings.TrimSpace(s.pos+" "+s.definition))
			}
			drawMu.Lock()
			defer drawMu.Unlock()
			for i := range c.words.Words {
				if strings.EqualFold(c.words.Words[i].Word, word) && c.words.Words[i].Meaning == "" {
					c.words.Words[i].Meaning = strings.Join(parts, "; ")
					c.words.save()
				}
			}
		}()
	}
}

// --- taps ------------------------------------------------------------------------------------

// readerTap handles the shelf's, the reader's and its overlays' buttons. Caller holds drawMu.
func (c *console) readerTap(id string) bool {
	ob := c.book
	pr := &c.lib.Prefs
	switch {
	case id == "books":
		c.setMode(modeBooks)
		return true
	case strings.HasPrefix(id, "book"):
		var i int
		if _, err := fmt.Sscanf(id, "book%d", &i); err == nil && i < len(c.shelf) {
			c.fromStore = false
			c.openBookAt(c.shelf[i].path)
		}
		return true
	case id == "shelf:prev" || id == "shelf:next":
		if id == "shelf:next" && c.shelfFrom+c.shelfPer < len(c.shelf) {
			c.shelfFrom += c.shelfPer
		} else if id == "shelf:prev" {
			c.shelfFrom = max(c.shelfFrom-c.shelfPer, 0)
		}
		c.showPage()
		return true
	case id == "r:goal":
		pr.GoalMinutes = nextGoal(pr.GoalMinutes)
		c.lib.save()
		c.showPage()
		return true
	}
	if ob == nil || c.mode != modeReader {
		return false
	}
	relayout := false
	switch {
	case id == "shelf" && c.fromStore:
		c.saveProgress()
		c.setMode(modeStore)
		return true
	case id == "shelf":
		c.saveProgress()
		c.setMode(modeBooks)
		return true
	case id == "prev" || id == "next":
		dir := map[string]int{"prev": -1, "next": 1}[id]
		if pr.LineFocus {
			c.stepLine(dir)
		} else {
			c.turn(dir)
		}
		return true
	case id == "chrome":
		c.rd.chrome = !c.rd.chrome
	case id == "r:bar":
		return true // the controls' bars, between their buttons
	case strings.HasPrefix(id, "r:seek:"):
		var at int
		fmt.Sscanf(id, "r:seek:%d", &at)
		c.seek(at)
	case id == "contents" || id == "marks" || id == "r:list:contents" || id == "r:list:marks":
		c.closeOverlays()
		c.rd.chrome = false
		c.rd.view, c.rd.listFrom = strings.TrimPrefix(id, "r:list:"), -1
	case id == "linemode":
		pr.LineFocus = !pr.LineFocus
		ob.line = 0
		c.rd.chrome = false
		c.lib.save()
		c.invalidatePage()
	case id == "settings":
		open := !c.rd.settings
		c.closeOverlays()
		c.rd.settings, c.rd.chrome = open, false
	case id == "r:hl":
		c.rd.menu = menuInk
	case id == "r:inkback":
		c.rd.menu = menuMain
	case strings.HasPrefix(id, "r:ink:"):
		c.highlight(strings.TrimPrefix(id, "r:ink:"))
		c.closeOverlays()
	case id == "r:unmark":
		if m := markAt(c.lib.Marks[ob.path], ob.chapter, c.rd.sel.from); m != nil {
			c.unmark(m.ID)
		}
		c.closeOverlays()
	case id == "r:lookup":
		c.ask("meaning")
	case id == "r:translate":
		c.ask("translate")
	case strings.HasPrefix(id, "r:lang:"):
		pr.TranslateTo = strings.TrimPrefix(id, "r:lang:")
		c.lib.save()
		c.ask("translate")
	case id == "r:keep":
		c.keepWord()
	case id == "r:close":
		c.closeOverlays()
	case id == "r:panel":
		return true // a tap on a card, between its buttons
	case strings.HasPrefix(id, "r:set:"):
		relayout = c.applySetting(strings.TrimPrefix(id, "r:set:"))
	case strings.HasPrefix(id, "r:list:") || strings.HasPrefix(id, "r:toc:") || strings.HasPrefix(id, "r:mark:") ||
		strings.HasPrefix(id, "r:del:"):
		c.listTap(id)
	default:
		return false
	}
	if relayout {
		c.relayout()
		c.saveProgress()
	}
	c.showPage()
	return true
}

func nextGoal(m int) int {
	for _, g := range []int{10, 15, 20, 30, 45, 60, 90} {
		if g > m {
			return g
		}
	}
	return 10
}

// applySetting changes one reader setting; it reports whether the pages must be laid out
// again.
func (c *console) applySetting(s string) bool {
	pr := &c.lib.Prefs
	defer c.lib.save()
	parts := strings.Split(s, ":")
	what, arg := parts[0], ""
	if len(parts) > 1 {
		arg = parts[1]
	}
	step := map[string]int{"-": -1, "+": 1}[arg]
	switch what {
	case "theme":
		fmt.Sscan(arg, &pr.Theme)
		pr.Theme = min(max(pr.Theme, 0), len(readerThemes)-1)
		c.invalidatePage()
	case "font":
		pr.Font = readerFontList[fontIndex(arg)].id
		return true
	case "size":
		pr.Size = min(max(pr.Size+4*step, textSizeMin), textSizeMax)
		return true
	case "lh":
		pr.LineHeight = math.Round(min(max(pr.LineHeight+0.1*float64(step), lineHeightMin), lineHeightMax)*10) / 10
		return true
	case "margin":
		pr.Margin = min(max(pr.Margin+24*step, marginMin), marginMax)
		return true
	case "line":
		pr.LineFocus = arg == "on"
		c.book.line = 0
		c.invalidatePage()
	case "tr":
		if isLanguage(arg) {
			pr.TranslateTo = arg
		}
	case "trnext":
		for i, l := range languages {
			if l.code == pr.TranslateTo {
				pr.TranslateTo = languages[(i+1)%len(languages)].code
				break
			}
		}
	case "bright":
		c.cfg.Brightness = min(max(c.cfg.Brightness+10*step, 10), 100)
		setBacklight(c.cfg.Brightness)
		c.cfg.save()
	case "goal":
		pr.GoalMinutes = min(max(pr.GoalMinutes+5*step, 5), 240)
	}
	return false
}

// --- contents and highlights -------------------------------------------------------------

const listPerPage = 10

func (c *console) readerListPage() *page {
	ob := c.book
	th := readerThemes[c.lib.Prefs.Theme]
	f := apple()
	h := c.s.H - c.barH
	img := canvas(c.s.W, h)
	ui.Fill(img, img.Rect, th.bg)
	p := &page{img: img}
	acc := th.accent()
	_, _, secondary, sep := sheetColours(th)
	label := th.fg

	apTextCenter(img, f.headline, c.s.W/2, 70, label, clip(f.headline, ob.b.Title, c.s.W-480))
	apTextRight(img, f.headline, c.s.W-48, 82, acc, "Done")
	p.buttons = append(p.buttons, button{"r:list:back", image.Rect(c.s.W-240, 0, c.s.W, 130)})
	segmented(p, image.Rect(c.s.W/2-330, 120, c.s.W/2+330, 196), []string{"r:list:contents", "r:list:marks"},
		[]string{"Contents", "Highlights"}, "r:list:"+c.rd.view, th.bg, label)

	type row struct {
		id, text, sub, right string
		chip                 *inkColour
		del                  string
		current              bool
	}
	var rows []row
	if c.rd.view == "contents" {
		if ob.titles == nil {
			apText(img, f.body, 48, 300, secondary, "Reading the chapters…")
			return p
		}
		for i, t := range ob.titles {
			rows = append(rows, row{id: fmt.Sprintf("r:toc:%d", i), text: t, right: fmt.Sprint(i + 1), current: i == ob.chapter})
		}
	} else {
		for _, m := range c.lib.Marks[ob.path] {
			mc := markColour(m.Colour)
			sub := fmt.Sprintf("Chapter %d", m.Chapter+1)
			if ob.titles != nil && m.Chapter < len(ob.titles) {
				sub = ob.titles[m.Chapter]
			}
			rows = append(rows, row{id: "r:mark:" + m.ID, text: m.Text, sub: sub + "  ·  " + m.Added, chip: &mc, del: "r:del:" + m.ID})
		}
		if len(rows) == 0 {
			apTextCenter(img, f.title, c.s.W/2, 480, label, "No Highlights")
			apTextCenter(img, f.callout, c.s.W/2, 550, secondary, "Touch and hold a word, drag to take in more,")
			apTextCenter(img, f.callout, c.s.W/2, 596, secondary, "then tap Highlight.")
			return p
		}
	}
	if c.rd.listFrom < 0 { // open on the current chapter
		c.rd.listFrom = 0
		for i, r := range rows {
			if r.current {
				c.rd.listFrom = i / listPerPage * listPerPage
			}
		}
	}
	c.rd.listFrom = min(c.rd.listFrom, (len(rows)-1)/listPerPage*listPerPage)
	rowH := 150
	y := 230
	for i := c.rd.listFrom; i < len(rows) && i < c.rd.listFrom+listPerPage; i++ {
		r := rows[i]
		rr := image.Rect(0, y, c.s.W, y+rowH)
		x, tw := 48, c.s.W-96
		if r.chip != nil {
			ui.RoundRect(img, image.Rect(x, rr.Min.Y+26, x+10, rr.Max.Y-26), 5, r.chip.chip)
			x, tw = x+34, tw-34
		}
		if r.del != "" {
			apTextRight(img, f.callout, c.s.W-48, rr.Min.Y+rowH/2+10, rgb(0xff453a), "Delete")
			p.buttons = append(p.buttons, button{r.del, image.Rect(c.s.W-200, rr.Min.Y, c.s.W, rr.Max.Y)})
			tw -= 170
		}
		if r.right != "" {
			apTextRight(img, f.callout, c.s.W-48, rr.Min.Y+rowH/2+10, secondary, r.right)
			tw -= 90
		}
		face, col := f.body, label
		if r.current {
			face, col = f.headline, acc
		}
		lines := layoutWords(face, strings.Fields(r.text), 0, tw, false)
		ly := rr.Min.Y + 58
		if r.sub == "" && len(lines) == 1 {
			ly = rr.Min.Y + rowH/2 + 12
		}
		for li, l := range lines {
			if li == 2 {
				break
			}
			if li == 1 && len(lines) > 2 {
				l.words = append(l.words, tword{text: "…", x: l.words[len(l.words)-1].x + l.words[len(l.words)-1].w + 6})
			}
			drawWords(img, face, l, x, ly, col)
			ly += 44
		}
		if r.sub != "" {
			apText(img, f.caption, x, rr.Max.Y-20, secondary, clip(f.caption, r.sub, tw))
		}
		ui.Fill(img, image.Rect(48, rr.Max.Y-1, c.s.W, rr.Max.Y+1), sep)
		p.buttons = append(p.buttons, button{r.id, rr})
		y += rowH
	}
	if len(rows) > listPerPage {
		by := h - 100
		apText(img, f.body, 48, by+48, acc, "‹ Previous")
		p.buttons = append(p.buttons, button{"r:list:prev", image.Rect(0, by, 360, by+90)})
		apTextCenter(img, f.caption, c.s.W/2, by+38, secondary,
			fmt.Sprintf("%d–%d of %d", c.rd.listFrom+1, min(c.rd.listFrom+listPerPage, len(rows)), len(rows)))
		apTextRight(img, f.body, c.s.W-48, by+48, acc, "Next ›")
		p.buttons = append(p.buttons, button{"r:list:next", image.Rect(c.s.W-360, by, c.s.W, by+90)})
	}
	return p
}

func (c *console) listTap(id string) {
	ob := c.book
	switch {
	case id == "r:list:back":
		c.rd.view = ""
	case id == "r:list:prev":
		c.rd.listFrom = max(c.rd.listFrom-listPerPage, 0)
	case id == "r:list:next":
		c.rd.listFrom += listPerPage
	case strings.HasPrefix(id, "r:toc:"):
		var i int
		fmt.Sscanf(id, "r:toc:%d", &i)
		c.rd.view = ""
		if c.loadChapter(i, 0) {
			c.saveProgress()
		}
	case strings.HasPrefix(id, "r:mark:"):
		mid := strings.TrimPrefix(id, "r:mark:")
		for _, m := range c.lib.Marks[ob.path] {
			if m.ID == mid {
				c.rd.view = ""
				if m.Chapter != ob.chapter {
					c.loadChapter(m.Chapter, m.Start)
				} else {
					ob.page, ob.line = findWord(ob.pages, m.Start)
				}
				c.invalidatePage()
				c.saveProgress()
			}
		}
	case strings.HasPrefix(id, "r:del:"):
		c.unmark(strings.TrimPrefix(id, "r:del:"))
	}
}
