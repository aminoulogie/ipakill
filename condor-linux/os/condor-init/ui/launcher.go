package ui

import (
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// App is one entry on the home screen.
type App struct {
	ID    string // stable name used by condor-init to start the app
	Name  string
	Glyph string // one or two characters drawn in the icon
	Color color.RGBA
}

// DefaultApps is the whole system: nothing else is installed or running.
var DefaultApps = []App{
	{"books", "Books", "B", color.RGBA{230, 120, 30, 255}},
	{"terminal", "Terminal", ">_", color.RGBA{40, 40, 48, 255}},
	{"files", "Files", "F", color.RGBA{0, 122, 255, 255}},
	{"settings", "Settings", "S", color.RGBA{142, 142, 147, 255}},
}

// Status is what the status bar shows.
type Status struct {
	Time     time.Time
	Battery  int // percent, or -1 if unknown
	Charging bool
}

// Layout constants, in logical pixels on the 1200x1920 portrait screen.
const (
	statusH = 72
	margin  = 56
	titleY  = 260 // baseline of the "condor" title
	listTop = 340
	rowH    = 176
	iconR   = 56
	backH   = 140 // height of the bar with "‹ Home" on app screens
)

// Launcher draws the home screen and app screens and maps taps to actions.
type Launcher struct {
	W, H  int
	Apps  []App
	faces *Faces
}

func NewLauncher(w, h int, apps []App) (*Launcher, error) {
	f, err := LoadFaces()
	if err != nil {
		return nil, err
	}
	return &Launcher{W: w, H: h, Apps: apps, faces: f}, nil
}

func (l *Launcher) canvas() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, l.W, l.H))
	Fill(img, img.Rect, Background)
	return img
}

// statusBar draws the time on the left and the battery on the right.
func (l *Launcher) statusBar(img *image.RGBA, st Status) {
	cy := statusH/2 + 6
	DrawTextCentered(img, l.faces.Status, margin+60, cy, Ink, st.Time.Format("15:04"))
	if st.Battery < 0 {
		return
	}
	// Battery outline with a fill proportional to the charge, then the percentage.
	bw, bh := 64, 30
	bx, by := l.W-margin-bw, cy-bh/2
	RoundRect(img, image.Rect(bx, by, bx+bw, by+bh), 7, Ink)
	RoundRect(img, image.Rect(bx+3, by+3, bx+bw-3, by+bh-3), 5, Background)
	fill := color.RGBA{52, 199, 89, 255}
	if st.Battery <= 20 && !st.Charging {
		fill = color.RGBA{255, 59, 48, 255}
	}
	inner := (bw - 10) * min(max(st.Battery, 0), 100) / 100
	RoundRect(img, image.Rect(bx+5, by+5, bx+5+inner, by+bh-5), 3, fill)
	Fill(img, image.Rect(bx+bw, by+9, bx+bw+5, by+bh-9), Ink)
	label := strconv.Itoa(st.Battery) + "%"
	if st.Charging {
		label = "+" + label
	}
	DrawText(img, l.faces.Status, bx-16-TextWidth(l.faces.Status, label), cy+12, Ink, label)
}

// rowRect is the tappable card of app i.
func (l *Launcher) rowRect(i int) image.Rectangle {
	y := listTop + i*(rowH+24)
	return image.Rect(margin, y, l.W-margin, y+rowH)
}

// Home draws the home screen: status bar, title, one card per app.
func (l *Launcher) Home(st Status) *image.RGBA {
	img := l.canvas()
	l.statusBar(img, st)
	DrawText(img, l.faces.Title, margin, titleY, Ink, "condor")
	for i, a := range l.Apps {
		r := l.rowRect(i)
		RoundRect(img, r, 36, Card)
		cy := (r.Min.Y + r.Max.Y) / 2
		icx := r.Min.X + 40 + iconR
		RoundRect(img, image.Rect(icx-iconR, cy-iconR, icx+iconR, cy+iconR), 30, a.Color)
		DrawTextCentered(img, l.faces.Glyph, icx, cy, color.White, a.Glyph)
		m := l.faces.Body.Metrics()
		DrawText(img, l.faces.Body, icx+iconR+40, cy+(m.Ascent.Ceil()-m.Descent.Ceil())/2, Ink, a.Name)
		DrawTextCentered(img, l.faces.Body, r.Max.X-60, cy, Muted, ">")
	}
	return img
}

// AppScreen draws a placeholder screen for an app that isn't built yet, with a back bar.
func (l *Launcher) AppScreen(a App, st Status) *image.RGBA {
	img := l.canvas()
	l.statusBar(img, st)
	DrawText(img, l.faces.Body, margin, statusH+backH/2+20, Accent, "< Home")
	Fill(img, image.Rect(0, statusH+backH, l.W, statusH+backH+2), Divider)
	DrawText(img, l.faces.Title, margin, statusH+backH+180, Ink, a.Name)
	DrawText(img, l.faces.Small, margin, statusH+backH+280, Muted, "Coming soon.")
	return img
}

// Hit returns the app whose card contains the logical point (x, y).
func (l *Launcher) Hit(x, y int) (App, bool) {
	for i, a := range l.Apps {
		if (image.Point{x, y}).In(l.rowRect(i)) {
			return a, true
		}
	}
	return App{}, false
}

// HitBack reports whether (x, y) is on an app screen's "‹ Home" bar.
func (l *Launcher) HitBack(x, y int) bool {
	return y >= statusH && y < statusH+backH && x < l.W/2
}

// ReadBattery reads charge and charging state from /sys/class/power_supply. It returns
// Battery -1 where there's no battery (e.g. on the PC).
func ReadBattery() (percent int, charging bool) {
	dirs, _ := filepath.Glob("/sys/class/power_supply/*")
	for _, d := range dirs {
		if t, _ := os.ReadFile(filepath.Join(d, "type")); strings.TrimSpace(string(t)) != "Battery" {
			continue
		}
		c, err := os.ReadFile(filepath.Join(d, "capacity"))
		if err != nil {
			continue
		}
		p, err := strconv.Atoi(strings.TrimSpace(string(c)))
		if err != nil {
			continue
		}
		s, _ := os.ReadFile(filepath.Join(d, "status"))
		return p, strings.TrimSpace(string(s)) == "Charging"
	}
	return -1, false
}

// String is used in logs.
func (a App) String() string { return fmt.Sprintf("%s (%s)", a.Name, a.ID) }
