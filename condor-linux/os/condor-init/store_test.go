package main

import (
	"fmt"
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
	oldURL, oldStore, oldPrev, oldDirs, oldClient := gutendexURL, storeDir, previewDir, bookDirs, webClient
	gutendexURL, storeDir, previewDir, bookDirs = srv.URL+"/books/", filepath.Join(dir, "Books"), filepath.Join(dir, "previews"), []string{filepath.Join(dir, "Books")}
	webClient = func() *http.Client { return srv.Client() }
	defer func() {
		gutendexURL, storeDir, previewDir, bookDirs, webClient = oldURL, oldStore, oldPrev, oldDirs, oldClient
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
	old, oldClient := gutendexURL, webClient
	gutendexURL = "http://127.0.0.1:1/books/"
	webClient = func() *http.Client { return &http.Client{Timeout: 2 * time.Second} }
	defer func() { gutendexURL, webClient = old, oldClient }()
	c := testConsole(t)
	drawMu.Lock()
	c.setMode(modeStore)
	drawMu.Unlock()
	waitFor(t, "error", func() bool { return c.store.loaded })
	if !strings.Contains(c.store.status, "no connection") {
		t.Errorf("status = %q", c.store.status)
	}
}
