package main

import (
	"image"
	"strings"
	"time"

	"condor-init/ui"
)

// The terminal's touch layer, above the shell's text: swipe up and down through the
// scrollback, hold a finger on the text to select it (drag to extend), Copy and Paste. A bar
// over the keyboard holds those two and one-tap commands, like iPadOS's shortcuts bar.

const accH = 84 // the shortcuts bar

// termShortcuts: label and what it types ("" = Copy/Paste, handled apart).
var termShortcuts = []struct{ label, out string }{
	{"Copy", ""}, {"Paste", ""}, {"update", "update\r"}, {"clear", "clear\r"},
	{"ls", "ls\r"}, {"cd ..", "cd ..\r"}, {"wifi", "wifi status\r"},
}

// termPos is a place in the scrollback-plus-screen page: line (0 = the oldest kept) and column.
type termPos struct{ line, col int }

func (a termPos) before(b termPos) bool {
	return a.line < b.line || (a.line == b.line && a.col < b.col)
}

type termUI struct {
	back       int // lines scrolled back (0 = live)
	selOn      bool
	selA, selB termPos
	clip       string // copied text, for Paste

	// the finger on the text
	down       bool
	downX      int
	downY      int
	backAtDown int
	scrolling  bool
	selecting  bool
	hold       *time.Timer
	gesture    int // counts gestures, so a stale long-press timer does nothing
}

// accY is the top of the shortcuts bar.
func (c *console) accY() int {
	if c.kb.visible {
		return c.kb.y0 - accH
	}
	return c.s.H - accH
}

// termAreaH is the height left for the shell's rows.
func (c *console) termAreaH() int {
	if c.kb.visible {
		return c.s.H - kbHeight - accH
	}
	return c.s.H - accH
}

// cellAt is the page position under logical (x, y), or ok=false off the text.
func (c *console) cellAt(x, y int) (termPos, bool) {
	row := (y - c.offY) / c.ch
	if y < c.offY || row >= c.t.Rows {
		return termPos{}, false
	}
	col := min(max((x-c.offX)/c.cw, 0), c.t.Cols-1)
	return termPos{c.t.History() - c.tu.back + row, col}, true
}

// selected says whether page cell p is in the selection.
func (c *console) selected(p termPos) bool {
	if !c.tu.selOn {
		return false
	}
	a, b := c.tu.selA, c.tu.selB
	if b.before(a) {
		a, b = b, a
	}
	return !p.before(a) && !b.before(p)
}

// selectionText is the selected text, one line per row, trailing spaces dropped.
func (c *console) selectionText() string {
	a, b := c.tu.selA, c.tu.selB
	if b.before(a) {
		a, b = b, a
	}
	hist := c.t.History()
	var lines []string
	for l := a.line; l <= b.line; l++ {
		row := c.t.View(l-hist, 0) // line l as a row of the live view, scrollback included
		x0, x1 := 0, len(row)-1
		if l == a.line {
			x0 = a.col
		}
		if l == b.line {
			x1 = min(b.col, len(row)-1)
		}
		var sb strings.Builder
		for x := x0; x <= x1; x++ {
			sb.WriteRune(row[x].Ch)
		}
		lines = append(lines, strings.TrimRight(sb.String(), " "))
	}
	return strings.Join(lines, "\n")
}

// renderView draws every row of the (possibly scrolled-back) view with the selection, then
// the shortcuts bar. Caller holds drawMu.
func (c *console) renderView() {
	line := image.NewRGBA(image.Rect(0, 0, c.t.Cols*c.cw, c.ch))
	hist := c.t.History()
	for y := 0; y < c.t.Rows; y++ {
		cells := c.t.View(y, c.tu.back)
		cx := -1
		if c.tu.back == 0 {
			_, cx = c.t.Snapshot(y)
		}
		for x, cell := range cells {
			fg, bg := colorOf(cell.FG, consoleFG, cell.Bold), colorOf(cell.BG, consoleBG, false)
			if x == cx {
				fg, bg = bg, consoleFG
			}
			if c.selected(termPos{hist - c.tu.back + y, x}) {
				fg, bg = rgb(0xffffff), rgb(0x0a84ff)
			}
			c.putGlyph(line, x*c.cw, cell.Ch, fg, bg, cell.Bold)
		}
		c.s.blitRGBA(line, c.offX, c.offY+y*c.ch)
	}
	if c.tu.back > 0 { // where you are in the scrollback
		f := apple()
		label := "↑ " + itoa(c.tu.back) + " lines back · tap to return"
		w := ui.TextWidth(f.caption, label) + 48
		pill := image.NewRGBA(image.Rect(0, 0, w, 52))
		ui.Fill(pill, pill.Rect, consoleBG)
		ui.RoundRect(pill, pill.Rect, 26, rgb(0x3a3a3c))
		ui.DrawTextCentered(pill, f.caption, w/2, 26, rgb(0xffffff), label)
		c.s.blitRGBA(pill, (c.s.W-w)/2, c.offY+8)
	}
	c.drawShortcuts()
}

// drawShortcuts paints the bar over the keyboard. Caller holds drawMu.
func (c *console) drawShortcuts() {
	f := apple()
	col := kbDarkC
	img := image.NewRGBA(image.Rect(0, 0, c.s.W, accH))
	ui.Fill(img, img.Rect, col.bg)
	x := kbPad
	for _, sc := range termShortcuts {
		w := ui.TextWidth(f.callout, sc.label) + 44
		r := image.Rect(x, 10, x+w, accH-8)
		bg, fg := col.special, col.text
		switch {
		case sc.label == "Copy" && c.tu.selOn:
			bg = col.active
		case sc.label == "Copy", sc.label == "Paste" && c.tu.clip == "":
			fg = rgb(0x8d8d93) // nothing to copy or paste yet
		case sc.out != "":
			fg = rgb(0x64d2ff) // commands in blue
		}
		ui.RoundRect(img, r, 14, bg)
		ui.DrawTextCentered(img, f.callout, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, fg, sc.label)
		x += w + 12
	}
	c.s.blitRGBA(img, 0, c.accY())
}

// shortcutAt is the shortcut under logical x on the bar, or -1.
func (c *console) shortcutAt(x int) int {
	f := apple()
	x0 := kbPad
	for i, sc := range termShortcuts {
		w := ui.TextWidth(f.callout, sc.label) + 44
		if x >= x0 && x < x0+w+12 {
			return i
		}
		x0 += w + 12
	}
	return -1
}

func (c *console) shortcut(i int) {
	sc := termShortcuts[i]
	switch sc.label {
	case "Copy":
		if !c.tu.selOn {
			return
		}
		c.tu.clip = c.selectionText()
		c.tu.selOn = false
	case "Paste":
		if c.tu.clip == "" {
			return
		}
		c.goLive()
		c.input([]byte(strings.ReplaceAll(c.tu.clip, "\n", "\r")))
	default:
		c.tu.selOn = false
		c.goLive()
		c.input([]byte(sc.out))
	}
	c.renderView()
	c.s.Flush()
}

// goLive scrolls back to the live screen. Caller holds drawMu.
func (c *console) goLive() {
	if c.tu.back != 0 {
		c.tu.back = 0
		c.t.MarkAll()
	}
}

// termTouch handles a finger on the terminal outside the keyboard. Caller holds drawMu.
func (c *console) termTouch(p TouchPoint) {
	tu := &c.tu
	if p.Y >= c.accY() && p.Y < c.accY()+accH { // the shortcuts bar: acts on lift
		if p.Up {
			if i := c.shortcutAt(p.X); i >= 0 {
				c.shortcut(i)
			}
		}
		return
	}
	switch {
	case p.Down:
		tu.down, tu.downX, tu.downY, tu.backAtDown = true, p.X, p.Y, tu.back
		tu.scrolling, tu.selecting = false, false
		tu.gesture++
		g := tu.gesture
		tu.hold = time.AfterFunc(350*time.Millisecond, func() {
			drawMu.Lock()
			defer drawMu.Unlock()
			if tu.gesture != g || !tu.down || tu.scrolling {
				return
			}
			if at, ok := c.cellAt(tu.downX, tu.downY); ok { // hold: start selecting here
				tu.selecting, tu.selOn, tu.selA, tu.selB = true, true, at, at
				c.renderView()
				c.s.Flush()
			}
		})
	case p.Moved && tu.down:
		dy := p.Y - tu.downY
		switch {
		case tu.selecting:
			if at, ok := c.cellAt(p.X, p.Y); ok && at != tu.selB {
				tu.selB = at
				c.renderView()
				c.s.Flush()
			}
		case tu.scrolling || abs(dy) > 24:
			tu.scrolling = true
			back := min(max(tu.backAtDown+dy/c.ch, 0), c.t.History()) // drag down = older lines
			if back != tu.back {
				tu.back = back
				c.renderView()
				c.s.Flush()
			}
		}
	case p.Up:
		if tu.hold != nil {
			tu.hold.Stop()
		}
		wasTap := tu.down && !tu.scrolling && !tu.selecting
		tu.down = false
		if !wasTap {
			return
		}
		switch {
		case tu.selOn: // a tap clears the selection
			tu.selOn = false
		case tu.back > 0: // a tap returns to the live screen
			c.goLive()
		case !c.kb.visible: // a tap brings the keyboard back
			c.kb.visible = true
			c.kb.onHide(true)
			return
		default:
			return
		}
		c.renderView()
		c.s.Flush()
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}
