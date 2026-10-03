package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"image"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/image/font"

	"condor-init/epub"
	"condor-init/ui"
)

// The Store app: free EPUBs from Project Gutenberg (75,000+ public-domain books), searched
// through Gutendex (github.com/garethbjohnson/gutendex), the open-source JSON API for
// Gutenberg's catalogue. A book's page shows its summary; "preview" opens it in the reader
// without putting it on the shelf, "download" saves it to Books.
//
// Everything goes through condor-init's own proxy (127.0.0.1:3128), so it works over Wi-Fi
// and over USB with `condor net` alike. Certificates come from Alpine's bundle: Android
// 4.2's store is from 2013 and misses today's roots.

var (
	gutendexURL   = "https://gutendex.com/books/"
	gutenbergBase = "https://www.gutenberg.org"
	storeDir      = "/data/media/0/Books" // the first of bookDirs: internal storage
	previewDir    = condorHome + "/previews"
)

type gbook struct {
	ID      int    `json:"id"`
	Title   string `json:"title"`
	Authors []struct {
		Name string `json:"name"`
	} `json:"authors"`
	Summaries []string          `json:"summaries"`
	Subjects  []string          `json:"subjects"`
	Languages []string          `json:"languages"`
	Formats   map[string]string `json:"formats"`
	Downloads int               `json:"download_count"`
}

// author is "First Last" (Gutenberg stores "Last, First").
func (b *gbook) author() string {
	var names []string
	for _, a := range b.Authors {
		n := a.Name
		if last, first, ok := strings.Cut(n, ", "); ok {
			n = first + " " + last
		}
		names = append(names, n)
	}
	if len(names) == 0 {
		return "unknown author"
	}
	return strings.Join(names, ", ")
}

// epubURLs are where to get the book, best first. The reader shows text only, so the
// no-images edition (often a tenth of the size) comes first; the catalogue's own EPUB link
// and the EPUB 3 edition are fallbacks.
func (b *gbook) epubURLs() []string {
	urls := []string{fmt.Sprintf("%s/ebooks/%d.epub.noimages", gutenbergBase, b.ID)}
	for mt, u := range b.Formats {
		if strings.HasPrefix(mt, "application/epub") {
			urls = append(urls, u)
		}
	}
	return append(urls, fmt.Sprintf("%s/ebooks/%d.epub3.images", gutenbergBase, b.ID))
}

var unsafeName = regexp.MustCompile(`[^\pL\pN .,'()-]+`)

// fileName is "Title - Author.epub", safe on every filesystem.
func (b *gbook) fileName() string {
	title, _, _ := strings.Cut(b.Title, ";") // "Frankenstein; Or, The Modern Prometheus"
	n := strings.TrimSpace(unsafeName.ReplaceAllString(title+" - "+b.author(), " "))
	if r := []rune(n); len(r) > 100 {
		n = string(r[:100])
	}
	return n + ".epub"
}

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
	page           int    // 1-based
	results        []gbook
	count          int
	hasNext        bool
	loaded         bool
	loading        bool
	status         string // error or progress line
	gen            int    // drops answers to searches that were replaced
	preferSecond   bool   // gutendex failed: ask gutenberg.org first
	sel            *gbook // the book whose page is open, or nil for the list
	typing         bool   // keyboard up
	dl             map[int]string
}

// webClient talks to the internet through condor-init's proxy. Tests replace it.
var webClient = sync.OnceValue(func() *http.Client {
	return &http.Client{Timeout: 10 * time.Minute, Transport: &http.Transport{
		Proxy:                 http.ProxyURL(&url.URL{Scheme: "http", Host: proxyAddr}),
		TLSClientConfig:       &tls.Config{RootCAs: alpineRoots()},
		TLSHandshakeTimeout:   20 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
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

// storeSearch is what to list: a search, a topic, a language and a page.
type storeSearch struct {
	query, topic, lang string
	page               int
}

type storeResult struct {
	books   []gbook
	count   int // 0 when the source doesn't say
	hasNext bool
}

// A storeSource is a catalogue to search. gutendex.com is first (it has summaries), but it
// is a free hosted instance and sometimes doesn't answer; gutenberg.org's own OPDS feed is
// the fallback, and becomes the first choice once gutendex has failed.
type storeSource struct {
	host   string
	search func(context.Context, storeSearch) (storeResult, error)
}

var storeSources = []storeSource{
	{"gutendex.com", searchGutendex},
	{"gutenberg.org", searchGutenbergOPDS},
}

// storeTimeout is how long a search may take before the next source is tried.
var storeTimeout = 25 * time.Second

func searchGutendex(ctx context.Context, q storeSearch) (storeResult, error) {
	v := url.Values{}
	v.Set("mime_type", "application/epub")
	if q.query != "" {
		v.Set("search", q.query)
	}
	if q.topic != "" {
		v.Set("topic", q.topic)
	}
	if q.lang != "" {
		v.Set("languages", q.lang)
	}
	if q.page > 1 {
		v.Set("page", fmt.Sprint(q.page))
	}
	resp, err := webGetCtx(ctx, gutendexURL+"?"+v.Encode())
	if err != nil {
		return storeResult{}, err
	}
	defer resp.Body.Close()
	var res struct {
		Count   int     `json:"count"`
		Next    string  `json:"next"`
		Results []gbook `json:"results"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&res); err != nil {
		return storeResult{}, err
	}
	return storeResult{res.Results, res.Count, res.Next != ""}, nil
}

// storeLoad fetches the current results in the background. Caller holds drawMu.
func (c *console) storeLoad() {
	st := &c.store
	st.gen++
	gen := st.gen
	q := storeSearch{st.query, storeTopics[st.topic].topic, storeLangs[st.lang], max(st.page, 1)}
	srcs := storeSources
	if st.preferSecond {
		srcs = []storeSource{storeSources[1], storeSources[0]}
	}
	st.loading, st.status = true, "loading from "+srcs[0].host+"..."
	go func() {
		var res storeResult
		var err error
		var errs []string
		for i, src := range srcs {
			if i > 0 {
				drawMu.Lock()
				if gen == st.gen {
					st.status = srcs[i-1].host + " didn't answer, trying " + src.host + "..."
					if c.mode == modeStore {
						c.showPage()
					}
				}
				drawMu.Unlock()
			}
			start := time.Now()
			ctx, cancel := context.WithTimeout(context.Background(), storeTimeout)
			res, err = src.search(ctx, q)
			cancel()
			log.Printf("store: %s %+v: %d books in %v, err %v", src.host, q, len(res.books), time.Since(start).Round(time.Millisecond), err)
			if err == nil {
				drawMu.Lock()
				st.preferSecond = src.host == storeSources[1].host
				drawMu.Unlock()
				break
			}
			errs = append(errs, src.host+": "+shortErr(err))
		}
		drawMu.Lock()
		defer drawMu.Unlock()
		if gen != st.gen {
			return
		}
		st.loading, st.loaded = false, true
		if err != nil {
			st.status = "can't reach the library. " + strings.Join(errs, "; ")
		} else {
			st.status = ""
			st.results, st.count, st.hasNext = res.books, res.count, res.hasNext
		}
		if c.mode == modeStore {
			c.showPage()
		}
	}()
}

func shortErr(err error) string {
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i >= 0 && len(s)-i < 60 {
		s = s[i+2:]
	}
	return s
}

// fetchBook downloads b's EPUB to dst (via a temporary file), checking that it opens, trying
// each of its URLs in turn. progress gets the bytes so far.
func fetchBook(b *gbook, dst string, progress func(int64)) error {
	var errs []string
	for _, u := range b.epubURLs() {
		err := fetchEPUB(u, dst, progress)
		if err == nil {
			return nil
		}
		log.Printf("store: %s: %v", u, err)
		errs = append(errs, shortErr(err))
	}
	return fmt.Errorf("%s", strings.Join(errs, "; "))
}

func fetchEPUB(u, dst string, progress func(int64)) error {
	resp, err := webGet(u)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	var n int64
	buf := make([]byte, 64<<10)
	for err == nil {
		var k int
		k, err = resp.Body.Read(buf)
		if k > 0 {
			if _, werr := f.Write(buf[:k]); werr != nil {
				err = werr
				break
			}
			n += int64(k)
			if n > 200<<20 {
				err = fmt.Errorf("book too big")
				break
			}
			progress(n)
		}
	}
	f.Close()
	if err != io.EOF {
		os.Remove(tmp)
		return err
	}
	if eb, err := epub.Open(tmp); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("not a readable EPUB: %v", err)
	} else {
		eb.Close()
	}
	return os.Rename(tmp, dst)
}

// storeFetch downloads the open book, to Books (save) or as a preview, in the background.
// Caller holds drawMu.
func (c *console) storeFetch(save bool) {
	st := &c.store
	b := st.sel
	if b == nil || strings.HasPrefix(st.dl[b.ID], "downloading") {
		return
	}
	dst := filepath.Join(previewDir, fmt.Sprintf("%d.epub", b.ID))
	if save {
		dst = filepath.Join(storeDir, b.fileName())
	}
	if _, err := os.Stat(dst); err == nil { // already here: read it
		c.openFromStore(dst)
		return
	}
	st.dl[b.ID] = "downloading..."
	c.showPage()
	go func() {
		last := time.Now()
		err := fetchBook(b, dst, func(n int64) {
			if time.Since(last) < time.Second {
				return
			}
			last = time.Now()
			drawMu.Lock()
			st.dl[b.ID] = fmt.Sprintf("downloading... %.1f MB", float64(n)/(1<<20))
			if c.mode == modeStore && st.sel == b {
				c.showPage()
			}
			drawMu.Unlock()
		})
		drawMu.Lock()
		defer drawMu.Unlock()
		switch {
		case err != nil:
			log.Printf("store: book %d: %v", b.ID, err)
			st.dl[b.ID] = "failed: " + shortErr(err)
		case save:
			log.Printf("store: saved %s", dst)
			st.dl[b.ID] = "saved"
		default:
			st.dl[b.ID] = ""
			trimPreviews(5)
		}
		if c.mode != modeStore || st.sel != b {
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
	st.typing, st.query, st.page, st.results = false, strings.TrimSpace(st.editing), 1, nil
	c.storeLoad()
	c.showPage()
}

// Layout of the list page (page coordinates).
var (
	storeSearchR = image.Rect(48, 270, 1200-48-200, 370)
	storeGoR     = image.Rect(1200-48-180, 270, 1200-48, 370)
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
		text, col = "search title or author", pgMuted
	}
	// Show the end of a long search, where the typing is.
	for text != "" && ui.TextWidth(c.pf.body, text) > r.Dx()-60 {
		text = string([]rune(text)[1:])
	}
	ui.DrawText(img, c.pf.body, r.Min.X+30, r.Min.Y+62, col, text)
}

// storePage is the list (search, topics, results) or one book's page.
func (c *console) storePage() *page {
	st := &c.store
	if st.dl == nil {
		st.dl = map[int]string{}
	}
	st.page = max(st.page, 1)
	if !st.loaded && !st.loading {
		c.storeLoad()
	}
	if st.sel != nil {
		return c.storeBookPage()
	}
	h := c.s.H - c.barH
	pn := newPen(c.s.W, h, c.pf)
	pn.btn("home", "< home", image.Rect(pn.mx-12, 24, pn.mx+260, 124), pgBtn, pgText)
	lang := storeLangs[st.lang]
	if lang == "" {
		lang = "all"
	}
	pn.btn("s:lang", "lang: "+lang, image.Rect(c.s.W-pn.mx-300, 24, c.s.W-pn.mx, 124), pgBtn, pgText)
	pn.text(c.pf.title, pgText, pn.mx, 220, "store")
	pn.text(c.pf.small, pgMuted, pn.mx+ui.TextWidth(c.pf.title, "store ")+10, 220, "free books · Project Gutenberg")

	c.searchBox(pn.p.img)
	pn.p.buttons = append(pn.p.buttons, button{"s:search", storeSearchR})
	goLabel := "search"
	if st.query != "" && !st.typing {
		goLabel = "clear"
	}
	pn.btn("s:go", goLabel, storeGoR, pgSel, pgDark)

	// Topics: two rows of five.
	pn.y = storeGoR.Max.Y - 4
	for row := 0; row < 2; row++ {
		var ids, labels []string
		for i := row * 5; i < row*5+5; i++ {
			ids = append(ids, fmt.Sprintf("s:topic%d", i))
			labels = append(labels, storeTopics[i].label)
		}
		pn.smallRow(ids, labels, fmt.Sprintf("s:topic%d", st.topic))
	}

	bottom := h - 130
	if st.typing {
		bottom = c.skb.y0 - c.barH - 10
	}
	pn.y += 30
	head := storeTopics[st.topic].label
	if st.query != "" {
		head = "\"" + st.query + "\""
	}
	if st.loaded && st.status == "" && st.count > 0 {
		head += fmt.Sprintf("   ·   %d books", st.count)
	}
	pn.text(c.pf.small, pgAccent, pn.mx, pn.y+30, head)
	pn.y += 50
	if st.status != "" {
		for _, l := range wrapText(c.pf.small, st.status, c.s.W-2*pn.mx) {
			pn.y += 42
			pn.text(c.pf.small, pgMuted, pn.mx, pn.y, l)
		}
		if !st.loading {
			pn.row([]string{"s:retry"}, []string{"retry"}, "", false)
		}
		pn.y += 20
	}
	for i := range st.results {
		b := &st.results[i]
		if pn.y+130 > bottom {
			break
		}
		r := image.Rect(pn.mx, pn.y, c.s.W-pn.mx, pn.y+120)
		ui.RoundRect(pn.p.img, r, 20, pgCard)
		pn.text(c.pf.bold, pgText, r.Min.X+32, r.Min.Y+52, clip(c.pf.bold, b.Title, r.Dx()-64))
		sub := b.author()
		if b.Downloads > 0 {
			sub += fmt.Sprintf("   · %d downloads", b.Downloads)
		}
		pn.text(c.pf.small, pgMuted, r.Min.X+32, r.Min.Y+98, clip(c.pf.small, sub, r.Dx()-64))
		pn.p.buttons = append(pn.p.buttons, button{fmt.Sprintf("s:res%d", i), r})
		pn.y += 132
	}
	if !st.typing && (st.page > 1 || st.hasNext) {
		pn.y = h - 120
		ids, labels := []string{}, []string{}
		if st.page > 1 {
			ids, labels = append(ids, "s:prev"), append(labels, "< prev")
		}
		ids, labels = append(ids, "s:pageno"), append(labels, fmt.Sprintf("page %d", st.page))
		if st.hasNext {
			ids, labels = append(ids, "s:next"), append(labels, "next >")
		}
		pn.y -= 24
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
		r := image.Rect(x, pn.y, x+w, pn.y+76)
		ui.RoundRect(pn.p.img, r, 16, bg)
		ui.DrawTextCentered(pn.p.img, pn.f.small, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, fg, labels[i])
		pn.p.buttons = append(pn.p.buttons, button{ids[i], r})
	}
	pn.y += 76
}

// storeBookPage shows one book: title, author, summary, preview and download.
func (c *console) storeBookPage() *page {
	st := &c.store
	b := st.sel
	h := c.s.H - c.barH
	pn := newPen(c.s.W, h, c.pf)
	pn.btn("s:back", "< back", image.Rect(pn.mx-12, 24, pn.mx+260, 124), pgBtn, pgText)
	w := c.s.W - 2*pn.mx
	pn.y = 200
	for i, l := range wrapText(c.pf.title, b.Title, w) {
		if i == 3 {
			break
		}
		pn.text(c.pf.title, pgText, pn.mx, pn.y, l)
		pn.y += 76
	}
	pn.text(c.pf.body, pgAccent, pn.mx, pn.y, clip(c.pf.body, b.author(), w))
	pn.y += 50
	meta := fmt.Sprintf("Gutenberg #%d", b.ID)
	if len(b.Languages) > 0 {
		meta += "   ·   " + strings.Join(b.Languages, ", ")
	}
	if b.Downloads > 0 {
		meta += fmt.Sprintf("   ·   %d downloads", b.Downloads)
	}
	pn.text(c.pf.small, pgMuted, pn.mx, pn.y, meta)

	state := st.dl[b.ID]
	saved := state == "saved"
	if !saved {
		if _, err := os.Stat(filepath.Join(storeDir, b.fileName())); err == nil {
			saved = true
		}
	}
	dlLabel := "download"
	if saved {
		dlLabel = "open"
	}
	pn.y += 10
	pn.row([]string{"s:preview", "s:dl"}, []string{"read preview", dlLabel}, "", false)
	note := "preview opens the book now without adding it to your shelf"
	if state != "" && state != "saved" {
		note = state
	} else if saved {
		note = "saved to Books: it's on your shelf in the books app too"
	}
	pn.line(c.pf.small, pgMuted, note)

	pn.heading("ABOUT")
	about := strings.Join(b.Summaries, " ")
	if about == "" && len(b.Subjects) > 0 {
		about = "Subjects: " + strings.Join(b.Subjects, "; ")
	}
	if about == "" {
		about = "No summary from this catalogue: tap read preview to start reading."
	}
	about = strings.TrimSuffix(strings.TrimSpace(about), "(This is an automatically generated summary.)")
	lines := wrapText(c.pf.small, about, w)
	maxLines := (h - 60 - pn.y) / 42
	for i, l := range lines {
		if i == maxLines-1 && len(lines) > maxLines {
			l = clip(c.pf.small, l+" ...", w)
		}
		if i == maxLines {
			break
		}
		pn.y += 42
		pn.text(c.pf.small, pgText, pn.mx, pn.y, l)
	}
	return pn.p
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
	if id != "s:search" && st.typing && !strings.HasPrefix(id, "s:go") {
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
		if st.query != "" { // "clear"
			st.query, st.editing, st.page, st.results = "", "", 1, nil
			c.storeLoad()
		} else {
			st.typing, st.editing = true, ""
			c.skb.visible = true
		}
	case id == "s:retry":
		c.storeLoad()
	case id == "s:lang":
		st.lang = (st.lang + 1) % len(storeLangs)
		st.page, st.results = 1, nil
		c.storeLoad()
	case strings.HasPrefix(id, "s:topic"):
		fmt.Sscanf(id, "s:topic%d", &st.topic)
		st.page, st.results = 1, nil
		c.storeLoad()
	case id == "s:prev" || id == "s:next":
		if id == "s:next" {
			st.page++
		} else {
			st.page = max(st.page-1, 1)
		}
		st.results = nil
		c.storeLoad()
	case strings.HasPrefix(id, "s:res"):
		var i int
		if _, err := fmt.Sscanf(id, "s:res%d", &i); err == nil && i < len(st.results) {
			b := st.results[i]
			st.sel = &b
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
