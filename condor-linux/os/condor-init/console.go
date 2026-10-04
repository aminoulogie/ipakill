package main

import (
	"fmt"
	"image"
	"image/color"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-fonts/dejavu/dejavusansmono"
	"github.com/go-fonts/dejavu/dejavusansmonobold"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"

	"condor-init/vt"
)

// consoleAddr is where `condor term` connects: input goes into the shell on the tablet's
// screen, and the shell's output is copied back, so the PC and the tablet show one session.
const consoleAddr = "127.0.0.1:2323"

const consoleFontSize = 26 // ~73x57 cells on the 1200x1920 portrait screen

// Colours: black background, and Apple's dark-mode system colours for the 16 ANSI ones
// (red, green, yellow, blue, purple, teal...), so the terminal matches the rest of condor.
var (
	consoleFG = rgb(0xe5e5ea)
	consoleBG = rgb(0x000000)
	ansi      = [16]color.RGBA{
		rgb(0x48484a), rgb(0xff453a), rgb(0x32d74b), rgb(0xffd60a),
		rgb(0x0a84ff), rgb(0xbf5af2), rgb(0x64d2ff), rgb(0xe5e5ea),
		rgb(0x8e8e93), rgb(0xff6961), rgb(0x30db5b), rgb(0xffd426),
		rgb(0x409cff), rgb(0xda8fff), rgb(0x70d7ff), rgb(0xffffff),
	}
)

// console is the terminal shown on the tablet's screen.
type console struct {
	s              *Screen
	t              *vt.Term
	reg, bold      font.Face
	cw, ch, asc    int // cell width, cell height, baseline offset
	offX, offY     int // grid origin, centring the grid on the screen
	mu             sync.Mutex
	master         *os.File // the shell's pty, nil between shells
	kb             *keyboard
	tu             termUI                   // scrollback, selection, copy and paste (termui.go)
	locked         bool                     // the lock screen is up (lock.go)
	df             dictFetch                // the open book's words, saved for offline Look Up (dict.go)
	glyphs         map[glyphKey]*image.RGBA // rendered cells, reused (fonts are slow to rasterize)
	barH           int                      // status bar height at the top
	screenOn       bool
	mode           mode  // launcher, terminal or settings
	page           *page // the launcher/settings page on screen, for taps
	pf             *pageFonts
	cfg            savedSettings // brightness, screen-off timeout
	confirm        string        // power button waiting for its second tap
	wifiBusy       bool
	lastInput      time.Time // for the screen-off timeout
	lib            *library  // reader prefs + progress per book
	rf             *readerFonts
	book           *openBook
	shelf          []shelfBook
	store          storeState
	skb            *keyboard // the store's search keyboard
	fromStore      bool      // the open book came from the store (a preview or a download)
	rd             readerUI  // the reader's selection, menus, panels, gestures
	pcache         pageCache // the current book page, drawn once
	marksVersion   int
	lastRead       time.Time
	words          *wordBook
	shelfFrom      int // first book on the library page
	shelfPer       int
	homePop        []*storeItem // Home: Gutenberg\'s most read
	homePopLoading bool
	homePopErr     time.Time
	setPane        string // Settings: the pane shown
	readerFrom     mode   // where the open book was opened from, for "Library"
	animA, animB   []byte // the screen before and after a transition (native layout)
	gpu            gpuDev // animations on the GPU (gpu.go), nil when they're on the CPU
	gpuStarting    bool
	gpuFailed      bool
	wui            wordsUI
	clients        map[net.Conn]bool
}

func newConsole(s *Screen) (*console, error) {
	mk := func(ttf []byte) (font.Face, error) {
		f, err := opentype.Parse(ttf)
		if err != nil {
			return nil, err
		}
		return opentype.NewFace(f, &opentype.FaceOptions{Size: consoleFontSize, DPI: 72, Hinting: font.HintingFull})
	}
	reg, err := mk(dejavusansmono.TTF) // Menlo, macOS Terminal's font, is drawn from DejaVu Sans Mono
	if err != nil {
		return nil, err
	}
	bold, err := mk(dejavusansmonobold.TTF)
	if err != nil {
		return nil, err
	}
	adv, _ := reg.GlyphAdvance('M')
	m := reg.Metrics()
	c := &console{s: s, reg: reg, bold: bold, cw: adv.Ceil(),
		ch: (m.Ascent + m.Descent).Ceil() + 2, asc: m.Ascent.Ceil() + 1, clients: map[net.Conn]bool{}}
	c.glyphs = map[glyphKey]*image.RGBA{}
	c.barH = c.ch + 8
	c.screenOn, c.mode, c.cfg, c.lastInput = true, modeBooksHome, loadSettings(), time.Now()
	if c.pf, err = loadPageFonts(); err != nil {
		return nil, err
	}
	c.lib = loadLibrary()
	setPalette(!c.cfg.Light)
	if c.lib.Prefs.ThemeSet < 3 { // the system went dark (Kindle style): books open dark too
		if !c.cfg.Light {
			c.lib.Prefs.Theme = themeNight
		}
		c.lib.Prefs.ThemeSet = 3
		c.lib.save()
	}
	c.rf = c.readerFontsNow()
	c.words = loadWords()
	cols, rows := (s.W-2*consolePad)/c.cw, c.rowsFor(s.H-kbHeight-accH)
	c.offX, c.offY = (s.W-cols*c.cw)/2, c.barH+consolePad/2
	c.t = vt.New(cols, rows)
	c.t.Reply = c.input
	if c.kb, err = newKeyboard(s, c.input, c.keyboardShown); err != nil {
		return nil, err
	}
	c.kb.dark = true                             // the terminal's keyboard
	c.skb, err = newKeyboard(s, func(b []byte) { // typing on a page: the store's search, a word's meaning
		if c.mode == modeWords {
			c.wordsKey(b)
		} else {
			c.storeKey(b)
		}
	}, func(visible bool) {
		if !visible { // its hide key: stop typing
			c.store.typing, c.wui.edit = false, false
			c.showPage()
		}
	})
	if err != nil {
		return nil, err
	}
	return c, nil
}

// consolePad keeps text off the bezel.
const consolePad = 16

// rowsFor is how many text rows fit in a console area h pixels tall.
func (c *console) rowsFor(h int) int { return (h - c.barH - consolePad) / c.ch }

// keyboardShown resizes the console around the on-screen keyboard (shown or hidden): the
// grid, the shell's window size (programs get SIGWINCH), and a full redraw.
// Caller holds drawMu (the keyboard calls it from a touch).
func (c *console) keyboardShown(visible bool) {
	c.t.Resize(c.t.Cols, c.rowsFor(c.termAreaH()))
	c.mu.Lock()
	if c.master != nil {
		setWinsize(c.master, c.t.Cols, c.t.Rows)
	}
	c.mu.Unlock()
	c.redrawAll()
}

// touchLoop feeds touches to the on-screen keyboard. It doesn't return.
func (c *console) touchLoop() {
	for {
		err := readTouch("Goodix", c.s.fbW, c.s.fbH, c.s.rot, func(string, ...any) {}, func(pts []TouchPoint) {
			drawMu.Lock()
			defer drawMu.Unlock()
			c.lastInput = time.Now()
			if !c.screenOn {
				return // only the power button wakes the screen
			}
			for _, p := range pts {
				if c.locked { // the lock screen: a tap opens condor where it was
					if p.Up {
						c.unlock()
					}
					continue
				}
				// The left of the status bar ("‹ Books" in Terminal and Settings) goes back to
				// the Books app from any screen.
				if p.Up && p.Y < c.barH && p.X < c.s.W/3 {
					if c.mode != modeBooksHome {
						c.transition("pop", image.Rectangle{}, func() { c.setMode(modeBooksHome) })
					}
					continue
				}
				switch {
				case c.mode == modeTerminal && c.kb.visible && p.Y >= c.kb.y0:
					c.kb.touch(p)
				case c.mode == modeTerminal:
					c.termTouch(p) // scrollback, selection, the shortcuts bar
				case c.mode == modeStore && c.store.typing && c.skb.visible && p.Y >= c.skb.y0:
					c.skb.touch(p)
				case c.mode == modeWords && c.wui.edit && c.skb.visible && p.Y >= c.skb.y0:
					c.skb.touch(p)
				case c.mode == modeReader && c.book != nil:
					c.readerTouch(p)
				case p.Up:
					c.pageTap(p.X, p.Y)
				}
			}
		})
		log.Printf("touch: %v; retrying in 2s", err)
		time.Sleep(2 * time.Second)
	}
}

func colorOf(i uint8, def color.RGBA, bold bool) color.RGBA {
	if i == vt.Default {
		return def
	}
	if bold && i < 8 {
		i += 8 // bold ANSI colours are drawn bright, like a Linux tty
	}
	return ansi[i]
}

// render draws the rows that changed and writes them to the screen. Callers hold drawMu.
func (c *console) render() {
	if !c.screenOn || c.mode != modeTerminal {
		return // dirty marks stay; redrawAll repaints everything when the terminal shows again
	}
	if c.tu.back > 0 {
		return // reading the scrollback: new output waits until the view goes live again
	}
	if c.tu.selOn {
		c.t.TakeDirty()
		c.renderView()
		c.s.Flush()
		return
	}
	rows := c.t.TakeDirty()
	if len(rows) == 0 {
		return
	}
	line := image.NewRGBA(image.Rect(0, 0, c.t.Cols*c.cw, c.ch))
	for _, y := range rows {
		cells, cursor := c.t.Snapshot(y)
		for x, cell := range cells {
			fg, bg := colorOf(cell.FG, consoleFG, cell.Bold), colorOf(cell.BG, consoleBG, false)
			if x == cursor {
				fg, bg = bg, consoleFG
			}
			c.putGlyph(line, x*c.cw, cell.Ch, fg, bg, cell.Bold)
		}
		oy := c.offY + y*c.ch
		for py := 0; py < c.ch; py++ {
			row := line.Pix[line.PixOffset(0, py):]
			for px := 0; px < line.Rect.Dx(); px++ {
				c.s.Set(c.offX+px, oy+py, row[4*px], row[4*px+1], row[4*px+2])
			}
		}
	}
	if err := c.s.Flush(); err != nil {
		log.Printf("flush: %v", err)
	}
}

// output shows shell output on the screen and copies it to connected PCs.
func (c *console) output(p []byte) {
	c.t.Write(p)
	drawMu.Lock()
	c.render()
	drawMu.Unlock()
	c.mu.Lock()
	for conn := range c.clients {
		conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
		if _, err := conn.Write(p); err != nil {
			conn.Close()
			delete(c.clients, conn)
		}
	}
	c.mu.Unlock()
}

// input types p into the shell.
func (c *console) input(p []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.master != nil {
		c.master.Write(p)
	}
}

func banner() string {
	rel, _ := os.ReadFile("/proc/sys/kernel/osrelease")
	system := "Android shell (install Alpine: condor alpine install)"
	if v := alpineVersion(); v != "" {
		system = "Alpine Linux " + v
	}
	return fmt.Sprintf("\r\n\x1b[1;36mcondor\x1b[0m   \x1b[32m%s\x1b[0m   \x1b[90mkernel %s\x1b[0m\r\n"+
		"\x1b[90mswipe to scroll back · hold a finger to select text\x1b[0m\r\n\r\n",
		system, strings.TrimSpace(string(rel)))
}

// runConsole keeps a shell running on the screen, starting a new one whenever it exits.
// It doesn't return.
func (c *console) run() {
	c.output([]byte(banner()))
	for {
		m, wait, err := startShell(c.t.Cols, c.t.Rows)
		if err != nil {
			c.output([]byte("condor: can't start a shell: " + err.Error() + "\r\n"))
			time.Sleep(5 * time.Second)
			continue
		}
		c.mu.Lock()
		c.master = m
		c.mu.Unlock()
		buf := make([]byte, 4096)
		for {
			n, err := m.Read(buf)
			if n > 0 {
				c.output(buf[:n])
			}
			if err != nil {
				break
			}
		}
		c.mu.Lock()
		c.master = nil
		c.mu.Unlock()
		m.Close()
		wait()
		c.output([]byte("\r\n[shell exited; starting a new one]\r\n"))
		time.Sleep(time.Second)
	}
}

// serve accepts `condor term` connections and joins them to the screen's session.
func (c *console) serve() {
	for {
		ln, err := net.Listen("tcp", consoleAddr)
		if err != nil {
			log.Printf("listen %s: %v; retrying", consoleAddr, err)
			time.Sleep(2 * time.Second)
			continue
		}
		log.Printf("console listening on %s", consoleAddr)
		for {
			conn, err := ln.Accept()
			if err != nil {
				break
			}
			log.Printf("console client %s", conn.RemoteAddr())
			c.mu.Lock()
			c.clients[conn] = true
			c.mu.Unlock()
			c.input([]byte("\n")) // fresh prompt for the newcomer
			go func() {
				buf := make([]byte, 1024)
				for {
					n, err := conn.Read(buf)
					if n > 0 {
						c.input(buf[:n])
					}
					if err != nil {
						break
					}
				}
				c.mu.Lock()
				delete(c.clients, conn)
				c.mu.Unlock()
				conn.Close()
			}()
		}
		ln.Close()
	}
}
