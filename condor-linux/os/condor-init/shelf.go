package main

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/image/font"

	"condor-init/epub"
	"condor-init/ui"
)

// The shelf: the EPUBs found on the tablet.

// bookDirs are searched for .epub files (two levels deep).
var bookDirs = []string{
	"/data/media/0/Books", "/data/media/Books", // internal storage (condor books puts them here)
	"/storage/sdcard_ext/Books", "/storage/sdcard1/Books", "/storage/sdcard_ext", // microSD
	alpineRoot + "/root/books", condorHome + "/books",
}

type shelfBook struct {
	path, title, author string
}

// shelfCache remembers each book's title and author by path, size and date, so drawing the
// shelf doesn't reopen every EPUB (slow on the tablet's eMMC and CPU).
var shelfCache = map[string]struct {
	size int64
	mod  time.Time
	sb   shelfBook
}{}

func bookInfo(p string, e os.DirEntry) shelfBook {
	fi, err := e.Info()
	if err == nil {
		if ce, ok := shelfCache[p]; ok && ce.size == fi.Size() && ce.mod.Equal(fi.ModTime()) {
			return ce.sb
		}
	}
	sb := shelfBook{path: p, title: strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))}
	if b, err := epub.Open(p); err == nil {
		sb.title, sb.author = b.Title, b.Author
		b.Close()
	}
	if fi != nil {
		shelfCache[p] = struct {
			size int64
			mod  time.Time
			sb   shelfBook
		}{fi.Size(), fi.ModTime(), sb}
	}
	return sb
}

// findBooks lists the EPUBs on the tablet, by title.
func findBooks() []shelfBook {
	seen := map[string]bool{}
	var books []shelfBook
	var scan func(dir string, depth int)
	scan = func(dir string, depth int) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			p := filepath.Join(dir, e.Name())
			if e.IsDir() && depth > 0 && !strings.HasPrefix(e.Name(), ".") {
				scan(p, depth-1)
				continue
			}
			if !strings.EqualFold(filepath.Ext(e.Name()), ".epub") || seen[p] {
				continue
			}
			seen[p] = true
			books = append(books, bookInfo(p, e))
		}
	}
	for _, d := range bookDirs {
		scan(d, 1)
	}
	sort.Slice(books, func(i, j int) bool { return strings.ToLower(books[i].title) < strings.ToLower(books[j].title) })
	return books
}

// shelfPage lists the books.
func (c *console) shelfPage() *page {
	pn := newPen(c.s.W, c.s.H-c.barH, c.pf)
	pn.btn("home", "< home", image.Rect(pn.mx-12, 24, pn.mx+260, 124), pgBtn, pgText)
	pn.y = 230
	pn.text(c.pf.title, pgText, pn.mx, pn.y, "books")
	// Soma's reading goal: minutes today against the daily goal, books finished this year.
	mins, goal := c.lib.readingToday(), c.lib.Prefs.GoalMinutes
	gr := image.Rect(pn.mx, pn.y+30, c.s.W-pn.mx, pn.y+120)
	ui.RoundRect(pn.p.img, gr, 20, pgCard)
	if done := min(mins, goal) * (gr.Dx() - 8) / max(goal, 1); done > 0 {
		ui.RoundRect(pn.p.img, image.Rect(gr.Min.X+4, gr.Max.Y-14, gr.Min.X+4+done, gr.Max.Y-6), 4, pgAccent)
	}
	ui.DrawText(pn.p.img, c.pf.small, gr.Min.X+28, gr.Min.Y+52, pgText, fmt.Sprintf("today %d of %d min", mins, goal))
	right := fmt.Sprintf("%d of %d books this year", c.lib.booksThisYear(), c.lib.Prefs.BooksPerYear)
	ui.DrawText(pn.p.img, c.pf.small, gr.Max.X-28-ui.TextWidth(c.pf.small, right), gr.Min.Y+52, pgMuted, right)
	pn.p.buttons = append(pn.p.buttons, button{"r:goal", gr})
	pn.y = gr.Max.Y
	c.shelf = findBooks()
	if len(c.shelf) == 0 {
		pn.line(c.pf.body, pgText, "no books yet")
		pn.line(c.pf.small, pgMuted, "copy .epub files from the PC:  condor books C:\\path\\book.epub")
		pn.line(c.pf.small, pgMuted, "or put them in a Books folder on the microSD card")
		return pn.p
	}
	pn.y += 30
	maxY := c.s.H - c.barH - 40
	for i, b := range c.shelf {
		if pn.y+150 > maxY {
			pn.line(c.pf.small, pgMuted, fmt.Sprintf("... and %d more", len(c.shelf)-i))
			break
		}
		r := image.Rect(pn.mx, pn.y, c.s.W-pn.mx, pn.y+140)
		ui.RoundRect(pn.p.img, r, 22, pgCard)
		pn.text(c.pf.bold, pgText, r.Min.X+36, r.Min.Y+60, clip(c.pf.bold, b.title, r.Dx()-72))
		sub := b.author
		if pr, ok := c.lib.Progress[b.path]; ok {
			sub = strings.TrimSpace(fmt.Sprintf("%s   · chapter %d", sub, pr.Chapter+1))
		}
		pn.text(c.pf.small, pgMuted, r.Min.X+36, r.Min.Y+110, clip(c.pf.small, sub, r.Dx()-72))
		pn.p.buttons = append(pn.p.buttons, button{fmt.Sprintf("book%d", i), r})
		pn.y += 160
	}
	return pn.p
}

// clip shortens s with "..." to fit width pixels.
func clip(f font.Face, s string, width int) string {
	if font.MeasureString(f, s).Ceil() <= width {
		return s
	}
	for s != "" && font.MeasureString(f, s+"...").Ceil() > width {
		_, n := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-n]
	}
	return s + "..."
}
