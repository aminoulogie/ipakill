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
	if c.homePopLoading || len(c.homePop) > 0 {
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
	h := c.s.H - c.barH
	img := canvas(c.s.W, h)
	ui.Fill(img, img.Rect, apBG)
	p := &page{img: img}
	mx := 48
	covers.mu.Lock()
	covers.loaded = func() {
		drawMu.Lock()
		if c.mode == modeBooksHome {
			c.showPage()
		}
		drawMu.Unlock()
	}
	covers.mu.Unlock()
	c.shelf = findBooks()
	if time.Since(c.homePopErr) > time.Minute {
		c.loadPopular()
	}
	item := func(b shelfBook) *storeItem {
		return &storeItem{key: b.path, title: b.title, author: b.author, cover: b.cover}
	}

	c.booksTabs(p, "tab:home")
	apText(img, f.serifLarge, mx, 228, apLabel, "Home")
	y := 228

	// Continue.
	if now := c.readingNow(); len(now) > 0 {
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

	// Want to Read: the library, on the grouped band.
	band := image.Rect(0, y, c.s.W, y+500)
	ui.Fill(img, band, apBand)
	apText(img, f.serifTitle, mx, y+78, apLabel, "Want to Read")
	iconChevronRight(img, mx+ui.TextWidth(f.serifTitle, "Want to Read")+18, y+62, apSecondary)
	p.buttons = append(p.buttons, button{"tab:library", image.Rect(0, y, c.s.W/2, y+100)})
	apText(img, f.caption, mx, y+118, apSecondary, "Books you'd like to read next.")
	cy := y + 150
	const cw, chh, gap = 150, 225, 33
	if len(c.shelf) == 0 {
		apText(img, f.body, mx, cy+80, apSecondary, "Your library is empty.")
		r := image.Rect(mx, cy+120, mx+340, cy+210)
		ui.RoundRect(img, r, 45, apBlue)
		apTextCenter(img, f.headline, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, rgb(0xffffff), "Book Store")
		p.buttons = append(p.buttons, button{"tab:store", r})
	}
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
	for k, i := range order[:min(len(order), 6)] {
		b := c.shelf[i]
		x := mx + k*(cw+gap)
		r := image.Rect(x, cy+chh-chh, x+cw, cy+chh)
		shadowRect(img, r)
		c.drawCover(img, r, item(b))
		by := r.Max.Y + 34
		if pr, ok := c.lib.Progress[b.path]; ok {
			apText(img, f.caption, x, by+8, apSecondary, fmt.Sprintf("%d%%", pr.Pct))
		} else {
			nb := image.Rect(x, by-16, x+70, by+18)
			ui.RoundRect(img, nb, 17, apNewBadge)
			apTextCenter(img, textFace("inter-bold", fonts.InterBold, true, 20), (nb.Min.X+nb.Max.X)/2, (nb.Min.Y+nb.Max.Y)/2, rgb(0xffffff), "NEW")
		}
		iconDots(img, r.Max.X-22, by, apSecondary)
		p.buttons = append(p.buttons, button{fmt.Sprintf("book%d", i), image.Rect(x, cy, x+cw, by+24)})
	}
	y = band.Max.Y

	// The most read free books.
	apText(img, f.serifTitle, mx, y+86, apLabel, "Popular Free Books")
	iconChevronRight(img, mx+ui.TextWidth(f.serifTitle, "Popular Free Books")+18, y+70, apSecondary)
	p.buttons = append(p.buttons, button{"tab:store", image.Rect(0, y, c.s.W/2, y+110)})
	apText(img, f.caption, mx, y+126, apSecondary, "See what readers love right now.")
	cy = y + 160
	switch {
	case len(c.homePop) > 0:
		for k, it := range c.homePop[:min(len(c.homePop), 6)] {
			x := mx + k*(cw+gap)
			r := image.Rect(x, cy, x+cw, cy+chh)
			shadowRect(img, r)
			c.drawCover(img, r, it)
			p.buttons = append(p.buttons, button{fmt.Sprintf("home:pop:%d", k), r})
		}
	case c.homePopLoading:
		apText(img, f.callout, mx, cy+60, apSecondary, "Loading…")
	default:
		apText(img, f.callout, mx, cy+60, apSecondary, "Connect to Wi-Fi to see the Book Store.")
	}
	return p
}

// drawTopPick draws one of the illustrated cards.
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
