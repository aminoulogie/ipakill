package main

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"sort"
	"strings"
	"time"

	"condor-init/fonts"
	"condor-init/ui"
)

// The Books app's Home, after Apple Books on iPad: Continue (the books being read, on cards
// tinted with their cover's colour), Top Picks (big illustrated cards into the store), Want
// to Read (the rest of the library), and the most read free books.

type topPick struct {
	title, caption, topic string
	bg, titleCol          color.RGBA
	art                   int
}

var topPicks = []topPick{
	{"", "The books everyone is reading this month.", "", rgb(0xf7a8b8), rgb(0xe23e57), 0},
	{"Adventure\nAwaits", "Explore the classics' great journeys.", "adventure", rgb(0x1a9ff0), rgb(0xffffff), 1},
	{"MYSTERY\nPICKS", "Our favourite detective stories.", "detective", rgb(0x8fd69b), rgb(0x111111), 2},
}

// coverTint is a cover's average colour, darkened for white text (Continue cards).
var coverTints = map[string]color.RGBA{}

func coverTint(img *image.RGBA, url string) color.RGBA {
	if t, ok := coverTints[url]; ok {
		return t
	}
	var r, g, b, n int
	for i := 0; i < len(img.Pix); i += 4 * 7 {
		r, g, b, n = r+int(img.Pix[i]), g+int(img.Pix[i+1]), b+int(img.Pix[i+2]), n+1
	}
	if n == 0 {
		return rgb(0x6e6e73)
	}
	t := blend(color.RGBA{uint8(r / n), uint8(g / n), uint8(b / n), 255}, rgb(0x2a2a2a), 0.45)
	coverTints[url] = t
	return t
}

// readingNow is the library's books in progress, most recently opened first.
func (c *console) readingNow() []int {
	var idx []int
	for i, b := range c.shelf {
		if pr, ok := c.lib.Progress[b.path]; ok && pr.Opened != "" && c.lib.Finished[b.path] == "" {
			idx = append(idx, i)
		}
	}
	sort.SliceStable(idx, func(a, b int) bool {
		return c.lib.Progress[c.shelf[idx[a]].path].Opened > c.lib.Progress[c.shelf[idx[b]].path].Opened
	})
	return idx
}

func (c *console) loadPopular() {
	if c.homePopLoading || len(c.homePop) > 0 || !homeLoads {
		return
	}
	c.homePopLoading = true
	pref := c.store.preferOPDS
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), sourceTimeout)
		res, err := searchGutenberg(ctx, storeSearch{page: 1}, &pref)
		cancel()
		drawMu.Lock()
		defer drawMu.Unlock()
		c.homePopLoading = false
		c.store.preferOPDS = pref
		if err != nil {
			c.homePopErr = time.Now()
			return
		}
		c.homePop = mergeItems(nil, res.items)
		if c.mode == modeBooksHome {
			c.showPage()
		}
	}()
}

func (c *console) booksHomePage() *page {
	f := apple()
	mx := 48
	covers.mu.Lock()
	covers.loaded = func() {
		drawMu.Lock()
		c.homeRedrawSoon()
		drawMu.Unlock()
	}
	covers.mu.Unlock()
	c.shelf = findBooks()
	if time.Since(c.homePopErr) > time.Minute {
		c.loadPopular()
	}
	c.loadShelves()
	item := func(b shelfBook) *storeItem {
		return &storeItem{key: b.path, title: b.title, author: b.author, cover: b.cover}
	}
	now := c.readingNow()

	// The page is as tall as its sections: it scrolls (scroll.go).
	h := 228 + 120 + 400 + 40 + homeRowH + 40 + homeRowH + len(homeShelves)*homeRowH + 80
	if len(now) > 0 {
		h += 260
	}
	img := canvas(c.s.W, max(h, c.viewH()))
	ui.Fill(img, img.Rect, apBG)
	p := &page{img: img, header: tabsH}

	c.booksTabs(p, "tab:home")
	apText(img, f.serifLarge, mx, 228, apLabel, "Home")
	y := 228

	// Continue.
	if len(now) > 0 {
		apText(img, f.serifTitle, mx, y+90, apLabel, "Continue")
		y += 120
		cw := (c.s.W - 2*mx - 2*24) / 3
		for k, i := range now[:min(3, len(now))] {
			b := c.shelf[i]
			r := image.Rect(mx+k*(cw+24), y, mx+k*(cw+24)+cw, y+140)
			tint := rgb(0x6e6e73)
			if cv := covers.get(b.cover, 72, 108); cv != nil {
				tint = coverTint(cv, b.cover)
			}
			ui.RoundRect(img, r, 22, tint)
			cr := image.Rect(r.Min.X+16, r.Min.Y+16, r.Min.X+16+72, r.Min.Y+16+108)
			c.drawCover(img, cr, item(b))
			tx, tw := cr.Max.X+18, r.Max.X-cr.Max.X-70
			ty := r.Min.Y + 40
			lines := layoutWords(f.captionBold, strings.Fields(b.title), 0, tw, false)
			for li, l := range lines {
				if li == 2 {
					break
				}
				drawWords(img, f.captionBold, l, tx, ty, rgb(0xffffff))
				ty += 30
			}
			apText(img, f.caption, tx, ty, blend(rgb(0xffffff), tint, 0.15), clip(f.caption, b.author, tw))
			apText(img, f.caption, tx, ty+30, blend(rgb(0xffffff), tint, 0.25), fmt.Sprintf("Book · %d%%", c.lib.Progress[b.path].Pct))
			iconDots(img, r.Max.X-40, (r.Min.Y+r.Max.Y)/2, rgb(0xffffff))
			p.buttons = append(p.buttons, button{fmt.Sprintf("book%d", i), r})
		}
		y += 140
	}

	// Top Picks.
	apText(img, f.serifTitle, mx, y+92, apLabel, "Top Picks")
	y += 120
	pw := (c.s.W - 2*mx - 2*24) / 3
	ph := 400
	for k, tp := range topPicks {
		r := image.Rect(mx+k*(pw+24), y, mx+k*(pw+24)+pw, y+ph)
		c.drawTopPick(img, r, tp)
		p.buttons = append(p.buttons, button{fmt.Sprintf("home:pick:%d", k), r})
	}
	y += ph + 40

	// Want to Read: the whole library, on the grouped band, sideways.
	band := image.Rect(0, y, c.s.W, y+homeRowH+40)
	ui.Fill(img, band, apBand)
	c.rowTitle(p, y, "Want to Read", "Books you'd like to read next.", "tab:library")
	order := make([]int, 0, len(c.shelf))
	for i, b := range c.shelf { // started books first, then new ones; finished ones last
		if _, ok := c.lib.Progress[b.path]; ok && c.lib.Finished[b.path] == "" {
			order = append(order, i)
		}
	}
	for i, b := range c.shelf {
		if _, ok := c.lib.Progress[b.path]; !ok {
			order = append(order, i)
		}
	}
	if len(c.shelf) == 0 {
		apText(img, f.body, mx, y+230, apSecondary, "Your library is empty.")
		r := image.Rect(mx, y+270, mx+340, y+360)
		ui.RoundRect(img, r, 45, apBlue)
		apTextCenter(img, f.headline, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, rgb(0xffffff), "Book Store")
		p.buttons = append(p.buttons, button{"tab:store", r})
	}
	var mine []rowItem
	for _, i := range order {
		b := c.shelf[i]
		ri := rowItem{it: item(b), id: fmt.Sprintf("book%d", i), badge: "NEW"}
		if pr, ok := c.lib.Progress[b.path]; ok {
			ri.badge, ri.caption = "", fmt.Sprintf("%d%%", pr.Pct)
		}
		mine = append(mine, ri)
	}
	c.coverRow(p, "home:mine", y+150, mine, apBand)
	y = band.Max.Y

	// The most read free books.
	c.rowTitle(p, y, "Popular Free Books", "See what readers love right now.", "tab:store")
	var pop []rowItem
	for k, it := range c.homePop {
		pop = append(pop, rowItem{it: it, id: fmt.Sprintf("home:pop:%d", k), caption: it.title})
	}
	c.rowOrStatus(p, "home:pop", y+150, pop, c.homePopLoading, "Connect to Wi-Fi to see the Book Store.")
	y += homeRowH + 40

	// Kindle-style shelves of the store, a row per category.
	for si, sh := range homeShelves {
		c.rowTitle(p, y, sh.title, sh.sub, fmt.Sprintf("home:all:%d", si))
		st := c.homeShelf[si]
		var items []rowItem
		loading := st == nil || st.loading
		if st != nil {
			for k, it := range st.items {
				items = append(items, rowItem{it: it, id: fmt.Sprintf("home:sh:%d:%d", si, k), caption: it.title})
			}
		}
		c.rowOrStatus(p, fmt.Sprintf("home:sh:%d", si), y+150, items, loading, "Couldn't load these. Is Wi-Fi on?")
		y += homeRowH
	}
	return p
}

// --- rows of covers --------------------------------------------------------------------------

const (
	tabsH    = 130 // the tab bar's band at the top of the books pages: it stays put
	homeRowH = 460 // a row's title and covers
	rowCW    = 150 // cover size in rows
	rowCH    = 225
	rowGap   = 33
)

// homeShelves are Home's store rows, Kindle style: Project Gutenberg by topic or language.
var homeShelves = []struct{ title, sub, topic, lang string }{
	{"Mystery & Detective", "Clues, crimes and great detectives.", "detective", ""},
	{"Adventure", "Voyages, quests and daring escapes.", "adventure", ""},
	{"Science Fiction", "Other worlds and other times.", "science fiction", ""},
	{"Romance", "Love stories from the classics.", "love stories", ""},
	{"Livres en français", "Les classiques, en version originale.", "", "fr"},
	{"Fantasy & Fairy Tales", "Myths, magic and enchanted lands.", "fairy tales", ""},
	{"Horror & Ghost Stories", "Haunted houses and things in the dark.", "horror", ""},
	{"Short Stories", "Something to finish tonight.", "short stories", ""},
	{"Philosophy", "Ideas that shaped the world.", "philosophy", ""},
	{"Poetry", "Verse to read slowly.", "poetry", ""},
	{"History", "True stories of the past.", "history", ""},
	{"Humor", "Books to make you laugh.", "humor", ""},
	{"Children's Classics", "For young readers, and everyone.", "children", ""},
}

type homeShelfState struct {
	items   []*storeItem
	loading bool
	failed  time.Time
}

// loadShelves fetches the store rows that aren't loaded, two at a time. Caller holds drawMu.
func (c *console) loadShelves() {
	if c.homeShelf == nil {
		c.homeShelf = map[int]*homeShelfState{}
	}
	if !homeLoads {
		return
	}
	busy := 0
	for _, st := range c.homeShelf {
		if st.loading {
			busy++
		}
	}
	for si := range homeShelves {
		if busy >= 2 {
			return
		}
		st := c.homeShelf[si]
		if st != nil && (st.loading || len(st.items) > 0 || time.Since(st.failed) < time.Minute) {
			continue
		}
		st = &homeShelfState{loading: true}
		c.homeShelf[si] = st
		busy++
		sh := homeShelves[si]
		pref := c.store.preferOPDS
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), sourceTimeout)
			res, err := searchGutenberg(ctx, storeSearch{topic: sh.topic, lang: sh.lang, page: 1}, &pref)
			cancel()
			drawMu.Lock()
			defer drawMu.Unlock()
			st.loading = false
			c.store.preferOPDS = c.store.preferOPDS || pref
			if err != nil || len(res.items) == 0 {
				st.failed = time.Now()
			} else {
				st.items = mergeItems(nil, res.items)
			}
			c.loadShelves() // the next ones
			c.homeRedrawSoon()
		}()
	}
}

// rowTitle is a row's serif title with a chevron (tap: see all) and its line under it.
func (c *console) rowTitle(p *page, y int, title, sub, id string) {
	f := apple()
	apText(p.img, f.serifTitle, 48, y+86, apLabel, title)
	iconChevronRight(p.img, 48+ui.TextWidth(f.serifTitle, title)+18, y+70, apSecondary)
	p.buttons = append(p.buttons, button{id, image.Rect(0, y+20, 48+ui.TextWidth(f.serifTitle, title)+60, y+110)})
	apText(p.img, f.caption, 48, y+126, apSecondary, sub)
}

type rowItem struct {
	it      *storeItem
	id      string // its button
	caption string // under the cover
	badge   string // "NEW"
}

// rowOrStatus draws a row, or a line saying it's loading or why it's empty.
func (c *console) rowOrStatus(p *page, id string, y int, items []rowItem, loading bool, empty string) {
	switch {
	case len(items) > 0:
		c.coverRow(p, id, y, items, apBG)
	case loading:
		apText(p.img, apple().callout, 48, y+60, apSecondary, "Loading…")
	default:
		apText(p.img, apple().callout, 48, y+60, apSecondary, empty)
	}
}

// coverRow draws covers in a row that scrolls sideways, on bg, and registers it so a finger
// can move it (its paint is called again at each new offset).
func (c *console) coverRow(p *page, id string, y int, items []rowItem, bg color.RGBA) {
	f := apple()
	band := image.Rect(0, y-10, c.s.W, y+rowCH+80)
	contentW := 2*48 + len(items)*(rowCW+rowGap) - rowGap
	paint := func(img *image.RGBA) {
		ui.Fill(img, band, bg)
		off := c.rowOffset(id)
		var btns []button
		for k, ri := range items {
			x := 48 + k*(rowCW+rowGap) - off
			if x+rowCW < 0 || x > c.s.W {
				continue // off the screen
			}
			r := image.Rect(x, y, x+rowCW, y+rowCH)
			shadowRect(img, r)
			c.drawCover(img, r, ri.it)
			by := r.Max.Y + 40
			switch {
			case ri.badge != "":
				nb := image.Rect(x, by-24, x+70, by+10)
				ui.RoundRect(img, nb, 17, apNewBadge)
				apTextCenter(img, textFace("inter-bold", fonts.InterBold, true, 20), (nb.Min.X+nb.Max.X)/2, (nb.Min.Y+nb.Max.Y)/2, rgb(0xffffff), ri.badge)
			case ri.caption != "":
				apText(img, f.caption, x, by, apSecondary, clip(f.caption, ri.caption, rowCW))
			}
			btns = append(btns, button{ri.id, image.Rect(x, y, x+rowCW, by+16)})
		}
		p.setRowButtons(id, btns)
	}
	paint(p.img)
	p.addRow(id, band, contentW, paint)
}

// backgroundRedraws: things arriving in the background (rows, covers) redraw Home; homeLoads:
// Home fetches its store rows. Tests turn both off (their consoles end before the arrivals),
// except the store test, which loads Home's rows from its fake libraries.
var backgroundRedraws, homeLoads = true, true

// homeRedrawSoon redraws Home a moment from now, once for many changes (covers arriving,
// rows loading), and not while a finger is scrolling. Caller holds drawMu.
func (c *console) homeRedrawSoon() {
	if c.homeRedrawQueued || !backgroundRedraws {
		return
	}
	c.homeRedrawQueued = true
	time.AfterFunc(400*time.Millisecond, func() {
		drawMu.Lock()
		defer drawMu.Unlock()
		c.homeRedrawQueued = false
		if c.sc.drag {
			c.homeRedrawSoon()
			return
		}
		if c.mode == modeBooksHome && c.screenOn && !c.locked {
			c.showPage()
		}
	})
}

func (c *console) drawTopPick(img *image.RGBA, r image.Rectangle, tp topPick) {
	f := apple()
	shadow(img, r, 26, 0.10)
	ui.RoundRect(img, r, 26, tp.bg)
	title := tp.title
	if title == "" {
		title = time.Now().Format("January")
	}
	art := image.Rect(r.Min.X, r.Min.Y+150, r.Max.X, r.Max.Y-118) // under the title, above the caption
	switch tp.art {
	case 0: // a mountain under a slanted ruler
		for i := 0; i < art.Dy(); i++ {
			w := i * (art.Dx() - 60) / art.Dy()
			ui.Fill(img, image.Rect(art.Min.X+30, art.Max.Y-i, art.Min.X+30+w-i/3, art.Max.Y-i+1), rgb(0x2f8a6a))
			ui.Fill(img, image.Rect(art.Min.X+30, art.Max.Y-i, art.Min.X+30+w/3, art.Max.Y-i+1), rgb(0x222222))
		}
		line(img, art.Max.X-24, art.Min.Y-60, art.Min.X+art.Dx()/2+30, art.Min.Y+art.Dy()/2, 34, rgb(0xf6cf3f))
	case 1: // sunglasses
		cx, cy := (art.Min.X+art.Max.X)/2, art.Min.Y+art.Dy()/2+6
		line(img, cx-150, cy-40, cx+150, cy-60, 26, rgb(0x2c8f4e))
		ui.Circle(img, cx-70, cy+10, 56, rgb(0xd8382f))
		ui.Circle(img, cx+70, cy+10, 56, rgb(0xd8382f))
		ui.Circle(img, cx-70, cy+10, 38, rgb(0x2a2a2a))
		ui.Circle(img, cx+70, cy+10, 38, rgb(0x2a2a2a))
		line(img, cx-20, cy, cx+20, cy, 10, rgb(0xd8382f))
	case 2: // an open book
		cx, cy := (art.Min.X+art.Max.X)/2, art.Min.Y+art.Dy()/2-6
		for i := 0; i < 4; i++ {
			line(img, cx-120, cy-50+i*4, cx-6, cy-30+i*4, 5, rgb(0x111111))
			line(img, cx+120, cy-50+i*4, cx+6, cy-30+i*4, 5, rgb(0x111111))
		}
		line(img, cx-120, cy-50, cx-120, cy+60, 5, rgb(0x111111))
		line(img, cx+120, cy-50, cx+120, cy+60, 5, rgb(0x111111))
		line(img, cx-120, cy+60, cx, cy+80, 5, rgb(0x111111))
		line(img, cx+120, cy+60, cx, cy+80, 5, rgb(0x111111))
		line(img, cx, cy-30, cx, cy+80, 5, rgb(0x111111))
	}
	ty := r.Min.Y + 70
	if tp.art == 2 {
		apText(img, f.captionBold, r.Min.X+28, r.Min.Y+46, rgb(0x111111), "condor Books")
		ty += 34
	}
	for _, l := range strings.Split(title, "\n") {
		apText(img, f.title, art.Min.X+28, ty, tp.titleCol, l)
		ty += 50
	}
	// The caption band.
	cb := image.Rect(r.Min.X, r.Max.Y-118, r.Max.X, r.Max.Y)
	blendRect(img, image.Rect(cb.Min.X, cb.Min.Y, cb.Max.X, cb.Max.Y-26), rgb(0xffffff), 0.18)
	ui.RoundRect(img, image.Rect(cb.Min.X, cb.Max.Y-52, cb.Max.X, cb.Max.Y), 26, blend(tp.bg, rgb(0xffffff), 0.18))
	ui.Fill(img, image.Rect(cb.Min.X, cb.Min.Y+40, cb.Max.X, cb.Max.Y-26), blend(tp.bg, rgb(0xffffff), 0.18))
	drawParagraphs(img, f.captionBold, tp.caption, cb.Min.X+28, cb.Min.Y+14, cb.Dx()-56, 30, cb.Max.Y, rgb(0xffffff))
}

// homeTap handles Home's cards. Caller holds drawMu.
func (c *console) homeTap(id string) bool {
	switch {
	case strings.HasPrefix(id, "home:pick:"):
		var k int
		fmt.Sscanf(id, "home:pick:%d", &k)
		st := &c.store
		st.query, st.sel = "", nil
		for i, t := range storeTopics {
			if t.topic == topPicks[k].topic {
				st.topic = i
			}
		}
		c.mode = modeStore
		c.storeLoad(false)
		c.showPage()
	case strings.HasPrefix(id, "home:sh:"):
		var si, k int
		fmt.Sscanf(id, "home:sh:%d:%d", &si, &k)
		if st := c.homeShelf[si]; st != nil && k < len(st.items) {
			c.store.sel = st.items[k]
			c.fetchSummary(c.store.sel)
			c.setMode(modeStore)
		}
	case strings.HasPrefix(id, "home:all:"): // the row's chevron: that topic in the store
		var si int
		fmt.Sscanf(id, "home:all:%d", &si)
		sh, st := homeShelves[si], &c.store
		st.query, st.sel, st.topic = "", nil, 0
		for i, t := range storeTopics {
			if t.topic == sh.topic && sh.topic != "" {
				st.topic = i
			}
		}
		if sh.lang != "" {
			for i, l := range storeLangs {
				if l == sh.lang {
					st.lang = i
				}
			}
		}
		c.mode = modeStore
		c.storeLoad(false)
		c.showPage()
	case strings.HasPrefix(id, "home:pop:"):
		var k int
		fmt.Sscanf(id, "home:pop:%d", &k)
		if k < len(c.homePop) {
			c.store.sel = c.homePop[k]
			c.fetchSummary(c.store.sel)
			c.setMode(modeStore)
		}
	default:
		return false
	}
	return true
}
