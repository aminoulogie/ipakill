package main

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A phone sends two books and a picture: the books land in the Library under safe names
// (a second copy gets " (2)"), the picture and a broken "EPUB" are refused, and the page lists
// what's on the tablet.
func TestSendToBooks(t *testing.T) {
	dir := t.TempDir()
	oldStore, oldDirs := storeDir, bookDirs
	storeDir, bookDirs = filepath.Join(dir, "Books"), []string{filepath.Join(dir, "Books")}
	defer func() { storeDir, bookDirs = oldStore, oldDirs }()
	good := filepath.Join(dir, "src.epub")
	writeEPUB(t, good, "The Long Walk", "en", "<p>The road went on.</p>")
	book, _ := os.ReadFile(good)

	c := testConsole(t)
	srv := httptest.NewServer(localOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/upload" {
			c.sendUpload(w, r)
		} else {
			c.sendPage(w, r)
		}
	})))
	defer srv.Close()

	send := func(files map[string][]byte) sendResult {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		for name, data := range files {
			fw, _ := mw.CreateFormFile("book", name)
			fw.Write(data)
		}
		mw.Close()
		resp, err := http.Post(srv.URL+"/upload", mw.FormDataContentType(), &body)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var res sendResult
		json.NewDecoder(resp.Body).Decode(&res)
		return res
	}
	res := send(map[string][]byte{"../L'Étranger: Camus.epub": book, "cover.jpg": []byte("jpeg"), "broken.epub": []byte("not a zip")})
	if len(res.Saved) != 1 || res.Saved[0] != "L'Étranger_ Camus.epub" || len(res.Errors) != 2 {
		t.Fatalf("first send: %+v", res)
	}
	if res = send(map[string][]byte{"L'Étranger: Camus.epub": book}); len(res.Saved) != 1 || res.Saved[0] != "L'Étranger_ Camus (2).epub" {
		t.Fatalf("second copy: %+v", res)
	}
	if b := findBooks(); len(b) != 2 {
		t.Fatalf("library has %d books", len(b))
	}
	if _, err := os.Stat(filepath.Join(dir, "L'Étranger_ Camus.epub")); err == nil {
		t.Fatal("a name with ../ escaped the Books folder")
	}
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	var page bytes.Buffer
	page.ReadFrom(resp.Body)
	resp.Body.Close()
	if !strings.Contains(page.String(), "The Long Walk") || !strings.Contains(page.String(), "Send to Books") {
		t.Fatal("the page should list the tablet's books")
	}
}

func TestSafeBookName(t *testing.T) {
	for in, want := range map[string]string{"../../etc/passwd.epub": "passwd.epub", `C:\Books\Dune.EPUB`: "Dune.epub",
		"مَلَنج.epub": "مَلَنج.epub", "...epub": "book.epub", "a/b\x00c.epub": "b_c.epub"} {
		if got := safeBookName(in); got != want {
			t.Errorf("safeBookName(%q) = %q, want %q", in, got, want)
		}
	}
}
