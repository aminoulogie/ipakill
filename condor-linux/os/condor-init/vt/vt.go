// Package vt is a small VT100/xterm-subset terminal: it takes the bytes a shell writes,
// keeps a grid of character cells, and records which rows changed so the caller can redraw
// only those. It handles what a shell and common command-line tools use: printable UTF-8,
// CR/LF/BS/TAB, cursor movement, erase, insert/delete, scroll regions and SGR colours.
package vt

import (
	"strconv"
	"sync"
	"unicode/utf8"
)

// Default marks "the terminal's default colour" in Cell.FG / Cell.BG.
const Default uint8 = 255

// Cell is one character position.
type Cell struct {
	Ch   rune
	FG   uint8 // 0..15 palette index, or Default
	BG   uint8
	Bold bool
}

type parseState int

const (
	ground parseState = iota
	escape
	csi
	osc
	charset // skip the one byte after ESC ( or ESC )
)

// Term is the terminal state. All methods are safe for concurrent use.
type Term struct {
	mu            sync.Mutex
	Cols, Rows    int
	cells         []Cell
	cx, cy        int
	wrapPending   bool
	fg, bg        uint8
	bold, inverse bool
	savedX        int
	savedY        int
	top, bot      int // scroll region, inclusive
	cursorVisible bool
	dirty         []bool
	history       [][]Cell // lines that scrolled off the top, oldest first (scrollback)

	state   parseState
	params  []int
	cur     int
	hasCur  bool
	private bool
	utf     []byte

	// Reply, if set, receives answers to queries such as "where is the cursor" (DSR).
	Reply func([]byte)
}

// New returns a cleared cols x rows terminal.
func New(cols, rows int) *Term {
	t := &Term{Cols: cols, Rows: rows}
	t.reset()
	return t
}

func (t *Term) reset() {
	t.cells = make([]Cell, t.Cols*t.Rows)
	t.dirty = make([]bool, t.Rows)
	t.fg, t.bg, t.bold, t.inverse = Default, Default, false, false
	t.top, t.bot = 0, t.Rows-1
	t.cursorVisible = true
	t.cx, t.cy, t.wrapPending = 0, 0, false
	for i := range t.cells {
		t.cells[i] = t.blank()
	}
	t.markAll()
}

func (t *Term) blank() Cell { return Cell{Ch: ' ', FG: Default, BG: t.bg} }

func (t *Term) markAll() {
	for i := range t.dirty {
		t.dirty[i] = true
	}
}

// Write feeds shell output into the terminal.
func (t *Term) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, b := range p {
		t.feed(b)
	}
	return len(p), nil
}

// Snapshot returns a copy of row y and whether the cursor is on it (and at which column).
func (t *Term) Snapshot(y int) (row []Cell, cursorX int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	row = append([]Cell(nil), t.cells[y*t.Cols:(y+1)*t.Cols]...)
	cursorX = -1
	if t.cursorVisible && y == t.cy {
		cursorX = min(t.cx, t.Cols-1)
	}
	return row, cursorX
}

// TakeDirty returns the rows changed since the last call and clears the marks.
func (t *Term) TakeDirty() []int {
	t.mu.Lock()
	defer t.mu.Unlock()
	var rows []int
	for y, d := range t.dirty {
		if d {
			rows = append(rows, y)
			t.dirty[y] = false
		}
	}
	return rows
}

// Text returns row y as a string with trailing spaces removed (for tests and logs).
func (t *Term) Text(y int) string {
	row, _ := t.Snapshot(y)
	end := len(row)
	for end > 0 && row[end-1].Ch == ' ' {
		end--
	}
	s := make([]rune, end)
	for i := range s {
		s[i] = row[i].Ch
	}
	return string(s)
}

// Cursor returns the cursor position.
func (t *Term) Cursor() (x, y int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cx, t.cy
}

func (t *Term) feed(b byte) {
	switch t.state {
	case escape:
		t.escape(b)
		return
	case csi:
		t.csiByte(b)
		return
	case osc:
		if b == 0x07 || b == '\\' { // BEL, or the end of ESC \
			t.state = ground
		}
		return
	case charset:
		t.state = ground
		return
	}
	if len(t.utf) > 0 || b >= 0x80 {
		t.utf = append(t.utf, b)
		if utf8.FullRune(t.utf) {
			r, _ := utf8.DecodeRune(t.utf)
			t.utf = t.utf[:0]
			t.put(r)
		} else if len(t.utf) >= utf8.UTFMax {
			t.utf = t.utf[:0]
			t.put(utf8.RuneError)
		}
		return
	}
	switch b {
	case 0x1b:
		t.state = escape
	case '\r':
		t.cx, t.wrapPending = 0, false
	case '\n', '\v', '\f':
		t.lineFeed()
	case '\b':
		if t.cx > 0 {
			t.cx--
		}
		t.wrapPending = false
	case '\t':
		t.cx = min((t.cx/8+1)*8, t.Cols-1)
	default:
		if b >= 0x20 && b != 0x7f {
			t.put(rune(b))
		}
	}
}

func (t *Term) put(r rune) {
	if t.wrapPending {
		t.cx, t.wrapPending = 0, false
		t.lineFeed()
	}
	fg, bg := t.fg, t.bg
	if t.inverse {
		fg, bg = swapDefault(bg, 0), swapDefault(fg, 7)
	}
	t.cells[t.cy*t.Cols+t.cx] = Cell{Ch: r, FG: fg, BG: bg, Bold: t.bold}
	t.dirty[t.cy] = true
	if t.cx == t.Cols-1 {
		t.wrapPending = true
	} else {
		t.cx++
	}
}

// swapDefault turns Default into a concrete colour so inverse video stays visible.
func swapDefault(c, def uint8) uint8 {
	if c == Default {
		return def
	}
	return c
}

func (t *Term) lineFeed() {
	t.wrapPending = false
	if t.cy == t.bot {
		t.scrollUp(t.top, t.bot, 1)
	} else if t.cy < t.Rows-1 {
		t.cy++
	}
}

// MaxHistory is how many lines of scrollback are kept.
const MaxHistory = 3000

func (t *Term) scrollUp(top, bot, n int) {
	n = min(n, bot-top+1)
	if top == 0 && bot == t.Rows-1 { // the whole screen scrolls: keep what leaves it
		for y := 0; y < n; y++ {
			t.history = append(t.history, append([]Cell(nil), t.cells[y*t.Cols:(y+1)*t.Cols]...))
		}
		if extra := len(t.history) - MaxHistory; extra > 0 {
			t.history = append(t.history[:0:0], t.history[extra:]...)
		}
	}
	copy(t.cells[top*t.Cols:], t.cells[(top+n)*t.Cols:(bot+1)*t.Cols])
	t.clearRows(bot-n+1, bot)
	for y := top; y <= bot; y++ {
		t.dirty[y] = true
	}
}

func (t *Term) scrollDown(top, bot, n int) {
	n = min(n, bot-top+1)
	copy(t.cells[(top+n)*t.Cols:(bot+1)*t.Cols], t.cells[top*t.Cols:(bot+1-n)*t.Cols])
	t.clearRows(top, top+n-1)
	for y := top; y <= bot; y++ {
		t.dirty[y] = true
	}
}

func (t *Term) clearRows(y0, y1 int) {
	for y := max(y0, 0); y <= min(y1, t.Rows-1); y++ {
		t.clearCells(y, 0, t.Cols)
	}
}

// clearCells blanks [x0, x1) on row y.
func (t *Term) clearCells(y, x0, x1 int) {
	for x := max(x0, 0); x < min(x1, t.Cols); x++ {
		t.cells[y*t.Cols+x] = t.blank()
	}
	t.dirty[y] = true
}

func (t *Term) escape(b byte) {
	t.state = ground
	switch b {
	case '[':
		t.state, t.params, t.cur, t.hasCur, t.private = csi, t.params[:0], 0, false, false
	case ']':
		t.state = osc
	case '(', ')':
		t.state = charset
	case '7':
		t.savedX, t.savedY = t.cx, t.cy
	case '8':
		t.cx, t.cy = t.savedX, t.savedY
	case 'D':
		t.lineFeed()
	case 'E':
		t.cx = 0
		t.lineFeed()
	case 'M': // reverse index
		if t.cy == t.top {
			t.scrollDown(t.top, t.bot, 1)
		} else if t.cy > 0 {
			t.cy--
		}
	case 'c':
		t.reset()
	}
}

func (t *Term) csiByte(b byte) {
	switch {
	case b >= '0' && b <= '9':
		t.cur, t.hasCur = t.cur*10+int(b-'0'), true
	case b == ';':
		t.params = append(t.params, t.cur)
		t.cur, t.hasCur = 0, false
	case b == '?' || b == '>' || b == '=':
		t.private = true
	case b >= 0x40 && b <= 0x7e:
		if t.hasCur || len(t.params) > 0 {
			t.params = append(t.params, t.cur)
		}
		t.state = ground
		t.dispatch(b)
	}
}

// arg returns parameter i, or def when it's missing or zero.
func (t *Term) arg(i, def int) int {
	if i < len(t.params) && t.params[i] != 0 {
		return t.params[i]
	}
	return def
}

func (t *Term) dispatch(final byte) {
	t.wrapPending = false
	if t.private {
		if final == 'h' || final == 'l' {
			for _, p := range t.params {
				switch p {
				case 25:
					t.cursorVisible = final == 'h'
					t.dirty[t.cy] = true
				case 1049, 47, 1047: // alternate screen: we only have one, so just clear it
					t.clearRows(0, t.Rows-1)
				}
			}
		}
		return
	}
	n := t.arg(0, 1)
	clampX := func(x int) int { return min(max(x, 0), t.Cols-1) }
	clampY := func(y int) int { return min(max(y, 0), t.Rows-1) }
	t.dirty[t.cy] = true // the cursor moves off this row
	switch final {
	case 'A':
		t.cy = max(t.cy-n, t.top)
	case 'B':
		t.cy = min(t.cy+n, t.bot)
	case 'C':
		t.cx = clampX(t.cx + n)
	case 'D':
		t.cx = clampX(t.cx - n)
	case 'E':
		t.cx, t.cy = 0, clampY(t.cy+n)
	case 'F':
		t.cx, t.cy = 0, clampY(t.cy-n)
	case 'G', '`':
		t.cx = clampX(n - 1)
	case 'd':
		t.cy = clampY(n - 1)
	case 'H', 'f':
		t.cy, t.cx = clampY(t.arg(0, 1)-1), clampX(t.arg(1, 1)-1)
	case 'J':
		switch t.arg(0, 0) {
		case 0:
			t.clearCells(t.cy, t.cx, t.Cols)
			t.clearRows(t.cy+1, t.Rows-1)
		case 1:
			t.clearRows(0, t.cy-1)
			t.clearCells(t.cy, 0, t.cx+1)
		case 3: // clear also forgets the scrollback
			t.history = nil
		default:
			t.clearRows(0, t.Rows-1)
		}
	case 'K':
		switch t.arg(0, 0) {
		case 0:
			t.clearCells(t.cy, t.cx, t.Cols)
		case 1:
			t.clearCells(t.cy, 0, t.cx+1)
		default:
			t.clearCells(t.cy, 0, t.Cols)
		}
	case 'L':
		if t.cy >= t.top && t.cy <= t.bot {
			t.scrollDown(t.cy, t.bot, n)
		}
	case 'M':
		if t.cy >= t.top && t.cy <= t.bot {
			t.scrollUp(t.cy, t.bot, n)
		}
	case 'S':
		t.scrollUp(t.top, t.bot, n)
	case 'T':
		t.scrollDown(t.top, t.bot, n)
	case 'P': // delete characters
		row := t.cells[t.cy*t.Cols : (t.cy+1)*t.Cols]
		n = min(n, t.Cols-t.cx)
		copy(row[t.cx:], row[t.cx+n:])
		t.clearCells(t.cy, t.Cols-n, t.Cols)
	case '@': // insert blanks
		row := t.cells[t.cy*t.Cols : (t.cy+1)*t.Cols]
		n = min(n, t.Cols-t.cx)
		copy(row[t.cx+n:], row[t.cx:])
		t.clearCells(t.cy, t.cx, t.cx+n)
	case 'X':
		t.clearCells(t.cy, t.cx, t.cx+n)
	case 'r':
		top, bot := t.arg(0, 1)-1, t.arg(1, t.Rows)-1
		if top < bot && bot < t.Rows {
			t.top, t.bot = top, bot
			t.cx, t.cy = 0, 0
		}
	case 's':
		t.savedX, t.savedY = t.cx, t.cy
	case 'u':
		t.cx, t.cy = t.savedX, t.savedY
	case 'm':
		t.sgr()
	case 'n':
		if t.arg(0, 0) == 6 && t.Reply != nil {
			reply := "\x1b[" + strconv.Itoa(t.cy+1) + ";" + strconv.Itoa(t.cx+1) + "R"
			go t.Reply([]byte(reply))
		}
	}
	t.dirty[t.cy] = true
}

func (t *Term) sgr() {
	if len(t.params) == 0 {
		t.params = append(t.params, 0)
	}
	for i := 0; i < len(t.params); i++ {
		switch p := t.params[i]; {
		case p == 0:
			t.fg, t.bg, t.bold, t.inverse = Default, Default, false, false
		case p == 1:
			t.bold = true
		case p == 22:
			t.bold = false
		case p == 7:
			t.inverse = true
		case p == 27:
			t.inverse = false
		case p >= 30 && p <= 37:
			t.fg = uint8(p - 30)
		case p == 39:
			t.fg = Default
		case p >= 40 && p <= 47:
			t.bg = uint8(p - 40)
		case p == 49:
			t.bg = Default
		case p >= 90 && p <= 97:
			t.fg = uint8(p - 90 + 8)
		case p >= 100 && p <= 107:
			t.bg = uint8(p - 100 + 8)
		case p == 38 || p == 48: // 256-colour (5;n) and truecolour (2;r;g;b): the nearest of the 16
			c, ok := uint8(0), false
			if i+2 < len(t.params) && t.params[i+1] == 5 {
				c, ok = nearest16(rgb256(t.params[i+2]))
				i += 2
			} else if i+4 < len(t.params) && t.params[i+1] == 2 {
				c, ok = nearest16([3]int{t.params[i+2], t.params[i+3], t.params[i+4]})
				i += 4
			}
			if ok && p == 38 {
				t.fg = c
			} else if ok {
				t.bg = c
			}
		}
	}
}

// Resize changes the grid to cols x rows, keeping the text around the cursor: when the grid
// gets shorter, rows scroll off the top so the cursor's line stays visible.
func (t *Term) Resize(cols, rows int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if cols == t.Cols && rows == t.Rows {
		return
	}
	shift := max(0, t.cy-(rows-1))
	cells := make([]Cell, cols*rows)
	for i := range cells {
		cells[i] = Cell{Ch: ' ', FG: Default, BG: Default}
	}
	for y := 0; y < rows; y++ {
		sy := y + shift
		if sy >= t.Rows {
			break
		}
		copy(cells[y*cols:y*cols+min(cols, t.Cols)], t.cells[sy*t.Cols:sy*t.Cols+min(cols, t.Cols)])
	}
	t.cells, t.Cols, t.Rows = cells, cols, rows
	t.cy = min(t.cy-shift, rows-1)
	t.cx = min(t.cx, cols-1)
	t.savedX, t.savedY = min(t.savedX, cols-1), min(t.savedY, rows-1)
	t.top, t.bot = 0, rows-1
	t.wrapPending = false
	t.dirty = make([]bool, rows)
	t.markAll()
}

// MarkAll marks every row changed, so the next TakeDirty redraws the whole screen.
func (t *Term) MarkAll() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.markAll()
}

// History is how many lines of scrollback there are.
func (t *Term) History() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.history)
}

// View returns row y of the screen scrolled back by back lines (0 = live): the scrollback
// followed by the screen, as one long page. Rows are padded or cut to the current width.
func (t *Term) View(y, back int) []Cell {
	t.mu.Lock()
	defer t.mu.Unlock()
	back = min(max(back, 0), len(t.history))
	v := len(t.history) - back + y
	row := make([]Cell, t.Cols)
	for i := range row {
		row[i] = Cell{Ch: ' ', FG: Default, BG: Default}
	}
	switch {
	case v < 0:
	case v < len(t.history):
		copy(row, t.history[v])
	case v-len(t.history) < t.Rows:
		sy := v - len(t.history)
		copy(row, t.cells[sy*t.Cols:(sy+1)*t.Cols])
	}
	return row
}

// xterm16 is xterm's own 16 colours: what programs mean by each index, used to pick the
// nearest one for a 256-colour or truecolour request (the screen then draws condor's palette).
var xterm16 = [16][3]int{
	{0, 0, 0}, {205, 0, 0}, {0, 205, 0}, {205, 205, 0}, {0, 0, 238}, {205, 0, 205}, {0, 205, 205}, {229, 229, 229},
	{127, 127, 127}, {255, 0, 0}, {0, 255, 0}, {255, 255, 0}, {92, 92, 255}, {255, 0, 255}, {0, 255, 255}, {255, 255, 255},
}

// rgb256 is xterm's 256-colour palette entry n.
func rgb256(n int) [3]int {
	switch {
	case n < 0 || n > 255:
		return [3]int{229, 229, 229}
	case n < 16:
		return xterm16[n]
	case n < 232: // 6x6x6 cube
		n -= 16
		lv := func(v int) int {
			if v == 0 {
				return 0
			}
			return 55 + 40*v
		}
		return [3]int{lv(n / 36), lv(n / 6 % 6), lv(n % 6)}
	default: // greys
		g := 8 + 10*(n-232)
		return [3]int{g, g, g}
	}
}

// nearest16 picks the closest of the 16 colours. A colour with some saturation keeps its
// hue (greys only match greys), so syntax colours don't all turn grey or white.
func nearest16(c [3]int) (uint8, bool) {
	sat := max(c[0], c[1], c[2]) - min(c[0], c[1], c[2])
	best, bestD := 0, 1<<30 // fits a 32-bit int: the tablet is 386
	for i, p := range xterm16 {
		psat := max(p[0], p[1], p[2]) - min(p[0], p[1], p[2])
		if (sat > 40) != (psat > 40) {
			continue
		}
		d := 0
		for k := 0; k < 3; k++ {
			d += (c[k] - p[k]) * (c[k] - p[k])
		}
		if d < bestD {
			best, bestD = i, d
		}
	}
	return uint8(best), true
}
