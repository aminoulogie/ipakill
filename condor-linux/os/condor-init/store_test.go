package main

import (
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeLibraries serves all four sources, covers and EPUBs, shaped like the real APIs.
type fakeLibraries struct {
	srv     *httptest.Server
	queries []string // path?query of every API call
}

func newFakeLibraries(t *testing.T, gutendexHangs bool) *fakeLibraries {
	epubFile := filepath.Join(t.TempDir(), "book.epub")
	writeLongEPUB(t, epubFile)
	f := &fakeLibraries{}
	mux := http.NewServeMux()
	note := func(r *http.Request) {
		drawMu.Lock()
		f.queries = append(f.queries, r.URL.Path+"?"+r.URL.RawQuery)
		drawMu.Unlock()
	}
	mux.HandleFunc("/books/", func(w http.ResponseWriter, r *http.Request) {
		note(r)
		if gutendexHangs {
			<-r.Context().Done()
			return
		}
		fmt.Fprintf(w, `{"count": 2, "next": "%[1]s/books/?page=2", "previous": null, "results": [
		 {"id": 84, "title": "Frankenstein; Or, The Modern Prometheus",
		  "authors": [{"name": "Shelley, Mary Wollstonecraft", "birth_year": 1797, "death_year": 1851}],
		  "summaries": ["\"Frankenstein; Or, The Modern Prometheus\" by Mary Wollstonecraft Shelley is a novel written in the early 19th century. The story explores themes of ambition, the quest for knowledge, and the consequences of defying nature. (This is an automatically generated summary.)"],
		  "subjects": ["Horror tales", "Science fiction"], "languages": ["en"], "copyright": false,
		  "formats": {"application/epub+zip": "%[1]s/ebooks/84.epub3.images", "image/jpeg": "%[1]s/img/84.jpg"},
		  "download_count": 98765},
		 {"id": 1342, "title": "Pride and Prejudice", "authors": [{"name": "Austen, Jane"}],
		  "summaries": [], "subjects": ["Courtship -- Fiction"], "languages": ["en"],
		  "formats": {"application/epub+zip": "%[1]s/ebooks/1342.epub3.images"}, "download_count": 76543}
		]}`, f.srv.URL)
	})
	mux.HandleFunc("/ebooks/search.opds/", func(w http.ResponseWriter, r *http.Request) {
		note(r)
		io.WriteString(w, sampleOPDS)
	})
	mux.HandleFunc("/ebooks/", func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, epubFile) })
	mux.HandleFunc("/advancedsearch.php", func(w http.ResponseWriter, r *http.Request) {
		note(r)
		io.WriteString(w, `{"responseHeader": {"status": 0}, "response": {"numFound": 2, "start": 0, "docs": [
		 {"identifier": "frankensteinormo00shel", "title": "Frankenstein, or, The modern Prometheus",
		  "creator": "Shelley, Mary Wollstonecraft, 1797-1851", "downloads": 5000, "year": "1831", "language": ["eng"]},
		 {"identifier": "frankensteinsdaughter", "title": "Frankenstein's Daughter", "creator": ["Old Writer"],
		  "description": "<p>A forgotten <b>sequel</b>.</p>", "downloads": 12}
		]}}`)
	})
	mux.HandleFunc("/metadata/", func(w http.ResponseWriter, r *http.Request) {
		note(r)
		id := strings.TrimPrefix(r.URL.Path, "/metadata/")
		fmt.Fprintf(w, `{"files": [{"name": "%[1]s.pdf", "format": "Text PDF"}, {"name": "%[1]s.epub", "format": "EPUB"}]}`, id)
	})
	mux.HandleFunc("/download/", func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, epubFile) })
	mux.HandleFunc("/search.json", func(w http.ResponseWriter, r *http.Request) {
		note(r)
		io.WriteString(w, `{"numFound": 2, "docs": [
		 {"key": "/works/OL450063W", "title": "Frankenstein", "author_name": ["Mary Shelley"], "cover_i": 12356249,
		  "ebook_access": "public", "ia": ["frankensteinormo00shel"], "first_publish_year": 1818},
		 {"key": "/works/OL2W", "title": "Frankenstein in Baghdad", "author_name": ["Ahmed Saadawi"],
		  "ebook_access": "borrowable", "ia": ["x"], "first_publish_year": 2013}
		]}`)
	})
	mux.HandleFunc("/works/", func(w http.ResponseWriter, r *http.Request) {
		note(r)
		io.WriteString(w, `{"description": {"type": "/type/text", "value": "A novel set in US-occupied Baghdad."}}`)
	})
	mux.HandleFunc("/volumes", func(w http.ResponseWriter, r *http.Request) {
		note(r)
		fmt.Fprintf(w, `{"kind": "books#volumes", "items": [
		 {"id": "g1", "volumeInfo": {"title": "Frankenstein", "authors": ["Mary Shelley"], "publishedDate": "1818",
		   "description": "The classic.", "imageLinks": {"thumbnail": "%[1]s/img/g1.jpg"}},
		  "saleInfo": {"saleability": "FREE"},
		  "accessInfo": {"publicDomain": true, "epub": {"isAvailable": true, "downloadLink": "%[1]s/ebooks/g1.epub"}}},
		 {"id": "g2", "volumeInfo": {"title": "Frankenstein in Baghdad", "authors": ["Ahmed Saadawi"],
		   "publishedDate": "2018-01-23", "description": "<b>Winner</b> of the International Prize for Arabic Fiction."},
		  "saleInfo": {"saleability": "FOR_SALE", "listPrice": {"amount": 9.99, "currencyCode": "USD"}},
		  "accessInfo": {"publicDomain": false, "epub": {"isAvailable": true}}}
		]}`, f.srv.URL)
	})
	mux.HandleFunc("/img/", func(w http.ResponseWriter, r *http.Request) {
		img := image.NewRGBA(image.Rect(0, 0, 300, 450))
		for y := 0; y < 450; y++ {
			for x := 0; x < 300; x++ {
				img.Set(x, y, color.RGBA{uint8(150 + x/6), 40, uint8(y / 3), 255})
			}
		}
		jpeg.Encode(w, img, nil)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)

	dir := t.TempDir()
	type saved struct {
		p *string
		v string
	}
	var olds []saved
	set := func(p *string, v string) { olds = append(olds, saved{p, *p}); *p = v }
	set(&gutendexURL, f.srv.URL+"/books/")
	set(&gutenbergBase, f.srv.URL)
	set(&gutenbergOPDSURL, f.srv.URL+"/ebooks/search.opds/")
	set(&archiveBase, f.srv.URL)
	set(&openLibraryBase, f.srv.URL)
	set(&openLibraryCover, f.srv.URL)
	set(&googleBooksURL, f.srv.URL+"/volumes")
	set(&storeDir, filepath.Join(dir, "Books"))
	set(&previewDir, filepath.Join(dir, "previews"))
	set(&coverDir, filepath.Join(dir, "covers"))
	oldDirs, oldClient, oldTimeout := bookDirs, webClient, gutenbergTimeout
	bookDirs = []string{storeDir}
	webClient = func() *http.Client { return f.srv.Client() }
	gutenbergTimeout = 300 * time.Millisecond
	t.Cleanup(func() {
		// A check that failed while holding drawMu would leave the fake server's handlers
		// (which take drawMu) and so srv.Close stuck: report the failure instead of hanging.
		if t.Failed() && !drawMu.TryLock() {
			drawMu.Unlock()
		} else if t.Failed() {
			drawMu.Unlock()
		}
		for _, o := range olds {
			*o.p = o.v
		}
		bookDirs, webClient, gutenbergTimeout = oldDirs, oldClient, oldTimeout
		os.Remove(libraryPath)
		covers.mu.Lock()
		covers.loaded = nil
		covers.mu.Unlock()
	})
	return f
}

func (f *fakeLibraries) asked(prefix string) []string {
	drawMu.Lock()
	defer drawMu.Unlock()
	var out []string
	for _, q := range f.queries {
		if strings.HasPrefix(q, prefix) {
			out = append(out, q)
		}
	}
	return out
}

// waitFor polls cond under drawMu.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 300; i++ {
		drawMu.Lock()
		ok := cond()
		drawMu.Unlock()
		if ok {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func shot(t *testing.T, c *console, name string) {
	if out := os.Getenv("CONDOR_SHOTS"); out != "" { // set to a folder to look at the screens
		screenPNG(t, c.s, filepath.Join(out, name+".png"))
	}
}

func TestStoreBrowseSearchPreviewDownload(t *testing.T) {
	f := newFakeLibraries(t, false)
	c := testConsole(t)
	drawMu.Lock()
	c.showPage() // the launcher
	tapButton(t, c, "store")
	drawMu.Unlock()

	// Browsing: Gutenberg's popular books, with covers.
	waitFor(t, "popular books", func() bool { return len(c.store.results) == 2 && c.store.pending == 0 })
	if q := f.asked("/books/"); len(q) != 1 || !strings.Contains(q[0], "mime_type=application%2Fepub") {
		t.Errorf("gutendex asked %v", q)
	}
	waitFor(t, "a cover", func() bool { return covers.get(f.srv.URL+"/img/84.jpg", gridCellW, gridCover) != nil })
	drawMu.Lock()
	c.showPage()
	shot(t, c, "store-grid")

	// Search with the keyboard: all four libraries.
	tapButton(t, c, "s:search")
	if !c.store.typing || !c.skb.visible {
		t.Fatal("tapping the search box should bring up the keyboard")
	}
	c.storeKey([]byte("frankx"))
	shot(t, c, "store-typing")
	c.storeKey([]byte{0x7f})
	c.storeKey([]byte("\r"))
	drawMu.Unlock()
	waitFor(t, "all sources", func() bool { return c.store.pending == 0 })
	for _, p := range []string{"/books/", "/advancedsearch.php", "/search.json", "/volumes"} {
		if len(f.asked(p)) == 0 {
			t.Errorf("%s not asked", p)
		}
	}
	if q := f.asked("/volumes"); !strings.Contains(q[0], "q=frank") {
		t.Errorf("google query %v", q)
	}

	drawMu.Lock()
	res := c.store.results
	var titles []string
	for _, it := range res {
		b, _ := it.badge()
		titles = append(titles, it.title+" ["+b+"]")
	}
	t.Logf("results: %s", strings.Join(titles, " | "))
	// Frankenstein, found in all four, is one item with all its sources, on top.
	top := res[0]
	if !strings.HasPrefix(top.title, "Frankenstein; Or") || len(top.offers) != 4 || top.offers[0].src != srcGutenberg {
		t.Fatalf("top result %q with %d offers", top.title, len(top.offers))
	}
	// Full books before information-only ones.
	seenInfo := false
	for _, it := range res {
		if !it.full() {
			seenInfo = true
		} else if seenInfo {
			t.Errorf("full book %q listed after an info-only one", it.title)
		}
	}
	last := res[len(res)-1]
	if last.full() || !strings.Contains(last.title, "Baghdad") || len(last.offers) != 2 {
		t.Errorf("last %q full=%v offers=%d", last.title, last.full(), len(last.offers))
	}
	if b, _ := last.badge(); b != "9.99 USD · info" {
		t.Errorf("badge %q", b)
	}
	c.showPage()
	shot(t, c, "store-search")

	// The info-only book's page: price, notes, description (from Google, or Open Library).
	tapButton(t, c, fmt.Sprintf("s:item%d", len(res)-1))
	shot(t, c, "store-info-book")
	for _, b := range c.page.buttons {
		if b.id == "s:dl" || b.id == "s:preview" {
			t.Error("an info-only book should have no download")
		}
	}
	tapButton(t, c, "s:back")

	// Frankenstein: preview opens the reader, not the shelf.
	tapButton(t, c, "s:item0")
	shot(t, c, "store-book")
	tapButton(t, c, "s:preview")
	drawMu.Unlock()
	waitFor(t, "preview", func() bool { return c.mode == modeReader })
	drawMu.Lock()
	if !strings.HasPrefix(c.book.path, previewDir) || !c.fromStore {
		t.Errorf("preview opened %s (fromStore %v)", c.book.path, c.fromStore)
	}
	if len(findBooks()) != 0 {
		t.Error("a preview should not be on the shelf")
	}
	tapButton(t, c, "chrome")
	tapButton(t, c, "shelf") // "< Store"
	if c.mode != modeStore || c.store.sel != top {
		t.Fatalf("back from a preview should return to the book's page, mode %v", c.mode)
	}
	tapButton(t, c, "s:dl")
	drawMu.Unlock()
	waitFor(t, "download", func() bool { return c.store.dl[top.key] == "saved" })
	if b := findBooks(); len(b) != 1 || filepath.Base(b[0].path) != "Frankenstein - Mary Wollstonecraft Shelley.epub" {
		t.Fatalf("shelf after download: %+v", b)
	}
	if len(f.asked("/books/")) > 2 {
		t.Error("gutendex asked again for a download")
	}

	// A book only the Internet Archive has: its EPUB is found through the item's file list.
	drawMu.Lock()
	tapButton(t, c, "s:back")
	idx := -1
	for i, it := range c.store.results {
		if it.title == "Frankenstein's Daughter" {
			idx = i
		}
	}
	c.store.view = idx / perView * perView
	c.showPage()
	tapButton(t, c, fmt.Sprintf("s:item%d", idx))
	if c.store.sel.summary != "A forgotten sequel ." && c.store.sel.summary != "A forgotten sequel." {
		t.Errorf("summary %q", c.store.sel.summary)
	}
	tapButton(t, c, "s:dl")
	key := c.store.sel.key
	drawMu.Unlock()
	waitFor(t, "archive download", func() bool { return c.store.dl[key] == "saved" })
	if len(f.asked("/metadata/frankensteinsdaughter")) != 1 {
		t.Error("archive.org file list not asked")
	}
	drawMu.Lock()
	tapButton(t, c, "s:dl") // now "open"
	if c.mode != modeReader {
		t.Errorf("open after download: mode %v", c.mode)
	}
	// Home: the books being read, top picks, the library, the most read free books.
	c.setMode(modeBooksHome)
	drawMu.Unlock()
	waitFor(t, "popular on Home", func() bool { return len(c.homePop) > 0 })
	waitFor(t, "covers on Home", func() bool { return covers.get(f.srv.URL+"/img/84.jpg", 150, 225) != nil })
	drawMu.Lock()
	c.showPage()
	shot(t, c, "books-home")
	if len(c.readingNow()) != 1 {
		t.Errorf("continue: %v", c.readingNow())
	}
	tapButton(t, c, "home:pick:1")
	if c.mode != modeStore || storeTopics[c.store.topic].topic != "adventure" {
		t.Errorf("top pick: mode %v topic %d", c.mode, c.store.topic)
	}
	drawMu.Unlock()
	waitFor(t, "store settled", func() bool { return c.store.pending == 0 })
}

func TestMergeAndKeys(t *testing.T) {
	if a, b := normKey("Frankenstein; Or, The Modern Prometheus", "Shelley, Mary Wollstonecraft"),
		normKey("Frankenstein", "Mary W. Shelley"); a != b {
		t.Errorf("%q != %q", a, b)
	}
	if a, b := normKey("Frankenstein", "Mary Shelley"), normKey("Frankenstein in Baghdad", "Ahmed Saadawi"); a == b {
		t.Error("different books merged")
	}
	if got := personName("Twain, Mark (Samuel Clemens)"); got != "Mark Twain" {
		t.Errorf("personName %q", got)
	}
}

func TestStoreOfflineSaysSo(t *testing.T) {
	newFakeLibraries(t, false)
	for _, p := range []*string{&gutendexURL, &gutenbergOPDSURL} {
		*p = "http://127.0.0.1:1/x/"
	}
	c := testConsole(t)
	drawMu.Lock()
	c.setMode(modeStore)
	drawMu.Unlock()
	waitFor(t, "error", func() bool { return c.store.started && c.store.pending == 0 })
	drawMu.Lock()
	c.showPage()
	shot(t, c, "store-offline")
	if len(c.store.errs) == 0 {
		t.Error("no error shown")
	}
	tapButton(t, c, "s:retry")
	if c.store.pending == 0 {
		t.Error("retry should search again")
	}
	drawMu.Unlock()
	waitFor(t, "retry", func() bool { return c.store.pending == 0 })
}

const sampleOPDS = `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom" xmlns:opds="http://opds-spec.org/" xmlns:os="http://a9.com/-/spec/opensearch/1.1/">
<id>http://www.gutenberg.org/ebooks/search.opds/?query=frankenstein</id>
<title>Project Gutenberg: Books: frankenstein</title>
<link rel="next" title="Next Page" type="application/atom+xml;profile=opds-catalog" href="/ebooks/search.opds/?query=frankenstein&amp;start_index=26"/>
<os:itemsPerPage>25</os:itemsPerPage>
<os:startIndex>1</os:startIndex>
<entry>
<title>Sort Alphabetically by Title</title>
<content type="text"></content>
<id>http://www.gutenberg.org/ebooks/search.opds/?sort_order=title&amp;query=frankenstein</id>
<link type="application/atom+xml;profile=opds-catalog" rel="subsection" href="/ebooks/search.opds/?sort_order=title&amp;query=frankenstein"/>
</entry>
<entry>
<title>Frankenstein; Or, The Modern Prometheus</title>
<content type="text">Mary Wollstonecraft Shelley</content>
<id>http://www.gutenberg.org/ebooks/84.opds</id>
<link type="application/atom+xml;profile=opds-catalog" rel="subsection" href="/ebooks/84.opds"/>
<link type="image/jpeg" rel="http://opds-spec.org/image/thumbnail" href="/cache/epub/84/pg84.cover.small.jpg"/>
</entry>
<entry>
<title>Frankenstein: or, the Modern Prometheus. Volume 1 (of 3)</title>
<content type="text">1,234 downloads</content>
<id>urn:x</id>
<author><name>Mary Wollstonecraft Shelley</name></author>
<link type="application/atom+xml;profile=opds-catalog" rel="subsection" href="/ebooks/41445.opds"/>
</entry>
</feed>`

func TestParseOPDS(t *testing.T) {
	res, err := parseOPDS(strings.NewReader(sampleOPDS))
	if err != nil {
		t.Fatal(err)
	}
	if !res.hasNext || len(res.items) != 2 {
		t.Fatalf("got %+v", res)
	}
	b := res.items[0]
	if b.author != "Mary Wollstonecraft Shelley" || !strings.HasPrefix(b.title, "Frankenstein;") {
		t.Errorf("first book %+v", b)
	}
	if u := b.offers[0].urls; u[0] != "https://www.gutenberg.org/ebooks/84.epub.noimages" ||
		b.cover != "https://www.gutenberg.org/cache/epub/84/pg84.cover.medium.jpg" {
		t.Errorf("urls %v cover %s", u, b.cover)
	}
	if b2 := res.items[1]; b2.downloads != 1234 || b2.author != "Mary Wollstonecraft Shelley" ||
		!strings.Contains(b2.offers[0].urls[0], "/41445.") {
		t.Errorf("second book %+v", b2)
	}
	if got := opdsTerms(storeSearch{query: "jules verne", topic: "science fiction", lang: "fr"}); got != "jules verne s.science s.fiction l.fr" {
		t.Errorf("terms %q", got)
	}
}

// When gutendex.com doesn't answer, the store asks gutenberg.org and keeps asking it first.
func TestStoreFallsBackToGutenberg(t *testing.T) {
	f := newFakeLibraries(t, true)
	c := testConsole(t)
	drawMu.Lock()
	c.setMode(modeStore)
	drawMu.Unlock()
	waitFor(t, "fallback results", func() bool { return c.store.started && c.store.pending == 0 })
	drawMu.Lock()
	if len(c.store.results) != 2 || len(c.store.errs) != 0 || !c.store.preferOPDS {
		t.Fatalf("results %d, errs %v, preferOPDS %v", len(c.store.results), c.store.errs, c.store.preferOPDS)
	}
	c.showPage()
	drawMu.Unlock()
	if q := f.asked("/ebooks/search.opds/"); len(q) != 1 || !strings.HasSuffix(q[0], "?sort_order=downloads") {
		t.Errorf("opds asked %v", q)
	}
}
