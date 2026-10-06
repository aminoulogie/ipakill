package main

import (
	"fmt"
	"image"
	"image/color"
	"log"
	"math"
	"net"
	"time"

	"condor-init/ui"
)

// The status bar along the top: date and time on the left, Wi-Fi address and battery on the
// right. Redrawn every 20 seconds and whenever the screen is redrawn.

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

// barColours: the bar takes the colour of the screen under it, like iPadOS's.
func (c *console) barColours() (bg, fg color.RGBA) {
	switch {
	case c.locked:
		return rgb(0x000000), rgb(0xffffff)
	case c.mode == modeTerminal:
		return rgb(0x000000), rgb(0xffffff)
	case c.mode == modeReader && c.book != nil:
		th := c.theme()
		return th.bg, th.fg
	}
	return apBG, apLabel
}

// drawBar paints the status bar: time and date on the left; Wi-Fi, battery percentage and
// the battery on the right. Caller holds drawMu.
func (c *console) drawBar() {
	if !c.screenOn {
		return
	}
	bg, fg := c.barColours()
	f := apple()
	img := image.NewRGBA(image.Rect(0, 0, c.s.W, c.barH))
	ui.Fill(img, img.Rect, bg)
	now := time.Now().In(displayZone)
	cy := c.barH/2 + 9
	t := now.Format("15:04")
	x0 := 30
	if c.mode == modeTerminal { // the way back to the Books app (a tap on the bar's left)
		ui.DrawText(img, f.captionBold, x0, cy, rgb(0x0a84ff), "‹ Books")
		x0 += ui.TextWidth(f.captionBold, "‹ Books") + 30
	}
	ui.DrawText(img, f.captionBold, x0, cy, fg, t)
	ui.DrawText(img, f.caption, x0+ui.TextWidth(f.captionBold, t)+14, cy, fg, now.Format("Mon 2 Jan"))

	// The battery: an outline, filled to the charge (green while charging, red when low).
	x := c.s.W - 30
	pct, charging := ui.ReadBattery()
	br := image.Rect(x-50, c.barH/2-11, x-6, c.barH/2+11)
	ui.RoundRect(img, br, 7, blend(bg, fg, 0.45))
	ui.RoundRect(img, br.Inset(2), 5, bg)
	ui.RoundRect(img, image.Rect(br.Max.X+2, br.Min.Y+7, br.Max.X+6, br.Max.Y-7), 2, blend(bg, fg, 0.45))
	if pct >= 0 {
		fill := fg
		switch {
		case charging:
			fill = rgb(0x34c759)
		case pct <= 20:
			fill = rgb(0xff3b30)
		}
		in := br.Inset(4)
		ui.RoundRect(img, image.Rect(in.Min.X, in.Min.Y, in.Min.X+max(in.Dx()*pct/100, 4), in.Max.Y), 3, fill)
		label := fmt.Sprintf("%d%%", pct)
		x = br.Min.X - 10 - ui.TextWidth(f.caption, label)
		ui.DrawText(img, f.caption, x, cy, fg, label)
	}
	// Wi-Fi: three arcs and a dot, faint when not connected.
	wc := fg
	if wifiAddr() == "" {
		wc = blend(bg, fg, 0.3)
	}
	wx, wy := x-34, c.barH/2+10
	for i, rad := range []int{8, 16, 24} {
		for a := 225.0; a <= 315; a += 3 {
			th := a * math.Pi / 180
			ui.Circle(img, wx+int(float64(rad)*math.Cos(th)), wy+int(float64(rad)*math.Sin(th)), 2, wc)
		}
		_ = i
	}
	ui.Circle(img, wx, wy, 3, wc)
	c.s.blitRGBA(img, 0, 0)
}

// redrawAll repaints everything: status bar, console, keyboard. Caller holds drawMu.
func (c *console) redrawAll() {
	c.endGPUScroll()
	clear(c.s.buf)
	c.s.markRows(0, c.s.fbH-1)
	c.drawBar()
	if c.locked {
		c.drawLock()
		return
	}
	if c.mode == modeTerminal {
		if c.tu.back > 0 || c.tu.selOn {
			c.renderView()
		} else {
			c.t.MarkAll()
			c.render()
			c.drawShortcuts()
		}
		if c.kb.visible {
			c.kb.draw()
		}
	} else {
		c.showPage()
	}
	c.s.Flush()
}

// statusLoop keeps the bar's clock and battery current. It doesn't return.
func (c *console) statusLoop() {
	for range time.Tick(20 * time.Second) {
		drawMu.Lock()
		c.drawBar()
		if c.locked && c.screenOn {
			c.drawLock() // its clock
		}
		if err := c.s.Flush(); err != nil {
			log.Printf("flush: %v", err)
		}
		drawMu.Unlock()
	}
}
