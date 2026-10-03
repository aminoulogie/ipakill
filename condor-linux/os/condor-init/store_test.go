package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeGutendex serves a Gutendex-shaped search and one EPUB.
func fakeGutendex(t *testing.T) (*httptest.Server, *[]string) {
	epubFile := filepath.Join(t.TempDir(), "book.epub")
	writeLongEPUB(t, epubFile)
	var queries []string
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/books/", func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.RawQuery)
		fmt.Fprintf(w, `{"count": 2, "next": "%[1]s/books/?page=2", "previous": null, "results": [
		 {"id": 84, "title": "Frankenstein; Or, The Modern Prometheus",
		  "authors": [{"name": "Shelley, Mary Wollstonecraft", "birth_year": 1797, "death_year": 1851}],
		  "summaries": ["\"Frankenstein; Or, The Modern Prometheus\" by Mary Wollstonecraft Shelley is a novel written in the early 19th century. The story explores themes of ambition, the quest for knowledge, and the consequences of defying nature. (This is an automatically generated summary.)"],
		  "subjects": ["Horror tales", "Science fiction"], "languages": ["en"], "copyright": false,
		  "formats": {"application/epub+zip": "%[1]s/ebooks/84.epub3.images", "text/html": "x"},
		  "download_count": 98765},
		 {"id": 1342, "title": "Pride and Prejudice", "authors": [{"name": "Austen, Jane"}],
		  "summaries": [], "subjects": ["Courtship -- Fiction"], "languages": ["en"],
		  "formats": {"application/epub+zip": "%[1]s/ebooks/1342.epub3.images"}, "download_count": 76543}
		]}`, srv.URL)
	})
	mux.HandleFunc("/ebooks/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, epubFile)
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &queries
}

// waitFor polls cond under drawMu.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
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

func TestStoreSearchPreviewDownload(t *testing.T) {
	srv, queries := fakeGutendex(t)
	dir := t.TempDir()
	oldURL, oldStore, oldPrev, oldDirs, oldClient, oldBase := gutendexURL, storeDir, previewDir, bookDirs, webClient, gutenbergBase
	gutenbergBase = srv.URL + "/missing" // the no-images edition 404s: the catalogue's link is next
	gutendexURL, storeDir, previewDir, bookDirs = srv.URL+"/books/", filepath.Join(dir, "Books"), filepath.Join(dir, "previews"), []string{filepath.Join(dir, "Books")}
	webClient = func() *http.Client { return srv.Client() }
	defer func() {
		gutendexURL, storeDir, previewDir, bookDirs, webClient, gutenbergBase = oldURL, oldStore, oldPrev, oldDirs, oldClient, oldBase
		os.Remove(libraryPath)
	}()
	out := os.Getenv("CONDOR_SHOTS") // set to a folder to look at the screens

	c := testConsole(t)
	drawMu.Lock()
	c.showPage() // the launcher
	tapButton(t, c, "store")
	drawMu.Unlock()
	waitFor(t, "results", func() bool { return len(c.store.results) == 2 })
	drawMu.Lock()
	c.showPage()
	if out != "" {
		screenPNG(t, c.s, filepath.Join(out, "store-list.png"))
	}
	if !strings.Contains((*queries)[0], "mime_type=application%2Fepub") {
		t.Errorf("query %q should ask for EPUBs only", (*queries)[0])
	}

	// Type a search with the keyboard and press enter.
	tapButton(t, c, "s:search")
	if !c.store.typing || !c.skb.visible {
		t.Fatal("tapping the search box should bring up the keyboard")
	}
	c.storeKey([]byte("frank"))
	c.storeKey([]byte{0x7f})
	c.storeKey([]byte("k"))
	if out != "" {
		screenPNG(t, c.s, filepath.Join(out, "store-typing.png"))
	}
	c.storeKey([]byte("\r"))
	drawMu.Unlock()
	waitFor(t, "search", func() bool { return !c.store.loading && len(*queries) == 2 })
	if q := (*queries)[1]; !strings.Contains(q, "search=frank") {
		t.Errorf("search query = %q, want search=frank", q)
	}

	// Topic and language buttons change the query.
	drawMu.Lock()
	tapButton(t, c, "s:topic2")
	drawMu.Unlock()
	waitFor(t, "topic", func() bool { return !c.store.loading && len(*queries) == 3 })
	if q := (*queries)[2]; !strings.Contains(q, "topic=science+fiction") {
		t.Errorf("topic query = %q", q)
	}

	// A book's page, then a preview: opens in the reader, not on the shelf.
	drawMu.Lock()
	tapButton(t, c, "s:res0")
	if c.store.sel == nil || c.store.sel.author() != "Mary Wollstonecraft Shelley" {
		t.Fatalf("selected %+v", c.store.sel)
	}
	if out != "" {
		screenPNG(t, c.s, filepath.Join(out, "store-book.png"))
	}
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
	tapButton(t, c, "shelf") // "< store"
	if c.mode != modeStore || c.store.sel == nil {
		t.Fatalf("back from a preview should return to the book's page, mode %v", c.mode)
	}

	// Download: saved to Books, then on the shelf.
	tapButton(t, c, "s:dl")
	drawMu.Unlock()
	waitFor(t, "download", func() bool { return c.store.dl[84] == "saved" })
	books := findBooks()
	if len(books) != 1 || !strings.HasSuffix(books[0].path, "Frankenstein - Mary Wollstonecraft Shelley.epub") {
		t.Fatalf("shelf after download: %+v", books)
	}
	drawMu.Lock()
	c.showPage()
	if out != "" {
		screenPNG(t, c.s, filepath.Join(out, "store-saved.png"))
	}
	tapButton(t, c, "s:dl") // now "open"
	if c.mode != modeReader || c.book.path != books[0].path {
		t.Errorf("open after download: mode %v", c.mode)
	}
	drawMu.Unlock()
}

func TestStoreOfflineSaysSo(t *testing.T) {
	old, oldO, oldClient := gutendexURL, gutenbergOPDSURL, webClient
	gutendexURL, gutenbergOPDSURL = "http://127.0.0.1:1/books/", "http://127.0.0.1:1/ebooks/search.opds/"
	webClient = func() *http.Client { return &http.Client{Timeout: 2 * time.Second} }
	defer func() { gutendexURL, gutenbergOPDSURL, webClient = old, oldO, oldClient }()
	c := testConsole(t)
	drawMu.Lock()
	c.setMode(modeStore)
	drawMu.Unlock()
	waitFor(t, "error", func() bool { return c.store.loaded })
	drawMu.Lock()
	c.showPage()
	if !strings.Contains(c.store.status, "can't reach the library") {
		t.Errorf("status = %q", c.store.status)
	}
	tapButton(t, c, "s:retry")
	if !c.store.loading {
		t.Error("retry should search again")
	}
	drawMu.Unlock()
	waitFor(t, "retry", func() bool { return !c.store.loading })
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
	if !res.hasNext || len(res.books) != 2 {
		t.Fatalf("got %+v", res)
	}
	b := res.books[0]
	if b.ID != 84 || b.author() != "Mary Wollstonecraft Shelley" || !strings.HasPrefix(b.Title, "Frankenstein;") {
		t.Errorf("first book %+v", b)
	}
	if u := b.epubURLs(); u[0] != "https://www.gutenberg.org/ebooks/84.epub.noimages" || u[len(u)-1] != "https://www.gutenberg.org/ebooks/84.epub3.images" {
		t.Errorf("epub urls %v", u)
	}
	if b2 := res.books[1]; b2.ID != 41445 || b2.Downloads != 1234 || b2.author() != "Mary Wollstonecraft Shelley" {
		t.Errorf("second book %+v", b2)
	}
	if got := opdsTerms(storeSearch{query: "jules verne", topic: "science fiction", lang: "fr"}); got != "jules verne s.science s.fiction l.fr" {
		t.Errorf("terms %q", got)
	}
}

// When gutendex.com doesn't answer, the store asks gutenberg.org and keeps asking it first.
func TestStoreFallsBackToGutenberg(t *testing.T) {
	hang := make(chan struct{})
	defer close(hang)
	var opdsQueries []string
	mux := http.NewServeMux()
	mux.HandleFunc("/books/", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-hang:
		case <-r.Context().Done():
		}
	})
	mux.HandleFunc("/ebooks/search.opds/", func(w http.ResponseWriter, r *http.Request) {
		opdsQueries = append(opdsQueries, r.URL.RawQuery)
		io.WriteString(w, sampleOPDS)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	oldG, oldO, oldT, oldC := gutendexURL, gutenbergOPDSURL, storeTimeout, webClient
	gutendexURL, gutenbergOPDSURL, storeTimeout = srv.URL+"/books/", srv.URL+"/ebooks/search.opds/", 300*time.Millisecond
	webClient = func() *http.Client { return srv.Client() }
	defer func() { gutendexURL, gutenbergOPDSURL, storeTimeout, webClient = oldG, oldO, oldT, oldC }()

	c := testConsole(t)
	drawMu.Lock()
	c.setMode(modeStore)
	drawMu.Unlock()
	waitFor(t, "fallback results", func() bool { return c.store.loaded })
	drawMu.Lock()
	defer drawMu.Unlock()
	if len(c.store.results) != 2 || c.store.status != "" || !c.store.preferSecond {
		t.Fatalf("results %d, status %q, preferSecond %v", len(c.store.results), c.store.status, c.store.preferSecond)
	}
	if opdsQueries[0] != "sort_order=downloads" {
		t.Errorf("popular query %q", opdsQueries[0])
	}
	c.showPage() // draws without a summary or download count
}
