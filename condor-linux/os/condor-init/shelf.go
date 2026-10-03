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
	cover               string // "epub:<file>#<path in the archive>", or ""
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
		if b.CoverPath != "" {
			sb.cover = "epub:" + p + "#" + b.CoverPath
		}
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

// Library layout (page coordinates).
const (
	libCols  = 4
	libGap   = 36
	libCellW = (1200 - 2*48 - (libCols-1)*libGap) / libCols // 249
	libCover = libCellW * 3 / 2                             // 373
	libCellH = libCover + 70
	libRows  = 2
)

// shelfPage is the Library: the book being read in a "continue reading" card with the
// reading goal, then every book as a cover with how far along it is.
func (c *console) shelfPage() *page {
	f := apple()
	h := c.s.H - c.barH
	img := canvas(c.s.W, h)
	ui.Fill(img, img.Rect, apBG)
	p := &page{img: img}
	mx := 48

	// Top: back to home, and the store.
	iconBack(img, mx, 64, apOrange)
	apText(img, f.body, mx+30, 76, apOrange, "Home")
	p.buttons = append(p.buttons, button{"home", image.Rect(0, 10, 260, 120)})
	apTextRight(img, f.body, c.s.W-mx, 76, apOrange, "Book Store")
	p.buttons = append(p.buttons, button{"store", image.Rect(c.s.W-320, 10, c.s.W, 120)})
	apText(img, f.largeTitle, mx, 200, apLabel, "Library")

	c.shelf = findBooks()
	if len(c.shelf) == 0 {
		apText(img, f.title, mx, 420, apLabel, "Your library is empty")
		y := drawParagraphs(img, f.body, "Get free books from the Book Store, or copy EPUB files to the Books folder from your PC.",
			mx, 460, c.s.W-2*mx, 48, 700, apSecondary)
		r := image.Rect(mx, y+30, mx+420, y+130)
		ui.RoundRect(img, r, 50, apOrange)
		apTextCenter(img, f.headline, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, apBG, "Book Store")
		p.buttons = append(p.buttons, button{"store", r})
		return p
	}
	item := func(b shelfBook) *storeItem {
		return &storeItem{key: b.path, title: b.title, author: b.author, cover: b.cover}
	}

	// Continue reading: the book opened last.
	y := 250
	last, lastAt := -1, ""
	for i, b := range c.shelf {
		if pr, ok := c.lib.Progress[b.path]; ok && pr.Opened > lastAt && c.lib.Finished[b.path] == "" {
			last, lastAt = i, pr.Opened
		}
	}
	if last >= 0 {
		b := c.shelf[last]
		card := image.Rect(mx, y, c.s.W-mx, y+460)
		ui.RoundRect(img, card, 30, apCard)
		cr := image.Rect(card.Min.X+30, card.Min.Y+30, card.Min.X+30+267, card.Min.Y+30+400)
		shadowRect(img, cr)
		c.drawCover(img, cr, item(b))
		x, w := cr.Max.X+40, card.Max.X-cr.Max.X-70
		apText(img, f.captionBold, x, card.Min.Y+70, apOrange, "CONTINUE READING")
		ty := card.Min.Y + 130
		for i, l := range layoutWords(f.headline, strings.Fields(b.title), 0, w, false) {
			if i == 2 {
				break
			}
			drawWords(img, f.headline, l, x, ty, apLabel)
			ty += 46
		}
		apText(img, f.callout, x, ty+6, apSecondary, clip(f.callout, b.author, w))
		pct := c.lib.Progress[b.path].Pct
		bar := image.Rect(x, ty+40, x+w-110, ty+48)
		ui.RoundRect(img, bar, 4, apSeparator)
		if pct > 0 {
			ui.RoundRect(img, image.Rect(bar.Min.X, bar.Min.Y, bar.Min.X+bar.Dx()*pct/100, bar.Max.Y), 4, apLabel)
		}
		apTextRight(img, f.caption, x+w, ty+54, apSecondary, fmt.Sprintf("%d%%", pct))
		p.buttons = append(p.buttons, button{fmt.Sprintf("book%d", last), image.Rect(card.Min.X, card.Min.Y, x+w, ty+60)})

		// Reading goal ring.
		mins, goal := c.lib.readingToday(), c.lib.Prefs.GoalMinutes
		gy := card.Max.Y - 90
		ring(img, x+44, gy, 38, 12, float64(mins)/float64(max(goal, 1)), apSeparator, apOrange)
		apText(img, f.captionBold, x+110, gy-8, apLabel, "Reading Goal")
		apText(img, f.caption, x+110, gy+30, apSecondary,
			fmt.Sprintf("%d of %d min today · %d of %d books this year", mins, goal, c.lib.booksThisYear(), c.lib.Prefs.BooksPerYear))
		p.buttons = append(p.buttons, button{"r:goal", image.Rect(x, gy-50, card.Max.X, gy+50)})
		y = card.Max.Y + 40
	}

	// All books.
	apText(img, f.headline, mx, y+40, apLabel, "All Books")
	apTextRight(img, f.caption, c.s.W-mx, y+40, apSecondary, map[bool]string{true: "1 book", false: fmt.Sprintf("%d books", len(c.shelf))}[len(c.shelf) == 1])
	y += 76
	per := libCols * libRows
	if last < 0 {
		per = libCols * 3
	}
	c.shelfFrom = min(c.shelfFrom, (len(c.shelf)-1)/per*per)
	for i := c.shelfFrom; i < len(c.shelf) && i < c.shelfFrom+per; i++ {
		b := c.shelf[i]
		col, row := (i-c.shelfFrom)%libCols, (i-c.shelfFrom)/libCols
		x, cy := mx+col*(libCellW+libGap), y+row*(libCellH+20)
		cr := image.Rect(x, cy, x+libCellW, cy+libCover)
		shadowRect(img, cr)
		c.drawCover(img, cr, item(b))
		status, col2 := "NEW", rgb(0x0a84ff)
		if pr, ok := c.lib.Progress[b.path]; ok {
			status, col2 = fmt.Sprintf("%d%%", pr.Pct), apSecondary
		}
		if c.lib.Finished[b.path] != "" {
			status, col2 = "FINISHED", apSecondary
		}
		apText(img, f.captionBold, x, cr.Max.Y+44, col2, status)
		p.buttons = append(p.buttons, button{fmt.Sprintf("book%d", i), image.Rect(x, cy, x+libCellW, cy+libCellH)})
	}
	if len(c.shelf) > per {
		by := h - 100
		apText(img, f.body, mx, by+48, apOrange, "‹ Previous")
		p.buttons = append(p.buttons, button{"shelf:prev", image.Rect(0, by, 360, by+90)})
		apTextCenter(img, f.caption, c.s.W/2, by+38, apSecondary,
			fmt.Sprintf("%d–%d of %d", c.shelfFrom+1, min(c.shelfFrom+per, len(c.shelf)), len(c.shelf)))
		apTextRight(img, f.body, c.s.W-mx, by+48, apOrange, "Next ›")
		p.buttons = append(p.buttons, button{"shelf:next", image.Rect(c.s.W-360, by, c.s.W, by+90)})
		c.shelfPer = per
	}
	return p
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
