package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"condor-init/ui"
)

// A cover is fetched (here: found as downloaded) and scaled once; after that it's on the
// tablet ready to show: a new cache (a restart) has it at once, without the original.
func TestCoverRememberedForever(t *testing.T) {
	testConsole(t)
	url := "https://example.org/cover.png"
	src := image.NewRGBA(image.Rect(0, 0, 100, 150))
	ui.Fill(src, src.Rect, color.RGBA{200, 40, 40, 255})
	os.MkdirAll(coverDir, 0o755)
	f, _ := os.Create(filepath.Join(coverDir, hash(url)))
	png.Encode(f, src)
	f.Close()

	got := make(chan string, 1)
	covers.loaded = func(u string) { got <- u }
	if covers.get(url, 150, 225) != nil {
		t.Fatal("the first time, a cover loads in the background")
	}
	select {
	case u := <-got:
		if u != url {
			t.Fatalf("loaded %q", u)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the cover never loaded")
	}
	if covers.get(url, 150, 225) == nil {
		t.Fatal("loaded, but not in memory")
	}

	os.Remove(filepath.Join(coverDir, hash(url)))
	covers = newCoverCache()
	img := covers.get(url, 150, 225)
	if img == nil {
		t.Fatal("after a restart the cover isn't there at once")
	}
	if c := img.RGBAAt(75, 112); c.R < 180 || c.G > 60 {
		t.Fatalf("the remembered cover is %v", c)
	}
}

// Memory keeps the covers drawn most recently, up to coverMemBytes.
func TestCoverMemoryKeepsRecent(t *testing.T) {
	cc := newCoverCache()
	big := func() *image.RGBA { return image.NewRGBA(image.Rect(0, 0, 1024, 2048)) } // 8 MB
	n := coverMemBytes/(8<<20) + 2
	for i := 0; i < n; i++ {
		cc.mu.Lock()
		cc.keep(coverKey{fmt.Sprint(i), 1024, 2048}, big())
		cc.mu.Unlock()
		if i == 2 {
			cc.get("0", 1024, 2048) // drawn again: kept
		}
	}
	cc.mu.Lock()
	defer cc.mu.Unlock()
	if cc.bytes > coverMemBytes {
		t.Fatalf("%d bytes kept, budget %d", cc.bytes, coverMemBytes)
	}
	if cc.imgs[coverKey{"0", 1024, 2048}] == nil {
		t.Fatal("a cover drawn recently was dropped")
	}
	if cc.imgs[coverKey{"1", 1024, 2048}] != nil {
		t.Fatal("the least recently drawn cover was kept")
	}
}

// A cover arriving repaints the rows that show it, not the whole of Home.
func TestCoverArrivalRepaintsItsRow(t *testing.T) {
	c := testConsole(t)
	drawMu.Lock()
	defer drawMu.Unlock()
	c.mode = modeBooksHome
	items := []rowItem{{it: &storeItem{key: "k0", title: "A Book", cover: "u0"}, id: "b0"}}
	img := canvas(c.s.W, 4000)
	ui.Fill(img, img.Rect, apBG)
	c.page = &page{img: img, header: tabsH}
	c.coverRow(c.page, "row", 600, items, apBG)
	c.pageGen = 5
	c.blitPage()
	c.s.Flush()

	cv := image.NewRGBA(image.Rect(0, 0, rowCW, rowCH))
	ui.Fill(cv, cv.Rect, color.RGBA{10, 220, 30, 255})
	covers.mu.Lock()
	covers.keep(coverKey{"u0", rowCW, rowCH}, cv)
	covers.mu.Unlock()
	c.arrived = map[string]bool{"u0": true}
	c.paintArrived()

	x, y := 48+rowCW/2, 600+rowCH/2
	if p := c.page.img.RGBAAt(x, y); p.G != 220 {
		t.Fatalf("the row wasn't repainted with the cover: %v", p)
	}
	fx, fy := c.s.rot.toFB(x, c.barH+y, c.s.fbW, c.s.fbH)
	if g := c.s.buf[fy*c.s.stride+4*fx+1]; g != 220 {
		t.Fatalf("the screen doesn't show the cover (green %d)", g)
	}
	if c.pageGen != 5 || c.homeRedrawQueued {
		t.Fatal("the whole page was redrawn for one cover")
	}
}
