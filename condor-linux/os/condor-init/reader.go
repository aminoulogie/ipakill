package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"

	"condor-init/epub"
	"condor-init/ui"
)

// The Books app: a shelf of the EPUBs found on the tablet, and a paged reader. Themes and
// defaults follow Soma's reader (src/lib/reader-prefs.ts): paper, sepia, night (the
// default), line height 1.7. Progress is remembered per book as (chapter, first word on the
// page), so it survives a change of text size.

// bookDirs are searched for .epub files (two levels deep).
var bookDirs = []string{
	"/data/media/0/Books", "/data/media/Books", // internal storage (condor books puts them here)
	"/storage/sdcard_ext/Books", "/storage/sdcard1/Books", "/storage/sdcard_ext", // microSD
	alpineRoot + "/root/books", condorHome + "/books",
}

type readerTheme struct {
	name                  string
	bg, fg, strong, faint color.RGBA
}

// From Soma's READER_THEMES.
var readerThemes = []readerTheme{
	{"night", rgb(0x0b0b0d), rgb(0xe8e6e2), rgb(0xffffff), rgb(0x77736c)},
	{"paper", rgb(0xf8f6f1), rgb(0x1b1a17), rgb(0x000000), rgb(0x8a8578)},
	{"sepia", rgb(0xf2e6ce), rgb(0x3b2f1e), rgb(0x1f1708), rgb(0x9a8a6d)},
}

func rgb(v uint32) color.RGBA { return color.RGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 255} }

const (
	textSizeMin, textSizeMax, textSizeDefault = 26, 60, 38 // px on this 1200-wide screen
	lineHeight                                = 1.7        // Soma's default
	readerMargin                              = 72
	readerToolbarH                            = 120
	readerFooterH                             = 80
)

type readerPrefs struct {
	Theme int `json:"theme"`
	Size  int `json:"size"`
}

type bookProgress struct {
	Chapter int `json:"chapter"`
	Word    int `json:"word"`
}

// library is saved in /data/condor/books.json.
type library struct {
	Prefs    readerPrefs             `json:"prefs"`
	Progress map[string]bookProgress `json:"progress"`
}

const libraryPath = condorHome + "/books.json"

func loadLibrary() *library {
	l := &library{Prefs: readerPrefs{Theme: 0, Size: textSizeDefault}, Progress: map[string]bookProgress{}}
	if b, err := os.ReadFile(libraryPath); err == nil {
		json.Unmarshal(b, l)
	}
	if l.Progress == nil {
		l.Progress = map[string]bookProgress{}
	}
	l.Prefs.Size = min(max(l.Prefs.Size, textSizeMin), textSizeMax)
	l.Prefs.Theme = min(max(l.Prefs.Theme, 0), len(readerThemes)-1)
	return l
}

func (l *library) save() {
	b, _ := json.MarshalIndent(l, "", "  ")
	os.MkdirAll(condorHome, 0o755)
	os.WriteFile(libraryPath, b, 0o644)
}

type shelfBook struct {
	path, title, author string
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
			sb := shelfBook{path: p, title: strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))}
			if b, err := epub.Open(p); err == nil {
				sb.title, sb.author = b.Title, b.Author
				b.Close()
			}
			books = append(books, sb)
		}
	}
	for _, d := range bookDirs {
		scan(d, 1)
	}
	sort.Slice(books, func(i, j int) bool { return strings.ToLower(books[i].title) < strings.ToLower(books[j].title) })
	return books
}

// rline is one laid-out line of a chapter.
type rline struct {
	text      string
	heading   bool
	gapBefore int // extra pixels above (paragraph and heading spacing)
	word      int // index of its first word in the chapter
}

// openBook is the book being read.
type openBook struct {
	path    string
	b       *epub.Book
	chapter int
	pages   [][]rline
	page    int
	chTitle string
}

type readerFonts struct {
	size       int
	body, bold font.Face
}

func newReaderFonts(size int) *readerFonts {
	reg, _ := opentype.Parse(goregular.TTF)
	bold, _ := opentype.Parse(gobold.TTF)
	mk := func(f *opentype.Font, s float64) font.Face {
		face, _ := opentype.NewFace(f, &opentype.FaceOptions{Size: s, DPI: 72, Hinting: font.HintingFull})
		return face
	}
	return &readerFonts{size: size, body: mk(reg, float64(size)), bold: mk(bold, float64(size)*1.25)}
}

// layoutChapter wraps a chapter's paragraphs to the page width and cuts them into pages.
func layoutChapter(blocks []epub.Block, f *readerFonts, width, height int) [][]rline {
	lh := int(float64(f.size) * lineHeight)
	var lines []rline
	word := 0
	for i, blk := range blocks {
		face := f.body
		gap := lh / 3
		if blk.Heading {
			face, gap = f.bold, lh
		}
		if i == 0 {
			gap = 0
		}
		words := strings.Fields(blk.Text)
		cur, first := "", word
		for _, w := range words {
			try := w
			if cur != "" {
				try = cur + " " + w
			}
			if cur != "" && font.MeasureString(face, try).Ceil() > width {
				lines = append(lines, rline{cur, blk.Heading, gap, first})
				gap, cur, first = 0, w, word
			} else {
				cur = try
			}
			word++
		}
		if cur != "" {
			lines = append(lines, rline{cur, blk.Heading, gap, first})
		}
	}
	var pages [][]rline
	var pg []rline
	y := 0
	for _, l := range lines {
		h := lh + l.gapBefore
		if len(pg) > 0 && y+h > height {
			pages = append(pages, pg)
			pg, y = nil, 0
			l.gapBefore, h = 0, lh
		}
		if len(pg) == 0 {
			l.gapBefore, h = 0, lh
		}
		pg = append(pg, l)
		y += h
	}
	if len(pg) > 0 {
		pages = append(pages, pg)
	}
	return pages
}

func (c *console) textArea() (w, h int) {
	return c.s.W - 2*readerMargin, c.s.H - c.barH - readerToolbarH - readerFooterH - 40
}

// loadChapter lays out chapter i and moves to the page holding word (or the last page if
// word < 0). Empty chapters are skipped in the direction of travel.
func (c *console) loadChapter(i, word int) bool {
	ob := c.book
	dir := 1
	if word < 0 {
		dir = -1
	}
	for ; i >= 0 && i < len(ob.b.Chapters); i += dir {
		x, err := ob.b.ReadFile(ob.b.Chapters[i].Path)
		if err != nil {
			continue
		}
		w, h := c.textArea()
		pages := layoutChapter(epub.ChapterText(x), c.rf, w, h)
		if len(pages) == 0 {
			continue
		}
		ob.chapter, ob.pages = i, pages
		ob.chTitle = epub.ChapterTitle(x, fmt.Sprintf("chapter %d", i+1))
		ob.page = 0
		if word < 0 {
			ob.page = len(pages) - 1
		} else {
			for p, pg := range pages {
				if pg[0].word <= word {
					ob.page = p
				}
			}
		}
		return true
	}
	return false
}

func (c *console) openBookAt(path string) {
	b, err := epub.Open(path)
	if err != nil {
		log.Printf("books: %s: %v", path, err)
		return
	}
	if c.book != nil {
		c.book.b.Close()
	}
	c.book = &openBook{path: path, b: b}
	pr := c.lib.Progress[path]
	if !c.loadChapter(min(pr.Chapter, len(b.Chapters)-1), pr.Word) && !c.loadChapter(0, 0) {
		log.Printf("books: %s has no readable text", path)
		b.Close()
		c.book = nil
		return
	}
	c.setMode(modeReader)
}

// turn moves one page forward (+1) or back (-1), across chapters. Caller holds drawMu.
func (c *console) turn(dir int) {
	ob := c.book
	if ob == nil {
		return
	}
	switch {
	case dir > 0 && ob.page < len(ob.pages)-1:
		ob.page++
	case dir < 0 && ob.page > 0:
		ob.page--
	case dir > 0:
		if !c.loadChapter(ob.chapter+1, 0) {
			return // last page of the book
		}
	default:
		if ob.chapter == 0 || !c.loadChapter(ob.chapter-1, -1) {
			return
		}
	}
	c.saveProgress()
	c.showPage()
}

func (c *console) saveProgress() {
	ob := c.book
	c.lib.Progress[ob.path] = bookProgress{ob.chapter, ob.pages[ob.page][0].word}
	c.lib.save()
}

// relayout re-paginates after a text size change, keeping the first word on the page.
func (c *console) relayout() {
	ob := c.book
	word := ob.pages[ob.page][0].word
	c.rf = newReaderFonts(c.lib.Prefs.Size)
	c.loadChapter(ob.chapter, word)
}

func (c *console) progressPct() int {
	ob := c.book
	n := len(ob.b.Chapters)
	if n == 0 {
		return 0
	}
	return int(100 * (float64(ob.chapter) + float64(ob.page+1)/float64(len(ob.pages))) / float64(n))
}

// shelfPage lists the books.
func (c *console) shelfPage() *page {
	pn := newPen(c.s.W, c.s.H-c.barH, c.pf)
	pn.btn("home", "< home", image.Rect(pn.mx-12, 24, pn.mx+260, 124), pgBtn, pgText)
	pn.y = 230
	pn.text(c.pf.title, pgText, pn.mx, pn.y, "books")
	c.shelf = findBooks()
	if len(c.shelf) == 0 {
		pn.line(c.pf.body, pgText, "no books yet")
		pn.line(c.pf.small, pgMuted, "copy .epub files from the PC:  condor books C:\\path\\book.epub")
		pn.line(c.pf.small, pgMuted, "or put them in a Books folder on the microSD card")
		return pn.p
	}
	pn.y += 40
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

// readerPage draws the current page: toolbar, text, footer; taps on the left third go back,
// elsewhere forward.
func (c *console) readerPage() *page {
	ob := c.book
	th := readerThemes[c.lib.Prefs.Theme]
	h := c.s.H - c.barH
	img := image.NewRGBA(image.Rect(0, 0, c.s.W, h))
	ui.Fill(img, img.Rect, th.bg)
	p := &page{img: img}

	// Toolbar: shelf, smaller, bigger, theme.
	ids := []string{"shelf", "smaller", "bigger", "theme"}
	labels := []string{"< shelf", "A-", "A+", th.name}
	gap, mx := 16, 24
	bw := (c.s.W - 2*mx - gap*(len(ids)-1)) / len(ids)
	btnBG := blend(th.bg, th.fg, 0.12)
	for i, id := range ids {
		r := image.Rect(mx+i*(bw+gap), 16, mx+i*(bw+gap)+bw, readerToolbarH-12)
		ui.RoundRect(img, r, 16, btnBG)
		ui.DrawTextCentered(img, c.pf.bold, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, th.fg, labels[i])
		p.buttons = append(p.buttons, button{id, r})
	}

	// Text.
	lh := int(float64(c.rf.size) * lineHeight)
	asc := c.rf.body.Metrics().Ascent.Ceil()
	y := readerToolbarH + 30
	for _, l := range ob.pages[ob.page] {
		y += l.gapBefore
		face, col := c.rf.body, th.fg
		if l.heading {
			face, col = c.rf.bold, th.strong
		}
		ui.DrawText(img, face, readerMargin, y+asc, col, l.text)
		y += lh
	}

	// Footer: chapter title, page in chapter, whole-book progress.
	right := fmt.Sprintf("%d / %d   %d%%", ob.page+1, len(ob.pages), c.progressPct())
	fy := h - readerFooterH/2 + 10
	ui.DrawText(img, c.pf.small, c.s.W-readerMargin-ui.TextWidth(c.pf.small, right), fy, th.faint, right)
	ui.DrawText(img, c.pf.small, readerMargin, fy, th.faint,
		clip(c.pf.small, ob.chTitle, c.s.W-2*readerMargin-ui.TextWidth(c.pf.small, right)-40))

	// Page turns: the left third goes back, the rest forward (below the toolbar).
	third := c.s.W / 3
	p.buttons = append(p.buttons,
		button{"prev", image.Rect(0, readerToolbarH, third, h)},
		button{"next", image.Rect(third, readerToolbarH, c.s.W, h)})
	return p
}

func blend(a, b color.RGBA, t float64) color.RGBA {
	m := func(x, y uint8) uint8 { return uint8(float64(x)*(1-t) + float64(y)*t) }
	return color.RGBA{m(a.R, b.R), m(a.G, b.G), m(a.B, b.B), 255}
}

// readerTap handles the reader's and shelf's buttons. Caller holds drawMu.
func (c *console) readerTap(id string) bool {
	switch {
	case id == "books":
		c.setMode(modeBooks)
	case strings.HasPrefix(id, "book"):
		var i int
		if _, err := fmt.Sscanf(id, "book%d", &i); err == nil && i < len(c.shelf) {
			c.openBookAt(c.shelf[i].path)
		}
	case id == "shelf":
		c.setMode(modeBooks)
	case id == "prev":
		c.turn(-1)
	case id == "next":
		c.turn(+1)
	case id == "smaller" || id == "bigger":
		step := 4
		if id == "smaller" {
			step = -4
		}
		c.lib.Prefs.Size = min(max(c.lib.Prefs.Size+step, textSizeMin), textSizeMax)
		c.lib.save()
		c.relayout()
		c.saveProgress()
		c.showPage()
	case id == "theme":
		c.lib.Prefs.Theme = (c.lib.Prefs.Theme + 1) % len(readerThemes)
		c.lib.save()
		c.showPage()
	default:
		return false
	}
	return true
}
