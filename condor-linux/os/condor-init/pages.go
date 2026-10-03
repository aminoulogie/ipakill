package main

import (
	"encoding/json"
	"image"
	"image/color"
	"os"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/gomonobold"
	"golang.org/x/image/font/opentype"

	"condor-init/ui"
)

// Full-screen pages (the launcher and settings) drawn under the status bar. A page is an
// image plus the buttons on it; taps are matched against the buttons when the finger lifts.

type mode int

const (
	modeLauncher mode = iota
	modeTerminal
	modeSettings
)

type button struct {
	id string
	r  image.Rectangle // page coordinates
}

type page struct {
	img     *image.RGBA
	buttons []button
}

func (p *page) hit(x, y int) string {
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
	reg, err := opentype.Parse(gomono.TTF)
	if err != nil {
		return nil, err
	}
	bold, err := opentype.Parse(gomonobold.TTF)
	if err != nil {
		return nil, err
	}
	mk := func(f *opentype.Font, size float64) font.Face {
		face, _ := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
		return face
	}
	return &pageFonts{title: mk(bold, 64), body: mk(reg, 36), bold: mk(bold, 38), small: mk(reg, 28)}, nil
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
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	ui.Fill(img, img.Rect, pgBG)
	return &pen{p: &page{img: img}, f: f, W: w, mx: 48}
}

func (pn *pen) text(face font.Face, c color.Color, x, baseline int, s string) {
	ui.DrawText(pn.p.img, face, x, baseline, c, s)
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
	ui.DrawTextCentered(pn.p.img, pn.f.bold, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, fg, label)
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
