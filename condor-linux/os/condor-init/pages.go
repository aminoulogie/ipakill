package main

import (
	"encoding/json"
	"image"
	"image/color"
	"os"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"

	"github.com/go-fonts/dejavu/dejavusans"
	"github.com/go-fonts/dejavu/dejavusansbold"

	"condor-init/fonts"
	"condor-init/ui"
)

// Full-screen pages (the launcher and settings) drawn under the status bar. A page is an
// image plus the buttons on it; taps are matched against the buttons when the finger lifts.

type mode int

const (
	modeBooksHome mode = iota // where condor starts: the Books app is the whole system
	modeTerminal
	modeSettings
	modeBooks  // the shelf
	modeReader // a book open
	modeStore  // free books to download
	modeWords  // the word book
)

type button struct {
	id string
	r  image.Rectangle // page coordinates
}

type page struct {
	img     *image.RGBA
	buttons []button
	header  int                 // rows at the top that stay put when the page scrolls (the tab bar)
	rows    []hrow              // rows that scroll sideways (scroll.go)
	rowBtns map[string][]button // their buttons, replaced when a row moves
}

func (p *page) hit(x, y int) string {
	for _, btns := range p.rowBtns {
		for _, b := range btns {
			if (image.Point{x, y}).In(b.r) {
				return b.id
			}
		}
	}
	for _, b := range p.buttons {
		if (image.Point{x, y}).In(b.r) {
			return b.id
		}
	}
	return ""
}

// Theme: dark, monospace, like the console.
var (
	pgBG     = color.RGBA{29, 31, 33, 255}
	pgCard   = color.RGBA{40, 42, 46, 255}
	pgBtn    = color.RGBA{55, 59, 65, 255}
	pgSel    = color.RGBA{129, 162, 190, 255}
	pgWarn   = color.RGBA{204, 102, 102, 255}
	pgText   = color.RGBA{220, 223, 221, 255}
	pgMuted  = color.RGBA{130, 138, 146, 255}
	pgAccent = color.RGBA{181, 189, 104, 255}
	pgDark   = color.RGBA{20, 20, 24, 255}
)

type pageFonts struct{ title, body, bold, small font.Face }

func loadPageFonts() (*pageFonts, error) {
	reg, err := opentype.Parse(fonts.InterRegular)
	if err != nil {
		return nil, err
	}
	bold, err := opentype.Parse(fonts.InterSemiBold)
	if err != nil {
		return nil, err
	}
	// Go Mono, with DejaVu Sans for what it lacks (Arabic titles in the store and on the shelf).
	sans, sansBold := parseFont("dejavusans", dejavusans.TTF), parseFont("dejavusansbold", dejavusansbold.TTF)
	mk := func(f *opentype.Font, size float64) font.Face {
		fb := sans
		if f == bold {
			fb = sansBold
		}
		face, _ := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
		fbFace, _ := opentype.NewFace(fb, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
		return ui.Cache(ui.Fallback([]font.Face{face, fbFace}, []*sfnt.Font{f, fb}))
	}
	return &pageFonts{title: mk(bold, 64), body: mk(reg, 36), bold: mk(bold, 38), small: mk(reg, 28)}, nil
}

// pageCanvas is reused for every page: a fresh 9 MB image per tap kept the garbage collector
// busy on the tablet. Pages are drawn and blitted under drawMu, one at a time.
var pageCanvas *image.RGBA

func canvas(w, h int) *image.RGBA {
	if pageCanvas == nil || pageCanvas.Rect.Dx() != w || pageCanvas.Rect.Dy() != h {
		pageCanvas = image.NewRGBA(image.Rect(0, 0, w, h))
	}
	return pageCanvas
}

// pen is a small helper for laying out text and buttons on a page.
type pen struct {
	p  *page
	f  *pageFonts
	W  int
	y  int // next baseline
	mx int // left margin
}

func newPen(w, h int, f *pageFonts) *pen {
	img := canvas(w, h)
	ui.Fill(img, img.Rect, pgBG)
	return &pen{p: &page{img: img}, f: f, W: w, mx: 48}
}

func (pn *pen) text(face font.Face, c color.Color, x, baseline int, s string) {
	ui.DrawText(pn.p.img, face, x, baseline, c, visual(s))
}

// heading writes a section title and moves down.
func (pn *pen) heading(s string) {
	pn.y += 70
	pn.text(pn.f.small, pgAccent, pn.mx, pn.y, s)
	pn.y += 16
}

// line writes a line of body text and moves down.
func (pn *pen) line(face font.Face, c color.Color, s string) {
	pn.y += 52
	pn.text(face, c, pn.mx, pn.y, s)
}

// btn draws a button at r with a centred label and registers it.
func (pn *pen) btn(id, label string, r image.Rectangle, bg, fg color.RGBA) {
	ui.RoundRect(pn.p.img, r, 18, bg)
	ui.DrawTextCentered(pn.p.img, pn.f.bold, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, fg, visual(label))
	pn.p.buttons = append(pn.p.buttons, button{id, r})
}

// row lays out labels as equal buttons across the page at the current position; selected is
// highlighted (in red when warn is set).
func (pn *pen) row(ids, labels []string, selected string, warn bool) {
	pn.y += 24
	n := len(ids)
	gap := 20
	w := (pn.W - 2*pn.mx - gap*(n-1)) / n
	for i := range ids {
		x := pn.mx + i*(w+gap)
		bg, fg := pgBtn, pgText
		if ids[i] == selected {
			bg, fg = pgSel, pgDark
			if warn { // a dangerous action waiting for its confirming tap
				bg = pgWarn
			}
		}
		pn.btn(ids[i], labels[i], image.Rect(x, pn.y, x+w, pn.y+100), bg, fg)
	}
	pn.y += 100
}

// settings stored in /data/condor/settings.json.
type savedSettings struct {
	Brightness int `json:"brightness"`
	ScreenOff  int `json:"screen_off_minutes"` // 0 = never
	// Animations: off by default. Without a GPU (the SGX only works through Android's
	// drivers) every frame is pushed by the CPU, so instant changes, as on e-readers, feel
	// fastest. Settings > Display & Brightness turns them on.
	Animations bool `json:"animations"`
	// Light: the light look (Apple Books' white). Dark, like a Kindle's dark mode, is the
	// default.
	Light bool `json:"light"`
	// NoLock: wake straight into condor, without the lock screen (cover, time).
	NoLock bool `json:"no_lock_screen"`
	// SmoothScroll: pages follow the finger. Off (the default): a swipe jumps a screen.
	SmoothScroll bool `json:"smooth_scroll"`
}

const settingsPath = condorHome + "/settings.json"

func loadSettings() savedSettings {
	s := savedSettings{Brightness: 80, ScreenOff: 5}
	if b, err := os.ReadFile(settingsPath); err == nil {
		json.Unmarshal(b, &s)
	}
	s.Brightness = min(max(s.Brightness, 10), 100)
	return s
}

func (s savedSettings) save() {
	b, _ := json.MarshalIndent(s, "", "  ")
	os.MkdirAll(condorHome, 0o755)
	os.WriteFile(settingsPath, b, 0o644)
}
