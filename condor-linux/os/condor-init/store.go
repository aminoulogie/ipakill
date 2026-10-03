package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"image"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/image/font"

	"condor-init/ui"
)

// The Store app, laid out like a Kindle store: a grid of covers, and a page per book with a
// big cover, where it can be had (storesrc.go), and its summary. Browsing lists Project
// Gutenberg's most read books (by topic); a search asks every source at once, merges the
// same book found in several places, and puts the books you can read in full first.
// "read preview" opens a book without putting it on the shelf; "download" saves it to Books.
//
// Everything goes through condor-init's own proxy (127.0.0.1:3128), so it works over Wi-Fi
// and over USB with `condor net` alike. Certificates come from Alpine's bundle: Android
// 4.2's store is from 2013 and misses today's roots.

type storeTopic struct{ label, topic string }

var storeTopics = []storeTopic{
	{"popular", ""}, {"fiction", "fiction"}, {"sci-fi", "science fiction"}, {"mystery", "detective"},
	{"romance", "love stories"}, {"adventure", "adventure"}, {"children", "children"},
	{"history", "history"}, {"philosophy", "philosophy"}, {"poetry", "poetry"},
}

var storeLangs = []string{"", "en", "fr", "es", "de", "it", "ar"}

type storeState struct {
	query, editing string // the search in use, and the one being typed
	topic, lang    int    // indexes into storeTopics, storeLangs
	results        []*storeItem
	view           int  // index of the first result on screen
	remotePage     int  // Gutenberg pages loaded while browsing
	hasMore        bool // Gutenberg has more pages
	started        bool
	pending        int                // sources still answering
	srcState       [numSources]string // per source: "", "...", "12 books", "failed"
	errs           []string
	gen            int  // drops answers to searches that were replaced
	preferOPDS     bool // gutendex failed: ask gutenberg.org first
	sel            *storeItem
	typing         bool // keyboard up
	dl             map[string]string
	redrawQueued   bool
	lastRedraw     time.Time
}

// webClient talks to the internet through condor-init's proxy. Tests replace it.
var webClient = sync.OnceValue(func() *http.Client {
	return &http.Client{Timeout: 10 * time.Minute, Transport: &http.Transport{
		Proxy:                 http.ProxyURL(&url.URL{Scheme: "http", Host: proxyAddr}),
		TLSClientConfig:       &tls.Config{RootCAs: alpineRoots()},
		TLSHandshakeTimeout:   20 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		MaxIdleConnsPerHost:   4,
	}}
})

// alpineRoots is Alpine's certificate bundle, or nil (Go's default search) without it.
func alpineRoots() *x509.CertPool {
	b, err := os.ReadFile(alpineRoot + "/etc/ssl/certs/ca-certificates.crt")
	if err != nil {
		return nil
	}
	p := x509.NewCertPool()
	if !p.AppendCertsFromPEM(b) {
		return nil
	}
	return p
}

func webGet(u string) (*http.Response, error) { return webGetCtx(context.Background(), u) }

func webGetCtx(ctx context.Context, u string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "condor-books/1.0 (Condor TRA-901G tablet reader)")
	resp, err := webClient().Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("%s: %s", u, resp.Status)
	}
	return resp, nil
}

func shortErr(err error) string {
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i >= 0 && len(s)-i < 60 {
		s = s[i+2:]
	}
	return s
}

// sourceTimeout bounds each source's search.
var sourceTimeout = 40 * time.Second

// storeLoad starts a search (or, with more, loads Gutenberg's next page while browsing).
// Sources answer in the background; results are merged as they come. Caller holds drawMu.
func (c *console) storeLoad(more bool) {
	st := &c.store
	st.gen++
	st.started = true
	gen := st.gen
	if more {
		st.remotePage++
	} else {
		st.results, st.view, st.remotePage, st.hasMore, st.errs = nil, 0, 1, false, nil
	}
	q := storeSearch{query: st.query, lang: storeLangs[st.lang], page: st.remotePage}
	type source struct {
		src int
		f   func(context.Context, storeSearch) (sourceResult, error)
	}
	pref := st.preferOPDS
	gutenberg := func(ctx context.Context, q storeSearch) (sourceResult, error) {
		res, err := searchGutenberg(ctx, q, &pref)
		drawMu.Lock()
		st.preferOPDS = pref
		drawMu.Unlock()
		return res, err
	}
	srcs := []source{{srcGutenberg, gutenberg}}
	if q.query == "" {
		q.topic = storeTopics[st.topic].topic
	} else {
		srcs = append(srcs, source{srcGoogle, searchGoogle}, source{srcArchive, searchArchive},
			source{srcOpenLibrary, searchOpenLibrary})
	}
	st.srcState = [numSources]string{}
	for _, s := range srcs {
		st.pending++
		st.srcState[s.src] = "..."
		go func() {
			start := time.Now()
			ctx, cancel := context.WithTimeout(context.Background(), sourceTimeout)
			res, err := s.f(ctx, q)
			cancel()
			log.Printf("store: %s %+v: %d books in %v, err %v", sourceShort[s.src], q, len(res.items),
				time.Since(start).Round(time.Millisecond), err)
			drawMu.Lock()
			defer drawMu.Unlock()
			if gen != st.gen {
				return
			}
			st.pending--
			if err != nil {
				st.srcState[s.src] = "failed"
				st.errs = append(st.errs, sourceShort[s.src]+": "+shortErr(err))
			} else {
				st.srcState[s.src] = fmt.Sprintf("%d", len(res.items))
				for _, it := range res.items { // later pages come after earlier ones
					for i := range it.offers {
						it.offers[i].rank += (q.page - 1) * 100
					}
				}
				st.results = mergeItems(st.results, res.items)
				if s.src == srcGutenberg {
					st.hasMore = res.hasNext && q.query == ""
				}
			}
			c.storeRedraw()
		}()
	}
}

// storeRedraw redraws the store soon, at most every 300 ms (results and covers arrive in
// bursts). Caller holds drawMu.
func (c *console) storeRedraw() {
	st := &c.store
	if c.mode != modeStore || st.redrawQueued {
		return
	}
	if wait := 300*time.Millisecond - time.Since(st.lastRedraw); wait > 0 {
		st.redrawQueued = true
		time.AfterFunc(wait, func() {
			drawMu.Lock()
			defer drawMu.Unlock()
			st.redrawQueued = false
			if c.mode == modeStore {
				st.lastRedraw = time.Now()
				c.showPage()
			}
		})
		return
	}
	st.lastRedraw = time.Now()
	c.showPage()
}

// storeFetch downloads the open book, to Books (save) or as a preview, in the background.
// Caller holds drawMu.
func (c *console) storeFetch(save bool) {
	st := &c.store
	it := st.sel
	if it == nil || !it.full() || strings.HasPrefix(st.dl[it.key], "downloading") {
		return
	}
	dst := filepath.Join(previewDir, hash(it.key)+".epub")
	if save {
		dst = filepath.Join(storeDir, it.fileName())
	}
	if _, err := os.Stat(dst); err == nil { // already here: read it
		c.openFromStore(dst)
		return
	}
	st.dl[it.key] = "downloading..."
	c.showPage()
	go func() {
		last := time.Now()
		err := fetchItem(it, dst, func(n int64) {
			if time.Since(last) < time.Second {
				return
			}
			last = time.Now()
			drawMu.Lock()
			st.dl[it.key] = fmt.Sprintf("downloading... %.1f MB", float64(n)/(1<<20))
			if c.mode == modeStore && st.sel == it {
				c.showPage()
			}
			drawMu.Unlock()
		})
		drawMu.Lock()
		defer drawMu.Unlock()
		switch {
		case err != nil:
			log.Printf("store: %s: %v", it.title, err)
			st.dl[it.key] = "failed: " + err.Error()
		case save:
			log.Printf("store: saved %s", dst)
			st.dl[it.key] = "saved"
		default:
			st.dl[it.key] = ""
			trimPreviews(5)
		}
		if c.mode != modeStore || st.sel != it {
			return
		}
		if err == nil && !save {
			c.openFromStore(dst)
			return
		}
		c.showPage()
	}()
}

// trimPreviews keeps the newest n previews.
func trimPreviews(n int) {
	es, _ := os.ReadDir(previewDir)
	type f struct {
		p string
		t time.Time
	}
	var fs []f
	for _, e := range es {
		if fi, err := e.Info(); err == nil && strings.HasSuffix(e.Name(), ".epub") {
			fs = append(fs, f{filepath.Join(previewDir, e.Name()), fi.ModTime()})
		}
	}
	sort.Slice(fs, func(i, j int) bool { return fs[i].t.After(fs[j].t) })
	for i := n; i < len(fs); i++ {
		os.Remove(fs[i].p)
	}
}

func (c *console) openFromStore(path string) {
	c.fromStore = true
	c.openBookAt(path)
}

// storeKey takes the keyboard's output while typing a search. Caller holds drawMu.
func (c *console) storeKey(b []byte) {
	st := &c.store
	if len(b) == 0 || b[0] == 0x1b { // arrows, esc: nothing to do in a one-line box
		return
	}
	for _, ch := range string(b) {
		switch {
		case ch == '\r' || ch == '\n':
			c.storeSearch()
			return
		case ch == 0x7f || ch == 0x08:
			if r := []rune(st.editing); len(r) > 0 {
				st.editing = string(r[:len(r)-1])
			}
		case ch == 0x15: // ctrl+u clears
			st.editing = ""
		case ch >= ' ' && len(st.editing) < 80:
			st.editing += string(ch)
		}
	}
	c.drawSearchBox()
}

// storeSearch runs what was typed and puts the keyboard away. Caller holds drawMu.
func (c *console) storeSearch() {
	st := &c.store
	st.typing, st.query = false, strings.TrimSpace(st.editing)
	c.storeLoad(false)
	c.showPage()
}

// Layout of the store (page coordinates; the screen is 1200 wide), after Apple's Book Store.
const (
	gridTop   = 590
	gridCols  = 4
	gridRows  = 2
	gridGap   = 36
	gridCellW = (1200 - 2*48 - (gridCols-1)*gridGap) / gridCols // 249
	gridCover = gridCellW * 3 / 2                               // 373
	gridCellH = gridCover + 124
	perView   = gridCols * gridRows
)

var (
	storeSearchR = image.Rect(48, 236, 1200-48-180, 326)
	storeGoR     = image.Rect(1200-48-170, 236, 1200-48, 326)
)

// drawSearchBox redraws just the search field (fast, for each key). Caller holds drawMu.
func (c *console) drawSearchBox() {
	r := storeSearchR
	img := image.NewRGBA(r)
	ui.Fill(img, r, apBG)
	c.searchBox(img)
	c.s.blitRGBA(img, r.Min.X, r.Min.Y+c.barH)
	c.s.Flush()
}

// searchBox is an iOS search field: a magnifier, then the search or its placeholder.
func (c *console) searchBox(img *image.RGBA) {
	f := apple()
	st := &c.store
	r := storeSearchR
	ui.RoundRect(img, r, 22, apCard2)
	cx, cy := r.Min.X+44, (r.Min.Y+r.Max.Y)/2-4
	ring(img, cx, cy, 13, 5, 1, apSecondary, apSecondary)
	line(img, cx+10, cy+10, cx+20, cy+20, 6, apSecondary)
	text, col := st.query, apLabel
	if st.typing {
		text = st.editing + "|"
	}
	if text == "" {
		text, col = "Books, Authors", apSecondary
	}
	for text != "" && ui.TextWidth(f.body, text) > r.Dx()-110 { // show the end, where the typing is
		text = string([]rune(text)[1:])
	}
	apText(img, f.body, r.Min.X+84, r.Min.Y+58, col, text)
}

// badge is the line under a cover: free and where, or a price, or info only.
func (it *storeItem) badge() (string, bool) {
	if it.full() {
		n := 0
		for _, o := range it.offers {
			if o.full {
				n++
			}
		}
		if n > 1 {
			return fmt.Sprintf("FREE · %d sources", n), true
		}
		return "FREE · " + sourceShort[it.offers[0].src], true
	}
	for _, o := range it.offers {
		if o.price != "" {
			return o.price + " · info", false
		}
	}
	return "info only", false
}

// capsules lays out pill buttons across rows, wrapping at the margin.
func capsules(p *page, x, y, right int, ids, labels []string, on string) int {
	f := apple()
	px := x
	for i, id := range ids {
		w := ui.TextWidth(f.captionBold, labels[i]) + 56
		if px+w > right {
			px, y = x, y+84
		}
		r := image.Rect(px, y, px+w, y+66)
		bg, fg := apCard2, apLabel
		if id == on {
			bg, fg = apOrange, apBG
		}
		ui.RoundRect(p.img, r, 33, bg)
		apTextCenter(p.img, f.captionBold, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, fg, labels[i])
		p.buttons = append(p.buttons, button{id, r})
		px += w + 16
	}
	return y + 66
}

// storePage is the cover grid, or one book's page.
func (c *console) storePage() *page {
	st := &c.store
	if st.dl == nil {
		st.dl = map[string]string{}
	}
	covers.mu.Lock()
	covers.loaded = func() {
		drawMu.Lock()
		c.storeRedraw()
		drawMu.Unlock()
	}
	covers.mu.Unlock()
	if !st.started {
		c.storeLoad(false)
	}
	if st.sel != nil {
		return c.storeBookPage()
	}
	f := apple()
	h := c.s.H - c.barH
	img := canvas(c.s.W, h)
	ui.Fill(img, img.Rect, apBG)
	p := &page{img: img}
	mx := 48

	iconBack(img, mx, 64, apOrange)
	apText(img, f.body, mx+30, 76, apOrange, "Home")
	p.buttons = append(p.buttons, button{"home", image.Rect(0, 10, 260, 120)})
	lang := "All Languages"
	if l := storeLangs[st.lang]; l != "" {
		lang = languageLabel(l)
	}
	apTextRight(img, f.body, c.s.W-mx, 76, apOrange, lang)
	p.buttons = append(p.buttons, button{"s:lang", image.Rect(c.s.W-420, 10, c.s.W, 120)})
	apText(img, f.largeTitle, mx, 200, apLabel, "Book Store")

	c.searchBox(img)
	p.buttons = append(p.buttons, button{"s:search", storeSearchR})
	goLabel := "Search"
	if st.query != "" && !st.typing {
		goLabel = "Cancel"
	}
	apTextCenter(img, f.body, (storeGoR.Min.X+storeGoR.Max.X)/2, (storeGoR.Min.Y+storeGoR.Max.Y)/2, apOrange, goLabel)
	p.buttons = append(p.buttons, button{"s:go", storeGoR})

	head := ""
	if st.query == "" { // browsing: topics as capsules
		var ids, labels []string
		for i, tp := range storeTopics {
			ids = append(ids, fmt.Sprintf("s:topic%d", i))
			labels = append(labels, strings.ToUpper(tp.label[:1])+tp.label[1:])
		}
		capsules(p, mx, 352, c.s.W-mx, ids, labels, fmt.Sprintf("s:topic%d", st.topic))
		head = strings.ToUpper(storeTopics[st.topic].label[:1]) + storeTopics[st.topic].label[1:] + " on Project Gutenberg"
	} else { // searching: how each library did
		var parts []string
		for s := 0; s < numSources; s++ {
			switch v := st.srcState[s]; v {
			case "":
			case "...":
				parts = append(parts, sourceShort[s]+" …")
			case "failed":
				parts = append(parts, sourceShort[s]+" failed")
			default:
				parts = append(parts, sourceShort[s]+" "+v)
			}
		}
		apText(img, f.caption, mx, 400, apSecondary, clip(f.caption, strings.Join(parts, "  ·  "), c.s.W-2*mx))
		apText(img, f.caption, mx, 444, apSecondary, "Free full books first, then books to look at before buying")
		head = "Results for “" + st.query + "”"
	}
	apText(img, f.headline, mx, gridTop-30, apLabel, clip(f.headline, head, c.s.W-2*mx))

	switch {
	case len(st.results) == 0 && st.pending > 0:
		apText(img, f.body, mx, gridTop+80, apSecondary, "Looking for books…")
	case len(st.results) == 0:
		msg := "No Results"
		if len(st.errs) > 0 {
			msg = "Can't reach the libraries. Is Wi-Fi on?"
		}
		apText(img, f.title, mx, gridTop+90, apLabel, msg)
		y := gridTop + 110
		for _, e := range st.errs {
			y = drawParagraphs(img, f.caption, e, mx, y+10, c.s.W-2*mx, 38, y+200, apSecondary)
		}
		r := image.Rect(mx, y+40, mx+300, y+136)
		ui.RoundRect(img, r, 48, apOrange)
		apTextCenter(img, f.headline, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, apBG, "Try Again")
		p.buttons = append(p.buttons, button{"s:retry", r})
	}

	st.view = min(st.view, max(len(st.results)-1, 0)/perView*perView)
	for i := st.view; i < len(st.results) && i < st.view+perView; i++ {
		it := st.results[i]
		col, row := (i-st.view)%gridCols, (i-st.view)/gridCols
		x, y := mx+col*(gridCellW+gridGap), gridTop+row*(gridCellH+16)
		cr := image.Rect(x, y, x+gridCellW, y+gridCover)
		shadowRect(img, cr)
		c.drawCover(img, cr, it)
		apText(img, f.captionBold, x, cr.Max.Y+40, apLabel, clip(f.captionBold, it.title, gridCellW))
		apText(img, f.caption, x, cr.Max.Y+76, apSecondary, clip(f.caption, it.author, gridCellW))
		badge, free := it.badge()
		bc := apSecondary
		if free {
			bc = apOrange
		}
		apText(img, f.captionBold, x, cr.Max.Y+112, bc, clip(f.captionBold, badge, gridCellW))
		p.buttons = append(p.buttons, button{fmt.Sprintf("s:item%d", i), image.Rect(x, y, x+gridCellW, y+gridCellH)})
	}

	if !st.typing && len(st.results) > 0 {
		by := h - 100
		more := ""
		if st.hasMore || st.pending > 0 {
			more = "+"
		}
		apText(img, f.body, mx, by+48, apOrange, "‹ Previous")
		p.buttons = append(p.buttons, button{"s:prev", image.Rect(0, by, 360, by+90)})
		apTextCenter(img, f.caption, c.s.W/2, by+38, apSecondary,
			fmt.Sprintf("%d–%d of %d%s", st.view+1, min(st.view+perView, len(st.results)), len(st.results), more))
		apTextRight(img, f.body, c.s.W-mx, by+48, apOrange, "Next ›")
		p.buttons = append(p.buttons, button{"s:next", image.Rect(c.s.W-360, by, c.s.W, by+90)})
	}
	return p
}

// storeBookPage is a book's page: big cover, Get and Sample, where to get it, about.
func (c *console) storeBookPage() *page {
	f := apple()
	st := &c.store
	it := st.sel
	h := c.s.H - c.barH
	img := canvas(c.s.W, h)
	ui.Fill(img, img.Rect, apBG)
	p := &page{img: img}
	mx := 48
	iconBack(img, mx, 64, apOrange)
	apText(img, f.body, mx+30, 76, apOrange, "Book Store")
	p.buttons = append(p.buttons, button{"s:back", image.Rect(0, 10, 340, 120)})

	cr := image.Rect(mx, 150, mx+420, 150+630)
	shadowRect(img, cr)
	c.drawCover(img, cr, it)
	x, w := cr.Max.X+44, c.s.W-mx-(cr.Max.X+44)
	y := cr.Min.Y + 10
	for i, l := range layoutWords(f.title, strings.Fields(it.title), 0, w, false) {
		if i == 5 {
			break
		}
		y += 54
		drawWords(img, f.title, l, x, y, apLabel)
	}
	y += 50
	apText(img, f.body, x, y, apOrange, clip(f.body, it.author, w))
	var meta []string
	if it.year > 0 {
		meta = append(meta, fmt.Sprint(it.year))
	}
	if len(it.langs) > 0 {
		meta = append(meta, strings.Join(it.langs[:min(len(it.langs), 3)], ", "))
	}
	if it.downloads > 0 {
		meta = append(meta, fmt.Sprintf("%d downloads", it.downloads))
	}
	y += 48
	apText(img, f.caption, x, y, apSecondary, clip(f.caption, strings.Join(meta, "  ·  "), w))
	badge, free := it.badge()
	bc := apSecondary
	if free {
		bc = apOrange
	}
	y += 44
	apText(img, f.captionBold, x, y, bc, badge)

	// Get and Sample, the store's two capsules.
	y = cr.Max.Y + 50
	state := st.dl[it.key]
	if it.full() {
		saved := state == "saved"
		if !saved {
			if _, err := os.Stat(filepath.Join(storeDir, it.fileName())); err == nil {
				saved = true
			}
		}
		get := "Get"
		if saved {
			get = "Open"
		}
		half := (c.s.W - 2*mx - 24) / 2
		gr := image.Rect(mx, y, mx+half, y+100)
		ui.RoundRect(img, gr, 50, apOrange)
		apTextCenter(img, f.headline, (gr.Min.X+gr.Max.X)/2, (gr.Min.Y+gr.Max.Y)/2, apBG, get)
		p.buttons = append(p.buttons, button{"s:dl", gr})
		sr := image.Rect(mx+half+24, y, c.s.W-mx, y+100)
		ui.RoundRect(img, sr, 50, apCard2)
		apTextCenter(img, f.headline, (sr.Min.X+sr.Max.X)/2, (sr.Min.Y+sr.Max.Y)/2, apOrange, "Sample")
		p.buttons = append(p.buttons, button{"s:preview", sr})
		note := "Sample opens the whole book now, without adding it to your library."
		if state != "" && state != "saved" {
			note = strings.ToUpper(state[:1]) + state[1:]
		} else if saved {
			note = "In your library."
		}
		y = drawParagraphs(img, f.caption, note, mx, y+120, c.s.W-2*mx, 38, y+240, apSecondary)
	} else {
		apText(img, f.headline, mx, y+40, apLabel, "Not free to read")
		apText(img, f.caption, mx, y+84, apSecondary, "Read the description below before you buy it elsewhere.")
		y += 110
	}

	section := func(title string) {
		y += 30
		ui.Fill(img, image.Rect(mx, y, c.s.W-mx, y+2), apSeparator)
		y += 66
		apText(img, f.headline, mx, y, apLabel, title)
		y += 12
	}
	section("Where to Get It")
	for _, o := range it.offers {
		what := "Full book, free"
		if !o.full {
			what = o.note
			if o.price != "" {
				what = o.price + ": " + o.note
			}
		}
		y += 46
		apText(img, f.callout, mx, y, apLabel, sourceNames[o.src])
		apText(img, f.caption, mx+330, y, apSecondary, clip(f.caption, what, c.s.W-2*mx-330))
	}
	section("About")
	about := it.summary
	if about == "" && len(it.subjects) > 0 {
		about = "Subjects: " + strings.Join(it.subjects, "; ")
	}
	if about == "" {
		about = "No description from these libraries."
		if it.full() {
			about += " Tap Sample to start reading."
		}
	}
	drawParagraphs(img, f.callout, about, mx, y+16, c.s.W-2*mx, 44, h-30, apLabel)
	return p
}

// fetchSummary fills in an Open Library book's description in the background.
// Caller holds drawMu.
func (c *console) fetchSummary(it *storeItem) {
	if it.summary != "" || it.olWork == "" {
		return
	}
	work := it.olWork
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		s := openLibrarySummary(ctx, work)
		cancel()
		if s == "" {
			return
		}
		drawMu.Lock()
		defer drawMu.Unlock()
		if it.summary == "" {
			it.summary = s
			if c.store.sel == it {
				c.storeRedraw()
			}
		}
	}()
}

// wrapText breaks s into lines no wider than width.
func wrapText(f font.Face, s string, width int) []string {
	var lines []string
	cur := ""
	for _, w := range strings.Fields(s) {
		next := w
		if cur != "" {
			next = cur + " " + w
		}
		if cur != "" && ui.TextWidth(f, next) > width {
			lines = append(lines, cur)
			next = w
		}
		for ui.TextWidth(f, next) > width && len([]rune(next)) > 1 { // one very long word
			r := []rune(next)
			cut := len(r) - 1
			for cut > 1 && ui.TextWidth(f, string(r[:cut])) > width {
				cut--
			}
			lines = append(lines, string(r[:cut]))
			next = string(r[cut:])
		}
		cur = next
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

// storeTap handles the store's buttons. Caller holds drawMu.
func (c *console) storeTap(id string) bool {
	st := &c.store
	if !strings.HasPrefix(id, "s:") && id != "store" {
		return false
	}
	if id != "s:search" && st.typing && id != "s:go" {
		st.typing = false // a tap elsewhere puts the keyboard away
	}
	switch {
	case id == "store":
		c.setMode(modeStore)
		return true
	case id == "s:search":
		if !st.typing {
			st.typing, st.editing = true, st.query
			c.skb.visible = true
		}
	case id == "s:go":
		if st.typing {
			c.storeSearch()
			return true
		}
		if st.query != "" { // "clear": back to browsing
			st.query, st.editing = "", ""
			c.storeLoad(false)
		} else {
			st.typing, st.editing = true, ""
			c.skb.visible = true
		}
	case id == "s:retry":
		c.storeLoad(false)
	case id == "s:lang":
		st.lang = (st.lang + 1) % len(storeLangs)
		c.storeLoad(false)
	case strings.HasPrefix(id, "s:topic"):
		fmt.Sscanf(id, "s:topic%d", &st.topic)
		c.storeLoad(false)
	case id == "s:prev":
		st.view = max(st.view-perView, 0)
	case id == "s:next":
		switch {
		case st.view+perView < len(st.results):
			st.view += perView
		case st.hasMore && st.pending == 0:
			st.view += perView
			c.storeLoad(true)
		}
	case strings.HasPrefix(id, "s:item"):
		var i int
		if _, err := fmt.Sscanf(id, "s:item%d", &i); err == nil && i < len(st.results) {
			st.sel = st.results[i]
			c.fetchSummary(st.sel)
		}
	case id == "s:back":
		st.sel = nil
	case id == "s:preview":
		c.storeFetch(false)
		return true
	case id == "s:dl":
		c.storeFetch(true)
		return true
	}
	c.showPage()
	return true
}
