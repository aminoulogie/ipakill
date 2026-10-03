package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"log"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-fonts/dejavu/dejavusans"
	"github.com/go-fonts/dejavu/dejavusansbold"
	"github.com/go-fonts/dejavu/dejavuserif"
	"github.com/go-fonts/dejavu/dejavuserifbold"
	"github.com/go-fonts/latin-modern/lmroman10bold"
	"github.com/go-fonts/latin-modern/lmroman12regular"
	"github.com/go-fonts/liberation/liberationserifbold"
	"github.com/go-fonts/liberation/liberationserifregular"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"

	"condor-init/epub"
	"condor-init/ui"
)

// The Books reader, after Soma's (src/components/BookReader.tsx and src/lib: reader-prefs,
// lines, marks, word-capture, lookup, translate, review-queue, reading-goal):
//   - themes paper / sepia / night with Soma's colours, five fonts, text size, line spacing,
//     margins, justified text, Arabic shaped and set right to left
//   - line by line: the page dims except the lit line; a tap anywhere moves to the next
//     line, a tap in the left sixth to the line before, a long press jumps to that line; past
//     the last line the page turns
//   - long press a word to select it (drag to take in more), then: highlight in one of
//     Soma's five colours, look it up, translate it, keep it in the word book
//   - contents, the book's highlights, reading time against a daily goal
// Progress is (chapter, first word of the page or lit line), so it survives any change of
// size or font. Pages are drawn once into a cache (normal and dimmed), so moving the lit
// line only redraws two strips of the screen.

type readerTheme struct {
	name                  string
	bg, fg, strong, faint color.RGBA
	mark                  color.RGBA // the lit line and a selection
	markA                 float64
	dark                  bool
}

// From Soma's READER_THEMES (night first: it was this tablet's default before paper/sepia).
var readerThemes = []readerTheme{
	{"night", rgb(0x0b0b0d), rgb(0xe8e6e2), rgb(0xffffff), rgb(0x77736c), color.RGBA{120, 170, 255, 255}, 0.30, true},
	{"paper", rgb(0xf8f6f1), rgb(0x1b1a17), rgb(0x000000), rgb(0x8a8578), color.RGBA{255, 214, 10, 255}, 0.42, false},
	{"sepia", rgb(0xf2e6ce), rgb(0x3b2f1e), rgb(0x1f1708), rgb(0x9a8a6d), color.RGBA{214, 138, 10, 255}, 0.34, false},
}

func rgb(v uint32) color.RGBA { return color.RGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 255} }

const (
	textSizeMin, textSizeMax, textSizeDefault   = 26, 60, 38    // px on this 1200-wide screen
	lineHeightMin, lineHeightMax, lineHeightDef = 1.2, 2.2, 1.7 // Soma's LINE_HEIGHT
	marginMin, marginMax, marginDef             = 24, 168, 72   // Soma's MARGIN, scaled to this screen
	readerToolbarH                              = 120
	readerFooterH                               = 80
	textTopPad                                  = 30  // between the toolbar and the first line
	dimAmount                                   = 0.8 // Soma: the page under a lit line, at 0.8 opacity
)

type readerFont struct {
	id, label string
	reg, bold []byte
}

// readerFontList: free fonts in the spirit of Soma's (which are Apple's and can't ship here).
var readerFontList = []readerFont{
	{"serif", "Serif", liberationserifregular.TTF, liberationserifbold.TTF},
	{"book", "Book", lmroman12regular.TTF, lmroman10bold.TTF},
	{"classic", "Classic", dejavuserif.TTF, dejavuserifbold.TTF},
	{"sans", "Sans", dejavusans.TTF, dejavusansbold.TTF},
	{"go", "Go", goregular.TTF, gobold.TTF},
}

func fontIndex(id string) int {
	for i, f := range readerFontList {
		if f.id == id {
			return i
		}
	}
	return 0
}

type readerPrefs struct {
	Theme        int     `json:"theme"`
	Size         int     `json:"size"`
	Font         string  `json:"font"`
	LineHeight   float64 `json:"line_height"`
	Margin       int     `json:"margin"`
	LineFocus    bool    `json:"line_focus"`
	TranslateTo  string  `json:"translate_to"`
	GoalMinutes  int     `json:"goal_minutes"`
	BooksPerYear int     `json:"books_per_year"`
}

type bookProgress struct {
	Chapter int `json:"chapter"`
	Word    int `json:"word"`
}

// library is saved in /data/condor/books.json.
type library struct {
	Prefs    readerPrefs             `json:"prefs"`
	Progress map[string]bookProgress `json:"progress"`
	Marks    map[string][]bookMark   `json:"marks"`    // highlights, by book path
	Finished map[string]string       `json:"finished"` // book path -> date finished
	Reading  map[string]int          `json:"reading"`  // date -> seconds read
}

const libraryPath = condorHome + "/books.json"

func loadLibrary() *library {
	l := &library{Prefs: readerPrefs{Size: textSizeDefault}}
	if b, err := os.ReadFile(libraryPath); err == nil {
		json.Unmarshal(b, l)
	}
	if l.Progress == nil {
		l.Progress = map[string]bookProgress{}
	}
	if l.Marks == nil {
		l.Marks = map[string][]bookMark{}
	}
	if l.Finished == nil {
		l.Finished = map[string]string{}
	}
	if l.Reading == nil {
		l.Reading = map[string]int{}
	}
	p := &l.Prefs
	if p.Size == 0 {
		p.Size = textSizeDefault
	}
	p.Size = min(max(p.Size, textSizeMin), textSizeMax)
	p.Theme = min(max(p.Theme, 0), len(readerThemes)-1)
	p.Font = readerFontList[fontIndex(p.Font)].id
	if p.LineHeight == 0 {
		p.LineHeight = lineHeightDef
	}
	p.LineHeight = math.Round(min(max(p.LineHeight, lineHeightMin), lineHeightMax)*10) / 10
	if p.Margin == 0 {
		p.Margin = marginDef
	}
	p.Margin = min(max(p.Margin, marginMin), marginMax)
	if !isLanguage(p.TranslateTo) {
		p.TranslateTo = "fr" // Soma's default: a reader of English books here wants it out of English
	}
	if p.GoalMinutes == 0 {
		p.GoalMinutes = 30 // Soma's DEFAULT_GOAL_MIN
	}
	if p.BooksPerYear == 0 {
		p.BooksPerYear = 12 // Soma's DEFAULT_BOOKS_PER_YEAR
	}
	return l
}

func (l *library) save() {
	b, _ := json.MarshalIndent(l, "", "  ")
	os.MkdirAll(condorHome, 0o755)
	tmp := libraryPath + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		os.Rename(tmp, libraryPath)
	}
}

// --- fonts -----------------------------------------------------------------------------

type readerFonts struct {
	font       string
	size, lh   int // text size and line height in px
	body, bold font.Face
}

var (
	parsedMu    sync.Mutex
	parsedFonts = map[string]*opentype.Font{}
	readerFaces = map[string]*readerFonts{}
)

func parseFont(key string, data []byte) *opentype.Font {
	parsedMu.Lock()
	defer parsedMu.Unlock()
	if f := parsedFonts[key]; f != nil {
		return f
	}
	f, err := opentype.Parse(data)
	if err != nil {
		log.Printf("font %s: %v", key, err)
		f, _ = opentype.Parse(goregular.TTF)
	}
	parsedFonts[key] = f
	return f
}

// textFace is a face of the font at size, falling back to DejaVu Sans (Arabic and more).
func textFace(key string, data []byte, bold bool, size float64) font.Face {
	primary := parseFont(key, data)
	fbKey, fbData := "dejavusans", dejavusans.TTF
	if bold {
		fbKey, fbData = "dejavusansbold", dejavusansbold.TTF
	}
	fallback := parseFont(fbKey, fbData)
	mk := func(f *opentype.Font) font.Face {
		face, _ := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
		return face
	}
	return ui.Cache(ui.Fallback([]font.Face{mk(primary), mk(fallback)}, []*sfnt.Font{primary, fallback}))
}

// newReaderFonts makes (once) the faces for a font, size and line spacing.
func newReaderFonts(fontID string, size int, lineHeight float64) *readerFonts {
	key := fmt.Sprintf("%s/%d/%.1f", fontID, size, lineHeight)
	if rf := readerFaces[key]; rf != nil {
		return rf
	}
	f := readerFontList[fontIndex(fontID)]
	rf := &readerFonts{font: f.id, size: size, lh: int(math.Round(float64(size) * lineHeight)),
		body: textFace(f.id, f.reg, false, float64(size)), bold: textFace(f.id+"-bold", f.bold, true, float64(size)*1.25)}
	readerFaces[key] = rf
	return rf
}

func (c *console) readerFontsNow() *readerFonts {
	p := c.lib.Prefs
	return newReaderFonts(p.Font, p.Size, p.LineHeight)
}

// --- layout ------------------------------------------------------------------------------

// rline is one laid-out line of a chapter, placed on its page.
type rline struct {
	tline
	heading bool
	block   int
	y, h    int // top and height within the text area
	word    int // index of its first word in the chapter
}

func (l rline) last() int { return l.words[len(l.words)-1].idx }

// openBook is the book being read.
type openBook struct {
	path     string
	b        *epub.Book
	chapter  int
	blocks   []epub.Block
	words    []string // the chapter's words, in order
	blockOf  []int    // words[i] is in blocks[blockOf[i]]
	pages    [][]rline
	page     int
	line     int // the lit line on the page (line by line)
	chTitle  string
	titles   []string // contents, filled in the background
	finished bool     // just turned past the last page
}

// layoutChapter wraps a chapter's paragraphs to the page and cuts them into pages.
func layoutChapter(blocks []epub.Block, f *readerFonts, width, height int) (pages [][]rline, words []string, blockOf []int) {
	var lines []rline
	for bi, blk := range blocks {
		ws := strings.Fields(blk.Text)
		face, gap, lh := f.body, f.lh/3, f.lh
		if blk.Heading {
			face, gap, lh = f.bold, f.lh, int(float64(f.lh)*1.25)
		}
		for i, tl := range layoutWords(face, ws, len(words), width, !blk.Heading) {
			g := 0
			if i == 0 && len(lines) > 0 {
				g = gap
			}
			lines = append(lines, rline{tline: tl, heading: blk.Heading, block: bi, y: g, h: lh, word: tl.words[0].idx})
		}
		for range ws {
			blockOf = append(blockOf, bi)
		}
		words = append(words, ws...)
	}
	var pg []rline
	y := 0
	for _, l := range lines {
		gap := l.y
		if len(pg) == 0 {
			gap = 0
		}
		if len(pg) > 0 && y+gap+l.h > height {
			pages = append(pages, pg)
			pg, y, gap = nil, 0, 0
		}
		l.y = y + gap
		pg = append(pg, l)
		y = l.y + l.h
	}
	if len(pg) > 0 {
		pages = append(pages, pg)
	}
	return pages, words, blockOf
}

// textRect is the text column on the reader page (page coordinates).
func (c *console) textRect() image.Rectangle {
	m := c.lib.Prefs.Margin
	return image.Rect(m, readerToolbarH+textTopPad, c.s.W-m, c.s.H-c.barH-readerFooterH-10)
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
		blocks := epub.ChapterText(x)
		tr := c.textRect()
		pages, words, blockOf := layoutChapter(blocks, c.rf, tr.Dx(), tr.Dy())
		if len(pages) == 0 {
			continue
		}
		ob.chapter, ob.pages, ob.blocks, ob.words, ob.blockOf = i, pages, blocks, words, blockOf
		ob.chTitle = epub.ChapterTitle(x, fmt.Sprintf("chapter %d", i+1))
		ob.page, ob.line = 0, 0
		if word < 0 {
			ob.page = len(pages) - 1
			ob.line = len(pages[ob.page]) - 1
		} else {
			ob.page, ob.line = findWord(pages, word)
		}
		c.invalidatePage()
		return true
	}
	return false
}

// findWord is the page and line holding a chapter's word.
func findWord(pages [][]rline, word int) (page, line int) {
	for p, pg := range pages {
		for li, l := range pg {
			if l.word <= word {
				page, line = p, li
			}
		}
	}
	return page, line
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
	c.rd = readerUI{}
	c.rf = c.readerFontsNow()
	c.book = &openBook{path: path, b: b}
	pr := c.lib.Progress[path]
	if !c.loadChapter(min(pr.Chapter, len(b.Chapters)-1), pr.Word) && !c.loadChapter(0, 0) {
		log.Printf("books: %s has no readable text", path)
		b.Close()
		c.book = nil
		return
	}
	c.lastRead = time.Now()
	go c.loadTitles(c.book)
	c.setMode(modeReader)
}

// loadTitles reads each chapter's title for the contents, in the background.
func (c *console) loadTitles(ob *openBook) {
	titles := make([]string, len(ob.b.Chapters))
	for i, ch := range ob.b.Chapters {
		x, err := ob.b.ReadFile(ch.Path)
		if err != nil {
			titles[i] = fmt.Sprintf("chapter %d", i+1)
			continue
		}
		titles[i] = epub.ChapterTitle(x, fmt.Sprintf("chapter %d", i+1))
	}
	drawMu.Lock()
	ob.titles = titles
	if c.book == ob && c.mode == modeReader && c.rd.view == "contents" {
		c.showPage()
	}
	drawMu.Unlock()
}

// turn moves one page forward (+1) or back (-1), across chapters. Caller holds drawMu.
func (c *console) turn(dir int) {
	ob := c.book
	if ob == nil {
		return
	}
	c.readTick()
	c.rd.sel, c.rd.menu, c.rd.panel = nil, menuNone, nil
	switch {
	case dir > 0 && ob.page < len(ob.pages)-1:
		ob.page, ob.line = ob.page+1, 0
	case dir < 0 && ob.page > 0:
		ob.page--
		ob.line = len(ob.pages[ob.page]) - 1
		if !c.lib.Prefs.LineFocus {
			ob.line = 0
		}
	case dir > 0:
		if !c.loadChapter(ob.chapter+1, 0) {
			c.markFinished()
			c.showPage()
			return
		}
	default:
		if !c.loadChapter(ob.chapter-1, -1) {
			return
		}
		if !c.lib.Prefs.LineFocus {
			ob.line = 0
		}
	}
	c.invalidatePage()
	c.saveProgress()
	c.showPage()
}

// stepLine moves the lit line (line by line), turning the page past either end.
func (c *console) stepLine(dir int) {
	ob := c.book
	if ob == nil {
		return
	}
	next := ob.line + dir
	if next < 0 || next >= len(ob.pages[ob.page]) {
		c.turn(dir)
		return
	}
	c.readTick()
	old := ob.line
	ob.line = next
	c.saveProgress()
	c.refreshLines(old, next)
}

func (c *console) saveProgress() {
	ob := c.book
	pg := ob.pages[ob.page]
	w := pg[0].word
	if c.lib.Prefs.LineFocus && ob.line < len(pg) {
		w = pg[ob.line].word
	}
	c.lib.Progress[ob.path] = bookProgress{ob.chapter, w}
	c.lib.save()
}

// relayout re-paginates after a change of size, font, spacing or margins, keeping the
// first word on the page (or the lit line).
func (c *console) relayout() {
	ob := c.book
	pg := ob.pages[ob.page]
	word := pg[0].word
	if c.lib.Prefs.LineFocus && ob.line < len(pg) {
		word = pg[ob.line].word
	}
	c.rf = c.readerFontsNow()
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

// --- drawing -------------------------------------------------------------------------------

// pageCache holds the current page drawn once, normally and dimmed (for line by line).
type pageCache struct {
	key         string
	normal, dim *image.RGBA
}

func (c *console) invalidatePage() { c.pcache.key = "" }

func (c *console) pageKey() string {
	ob, p := c.book, c.lib.Prefs
	return fmt.Sprintf("%s|%d|%d|%d|%s|%d|%.1f|%d|%d|%v|%v", ob.path, ob.chapter, ob.page, p.Theme, p.Font, p.Size,
		p.LineHeight, p.Margin, c.marksVersion, p.LineFocus, ob.finished)
}

func blend(a, b color.RGBA, t float64) color.RGBA {
	m := func(x, y uint8) uint8 { return uint8(float64(x)*(1-t) + float64(y)*t + 0.5) }
	return color.RGBA{m(a.R, b.R), m(a.G, b.G), m(a.B, b.B), 255}
}

// blendRect lays colour c at alpha a over r (text under it stays readable).
func blendRect(img *image.RGBA, r image.Rectangle, c color.RGBA, a float64) {
	r = r.Intersect(img.Rect)
	ai := int(a * 256)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		row := img.Pix[img.PixOffset(r.Min.X, y):][:4*r.Dx()]
		for i := 0; i < len(row); i += 4 {
			row[i] = uint8((int(row[i])*(256-ai) + int(c.R)*ai) >> 8)
			row[i+1] = uint8((int(row[i+1])*(256-ai) + int(c.G)*ai) >> 8)
			row[i+2] = uint8((int(row[i+2])*(256-ai) + int(c.B)*ai) >> 8)
		}
	}
}

// band is the strip a line's text occupies (for its highlight, the lit line, a selection),
// in page coordinates.
func (c *console) band(l rline) image.Rectangle {
	tr := c.textRect()
	face := c.rf.body
	if l.heading {
		face = c.rf.bold
	}
	m := face.Metrics()
	th := (m.Ascent + m.Descent).Ceil()
	top := tr.Min.Y + l.y + (l.h-th)/2 - 6
	return image.Rect(tr.Min.X, top, tr.Max.X, top+th+12)
}

// wordsRect is the box around words [from, to] of a line (both chapter word indices).
func (c *console) wordsRect(l rline, from, to int) (image.Rectangle, bool) {
	b := c.band(l)
	x0, x1 := math.MaxInt, math.MinInt
	for _, w := range l.words {
		if w.idx >= from && w.idx <= to {
			x0, x1 = min(x0, w.x), max(x1, w.x+w.w)
		}
	}
	if x0 > x1 {
		return image.Rectangle{}, false
	}
	return image.Rect(b.Min.X+x0-4, b.Min.Y, b.Min.X+x1+4, b.Max.Y), true
}

// drawText draws the page's lines (with their highlights) into img; dim fades everything
// toward the paper the way Soma's overlay does.
func (c *console) drawText(img *image.RGBA, th readerTheme, dim bool) {
	ob := c.book
	fade := func(col color.RGBA) color.RGBA {
		if dim {
			return blend(col, th.bg, dimAmount)
		}
		return col
	}
	tr := c.textRect()
	marks := c.lib.Marks[ob.path]
	for _, l := range ob.pages[ob.page] {
		for _, m := range marks {
			if m.Chapter != ob.chapter || m.End < l.word || m.Start > l.last() {
				continue
			}
			if r, ok := c.wordsRect(l, m.Start, m.End); ok {
				mc := markColour(m.Colour)
				a := mc.lightA
				if th.dark {
					a = mc.darkA
				}
				if th.dark {
					ui.Fill(img, r, fade(blend(th.bg, mc.dark, a)))
				} else {
					ui.Fill(img, r, fade(blend(th.bg, mc.light, a)))
				}
			}
		}
		face, col := c.rf.body, th.fg
		if l.heading {
			face, col = c.rf.bold, th.strong
		}
		asc := face.Metrics().Ascent.Ceil()
		top := tr.Min.Y + l.y + (l.h-(face.Metrics().Ascent+face.Metrics().Descent).Ceil())/2
		drawWords(img, face, l.tline, tr.Min.X, top+asc, fade(col))
	}
}

// buildPage draws the page's cache: toolbar, text, footer (normal), and the text dimmed.
func (c *console) buildPage() {
	key := c.pageKey()
	if c.pcache.key == key {
		return
	}
	th := readerThemes[c.lib.Prefs.Theme]
	w, h := c.s.W, c.s.H-c.barH
	if c.pcache.normal == nil || c.pcache.normal.Rect.Dx() != w || c.pcache.normal.Rect.Dy() != h {
		c.pcache.normal = image.NewRGBA(image.Rect(0, 0, w, h))
	}
	img := c.pcache.normal
	ui.Fill(img, img.Rect, th.bg)
	c.drawText(img, th, false)
	c.drawFooter(img, th)
	if c.lib.Prefs.LineFocus {
		if c.pcache.dim == nil || c.pcache.dim.Rect.Dx() != w || c.pcache.dim.Rect.Dy() != h {
			c.pcache.dim = image.NewRGBA(image.Rect(0, 0, w, h))
		}
		ui.Fill(c.pcache.dim, c.pcache.dim.Rect, th.bg)
		c.drawText(c.pcache.dim, th, true)
	}
	c.pcache.key = key
}

func (c *console) drawFooter(img *image.RGBA, th readerTheme) {
	ob := c.book
	h := img.Rect.Dy()
	right := fmt.Sprintf("%d / %d   %d%%", ob.page+1, len(ob.pages), c.progressPct())
	if ob.finished {
		right = "the end   100%"
	}
	m := c.lib.Prefs.Margin
	fy := h - readerFooterH/2 + 10
	title := visual(clip(c.pf.small, ob.chTitle, c.s.W-2*m-ui.TextWidth(c.pf.small, right)-40))
	if isRTL(ob.chTitle) { // an Arabic book: its chapter on the right, the page count on the left
		ui.DrawText(img, c.pf.small, m, fy, th.faint, right)
		ui.DrawText(img, c.pf.small, c.s.W-m-ui.TextWidth(c.pf.small, title), fy, th.faint, title)
		return
	}
	ui.DrawText(img, c.pf.small, c.s.W-m-ui.TextWidth(c.pf.small, right), fy, th.faint, right)
	ui.DrawText(img, c.pf.small, m, fy, th.faint, title)
}

// textRows is the band of rows the text area covers, where dimming applies.
func (c *console) textRows() image.Rectangle {
	return image.Rect(0, readerToolbarH, c.s.W, c.s.H-c.barH-readerFooterH)
}

// copyRows copies rows [r.Min.Y, r.Max.Y) of src into dst (same size images).
func copyRows(dst, src *image.RGBA, r image.Rectangle) {
	r = r.Intersect(dst.Rect)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		o := dst.PixOffset(r.Min.X, y)
		copy(dst.Pix[o:o+4*r.Dx()], src.Pix[o:o+4*r.Dx()])
	}
}

// litRect is the lit line's strip, padded a little (page coordinates).
func (c *console) litRect(li int) image.Rectangle {
	ob := c.book
	if li < 0 || li >= len(ob.pages[ob.page]) {
		return image.Rectangle{}
	}
	b := c.band(ob.pages[ob.page][li])
	return image.Rect(0, b.Min.Y-2, c.s.W, b.Max.Y+2)
}

// readerPage composes the page: cached text, the lit line, a selection, the toolbar and
// whatever is open over it (word menu, result card, settings, contents, highlights).
func (c *console) readerPage() *page {
	if c.rd.view != "" {
		return c.readerListPage()
	}
	ob := c.book
	th := readerThemes[c.lib.Prefs.Theme]
	c.buildPage()
	img := canvas(c.s.W, c.s.H-c.barH)
	copy(img.Pix, c.pcache.normal.Pix)
	if c.lib.Prefs.LineFocus {
		lit := c.litRect(ob.line)
		rows := c.textRows()
		copyRows(img, c.pcache.dim, image.Rect(0, rows.Min.Y, c.s.W, lit.Min.Y))
		copyRows(img, c.pcache.dim, image.Rect(0, lit.Max.Y, c.s.W, rows.Max.Y))
		if !lit.Empty() {
			b := c.band(ob.pages[ob.page][ob.line])
			blendRect(img, image.Rect(b.Min.X-16, b.Min.Y, b.Max.X+16, b.Max.Y), th.mark, th.markA)
		}
	}
	if s := c.rd.sel; s != nil {
		for _, l := range ob.pages[ob.page] {
			if r, ok := c.wordsRect(l, s.from, s.to); ok {
				blendRect(img, r, th.mark, th.markA+0.15)
			}
		}
	}
	p := &page{img: img}
	c.drawOverlays(p, th) // menu, card, settings: their buttons come first
	c.drawToolbar(p, th)
	if c.rd.menu == menuNone && c.rd.panel == nil && !c.rd.settings {
		third := c.s.W / 3
		if c.lib.Prefs.LineFocus {
			third = c.s.W / 6 // Soma: the left sixth steps back a line
		}
		h := c.s.H - c.barH
		p.buttons = append(p.buttons,
			button{"prev", image.Rect(0, readerToolbarH, third, h)},
			button{"next", image.Rect(third, readerToolbarH, c.s.W, h)})
	}
	return p
}

func (c *console) drawToolbar(p *page, th readerTheme) {
	ids := []string{"shelf", "contents", "marks", "linemode", "settings"}
	labels := []string{"< shelf", "contents", "marks", "line", "Aa"}
	if c.fromStore {
		labels[0] = "< store"
	}
	gap, mx := 14, 20
	bw := (c.s.W - 2*mx - gap*(len(ids)-1)) / len(ids)
	ui.Fill(p.img, image.Rect(0, 0, c.s.W, readerToolbarH), th.bg)
	for i, id := range ids {
		r := image.Rect(mx+i*(bw+gap), 16, mx+i*(bw+gap)+bw, readerToolbarH-14)
		bg, fg := blend(th.bg, th.fg, 0.12), th.fg
		if (id == "linemode" && c.lib.Prefs.LineFocus) || (id == "settings" && c.rd.settings) {
			bg, fg = th.fg, th.bg
		}
		ui.RoundRect(p.img, r, 16, bg)
		ui.DrawTextCentered(p.img, c.pf.small, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, fg, labels[i])
		p.buttons = append(p.buttons, button{id, r})
	}
}

// refreshLines redraws only the strips of two lines (moving the lit line). Caller holds drawMu.
func (c *console) refreshLines(a, b int) {
	if !c.screenOn || c.mode != modeReader || c.rd.view != "" {
		return
	}
	ra, rb := c.litRect(a), c.litRect(b)
	if c.page != nil && c.pcache.key == c.pageKey() && c.pcache.dim != nil && !c.overlayOpen() {
		// The page on screen is the cached page with another line lit: put the old line back
		// to dim and light the new one, touching only those two strips.
		img := c.page.img
		th := readerThemes[c.lib.Prefs.Theme]
		copyRows(img, c.pcache.dim, ra)
		copyRows(img, c.pcache.normal, rb)
		if line := c.book.pages[c.book.page]; b >= 0 && b < len(line) {
			bd := c.band(line[b])
			blendRect(img, image.Rect(bd.Min.X-16, bd.Min.Y, bd.Max.X+16, bd.Max.Y), th.mark, th.markA)
		}
	} else {
		c.page = c.readerPage()
	}
	for _, r := range []image.Rectangle{ra, rb} {
		if r.Empty() {
			continue
		}
		c.s.blitRGBA(c.page.img.SubImage(r).(*image.RGBA), 0, c.barH+r.Min.Y)
	}
	c.s.Flush()
}

// --- reading time ------------------------------------------------------------------------

// readTick counts the time since the last thing done in a book as reading, if it was under
// two minutes ago (longer means the tablet was put down). Caller holds drawMu.
func (c *console) readTick() {
	now := time.Now()
	d := now.Sub(c.lastRead)
	c.lastRead = now
	if d > 0 && d < 2*time.Minute {
		c.lib.Reading[now.Format("2006-01-02")] += int(d.Seconds())
	}
}

func (c *console) markFinished() {
	ob := c.book
	ob.finished = true
	if _, ok := c.lib.Finished[ob.path]; !ok {
		c.lib.Finished[ob.path] = time.Now().Format("2006-01-02")
		c.lib.save()
	}
	c.invalidatePage()
}

// readingToday is minutes read today; booksThisYear counts books finished this year.
func (l *library) readingToday() int {
	return l.Reading[time.Now().Format("2006-01-02")] / 60
}

func (l *library) booksThisYear() int {
	y, n := time.Now().Format("2006"), 0
	for _, d := range l.Finished {
		if strings.HasPrefix(d, y) {
			n++
		}
	}
	return n
}
