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
	custom     bool   // the settings popover is on its Customize page
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

// sheetColours are a sheet's or popover's background, text, secondary text and separators
// on a theme: white on light paper, dark grey on dark.
func sheetColours(th readerTheme) (bg, label, secondary, sep color.RGBA) {
	if th.dark {
		return rgb(0x2c2c2e), rgb(0xffffff), rgb(0x98989d), rgb(0x48484a)
	}
	return rgb(0xffffff), rgb(0x000000), rgb(0x8a8a8e), rgb(0xe0e0e4)
}

// drawOverlays draws the selection's menu, the Look Up / Translate sheet, the Themes &
// Settings popover and a toast, registering their buttons first.
func (c *console) drawOverlays(p *page, th readerTheme) {
	if c.rd.settings {
		c.drawSettings(p, th)
		return
	}
	first, last, ok := c.selBands()
	if c.rd.menu != menuNone && ok {
		c.drawCallout(p, th, first, last)
	}
	if pn := c.rd.panel; pn != nil {
		c.drawPanel(p, th, pn)
	}
	if c.rd.toast != "" && time.Now().Before(c.rd.toastUntil) {
		f := apple()
		w := ui.TextWidth(f.callout, c.rd.toast) + 80
		r := image.Rect((c.s.W-w)/2, 120, (c.s.W+w)/2, 196)
		shadow(p.img, r, 38, 0.12)
		ui.RoundRect(p.img, r, 38, rgb(0x1c1c1e))
		apTextCenter(p.img, f.callout, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, rgb(0xffffff), c.rd.toast)
	}
}

// drawCallout is iPadOS's edit menu: a white panel of rows (an icon and a label each), set
// beside the selection; Highlight swaps the rows for the five inks.
func (c *console) drawCallout(p *page, th readerTheme, first, last image.Rectangle) {
	f := apple()
	ph := c.s.H - c.barH
	bg, label, secondary, sep := sheetColours(th)
	if !th.dark {
		bg = apMenuBG
	}
	text := c.selText()
	onMark := markAt(c.lib.Marks[c.book.path], c.book.chapter, c.rd.sel.from) != nil
	type row struct {
		id, label string
		icon      func(cx, cy int, c color.RGBA)
		red       bool
		groupEnd  bool
	}
	var rows []row
	if c.rd.menu == menuMain {
		rows = append(rows, row{"r:hl", "Highlight", func(x, y int, col color.RGBA) { iconPen(p.img, x, y, col) }, false, !isCapturable(text)})
		if isCapturable(text) {
			rows = append(rows, row{"r:lookup", "Look Up", func(x, y int, col color.RGBA) { iconBook(p.img, x, y, col) }, false, true})
		}
		rows = append(rows, row{"r:translate", "Translate", func(x, y int, col color.RGBA) { iconGlobe(p.img, x, y, col) }, false, isCapturable(text)})
		if isCapturable(text) {
			rows = append(rows, row{"r:keep", "Keep Word", func(x, y int, col color.RGBA) { iconBookmark(p.img, x, y, col, false) }, false, onMark})
		}
		if onMark {
			rows = append(rows, row{"r:unmark", "Remove Highlight", func(x, y int, col color.RGBA) { iconTrash(p.img, x, y, col) }, true, false})
		}
	}
	const w, rh = 470, 84
	h := len(rows)*rh + 24
	if c.rd.menu == menuInk {
		h = 124
	}
	// Beside the selection: under its last line if there's room, else above its first.
	x := min(max(first.Min.X, 30), c.s.W-30-w)
	y := last.Max.Y + 30
	if y+h > ph-40 {
		y = first.Min.Y - 30 - h
	}
	y = max(y, 140)
	r := image.Rect(x, y, x+w, y+h)
	shadow(p.img, r, 28, 0.16)
	ui.RoundRect(p.img, r.Inset(-1), 29, sep)
	ui.RoundRect(p.img, r, 28, bg)
	if c.rd.menu == menuInk {
		n := len(markColours) + 1
		cw := (w - 40) / n
		for i, m := range markColours {
			cx := r.Min.X + 20 + i*cw + cw/2
			ui.Circle(p.img, cx, r.Min.Y+62, 31, m.chip)
			p.buttons = append(p.buttons, button{"r:ink:" + m.id, image.Rect(cx-cw/2, r.Min.Y, cx+cw/2, r.Max.Y)})
		}
		cx := r.Min.X + 20 + len(markColours)*cw + cw/2
		if onMark {
			iconTrash(p.img, cx, r.Min.Y+62, apRed)
			p.buttons = append(p.buttons, button{"r:unmark", image.Rect(cx-cw/2, r.Min.Y, cx+cw/2, r.Max.Y)})
		} else {
			iconBack(p.img, cx-8, r.Min.Y+62, secondary)
			p.buttons = append(p.buttons, button{"r:inkback", image.Rect(cx-cw/2, r.Min.Y, cx+cw/2, r.Max.Y)})
		}
		return
	}
	ry := r.Min.Y + 12
	for i, rw := range rows {
		rr := image.Rect(r.Min.X, ry, r.Max.X, ry+rh)
		col := label
		if rw.red {
			col = apRed
		}
		rw.icon(rr.Min.X+52, (rr.Min.Y+rr.Max.Y)/2, col)
		apText(p.img, f.body, rr.Min.X+100, (rr.Min.Y+rr.Max.Y)/2+12, col, rw.label)
		if rw.groupEnd && i < len(rows)-1 {
			ui.Fill(p.img, image.Rect(rr.Min.X+28, rr.Max.Y-1, rr.Max.X-28, rr.Max.Y+1), sep)
		}
		p.buttons = append(p.buttons, button{rw.id, rr})
		ry += rh
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

// iosSwitch draws an on/off switch with its right edge at right.
func iosSwitch(img *image.RGBA, right, cy int, on, dark bool) image.Rectangle {
	r := image.Rect(right-102, cy-31, right, cy+31)
	track := rgb(0xe9e9eb)
	if dark {
		track = rgb(0x39393d)
	}
	if on {
		track = rgb(0x34c759)
	}
	ui.RoundRect(img, r, 31, track)
	kx := r.Min.X + 31
	if on {
		kx = r.Max.X - 31
	}
	ui.Circle(img, kx, cy+2, 28, blend(track, rgb(0x000000), 0.12))
	ui.Circle(img, kx, cy, 27, rgb(0xffffff))
	return r
}

// drawSettings is Apple Books' Themes & Settings popover (under the Aa button): text size,
// light/dark, the six themes, and Customize (Soma's controls and the page turn).
func (c *console) drawSettings(p *page, th readerTheme) {
	f := apple()
	pr := c.lib.Prefs
	bg, label, secondary, sep := sheetColours(th)
	acc := th.accent()
	fill := blend(bg, label, 0.07)
	const w = 760
	x0 := c.s.W - 24 - w
	h := 780
	if c.rd.custom {
		h = 1480
	}
	r := image.Rect(x0, 140, x0+w, 140+h)
	p.buttons = append(p.buttons, button{"settings", image.Rect(0, 0, c.s.W, r.Min.Y)},
		button{"settings", image.Rect(0, r.Min.Y, r.Min.X, c.s.H)}) // tap outside: close
	shadow(p.img, r, 34, 0.18)
	ui.RoundRect(p.img, r.Inset(-1), 35, sep)
	ui.RoundRect(p.img, r, 34, bg)
	x, iw := r.Min.X+30, w-60
	y := r.Min.Y + 56

	if !c.rd.custom {
		apTextCenter(p.img, f.captionBold, (r.Min.X+r.Max.X)/2, y, secondary, "Themes & Settings")
		y += 40
		// [A | A]   [half moon]
		capW := iw - 130
		cr := image.Rect(x, y, x+capW, y+92)
		ui.RoundRect(p.img, cr, 46, fill)
		mid := (cr.Min.X + cr.Max.X) / 2
		ui.Fill(p.img, image.Rect(mid-1, cr.Min.Y+20, mid+1, cr.Max.Y-20), sep)
		apTextCenter(p.img, f.caption, (cr.Min.X+mid)/2, (cr.Min.Y+cr.Max.Y)/2, label, "A")
		apTextCenter(p.img, f.title, (mid+cr.Max.X)/2, (cr.Min.Y+cr.Max.Y)/2, label, "A")
		p.buttons = append(p.buttons, button{"r:set:size:-", image.Rect(cr.Min.X, cr.Min.Y, mid, cr.Max.Y)},
			button{"r:set:size:+", image.Rect(mid, cr.Min.Y, cr.Max.X, cr.Max.Y)})
		mr := image.Rect(cr.Max.X+20, y, x+iw, y+92)
		ui.RoundRect(p.img, mr, 46, fill)
		iconHalfMoon(p.img, (mr.Min.X+mr.Max.X)/2, (mr.Min.Y+mr.Max.Y)/2, label)
		p.buttons = append(p.buttons, button{"r:set:appearance", mr})
		y += 124

		// Six themes, two rows of three: "Aa" in the theme's ink on its paper.
		tw, thh, gap := (iw-2*20)/3, 190, 20
		for i, t := range readerThemes[:6] {
			col, row := i%3, i/3
			tr := image.Rect(x+col*(tw+gap), y+row*(thh+gap), x+col*(tw+gap)+tw, y+row*(thh+gap)+thh)
			if i == pr.Theme || (pr.Theme == themeNight && i == 0) {
				ui.RoundRect(p.img, tr.Inset(-5), 31, label)
				ui.RoundRect(p.img, tr.Inset(-2), 28, bg)
			} else {
				ui.RoundRect(p.img, tr.Inset(-1), 27, sep)
			}
			ui.RoundRect(p.img, tr, 26, t.bg)
			aa := newReaderFontsB(pr.Font, 56, 1.4, t.bold).body
			ui.DrawTextCentered(p.img, aa, (tr.Min.X+tr.Max.X)/2, (tr.Min.Y+tr.Max.Y)/2-14, t.fg, "Aa")
			nf := f.caption
			if t.bold {
				nf = f.captionBold
			}
			apTextCenter(p.img, nf, (tr.Min.X+tr.Max.X)/2, tr.Max.Y-34, t.fg, t.name)
			p.buttons = append(p.buttons, button{fmt.Sprintf("r:set:theme:%d", i), tr})
		}
		y += 2*thh + gap + 34
		// Customize.
		cu := image.Rect(x, y, x+iw, y+92)
		ui.RoundRect(p.img, cu, 46, fill)
		ring(p.img, cu.Min.X+iw/2-110, (cu.Min.Y+cu.Max.Y)/2, 12, 4, 1, label, label)
		ui.Circle(p.img, cu.Min.X+iw/2-110, (cu.Min.Y+cu.Max.Y)/2, 4, label)
		apTextCenter(p.img, f.headline, cu.Min.X+iw/2+20, (cu.Min.Y+cu.Max.Y)/2, label, "Customize")
		p.buttons = append(p.buttons, button{"r:custom", cu})
		p.buttons = append(p.buttons, button{"r:panel", r})
		return
	}

	// Customize.
	iconBack(p.img, x, y-12, acc)
	apText(p.img, f.body, x+28, y, acc, "Themes")
	p.buttons = append(p.buttons, button{"r:custom", image.Rect(r.Min.X, r.Min.Y, r.Min.X+260, r.Min.Y+110)})
	apTextCenter(p.img, f.captionBold, (r.Min.X+r.Max.X)/2, y-12, secondary, "Customize")
	y += 44

	// Font.
	apText(p.img, f.captionBold, x+8, y, secondary, "FONT")
	y += 16
	grp := image.Rect(x, y, x+iw, y+len(readerFontList)*78)
	ui.RoundRect(p.img, grp, 22, fill)
	for i, rf := range readerFontList {
		rr := image.Rect(x, y+i*78, x+iw, y+(i+1)*78)
		if i > 0 {
			ui.Fill(p.img, image.Rect(x+28, rr.Min.Y, x+iw, rr.Min.Y+1), sep)
		}
		ui.DrawText(p.img, newReaderFonts(rf.id, 34, 1.4).body, x+28, rr.Min.Y+52, label, rf.label)
		if rf.id == pr.Font {
			iconCheck(p.img, x+iw-60, (rr.Min.Y+rr.Max.Y)/2, acc)
		}
		p.buttons = append(p.buttons, button{"r:set:font:" + rf.id, rr})
	}
	y = grp.Max.Y + 50

	// Page turn: Slide, Curl, None.
	apText(p.img, f.captionBold, x+8, y, secondary, "PAGE TURN")
	y += 16
	segmented(p, image.Rect(x, y, x+iw, y+76), []string{"r:set:turn:slide", "r:set:turn:curl", "r:set:turn:none"},
		[]string{"Slide", "Curl", "None"}, "r:set:turn:"+pr.PageTurn, bg, label)
	y += 116

	// Text and reading.
	apText(p.img, f.captionBold, x+8, y, secondary, "TEXT & READING")
	y += 16
	const rows, rh = 6, 88
	grp = image.Rect(x, y, x+iw, y+rows*rh)
	ui.RoundRect(p.img, grp, 22, fill)
	row := func(i int, name string) (cy, right int) {
		top := y + i*rh
		if i > 0 {
			ui.Fill(p.img, image.Rect(x+28, top, x+iw, top+1), sep)
		}
		apText(p.img, f.body, x+28, top+56, label, name)
		return top + rh/2, x + iw - 22
	}
	stepper := func(i int, name, id, value string) {
		cy, right := row(i, name)
		sr := image.Rect(right-300, cy-32, right, cy+32)
		ui.RoundRect(p.img, sr, 18, blend(fill, label, 0.07))
		apTextCenter(p.img, f.headline, sr.Min.X+46, cy, label, "−")
		apTextCenter(p.img, f.callout, (sr.Min.X+sr.Max.X)/2, cy, label, value)
		apTextCenter(p.img, f.headline, sr.Max.X-46, cy, label, "+")
		p.buttons = append(p.buttons, button{id + ":-", image.Rect(sr.Min.X, sr.Min.Y-12, sr.Min.X+100, sr.Max.Y+12)},
			button{id + ":+", image.Rect(sr.Max.X-100, sr.Min.Y-12, sr.Max.X, sr.Max.Y+12)})
	}
	stepper(0, "Line Spacing", "r:set:lh", fmt.Sprintf("%.1f", pr.LineHeight))
	stepper(1, "Margins", "r:set:margin", fmt.Sprintf("%d", pr.Margin))
	stepper(2, "Brightness", "r:set:bright", fmt.Sprintf("%d%%", c.cfg.Brightness))
	cy, right := row(3, "Line by Line")
	sw := iosSwitch(p.img, right, cy, pr.LineFocus, th.dark)
	p.buttons = append(p.buttons, button{map[bool]string{true: "r:set:line:off", false: "r:set:line:on"}[pr.LineFocus], sw.Inset(-14)})
	cy, right = row(4, "Translate To")
	apTextRight(p.img, f.body, right-26, cy+12, secondary, languageLabel(pr.TranslateTo))
	iconChevronRight(p.img, right-12, cy, secondary)
	p.buttons = append(p.buttons, button{"r:set:trnext", image.Rect(x+iw/2, cy-rh/2, x+iw, cy+rh/2)})
	stepper(5, "Daily Goal", "r:set:goal", fmt.Sprintf("%d min", pr.GoalMinutes))
	y = grp.Max.Y + 34
	drawParagraphs(p.img, f.caption, fmt.Sprintf("Line by line: tap for the next line, the left edge to go back, hold a line to jump to it. Today you read %d of %d minutes.",
		c.lib.readingToday(), pr.GoalMinutes), x+8, y-26, iw-16, 34, r.Max.Y-10, secondary)
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
			c.readerFrom = c.mode
			c.transition("push", image.Rectangle{}, func() { c.openBookAt(c.shelf[i].path) })
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
	case id == "shelf": // back to where the book was opened from
		c.saveProgress()
		back := c.readerFrom
		if c.fromStore {
			back = modeStore
		} else if back != modeBooksHome && back != modeLauncher && back != modeStore {
			back = modeBooks
		}
		c.transition("pop", image.Rectangle{}, func() { c.setMode(back) })
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
	case id == "contents" || id == "marks": // the list rises as a sheet
		c.transition("rise", c.fullSheet(), func() {
			c.closeOverlays()
			c.rd.chrome = false
			c.rd.view, c.rd.listFrom = id, -1
			c.showPage()
		})
		return true
	case id == "r:list:contents" || id == "r:list:marks" || id == "r:list:bookmarks":
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
		c.rd.settings, c.rd.chrome, c.rd.custom = open, false, false
	case id == "r:custom":
		c.rd.custom = !c.rd.custom
	case id == "r:bookmark":
		c.toggleBookmark()
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
	case id == "r:lookup" || id == "r:translate":
		kind := map[string]string{"r:lookup": "meaning", "r:translate": "translate"}[id]
		if c.rd.panel != nil { // switching in the open sheet
			c.ask(kind)
			break
		}
		c.transition("rise", c.lookupSheet(), func() { c.ask(kind); c.showPage() })
		return true
	case strings.HasPrefix(id, "r:lang:"):
		pr.TranslateTo = strings.TrimPrefix(id, "r:lang:")
		c.lib.save()
		c.ask("translate")
	case id == "r:keep":
		c.keepWord()
	case id == "r:close":
		if c.rd.panel != nil {
			c.transition("fall", c.lookupSheet(), func() { c.closeOverlays(); c.showPage() })
			return true
		}
		c.closeOverlays()
	case id == "r:panel":
		return true // a tap on a card, between its buttons
	case strings.HasPrefix(id, "r:set:"):
		relayout = c.applySetting(strings.TrimPrefix(id, "r:set:"))
	case strings.HasPrefix(id, "r:list:") || strings.HasPrefix(id, "r:toc:") || strings.HasPrefix(id, "r:mark:") ||
		strings.HasPrefix(id, "r:del:") || strings.HasPrefix(id, "r:bm:") || strings.HasPrefix(id, "r:bmdel:"):
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
	case "theme", "appearance":
		wasBold := c.theme().bold
		if what == "appearance" { // the half moon: Original's dark version and back
			if c.theme().dark {
				pr.Theme = 0
			} else {
				pr.Theme = themeNight
			}
		} else {
			fmt.Sscan(arg, &pr.Theme)
			pr.Theme = min(max(pr.Theme, 0), len(readerThemes)-1)
		}
		c.invalidatePage()
		return c.theme().bold != wasBold // Bold sets the text in another face: lay it out again
	case "turn":
		if arg == "slide" || arg == "curl" || arg == "none" {
			pr.PageTurn = arg
		}
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
	th := c.theme()
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
	segmented(p, image.Rect(c.s.W/2-420, 120, c.s.W/2+420, 196), []string{"r:list:contents", "r:list:bookmarks", "r:list:marks"},
		[]string{"Contents", "Bookmarks", "Highlights"}, "r:list:"+c.rd.view, th.bg, label)

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
	} else if c.rd.view == "bookmarks" {
		for _, b := range c.lib.Bookmarks[ob.path] {
			sub := fmt.Sprintf("Chapter %d", b.Chapter+1)
			if ob.titles != nil && b.Chapter < len(ob.titles) {
				sub = ob.titles[b.Chapter]
			}
			red := inkColour{chip: apRed}
			rows = append(rows, row{id: "r:bm:" + b.ID, text: b.Text, sub: sub + "  ·  " + b.Added, chip: &red, del: "r:bmdel:" + b.ID})
		}
		if len(rows) == 0 {
			apTextCenter(img, f.title, c.s.W/2, 480, label, "No Bookmarks")
			apTextCenter(img, f.callout, c.s.W/2, 550, secondary, "Tap the bookmark button at the top of a page")
			apTextCenter(img, f.callout, c.s.W/2, 596, secondary, "to come back to it later.")
			return p
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
		c.transition("fall", c.fullSheet(), func() { c.rd.view = ""; c.showPage() })
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
	case strings.HasPrefix(id, "r:bm:"):
		bid := strings.TrimPrefix(id, "r:bm:")
		for _, b := range c.lib.Bookmarks[ob.path] {
			if b.ID == bid {
				c.rd.view = ""
				if b.Chapter != ob.chapter {
					c.loadChapter(b.Chapter, b.Word)
				} else {
					ob.page, ob.line = findWord(ob.pages, b.Word)
				}
				c.invalidatePage()
				c.saveProgress()
			}
		}
	case strings.HasPrefix(id, "r:bmdel:"):
		bid := strings.TrimPrefix(id, "r:bmdel:")
		var kept []bookmark
		for _, b := range c.lib.Bookmarks[ob.path] {
			if b.ID != bid {
				kept = append(kept, b)
			}
		}
		c.lib.Bookmarks[ob.path] = kept
		c.lib.save()
		c.marksVersion++
	}
}
