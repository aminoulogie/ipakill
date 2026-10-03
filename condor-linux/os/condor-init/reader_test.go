package main

import (
	"archive/zip"
	"bytes"
	"fmt"
	"image"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"condor-init/epub"
)

func TestFastBlitMatchesSet(t *testing.T) {
	a := newScreen(nil, 1920, 1200, 7680, 32, bitfield{16, 8, 0}, bitfield{8, 8, 0}, bitfield{0, 8, 0}, Rot90)
	b := newScreen(nil, 1920, 1200, 7680, 32, bitfield{16, 8, 0}, bitfield{8, 8, 0}, bitfield{0, 8, 0}, Rot90)
	img := image.NewRGBA(image.Rect(0, 0, 300, 200))
	for i := range img.Pix {
		img.Pix[i] = byte(i * 7)
	}
	a.blitRGBA(img, 50, 900)
	for y := 0; y < 200; y++ {
		for x := 0; x < 300; x++ {
			o := img.PixOffset(x, y)
			b.Set(50+x, 900+y, img.Pix[o], img.Pix[o+1], img.Pix[o+2])
		}
	}
	if !bytes.Equal(a.buf, b.buf) || a.dirtyLo != b.dirtyLo || a.dirtyHi != b.dirtyHi {
		t.Fatal("fast blit differs from Set")
	}
}

// writeEPUB makes a book from chapters of XHTML bodies.
func writeEPUB(t *testing.T, file, title, lang string, chapters ...string) {
	f, err := os.Create(file)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	add := func(name, body string) { w, _ := zw.Create(name); w.Write([]byte(body)) }
	add(epub.ContainerPath, `<container><rootfiles><rootfile full-path="content.opf"/></rootfiles></container>`)
	var items, refs strings.Builder
	for i, ch := range chapters {
		fmt.Fprintf(&items, `<item id="c%d" href="c%d.xhtml" media-type="application/xhtml+xml"/>`, i, i)
		fmt.Fprintf(&refs, `<itemref idref="c%d"/>`, i)
		add(fmt.Sprintf("c%d.xhtml", i), "<html><body>"+ch+"</body></html>")
	}
	add("content.opf", fmt.Sprintf(`<package><metadata><dc:title>%s</dc:title><dc:creator>Test Author</dc:creator><dc:language>%s</dc:language></metadata>
<manifest>%s</manifest><spine>%s</spine></package>`, title, lang, items.String(), refs.String()))
	zw.Close()
	f.Close()
}

// writeLongEPUB makes a two-chapter book long enough to need several pages.
func writeLongEPUB(t *testing.T, file string) {
	para := strings.Repeat("The road went on and on, past fields and rivers and quiet towns. ", 6)
	var ch strings.Builder
	ch.WriteString("<h1>One: The Road</h1>")
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&ch, "<p>%d. %s</p>", i+1, para)
	}
	writeEPUB(t, file, "The Long Walk", "en", ch.String(), "<h1>Two: Home</h1><p>And then they were home.</p>")
}

// pressKey sends a key the way the key loop does (key takes drawMu itself).
func pressKey(c *console, code uint16) {
	drawMu.Unlock()
	defer drawMu.Lock()
	c.key(code, 1)
}

// readerConsole is a console with a fresh library and word book, reading long.epub.
func readerConsole(t *testing.T) *console {
	dir := t.TempDir()
	writeLongEPUB(t, filepath.Join(dir, "long.epub"))
	oldDirs, oldWords := bookDirs, wordsPath
	bookDirs, wordsPath = []string{dir}, filepath.Join(dir, "words.json")
	os.Remove(libraryPath)
	t.Cleanup(func() { bookDirs, wordsPath = oldDirs, oldWords; os.Remove(libraryPath) })
	c := testConsole(t)
	c.lib, c.words = loadLibrary(), loadWords()
	c.lib.Prefs.PageTurn = "none" // turns are animated in TestPageTurns
	c.rf = c.readerFontsNow()
	// Hold the screen lock like the touch loop does: lookups and the contents finish in the
	// background and take it. Tests release it around waitFor.
	drawMu.Lock()
	t.Cleanup(drawMu.Unlock)
	return c
}

func openFirstBook(t *testing.T, c *console) {
	t.Helper()
	c.setMode(modeBooks)
	tapButton(t, c, "book0")
	if c.mode != modeReader || c.book == nil {
		t.Fatalf("open: mode %v", c.mode)
	}
}

// tapChrome taps one of the reader's controls, bringing them up first (a tap in the middle).
func tapChrome(t *testing.T, c *console, id string) {
	t.Helper()
	if !c.rd.chrome {
		tapButton(t, c, "chrome")
	}
	tapButton(t, c, id)
}

// pressWord long-presses the word with chapter index idx on the current page.
func pressWord(t *testing.T, c *console, idx int) {
	t.Helper()
	for _, l := range c.book.pages[c.book.page] {
		for _, w := range l.words {
			if w.idx == idx {
				tr := c.textRect()
				c.longPress(tr.Min.X+w.x+w.w/2, c.barH+tr.Min.Y+l.y+l.h/2)
				c.rd.g.selecting = false
				return
			}
		}
	}
	t.Fatalf("word %d not on this page", idx)
}

func TestReadingSession(t *testing.T) {
	c := readerConsole(t)
	c.setMode(modeBooks)
	if len(c.shelf) != 1 || c.shelf[0].title != "The Long Walk" {
		t.Fatalf("shelf %+v", c.shelf)
	}
	shot(t, c, "reader-shelf")
	openFirstBook(t, c)
	if len(c.book.pages) < 3 || c.bookLang() != "en" {
		t.Fatalf("pages %d lang %q", len(c.book.pages), c.bookLang())
	}
	shot(t, c, "reader-night")
	tapButton(t, c, "next")
	pressKey(c, keyVolumeDown)
	if c.book.page != 2 {
		t.Fatalf("after tap + volume: page %d, want 2", c.book.page)
	}
	pressKey(c, keyVolumeUp)
	if c.book.page != 1 {
		t.Fatalf("volume up: page %d, want 1", c.book.page)
	}

	// Settings: bigger text keeps the first word on screen; theme and font change.
	word := c.book.pages[c.book.page][0].word
	tapButton(t, c, "chrome")
	shot(t, c, "reader-chrome")
	tapButton(t, c, "settings")
	shot(t, c, "reader-settings")
	tapButton(t, c, "r:set:size:+")
	pg := c.book.pages[c.book.page]
	if pg[0].word > word || pg[len(pg)-1].last() < word {
		t.Fatalf("after A+: page shows words %d-%d, lost word %d", pg[0].word, pg[len(pg)-1].last(), word)
	}
	tapButton(t, c, "r:set:theme:4") // Calm
	tapButton(t, c, "r:custom")
	shot(t, c, "reader-customize")
	for _, f := range readerFontList {
		tapButton(t, c, "r:set:font:"+f.id)
		if c.lib.Prefs.Font != f.id || c.rf.font != f.id {
			t.Fatalf("font %s not applied", f.id)
		}
	}
	tapButton(t, c, "r:set:font:serif")
	tapButton(t, c, "r:set:lh:+")
	tapButton(t, c, "r:set:margin:+")
	tapButton(t, c, "r:set:turn:slide")
	if p := c.lib.Prefs; p.Theme != 4 || p.LineHeight != 1.8 || p.Margin != 96 || p.PageTurn != "slide" {
		t.Fatalf("prefs %+v", p)
	}
	tapButton(t, c, "r:custom")      // back to the themes
	tapButton(t, c, "r:set:theme:3") // Bold: the text in the bold face, laid out again
	if c.rf.body != newReaderFontsB(c.lib.Prefs.Font, c.lib.Prefs.Size, c.lib.Prefs.LineHeight, true).body {
		t.Fatal("Bold should lay the book out in the bold face")
	}
	tapButton(t, c, "r:set:appearance") // the half moon: dark
	if c.lib.Prefs.Theme != themeNight {
		t.Fatalf("appearance: theme %d", c.lib.Prefs.Theme)
	}
	tapButton(t, c, "r:set:theme:0")
	tapButton(t, c, "settings") // closes it
	c.lib.Prefs.PageTurn = "none"
	shot(t, c, "reader-original")

	// Into chapter two and back.
	for i := 0; i < 80 && c.book.chapter == 0; i++ {
		c.turn(1)
	}
	if c.book.chapter != 1 {
		t.Fatal("never reached chapter 2")
	}
	c.turn(-1)
	if c.book.chapter != 0 || c.book.page != len(c.book.pages)-1 {
		t.Fatalf("back from chapter 2 should land on chapter 1's last page, got ch %d page %d", c.book.chapter, c.book.page)
	}
	// Past the end: finished.
	c.turn(1)
	c.turn(1)
	if !c.book.finished || c.lib.Finished[c.book.path] == "" || c.lib.booksThisYear() != 1 {
		t.Fatalf("finished %v %v", c.book.finished, c.lib.Finished)
	}
	saved := c.lib.Progress[c.book.path]
	if saved.Pct != 100 || saved.Opened == "" {
		t.Errorf("progress for the library: %+v", saved)
	}
	c.setMode(modeBooks)
	shot(t, c, "reader-library")

	// A fresh start reopens the book where it was left, with the same settings.
	c2 := testConsole(t)
	c2.lib, c2.words = loadLibrary(), loadWords()
	c2.openBookAt(c.book.path)
	if c2.book.chapter != saved.Chapter || c2.book.pages[c2.book.page][0].word != saved.Word || c2.lib.Prefs.Theme != 0 {
		t.Fatalf("reopened at ch %d word %d, saved %+v", c2.book.chapter, c2.book.pages[c2.book.page][0].word, saved)
	}
}

func TestLineByLine(t *testing.T) {
	c := readerConsole(t)
	openFirstBook(t, c)
	tapChrome(t, c, "linemode")
	if !c.lib.Prefs.LineFocus || c.book.line != 0 {
		t.Fatal("line mode should start on the page's first line")
	}
	shot(t, c, "reader-line")
	// Any tap: next line. The left sixth: the line before.
	tapButton(t, c, "next")
	tapButton(t, c, "next")
	if c.book.line != 2 {
		t.Fatalf("line %d, want 2", c.book.line)
	}
	for _, b := range c.page.buttons {
		if b.id == "prev" && b.r.Max.X != c.s.W/6 {
			t.Errorf("back zone is %v, want the left sixth", b.r)
		}
	}
	tapButton(t, c, "prev")
	if c.book.line != 1 {
		t.Fatalf("line %d, want 1", c.book.line)
	}
	// The volume keys step lines too.
	pressKey(c, keyVolumeDown)
	if c.book.line != 2 {
		t.Fatalf("volume: line %d", c.book.line)
	}
	// Long press on a line: the lit line jumps there.
	l5 := c.book.pages[c.book.page][5]
	tr := c.textRect()
	c.longPress(tr.Min.X+l5.words[0].x+5, c.barH+tr.Min.Y+l5.y+l5.h/2)
	c.rd.g.selecting = false
	if c.book.line != 5 || c.rd.sel == nil || c.rd.sel.from != l5.word {
		t.Fatalf("long press: line %d sel %+v", c.book.line, c.rd.sel)
	}
	shot(t, c, "reader-line-press")
	c.closeOverlays()
	// Past the last line: the next page, first line. Before the first: back to the last.
	page := c.book.page
	n := len(c.book.pages[page])
	for c.book.page == page {
		c.stepLine(1)
	}
	if c.book.page != page+1 || c.book.line != 0 {
		t.Fatalf("page %d line %d", c.book.page, c.book.line)
	}
	c.stepLine(-1)
	if c.book.page != page || c.book.line != n-1 {
		t.Fatalf("back: page %d line %d, want %d %d", c.book.page, c.book.line, page, n-1)
	}
	// The lit line is where the book reopens.
	want := c.book.pages[c.book.page][c.book.line].word
	if got := c.lib.Progress[c.book.path].Word; got != want {
		t.Fatalf("saved word %d, want %d", got, want)
	}
	c2 := testConsole(t)
	c2.lib, c2.words = loadLibrary(), loadWords()
	c2.openBookAt(c.book.path)
	if c2.book.page != c.book.page || c2.book.line != c.book.line {
		t.Fatalf("reopened on page %d line %d, want %d %d", c2.book.page, c2.book.line, c.book.page, c.book.line)
	}
	// Dimmed above and below the lit line; the line itself in the mark colour.
	c.showPage()
	th := readerThemes[c.lib.Prefs.Theme]
	lit := c.litRect(c.book.line)
	above := c.page.img.RGBAAt(c.s.W/2, readerToolbarH+textTopPad+2)
	if c.book.line > 0 && (above.R != th.bg.R || above.G != th.bg.G) {
		// blank pixel between lines: dim and normal are both the paper colour
		t.Logf("pixel above %v", above)
	}
	if px := c.page.img.RGBAAt(c.textRect().Min.X-4, (lit.Min.Y+lit.Max.Y)/2); px == th.bg {
		t.Error("the lit line should carry the mark colour")
	}
	// Moving the line redraws two strips, not the page.
	c.s.dirtyLo, c.s.dirtyHi = c.s.fbH, -1
	c.stepLine(1)
	if c.s.dirtyHi-c.s.dirtyLo > 400 {
		t.Errorf("a line step redrew %d framebuffer columns", c.s.dirtyHi-c.s.dirtyLo)
	}
	// ...and what it drew is exactly what a full redraw draws.
	for i := 0; i < 4; i++ {
		c.stepLine(1)
	}
	c.stepLine(-1)
	fast := append([]byte(nil), c.s.buf...)
	c.showPage()
	if !bytes.Equal(fast, c.s.buf) {
		t.Error("line steps left the screen different from a full redraw")
	}
}

func TestSelectHighlightAndList(t *testing.T) {
	c := readerConsole(t)
	openFirstBook(t, c)
	pg := c.book.pages[c.book.page]
	w := pg[2].words[1].idx
	pressWord(t, c, w)
	if c.rd.sel == nil || c.rd.menu != menuMain {
		t.Fatal("a long press should select the word and open the menu")
	}
	c.rd.sel.to = w + 3 // dragged over four words
	c.showPage()
	shot(t, c, "reader-menu")
	tapButton(t, c, "r:hl")
	shot(t, c, "reader-ink")
	tapButton(t, c, "r:ink:mint")
	marks := c.lib.Marks[c.book.path]
	if len(marks) != 1 || marks[0].Start != w || marks[0].End != w+3 || marks[0].Colour != "mint" ||
		marks[0].Text != strings.Join(c.book.words[w:w+4], " ") {
		t.Fatalf("marks %+v", marks)
	}
	if c.rd.sel != nil {
		t.Error("highlighting should close the selection")
	}
	// Marking words that overlap it: one highlight, the new colour.
	pressWord(t, c, w+3)
	if c.rd.sel.from != w || c.rd.sel.to != w+3 {
		t.Fatalf("long press on a highlight should select all of it: %+v", c.rd.sel)
	}
	c.rd.sel = &selection{w + 2, w + 2, w + 6}
	tapButton(t, c, "r:hl")
	tapButton(t, c, "r:ink:sky")
	marks = c.lib.Marks[c.book.path]
	if len(marks) != 1 || marks[0].Start != w || marks[0].End != w+6 || marks[0].Colour != "sky" {
		t.Fatalf("after overlap: %+v", marks)
	}
	shot(t, c, "reader-highlight")
	// Another highlight on a later page, then the list.
	c.turn(1)
	pressWord(t, c, c.book.pages[c.book.page][0].words[0].idx)
	tapButton(t, c, "r:hl")
	tapButton(t, c, "r:ink:blossom")
	tapChrome(t, c, "marks")
	shot(t, c, "reader-marks")
	if c.rd.view != "marks" {
		t.Fatal("marks list not open")
	}
	tapButton(t, c, "r:mark:"+marks[0].ID) // jump back to the first
	if c.rd.view != "" || c.book.page != 0 {
		t.Fatalf("jump: view %q page %d", c.rd.view, c.book.page)
	}
	// Remove from the menu.
	pressWord(t, c, w)
	tapButton(t, c, "r:hl")
	tapButton(t, c, "r:unmark")
	if len(c.lib.Marks[c.book.path]) != 1 {
		t.Fatalf("after remove: %+v", c.lib.Marks[c.book.path])
	}
	// Contents.
	tapChrome(t, c, "contents")
	drawMu.Unlock()
	waitFor(t, "titles", func() bool { return c.book.titles != nil })
	drawMu.Lock()
	c.showPage()
	shot(t, c, "reader-contents")
	tapButton(t, c, "r:toc:1")
	if c.book.chapter != 1 || c.rd.view != "" {
		t.Fatalf("contents jump: chapter %d", c.book.chapter)
	}
}

// fakeDictionary serves Wiktionary, Datamuse and MyMemory.
func fakeDictionary(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/wiktionary/", func(w http.ResponseWriter, r *http.Request) {
		switch strings.TrimPrefix(r.URL.Path, "/wiktionary/") {
		case "road":
			io.WriteString(w, `{"en": [{"partOfSpeech": "Noun", "language": "English", "definitions": [
			  {"definition": "A <a href=\"/wiki/way\">way</a> used for travelling between places.",
			   "examples": ["<b>The road</b> to the city."]},
			  {"definition": "A path chosen in life."}]}]}`)
		default:
			http.NotFound(w, r)
		}
	})
	mux.HandleFunc("/datamuse", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("sp") == "rivers" {
			io.WriteString(w, `[{"word": "rivers", "defs": ["n\tlarge natural streams of water"], "tags": ["n"]}]`)
			return
		}
		io.WriteString(w, `[]`)
	})
	mux.HandleFunc("/mymemory", func(w http.ResponseWriter, r *http.Request) {
		q, pair := r.URL.Query().Get("q"), r.URL.Query().Get("langpair")
		switch pair {
		case "en|ar":
			fmt.Fprint(w, `{"responseData": {"translatedText": "الطريق", "match": 0.98}, "responseStatus": 200}`)
		case "en|fr":
			fmt.Fprintf(w, `{"responseData": {"translatedText": "la route (%s)", "match": "0.5"}, "responseStatus": "200"}`, q)
		default:
			fmt.Fprint(w, `{"responseData": {"translatedText": "PLEASE SELECT TWO DISTINCT LANGUAGES"}, "responseStatus": 403}`)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	oldW, oldD, oldT, oldC := wiktionaryURL, datamuseURL, translateURL, webClient
	wiktionaryURL, datamuseURL, translateURL = srv.URL+"/wiktionary", srv.URL+"/datamuse", srv.URL+"/mymemory"
	webClient = func() *http.Client { return srv.Client() }
	t.Cleanup(func() { wiktionaryURL, datamuseURL, translateURL, webClient = oldW, oldD, oldT, oldC })
	return srv
}

func TestLookUpTranslateKeep(t *testing.T) {
	fakeDictionary(t)
	c := readerConsole(t)
	openFirstBook(t, c)
	road := -1
	for _, l := range c.book.pages[c.book.page] {
		for _, w := range l.words {
			if c.book.words[w.idx] == "road" && road < 0 {
				road = w.idx
			}
		}
	}
	pressWord(t, c, road)
	tapButton(t, c, "r:lookup")
	drawMu.Unlock()
	waitFor(t, "definition", func() bool { return c.rd.panel != nil && !c.rd.panel.loading })
	drawMu.Lock()
	pn := c.rd.panel
	if pn.look == nil || len(pn.look.senses) != 2 || pn.look.senses[0].definition != "A way used for travelling between places." ||
		pn.look.senses[0].example != "The road to the city." || pn.look.source != "Wiktionary" {
		t.Fatalf("lookup %+v err %q", pn.look, pn.err)
	}
	c.showPage()
	shot(t, c, "reader-lookup")
	tapButton(t, c, "r:keep")
	if len(c.words.Words) != 1 {
		t.Fatalf("word book %+v", c.words.Words)
	}
	kept := c.words.Words[0]
	if kept.Word != "road" || !strings.HasPrefix(kept.Meaning, "Noun A way used") || kept.Book != "The Long Walk" ||
		!strings.Contains(kept.Sentence, "road went on") {
		t.Fatalf("kept %+v", kept)
	}
	c.showPage()
	shot(t, c, "reader-kept")

	// Translate into Arabic: shaped, right to left.
	pressWord(t, c, road)
	c.lib.Prefs.TranslateTo = "ar"
	tapButton(t, c, "r:translate")
	drawMu.Unlock()
	waitFor(t, "translation", func() bool { return c.rd.panel != nil && !c.rd.panel.loading })
	drawMu.Lock()
	if tr := c.rd.panel.tr; tr == nil || tr.text != "الطريق" {
		t.Fatalf("translation %+v err %q", c.rd.panel.tr, c.rd.panel.err)
	}
	c.showPage()
	shot(t, c, "reader-translate-ar")
	tapButton(t, c, "r:lang:fr")
	drawMu.Unlock()
	waitFor(t, "french", func() bool { return c.rd.panel != nil && !c.rd.panel.loading })
	drawMu.Lock()
	if tr := c.rd.panel.tr; tr == nil || tr.text != "la route (road)" || tr.quality != 0.5 || c.lib.Prefs.TranslateTo != "fr" {
		t.Fatalf("french %+v", c.rd.panel.tr)
	}
	tapButton(t, c, "r:close")
	if c.overlayOpen() {
		t.Error("close should close everything")
	}

	// Keep without a lookup: the meaning is filled in afterwards (Datamuse, as Wiktionary
	// has no "rivers").
	rivers := -1
	for _, l := range c.book.pages[c.book.page] {
		for _, w := range l.words {
			if strings.HasPrefix(c.book.words[w.idx], "rivers") && rivers < 0 {
				rivers = w.idx
			}
		}
	}
	pressWord(t, c, rivers)
	tapButton(t, c, "r:keep")
	drawMu.Unlock()
	waitFor(t, "meaning filled in", func() bool {
		return len(c.words.Words) == 2 && c.words.Words[1].Meaning == "n large natural streams of water"
	})
	drawMu.Lock()
	if c.words.Words[1].Word != "rivers" {
		t.Errorf("selection not cleaned: %q", c.words.Words[1].Word)
	}
}

func TestLookupErrors(t *testing.T) {
	fakeDictionary(t)
	if _, err := lookupWord(t.Context(), "zzqx", "en"); err == nil || !strings.Contains(err.Error(), "no definition found") {
		t.Errorf("unknown word: %v", err)
	}
	if _, err := translateText(t.Context(), "road", "en", "en"); err == nil || !strings.Contains(err.Error(), "already English") {
		t.Errorf("same language: %v", err)
	}
	if _, err := translateText(t.Context(), "road", "en", "de"); err == nil || !strings.Contains(err.Error(), "no German") {
		t.Errorf("service complaint: %v", err)
	}
	if _, err := translateText(t.Context(), strings.Repeat("a", 500), "en", "fr"); err == nil {
		t.Error("too long should be refused")
	}
	wiktionaryURL, datamuseURL = "http://127.0.0.1:1/w", "http://127.0.0.1:1/d"
	if _, err := lookupWord(t.Context(), "road", "en"); err == nil || !strings.Contains(err.Error(), "could not reach") {
		t.Errorf("offline: %v", err)
	}
}

// Soma's word-capture tests.
func TestWordCapture(t *testing.T) {
	for in, want := range map[string]string{"  kwisatz,  ": "kwisatz", `"gom jabbar."`: "gom jabbar", "“prescience”": "prescience",
		"—melange—": "melange", "word\n  break": "word break", "-well-meaning-": "well-meaning"} {
		if got := cleanSelection(in); got != want {
			t.Errorf("cleanSelection(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]bool{"": false, "  ": false, "a": false, "42": false, "—": false,
		strings.Repeat("word ", maxWords+1): false, strings.Repeat("x", 200): false,
		"melange": true, "gom jabbar": true, "  kwisatz haderach,  ": true, "مَلَنج": true, "déjà vu": true} {
		if got := isCapturable(in); got != want {
			t.Errorf("isCapturable(%q) = %v", in, got)
		}
	}
	para := "A beginning is the time for taking care. This is the year of the melange, and the spice must flow. Nobody said it would be easy!"
	for word, want := range map[string]string{"melange": "This is the year of the melange, and the spice must flow.",
		"beginning": "A beginning is the time for taking care.", "easy": "Nobody said it would be easy!", "shai-hulud": ""} {
		if got := sentenceAround(para, word); got != want {
			t.Errorf("sentenceAround(%q) = %q, want %q", word, got, want)
		}
	}
	if got := sentenceAround("Melange.", "melange"); got != "" {
		t.Errorf("a sentence that is only the word: %q", got)
	}
	long := strings.Repeat("word ", 200) + "melange" + strings.Repeat(" more", 200)
	if got := sentenceAround(long, "melange"); len([]rune(got)) > 220 || !strings.HasSuffix(got, "…") {
		t.Errorf("runaway sentence: %d runes", len([]rune(got)))
	}
	sentence := "A beginning is the time for taking care that the balances are correct."
	if !isSelectable(sentence) || isCapturable(sentence) || isSelectable(strings.Repeat("word ", 120)) || isSelectable(" , ") {
		t.Error("isSelectable rules")
	}
}

func TestArabicShaping(t *testing.T) {
	// كتاب: kaf initial, teh medial, alef final, beh isolated; drawn right to left.
	if got, want := shapeArabic("كتاب"), string([]rune{0xFE8F, 0xFE8E, 0xFE98, 0xFEDB}); got != want {
		t.Errorf("كتاب = %U, want %U", []rune(got), []rune(want))
	}
	// سلام: seen initial, lam-alef ligature (final), meem isolated.
	if got, want := shapeArabic("سلام"), string([]rune{0xFEE1, 0xFEFC, 0xFEB3}); got != want {
		t.Errorf("سلام = %U, want %U", []rune(got), []rune(want))
	}
	// Vowel marks dropped; brackets mirrored.
	if got, want := shapeArabic("(بَ)"), string([]rune{'(', 0xFE8F, ')'}); got != want {
		t.Errorf("(بَ) = %U, want %U", []rune(got), []rune(want))
	}
	// A right-to-left paragraph starts at the right edge.
	f := newReaderFonts("serif", 38, 1.7)
	lines := layoutWords(f.body, strings.Fields("هذا كتاب جميل"), 0, 1000, true)
	if len(lines) != 1 || !lines[0].rtl {
		t.Fatalf("lines %+v", lines)
	}
	w := lines[0].words
	if w[0].x+w[0].w != 1000 || !(w[2].x < w[1].x && w[1].x < w[0].x) {
		t.Errorf("RTL placement %+v", w)
	}
	if lines := layoutWords(f.body, strings.Fields("plain English words"), 0, 1000, true); lines[0].rtl || lines[0].words[0].x != 0 {
		t.Error("English should stay left to right")
	}
}

func TestArabicBook(t *testing.T) {
	c := readerConsole(t)
	dir := t.TempDir()
	para := strings.Repeat("كان يا ما كان في قديم الزمان كتاب جميل عن السلام والطريق. ", 8)
	writeEPUB(t, filepath.Join(dir, "ar.epub"), "كتاب", "ar", "<h1>الفصل الأول</h1><p>"+para+"</p><p>"+para+"</p>")
	c.openBookAt(filepath.Join(dir, "ar.epub"))
	if c.book == nil || c.bookLang() != "ar" {
		t.Fatal("arabic book did not open")
	}
	l := c.book.pages[0][1]
	if !l.rtl {
		t.Fatal("arabic lines should be right to left")
	}
	tr := c.textRect()
	if right := l.words[0].x + l.words[0].w; right != tr.Dx() {
		t.Errorf("first word ends at %d, want the right edge %d", right, tr.Dx())
	}
	shot(t, c, "reader-arabic")
}

func TestWordBookReview(t *testing.T) {
	dir := t.TempDir()
	old := wordsPath
	wordsPath = filepath.Join(dir, "words.json")
	defer func() { wordsPath = old }()
	c := testConsole(t)
	c.words = loadWords()
	day := func(n int) string { return time.Now().AddDate(0, 0, n).Format("2006-01-02") }
	c.words.keep(wordEntry{Word: "melange", Meaning: "the spice", Sentence: "This is the year of the melange.", Book: "Dune", Added: day(-3)})
	c.words.keep(wordEntry{Word: "kwisatz", Meaning: "", Added: day(-30)}) // no meaning: never due
	c.words.keep(wordEntry{Word: "gom jabbar", Meaning: "a needle", Added: day(-1)})
	if c.words.keep(wordEntry{Word: "Melange", Meaning: "other"}) || len(c.words.Words) != 3 || c.words.Words[0].Meaning != "the spice" {
		t.Fatal("keeping a word twice should not add it again or overwrite its meaning")
	}
	if due := c.words.dueToday(today()); len(due) != 1 || due[0].Word != "melange" {
		t.Fatalf("due %+v", due)
	}
	c.showPage()
	tapButton(t, c, "words")
	shot(t, c, "words-list")
	tapButton(t, c, "w:review")
	shot(t, c, "words-review")
	tapButton(t, c, "w:show")
	shot(t, c, "words-review-meaning")
	tapButton(t, c, "w:gotit")
	m := c.words.Words[0]
	if len(m.Reviews) != 1 || m.due() != addDays(today(), 7) {
		t.Fatalf("after one pass: reviews %v due %s", m.Reviews, m.due())
	}
	m.Reviews = []string{day(-40), day(-35), day(-31)}
	if m.due() != "" || m.stage() != 3 {
		t.Error("three passes: learned")
	}
	// Reloaded from disk.
	if wb := loadWords(); len(wb.Words) != 3 || len(wb.Words[0].Reviews) != 1 {
		t.Fatalf("saved %+v", wb.Words)
	}
	// A word's page: write a meaning with the keyboard, delete with two taps.
	tapButton(t, c, "w:back")
	tapButton(t, c, "w:open:"+c.words.Words[1].ID)
	tapButton(t, c, "w:edit")
	c.wordsKey([]byte("the voice"))
	c.wordsKey([]byte("\r"))
	if c.words.Words[1].Meaning != "the voice" || c.wui.edit {
		t.Fatalf("meaning %q", c.words.Words[1].Meaning)
	}
	shot(t, c, "words-word")
	tapButton(t, c, "w:delete")
	tapButton(t, c, "w:delete")
	if len(c.words.Words) != 2 {
		t.Fatal("delete")
	}
}

func TestReadingClock(t *testing.T) {
	c := readerConsole(t)
	openFirstBook(t, c)
	c.lastRead = time.Now().Add(-90 * time.Second)
	c.turn(1)
	if got := c.lib.Reading[today()]; got < 89 || got > 92 {
		t.Fatalf("read %d s", got)
	}
	c.lastRead = time.Now().Add(-10 * time.Minute) // put down: not counted
	c.turn(1)
	if got := c.lib.Reading[today()]; got > 92 {
		t.Fatalf("a break counted as reading: %d s", got)
	}
}

// The real touch path: hold to select (with the timer), drag to take in more, tap, swipe.
func TestGestures(t *testing.T) {
	c := readerConsole(t)
	openFirstBook(t, c)
	pg := c.book.pages[c.book.page]
	tr := c.textRect()
	at := func(l rline, w tword) (int, int) { return tr.Min.X + w.x + w.w/2, c.barH + tr.Min.Y + l.y + l.h/2 }
	x, y := at(pg[3], pg[3].words[1])
	c.readerTouch(TouchPoint{Slot: 0, X: x, Y: y, Down: true})
	drawMu.Unlock()
	waitFor(t, "hold", func() bool { return c.rd.sel != nil })
	drawMu.Lock()
	x2, y2 := at(pg[3], pg[3].words[4])
	c.readerTouch(TouchPoint{Slot: 0, X: x2, Y: y2, Moved: true})
	c.readerTouch(TouchPoint{Slot: 0, X: x2, Y: y2, Up: true})
	if s := c.rd.sel; s == nil || s.from != pg[3].words[1].idx || s.to != pg[3].words[4].idx || c.rd.menu != menuMain {
		t.Fatalf("drag selection %+v menu %v", c.rd.sel, c.rd.menu)
	}
	// A tap off the menu closes it; the next tap turns the page.
	c.readerTouch(TouchPoint{Slot: 0, X: 600, Y: 1700, Down: true})
	c.readerTouch(TouchPoint{Slot: 0, X: 600, Y: 1700, Up: true})
	if c.overlayOpen() || c.book.page != 0 {
		t.Fatalf("tap with the menu open: open %v page %d", c.overlayOpen(), c.book.page)
	}
	c.readerTouch(TouchPoint{Slot: 0, X: 900, Y: 1000, Down: true})
	c.readerTouch(TouchPoint{Slot: 0, X: 900, Y: 1000, Up: true})
	if c.book.page != 1 {
		t.Fatalf("tap: page %d", c.book.page)
	}
	// Swipe right: back a page.
	c.readerTouch(TouchPoint{Slot: 0, X: 200, Y: 1000, Down: true})
	c.readerTouch(TouchPoint{Slot: 0, X: 500, Y: 1010, Moved: true})
	c.readerTouch(TouchPoint{Slot: 0, X: 700, Y: 1010, Up: true})
	if c.book.page != 0 {
		t.Fatalf("swipe: page %d", c.book.page)
	}
	// A down that never came up (the status bar took it) doesn't break the next tap.
	c.readerTouch(TouchPoint{Slot: 0, X: 100, Y: 10, Down: true})
	c.readerTouch(TouchPoint{Slot: 0, X: 900, Y: 1000, Down: true})
	c.readerTouch(TouchPoint{Slot: 0, X: 900, Y: 1000, Up: true})
	if c.book.page != 1 {
		t.Fatalf("after a lost gesture: page %d", c.book.page)
	}
	drawMu.Unlock()
	time.Sleep(holdDelay + 50*time.Millisecond) // no stray hold from the taps above
	drawMu.Lock()
	if c.rd.sel != nil {
		t.Error("a tap turned into a hold")
	}
}

func TestPageTurnsAndBookmarks(t *testing.T) {
	c := readerConsole(t)
	openFirstBook(t, c)
	old := turnDuration
	turnDuration = map[string]time.Duration{"slide": 60 * time.Millisecond, "curl": 60 * time.Millisecond}
	defer func() { turnDuration = old }()
	for _, style := range []string{"slide", "curl"} {
		c.lib.Prefs.PageTurn = style
		before := animFrames
		c.turn(1)
		c.turn(-1)
		if animFrames == before {
			t.Errorf("%s: no frames drawn", style)
		}
		end := append([]byte(nil), c.s.buf...)
		c.showPage()
		if !bytes.Equal(end, c.s.buf) {
			t.Errorf("%s: the turn didn't end on the page", style)
		}
	}
	// A frame of the curl, to look at.
	c.lib.Prefs.PageTurn = "curl"
	from := c.turnFrom()
	c.book.page++
	c.invalidatePage()
	c.s.hold = true
	c.showPage()
	c.s.hold = false
	_, nu := c.animBufs()
	copy(nu, c.s.buf)
	c.curlFrame(from, nu, 0.45)
	shot(t, c, "reader-curl")
	c.lib.Prefs.PageTurn = "none"
	c.showPage()

	// Bookmarks: the ribbon, the list, a jump back.
	tapChrome(t, c, "r:bookmark")
	if c.bookmarked() == nil || len(c.lib.Bookmarks[c.book.path]) != 1 {
		t.Fatal("bookmark not added")
	}
	shot(t, c, "reader-bookmarked")
	c.turn(1)
	c.turn(1)
	tapChrome(t, c, "contents")
	tapButton(t, c, "r:list:bookmarks")
	shot(t, c, "reader-bookmarks")
	tapButton(t, c, "r:bm:"+c.lib.Bookmarks[c.book.path][0].ID)
	if c.book.page != 1 || c.bookmarked() == nil {
		t.Fatalf("jump to bookmark: page %d", c.book.page)
	}
	tapChrome(t, c, "r:bookmark")
	if c.bookmarked() != nil {
		t.Fatal("bookmark not removed")
	}
}
