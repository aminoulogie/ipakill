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

// Layout of the grid page (page coordinates; the screen is 1200 wide).
const (
	gridTop   = 470
	gridCols  = 3
	gridRows  = 2
	gridGap   = 36
	gridCellW = (1200 - 2*48 - (gridCols-1)*gridGap) / gridCols // 344
	gridCover = gridCellW * 7 / 5                               // 481: covers are about 5:7
	gridCellH = gridCover + 128
	perView   = gridCols * gridRows
)

var (
	storeSearchR = image.Rect(48, 130, 1200-48-200, 220)
	storeGoR     = image.Rect(1200-48-180, 130, 1200-48, 220)
)

// drawSearchBox redraws just the search box (fast, for each key). Caller holds drawMu.
func (c *console) drawSearchBox() {
	r := storeSearchR
	img := image.NewRGBA(r)
	ui.Fill(img, r, pgBG)
	c.searchBox(img)
	c.s.blitRGBA(img, r.Min.X, r.Min.Y+c.barH)
	c.s.Flush()
}

func (c *console) searchBox(img *image.RGBA) {
	st := &c.store
	r := storeSearchR
	bg := pgCard
	if st.typing {
		bg = pgBtn
	}
	ui.RoundRect(img, r, 18, bg)
	text, col := st.query, pgText
	if st.typing {
		text = st.editing + "_"
	}
	if text == "" {
		text, col = "search books, authors...", pgMuted
	}
	// Show the end of a long search, where the typing is.
	for text != "" && ui.TextWidth(c.pf.body, text) > r.Dx()-60 {
		text = string([]rune(text)[1:])
	}
	ui.DrawText(img, c.pf.body, r.Min.X+30, r.Min.Y+58, col, text)
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
	h := c.s.H - c.barH
	pn := newPen(c.s.W, h, c.pf)
	pn.btn("home", "< home", image.Rect(pn.mx-12, 20, pn.mx+240, 110), pgBtn, pgText)
	pn.text(c.pf.title, pgText, pn.mx+280, 88, "store")
	lang := storeLangs[st.lang]
	if lang == "" {
		lang = "all"
	}
	pn.btn("s:lang", "lang: "+lang, image.Rect(c.s.W-pn.mx-280, 20, c.s.W-pn.mx, 110), pgBtn, pgText)

	c.searchBox(pn.p.img)
	pn.p.buttons = append(pn.p.buttons, button{"s:search", storeSearchR})
	goLabel := "search"
	if st.query != "" && !st.typing {
		goLabel = "clear"
	}
	pn.btn("s:go", goLabel, storeGoR, pgSel, pgDark)

	pn.y = storeGoR.Max.Y
	head := ""
	if st.query == "" { // browsing: topics
		for row := 0; row < 2; row++ {
			var ids, labels []string
			for i := row * 5; i < row*5+5; i++ {
				ids = append(ids, fmt.Sprintf("s:topic%d", i))
				labels = append(labels, storeTopics[i].label)
			}
			pn.smallRow(ids, labels, fmt.Sprintf("s:topic%d", st.topic))
		}
		head = strings.ToUpper(storeTopics[st.topic].label) + " ON PROJECT GUTENBERG"
	} else { // searching: how each source did
		pn.y += 50
		var parts []string
		for s := 0; s < numSources; s++ {
			switch v := st.srcState[s]; v {
			case "":
			case "...":
				parts = append(parts, sourceShort[s]+" ...")
			case "failed":
				parts = append(parts, sourceShort[s]+" failed")
			default:
				parts = append(parts, sourceShort[s]+" "+v)
			}
		}
		pn.text(c.pf.small, pgMuted, pn.mx, pn.y, clip(c.pf.small, strings.Join(parts, "  ·  "), c.s.W-2*pn.mx))
		pn.y += 50
		pn.text(c.pf.small, pgMuted, pn.mx, pn.y, "full free books first, then books to look at before buying")
		head = fmt.Sprintf("RESULTS FOR \"%s\"", strings.ToUpper(st.query))
	}
	pn.text(c.pf.small, pgAccent, pn.mx, gridTop-24, clip(c.pf.small, head, c.s.W-2*pn.mx))

	switch {
	case len(st.results) == 0 && st.pending > 0:
		pn.text(c.pf.body, pgMuted, pn.mx, gridTop+80, "looking for books...")
	case len(st.results) == 0:
		msg := "nothing found"
		if len(st.errs) > 0 {
			msg = "can't reach the libraries: is Wi-Fi on?"
		}
		pn.text(c.pf.body, pgText, pn.mx, gridTop+80, msg)
		pn.y = gridTop + 100
		for _, e := range st.errs {
			for _, l := range wrapText(c.pf.small, e, c.s.W-2*pn.mx) {
				pn.y += 40
				pn.text(c.pf.small, pgMuted, pn.mx, pn.y, l)
			}
		}
		pn.y += 20
		pn.row([]string{"s:retry"}, []string{"retry"}, "", false)
	}

	st.view = min(st.view, max(len(st.results)-1, 0)/perView*perView)
	for i := st.view; i < len(st.results) && i < st.view+perView; i++ {
		it := st.results[i]
		col, row := (i-st.view)%gridCols, (i-st.view)/gridCols
		x, y := pn.mx+col*(gridCellW+gridGap), gridTop+row*(gridCellH+20)
		cr := image.Rect(x, y, x+gridCellW, y+gridCover)
		c.drawCover(pn.p.img, cr, it)
		pn.text(c.pf.small, pgText, x, cr.Max.Y+40, clip(c.pf.small, it.title, gridCellW))
		pn.text(c.pf.small, pgMuted, x, cr.Max.Y+78, clip(c.pf.small, it.author, gridCellW))
		badge, free := it.badge()
		bc := pgMuted
		if free {
			bc = pgAccent
		}
		pn.text(c.pf.small, bc, x, cr.Max.Y+116, clip(c.pf.small, badge, gridCellW))
		pn.p.buttons = append(pn.p.buttons, button{fmt.Sprintf("s:item%d", i), image.Rect(x, y, x+gridCellW, y+gridCellH)})
	}

	if !st.typing && len(st.results) > 0 {
		pn.y = h - 124
		more := ""
		if st.hasMore || st.pending > 0 {
			more = "+"
		}
		ids := []string{"s:prev", "s:count", "s:next"}
		labels := []string{"< prev", fmt.Sprintf("%d-%d of %d%s", st.view+1, min(st.view+perView, len(st.results)), len(st.results), more), "next >"}
		pn.row(ids, labels, "", false)
	}
	return pn.p
}

// smallRow is row with shorter buttons in the small font.
func (pn *pen) smallRow(ids, labels []string, selected string) {
	pn.y += 16
	gap := 14
	w := (pn.W - 2*pn.mx - gap*(len(ids)-1)) / len(ids)
	for i := range ids {
		x := pn.mx + i*(w+gap)
		bg, fg := pgBtn, pgText
		if ids[i] == selected {
			bg, fg = pgSel, pgDark
		}
		r := image.Rect(x, pn.y, x+w, pn.y+70)
		ui.RoundRect(pn.p.img, r, 16, bg)
		ui.DrawTextCentered(pn.p.img, pn.f.small, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, fg, labels[i])
		pn.p.buttons = append(pn.p.buttons, button{ids[i], r})
	}
	pn.y += 70
}

// storeBookPage shows one book: big cover, title, where to get it, summary.
func (c *console) storeBookPage() *page {
	st := &c.store
	it := st.sel
	h := c.s.H - c.barH
	pn := newPen(c.s.W, h, c.pf)
	pn.btn("s:back", "< back", image.Rect(pn.mx-12, 20, pn.mx+240, 110), pgBtn, pgText)

	cr := image.Rect(pn.mx, 140, pn.mx+440, 140+616)
	c.drawCover(pn.p.img, cr, it)
	x, w := cr.Max.X+44, c.s.W-pn.mx-(cr.Max.X+44)
	y := cr.Min.Y + 10
	for i, l := range wrapText(c.pf.bold, it.title, w) {
		if i == 5 {
			break
		}
		y += 50
		pn.text(c.pf.bold, pgText, x, y, l)
	}
	y += 56
	for i, l := range wrapText(c.pf.body, it.author, w) {
		if i == 2 {
			break
		}
		pn.text(c.pf.body, pgAccent, x, y, l)
		y += 46
	}
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
	y += 10
	pn.text(c.pf.small, pgMuted, x, y, clip(c.pf.small, strings.Join(meta, "  ·  "), w))
	badge, free := it.badge()
	bc := pgMuted
	if free {
		bc = pgAccent
	}
	y += 50
	pn.text(c.pf.small, bc, x, y, badge)

	// Buttons under the cover.
	pn.y = cr.Max.Y + 10
	state := st.dl[it.key]
	if it.full() {
		saved := state == "saved"
		if !saved {
			if _, err := os.Stat(filepath.Join(storeDir, it.fileName())); err == nil {
				saved = true
			}
		}
		dlLabel := "download"
		if saved {
			dlLabel = "open"
		}
		pn.row([]string{"s:preview", "s:dl"}, []string{"read preview", dlLabel}, "", false)
		note := "preview opens the full book now without adding it to your shelf"
		if state != "" && state != "saved" {
			note = state
		} else if saved {
			note = "saved to Books: it's on your shelf in the books app too"
		}
		for _, l := range wrapText(c.pf.small, note, c.s.W-2*pn.mx) {
			pn.y += 42
			pn.text(c.pf.small, pgMuted, pn.mx, pn.y, l)
		}
	} else {
		pn.y += 20
		pn.text(c.pf.body, pgText, pn.mx, pn.y+30, "not free to read: description below")
		pn.y += 40
	}

	pn.heading("WHERE TO GET IT")
	for _, o := range it.offers {
		what := "full book, free"
		if !o.full {
			what = o.note
			if o.price != "" {
				what = o.price + ": " + o.note
			}
		}
		for i, l := range wrapText(c.pf.small, sourceNames[o.src]+": "+what, c.s.W-2*pn.mx) {
			if i == 2 {
				break
			}
			pn.y += 40
			pn.text(c.pf.small, pgText, pn.mx, pn.y, l)
		}
	}

	pn.heading("ABOUT")
	about := it.summary
	if about == "" && len(it.subjects) > 0 {
		about = "Subjects: " + strings.Join(it.subjects, "; ")
	}
	if about == "" {
		about = "No description from these libraries."
		if it.full() {
			about += " Tap read preview to start reading."
		}
	}
	lines := wrapText(c.pf.small, about, c.s.W-2*pn.mx)
	maxLines := (h - 40 - pn.y) / 40
	for i, l := range lines {
		if i == maxLines {
			break
		}
		if i == maxLines-1 && len(lines) > maxLines {
			l = clip(c.pf.small, l+" ...", c.s.W-2*pn.mx)
		}
		pn.y += 40
		pn.text(c.pf.small, pgText, pn.mx, pn.y, l)
	}
	return pn.p
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
