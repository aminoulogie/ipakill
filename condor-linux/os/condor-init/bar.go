package main

import (
	"fmt"
	"image"
	"image/color"
	"log"
	"net"
	"strings"
	"time"

	"condor-init/ui"
)

// The status bar along the top: date and time on the left, Wi-Fi address and battery on the
// right. Redrawn every 20 seconds and whenever the screen is redrawn.

var (
	barBG    = color.RGBA{40, 42, 46, 255}
	barFG    = color.RGBA{197, 200, 198, 255}
	barGreen = color.RGBA{181, 189, 104, 255}
	barRed   = color.RGBA{204, 102, 102, 255}
)

func wifiAddr() string {
	ifc, err := net.InterfaceByName("wlan0")
	if err != nil {
		return ""
	}
	addrs, _ := ifc.Addrs()
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
			return ipn.IP.String()
		}
	}
	return ""
}

// barParts returns the left and right texts and the battery colour.
func barParts() (left, right string, batColor color.RGBA) {
	now := time.Now().In(displayZone)
	left = " condor  " + now.Format("Mon 02 Jan 15:04")
	wifi := "wifi off"
	if ip := wifiAddr(); ip != "" {
		wifi = "wifi " + ip
	}
	batColor = barFG
	bat := "bat ?"
	if pct, charging := ui.ReadBattery(); pct >= 0 {
		bat = fmt.Sprintf("bat %d%%", pct)
		switch {
		case charging:
			bat += "+"
			batColor = barGreen
		case pct <= 15:
			batColor = barRed
		}
	}
	return left, wifi + "  " + bat + " ", batColor
}

// drawBar paints the status bar. Caller holds drawMu.
func (c *console) drawBar() {
	if !c.screenOn {
		return
	}
	left, right, batColor := barParts()
	cols := c.s.W / c.cw
	cells := []rune(strings.Repeat(" ", cols))
	copy(cells, []rune(left))
	rr := []rune(right)
	if len(rr) <= cols {
		copy(cells[cols-len(rr):], rr)
	}
	batStart := cols - len(rr) + strings.Index(right, "bat")
	line := image.NewRGBA(image.Rect(0, 0, cols*c.cw, c.ch))
	for x, r := range cells {
		fg := barFG
		if x >= batStart && strings.Contains(right, "bat") {
			fg = batColor
		}
		c.putGlyph(line, x*c.cw, r, fg, barBG, true)
	}
	bg := image.NewRGBA(image.Rect(0, 0, c.s.W, c.barH))
	ui.Fill(bg, bg.Rect, barBG)
	c.s.blitRGBA(bg, 0, 0)
	c.s.blitRGBA(line, (c.s.W-cols*c.cw)/2, (c.barH-c.ch)/2)
}

// redrawAll repaints everything: status bar, console, keyboard. Caller holds drawMu.
func (c *console) redrawAll() {
	clear(c.s.buf)
	c.s.markRows(0, c.s.fbH-1)
	c.drawBar()
	c.t.MarkAll()
	c.render()
	if c.kb.visible {
		c.kb.draw()
	}
	c.s.Flush()
}

// statusLoop keeps the bar's clock and battery current. It doesn't return.
func (c *console) statusLoop() {
	for range time.Tick(20 * time.Second) {
		drawMu.Lock()
		c.drawBar()
		if err := c.s.Flush(); err != nil {
			log.Printf("flush: %v", err)
		}
		drawMu.Unlock()
	}
}
