package main

import (
	"archive/zip"
	"bytes"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

// writeLongEPUB makes a two-chapter book long enough to need several pages.
func writeLongEPUB(t *testing.T, file string) {
	f, err := os.Create(file)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	add := func(name, body string) { w, _ := zw.Create(name); w.Write([]byte(body)) }
	add(epub.ContainerPath, `<container><rootfiles><rootfile full-path="content.opf"/></rootfiles></container>`)
	add("content.opf", `<package><metadata><dc:title>The Long Walk</dc:title><dc:creator>Test Author</dc:creator></metadata>
<manifest><item id="a" href="a.xhtml" media-type="application/xhtml+xml"/><item id="b" href="b.xhtml" media-type="application/xhtml+xml"/></manifest>
<spine><itemref idref="a"/><itemref idref="b"/></spine></package>`)
	para := strings.Repeat("The road went on and on, past fields and rivers and quiet towns. ", 6)
	var ch strings.Builder
	ch.WriteString("<html><body><h1>One: The Road</h1>")
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&ch, "<p>%d. %s</p>", i+1, para)
	}
	ch.WriteString("</body></html>")
	add("a.xhtml", ch.String())
	add("b.xhtml", "<html><body><h1>Two: Home</h1><p>And then they were home.</p></body></html>")
	zw.Close()
	f.Close()
}

func TestReadingSession(t *testing.T) {
	dir := t.TempDir()
	writeLongEPUB(t, filepath.Join(dir, "long.epub"))
	old := bookDirs
	bookDirs = []string{dir}
	defer func() { bookDirs = old }()
	os.Remove(libraryPath)
	defer os.Remove(libraryPath)

	c := testConsole(t)
	c.lib = loadLibrary()
	c.rf = newReaderFonts(c.lib.Prefs.Size)
	c.setMode(modeBooks)
	if len(c.shelf) != 1 || c.shelf[0].title != "The Long Walk" {
		t.Fatalf("shelf %+v", c.shelf)
	}
	if out := os.Getenv("SCREENS_PNG"); out != "" {
		screenPNG(t, c.s, out+"-shelf.png")
	}
	tapButton(t, c, "book0")
	if c.mode != modeReader || c.book == nil || len(c.book.pages) < 3 {
		t.Fatalf("open: mode %v pages %d", c.mode, len(c.book.pages))
	}
	if out := os.Getenv("SCREENS_PNG"); out != "" {
		screenPNG(t, c.s, out+"-reader-night.png")
	}
	tapButton(t, c, "next")
	c.key(keyVolumeDown, 1)
	if c.book.page != 2 {
		t.Fatalf("after tap + volume: page %d, want 2", c.book.page)
	}
	c.key(keyVolumeUp, 1)
	if c.book.page != 1 {
		t.Fatalf("volume up: page %d, want 1", c.book.page)
	}
	word := c.book.pages[c.book.page][0].word
	tapButton(t, c, "bigger")
	if first := c.book.pages[c.book.page][0].word; first > word || c.book.pages[c.book.page][len(c.book.pages[c.book.page])-1].word < word {
		t.Fatalf("after A+, the page should still contain word %d (first word now %d)", word, first)
	}
	tapButton(t, c, "theme")
	if c.lib.Prefs.Theme != 1 {
		t.Fatalf("theme %d", c.lib.Prefs.Theme)
	}
	if out := os.Getenv("SCREENS_PNG"); out != "" {
		screenPNG(t, c.s, out+"-reader-paper.png")
	}
	for i := 0; i < 50 && c.book.chapter == 0; i++ {
		c.turn(+1)
	}
	if c.book.chapter != 1 {
		t.Fatal("paging forward should reach chapter 2")
	}
	c.turn(-1)
	if c.book.chapter != 0 || c.book.page != len(c.book.pages)-1 {
		t.Fatalf("back from chapter 2 should land on chapter 1's last page, got ch %d page %d", c.book.chapter, c.book.page)
	}
	saved := c.lib.Progress[c.book.path]

	// A fresh start reopens the book where it was left.
	c2 := testConsole(t)
	c2.lib = loadLibrary()
	c2.rf = newReaderFonts(c2.lib.Prefs.Size)
	c2.openBookAt(c.book.path)
	if c2.book.chapter != saved.Chapter || c2.book.pages[c2.book.page][0].word != saved.Word {
		t.Fatalf("reopened at ch %d word %d, saved %+v", c2.book.chapter, c2.book.pages[c2.book.page][0].word, saved)
	}
}
