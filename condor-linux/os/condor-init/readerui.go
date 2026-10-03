package main

import (
	"context"
	"fmt"
	"image"
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

// drawOverlays draws the word menu, the answer card, the settings sheet and a toast,
// registering their buttons.
func (c *console) drawOverlays(p *page, th readerTheme) {
	ph := c.s.H - c.barH
	card := blend(th.bg, th.fg, 0.10)
	if th.dark {
		card = blend(th.bg, th.fg, 0.16)
	}
	edge := blend(th.bg, th.fg, 0.30)
	btnBG := blend(card, th.fg, 0.12)
	btn := func(id, label string, r image.Rectangle, on bool) {
		bg, fg := btnBG, th.fg
		if on {
			bg, fg = th.fg, th.bg
		}
		ui.RoundRect(p.img, r, 16, bg)
		ui.DrawTextCentered(p.img, c.pf.small, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, fg, label)
		p.buttons = append(p.buttons, button{id, r})
	}
	box := func(r image.Rectangle) {
		ui.RoundRect(p.img, r.Inset(-2), 26, edge)
		ui.RoundRect(p.img, r, 24, card)
	}

	// The settings sheet takes the place of everything else.
	if c.rd.settings {
		c.drawSettings(p, th, box, btn)
		return
	}

	first, last, ok := c.selBands()
	menuTop := 0
	if c.rd.menu != menuNone && ok {
		const mh = 112
		y := first.Min.Y - mh - 18
		if y < readerToolbarH+10 {
			y = last.Max.Y + 18
		}
		y = min(y, ph-mh-10)
		r := image.Rect(20, y, c.s.W-20, y+mh)
		box(r)
		menuTop = y
		in := r.Inset(14)
		text := c.selText()
		onMark := markAt(c.lib.Marks[c.book.path], c.book.chapter, c.rd.sel.from) != nil
		if c.rd.menu == menuInk {
			n := len(markColours) + 1
			if onMark {
				n++
			}
			w := in.Dx() / n
			btn("r:inkback", "<", image.Rect(in.Min.X, in.Min.Y, in.Min.X+w-12, in.Max.Y), false)
			for i, m := range markColours {
				cx := in.Min.X + (i+1)*w + w/2
				ui.Circle(p.img, cx, (in.Min.Y+in.Max.Y)/2, 36, m.chip)
				p.buttons = append(p.buttons, button{"r:ink:" + m.id, image.Rect(cx-w/2, in.Min.Y, cx+w/2, in.Max.Y)})
			}
			if onMark {
				btn("r:unmark", "remove", image.Rect(in.Max.X-w+12, in.Min.Y, in.Max.X, in.Max.Y), false)
			}
		} else {
			ids := []string{"r:hl"}
			labels := []string{"highlight"}
			if isCapturable(text) {
				ids, labels = append(ids, "r:lookup"), append(labels, "look up")
			}
			ids, labels = append(ids, "r:translate"), append(labels, languageLabel(c.lib.Prefs.TranslateTo))
			if isCapturable(text) {
				ids, labels = append(ids, "r:keep"), append(labels, "keep")
			}
			gap := 12
			w := (in.Dx() - gap*(len(ids)-1)) / len(ids)
			for i := range ids {
				btn(ids[i], labels[i], image.Rect(in.Min.X+i*(w+gap), in.Min.Y, in.Min.X+i*(w+gap)+w, in.Max.Y), false)
			}
		}
	}

	if pn := c.rd.panel; pn != nil {
		c.drawPanel(p, th, pn, first, ok, menuTop, box, btn)
	}

	if c.rd.toast != "" && time.Now().Before(c.rd.toastUntil) {
		w := ui.TextWidth(c.pf.small, c.rd.toast) + 60
		r := image.Rect((c.s.W-w)/2, readerToolbarH+12, (c.s.W+w)/2, readerToolbarH+78)
		ui.RoundRect(p.img, r, 30, th.fg)
		ui.DrawTextCentered(p.img, c.pf.small, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, th.bg, c.rd.toast)
	}
}

// drawPanel is the answer card: a meaning or a translation, docked away from the selection.
func (c *console) drawPanel(p *page, th readerTheme, pn *lookupPanel, sel image.Rectangle, haveSel bool, menuTop int,
	box func(image.Rectangle), btn func(string, string, image.Rectangle, bool)) {
	ph := c.s.H - c.barH
	const h = 700
	top := ph - readerFooterH - h - 10 // bottom...
	if haveSel && sel.Max.Y > ph/2 {
		top = readerToolbarH + 10 // ...unless the word is down there
		if menuTop > 0 && menuTop < top+h && menuTop+112 > top {
			top = menuTop + 130
		}
	} else if menuTop > top-130 && menuTop > 0 {
		top = min(top, menuTop-h-18)
	}
	top = max(top, readerToolbarH+10)
	r := image.Rect(20, top, c.s.W-20, top+h)
	box(r)
	f := c.uiFonts()
	x, w := r.Min.X+36, r.Dx()-72
	y := r.Min.Y + 30

	// The words asked about.
	head := pn.query
	if len([]rune(head)) > 120 {
		head = string([]rune(head)[:119]) + "…"
	}
	lines := layoutWords(f.bold, strings.Fields(head), 0, w, false)
	for i, l := range lines {
		if i == 2 {
			break
		}
		drawWords(p.img, f.bold, l, x, y+f.bold.Metrics().Ascent.Ceil(), th.strong)
		y += int(float64(f.lh) * 1.25)
	}
	if pn.kind == "translate" {
		// Target language chips.
		y += 6
		n := len(languages)
		cw := (w - 8*(n-1)) / n
		for i, l := range languages {
			cr := image.Rect(x+i*(cw+8), y, x+i*(cw+8)+cw, y+64)
			btn("r:lang:"+l.code, strings.ToUpper(l.code), cr, l.code == pn.lang)
		}
		y += 84
	}
	bottom := r.Max.Y - 130
	switch {
	case pn.loading:
		ui.DrawText(p.img, c.pf.small, x, y+40, th.faint, map[bool]string{true: "looking it up...", false: "translating..."}[pn.kind == "meaning"])
	case pn.err != "":
		drawParagraphs(p.img, f.body, pn.err, x, y+10, w, f.lh, bottom, th.faint)
	case pn.look != nil:
		ui.DrawText(p.img, c.pf.small, x, y+28, th.faint, pn.look.source)
		y += 50
		for _, s := range pn.look.senses {
			text := s.definition
			if s.pos != "" {
				text = s.pos + " · " + text
			}
			y = drawParagraphs(p.img, f.body, text, x, y, w, f.lh, bottom, th.fg)
			if s.example != "" {
				y = drawParagraphs(p.img, f.body, "“"+s.example+"”", x+30, y, w-30, f.lh, bottom, th.faint)
			}
			y += 12
		}
	case pn.tr != nil:
		y = drawParagraphs(p.img, f.body, pn.tr.text, x, y+10, w, f.lh, bottom, th.fg)
		if pn.tr.quality >= 0 && pn.tr.quality < 0.7 {
			ui.DrawText(p.img, c.pf.small, x, min(y+40, bottom), th.faint, "machine translation, may be loose")
		}
	}

	// Actions.
	ids, labels := []string{}, []string{}
	if pn.isWord {
		ids, labels = append(ids, "r:keep"), append(labels, "keep")
	}
	if pn.kind == "meaning" {
		ids, labels = append(ids, "r:translate"), append(labels, languageLabel(c.lib.Prefs.TranslateTo))
	} else if pn.isWord {
		ids, labels = append(ids, "r:lookup"), append(labels, "look up")
	}
	ids, labels = append(ids, "r:close"), append(labels, "close")
	gap := 14
	bw := (w - gap*(len(ids)-1)) / len(ids)
	for i := range ids {
		btn(ids[i], labels[i], image.Rect(x+i*(bw+gap), r.Max.Y-110, x+i*(bw+gap)+bw, r.Max.Y-24), false)
	}
	p.buttons = append(p.buttons, button{"r:panel", r}) // taps on the card itself do nothing
}

// drawSettings is Soma's "Aa" sheet.
func (c *console) drawSettings(p *page, th readerTheme, box func(image.Rectangle), btn func(string, string, image.Rectangle, bool)) {
	ph := c.s.H - c.barH
	pr := c.lib.Prefs
	r := image.Rect(20, readerToolbarH+10, c.s.W-20, ph-20)
	box(r)
	x, w := r.Min.X+32, r.Dx()-64
	y := r.Min.Y + 20
	label := func(s string) {
		ui.DrawText(p.img, c.pf.small, x, y+34, th.faint, s)
		y += 50
	}
	row := func(ids, labels []string, on string) {
		gap := 12
		bw := (w - gap*(len(ids)-1)) / len(ids)
		for i := range ids {
			btn(ids[i], labels[i], image.Rect(x+i*(bw+gap), y, x+i*(bw+gap)+bw, y+84), ids[i] == on)
		}
		y += 104
	}
	stepper := func(id, value string) {
		bw := 200
		btn(id+":-", "-", image.Rect(x, y, x+bw, y+84), false)
		btn(id+":+", "+", image.Rect(x+w-bw, y, x+w, y+84), false)
		ui.DrawTextCentered(p.img, c.pf.bold, x+w/2, y+42, th.fg, value)
		y += 104
	}

	label("THEME")
	var ids, labels []string
	for i, t := range readerThemes {
		ids, labels = append(ids, fmt.Sprintf("r:set:theme:%d", i)), append(labels, t.name)
	}
	row(ids, labels, fmt.Sprintf("r:set:theme:%d", pr.Theme))

	label("FONT")
	gap := 12
	bw := (w - gap*(len(readerFontList)-1)) / len(readerFontList)
	for i, f := range readerFontList {
		fr := image.Rect(x+i*(bw+gap), y, x+i*(bw+gap)+bw, y+84)
		on := f.id == pr.Font
		bg, fg := blend(blend(th.bg, th.fg, 0.16), th.fg, 0.12), th.fg
		if on {
			bg, fg = th.fg, th.bg
		}
		ui.RoundRect(p.img, fr, 16, bg)
		face := newReaderFonts(f.id, 32, 1.4).body
		ui.DrawTextCentered(p.img, face, (fr.Min.X+fr.Max.X)/2, (fr.Min.Y+fr.Max.Y)/2, fg, f.label)
		p.buttons = append(p.buttons, button{"r:set:font:" + f.id, fr})
	}
	y += 104

	label("TEXT SIZE")
	stepper("r:set:size", fmt.Sprintf("%d", pr.Size))
	label("LINE SPACING")
	stepper("r:set:lh", fmt.Sprintf("%.1f", pr.LineHeight))
	label("MARGINS")
	stepper("r:set:margin", fmt.Sprintf("%d", pr.Margin))
	label("LINE BY LINE   tap: next · left edge: back · hold: jump")
	row([]string{"r:set:line:off", "r:set:line:on"}, []string{"off", "on"}, map[bool]string{true: "r:set:line:on", false: "r:set:line:off"}[pr.LineFocus])
	label("TRANSLATE TO")
	ids, labels = nil, nil
	for _, l := range languages {
		ids, labels = append(ids, "r:set:tr:"+l.code), append(labels, strings.ToUpper(l.code))
	}
	row(ids, labels, "r:set:tr:"+pr.TranslateTo)
	label(fmt.Sprintf("DAILY READING GOAL  (today %d min)", c.lib.readingToday()))
	stepper("r:set:goal", fmt.Sprintf("%d min", pr.GoalMinutes))
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
	case id == "contents" || id == "marks":
		c.closeOverlays()
		c.rd.view, c.rd.listFrom = id, -1
	case id == "linemode":
		pr.LineFocus = !pr.LineFocus
		ob.line = 0
		c.lib.save()
		c.invalidatePage()
	case id == "settings":
		open := !c.rd.settings
		c.closeOverlays()
		c.rd.settings = open
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
	case "goal":
		pr.GoalMinutes = min(max(pr.GoalMinutes+5*step, 5), 240)
	}
	return false
}

// --- contents and highlights -------------------------------------------------------------

const listPerPage = 9

func (c *console) readerListPage() *page {
	ob := c.book
	th := readerThemes[c.lib.Prefs.Theme]
	h := c.s.H - c.barH
	img := canvas(c.s.W, h)
	ui.Fill(img, img.Rect, th.bg)
	p := &page{img: img}
	card := blend(th.bg, th.fg, 0.08)
	btnBG := blend(th.bg, th.fg, 0.14)
	addBtn := func(id, label string, r image.Rectangle) {
		ui.RoundRect(img, r, 16, btnBG)
		ui.DrawTextCentered(img, c.pf.small, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, th.fg, label)
		p.buttons = append(p.buttons, button{id, r})
	}
	addBtn("r:list:back", "< book", image.Rect(28, 20, 300, 106))
	title := "contents"
	if c.rd.view == "marks" {
		title = "highlights"
	}
	ui.DrawText(img, c.pf.title, 330, 88, th.strong, title)
	f := c.uiFonts()

	type row struct {
		id, text, sub string
		chip          *inkColour
		del           string
		current       bool
	}
	var rows []row
	if c.rd.view == "contents" {
		if ob.titles == nil {
			ui.DrawText(img, c.pf.body, 48, 240, th.faint, "reading the chapters...")
			return p
		}
		for i, t := range ob.titles {
			rows = append(rows, row{id: fmt.Sprintf("r:toc:%d", i), text: t, sub: fmt.Sprintf("chapter %d", i+1), current: i == ob.chapter})
		}
	} else {
		for _, m := range c.lib.Marks[ob.path] {
			mc := markColour(m.Colour)
			sub := fmt.Sprintf("chapter %d", m.Chapter+1)
			if ob.titles != nil && m.Chapter < len(ob.titles) {
				sub = ob.titles[m.Chapter]
			}
			rows = append(rows, row{id: "r:mark:" + m.ID, text: m.Text, sub: sub + "  ·  " + m.Added, chip: &mc, del: "r:del:" + m.ID})
		}
		if len(rows) == 0 {
			ui.DrawText(img, c.pf.body, 48, 240, th.fg, "no highlights in this book yet")
			ui.DrawText(img, c.pf.small, 48, 300, th.faint, "long-press a word, drag to take in more, then highlight")
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
	y := 140
	for i := c.rd.listFrom; i < len(rows) && i < c.rd.listFrom+listPerPage; i++ {
		r := rows[i]
		rr := image.Rect(28, y, c.s.W-28, y+166)
		bg := card
		if r.current {
			bg = blend(th.bg, th.mark, th.markA)
		}
		ui.RoundRect(img, rr, 20, bg)
		x := rr.Min.X + 30
		if r.chip != nil {
			ui.RoundRect(img, image.Rect(x, rr.Min.Y+24, x+14, rr.Max.Y-24), 6, r.chip.chip)
			x += 36
		}
		tw := rr.Max.X - x - 30
		if r.del != "" {
			tw -= 120
			addBtn(r.del, "x", image.Rect(rr.Max.X-130, rr.Min.Y+40, rr.Max.X-30, rr.Max.Y-40))
		}
		lines := layoutWords(f.body, strings.Fields(r.text), 0, tw, false)
		ly := rr.Min.Y + 52
		for li, l := range lines {
			if li == 2 {
				break
			}
			if li == 1 && len(lines) > 2 {
				l.words = append(l.words, tword{text: "…", x: l.words[len(l.words)-1].x + l.words[len(l.words)-1].w + 6})
			}
			drawWords(img, f.body, l, x, ly, th.fg)
			ly += 46
		}
		ui.DrawText(img, c.pf.small, x, rr.Max.Y-18, th.faint, clip(c.pf.small, r.sub, tw))
		p.buttons = append(p.buttons, button{r.id, rr})
		y += 180
	}
	if len(rows) > listPerPage {
		by := h - 110
		bw := (c.s.W - 56 - 24) / 3
		addBtn("r:list:prev", "< prev", image.Rect(28, by, 28+bw, by+90))
		ui.DrawTextCentered(img, c.pf.small, c.s.W/2, by+45, th.faint,
			fmt.Sprintf("%d-%d of %d", c.rd.listFrom+1, min(c.rd.listFrom+listPerPage, len(rows)), len(rows)))
		addBtn("r:list:next", "next >", image.Rect(c.s.W-28-bw, by, c.s.W-28, by+90))
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
