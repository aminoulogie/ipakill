package main

import (
	"fmt"
	"image"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"condor-init/fonts"
	"condor-init/ui"
)

// The lock screen, as a Kindle's sleep cover meets iPadOS's lock screen: when the screen
// wakes it shows the time, the date and the book being read (its cover, title and how far
// along), on its cover's colour. A tap opens condor where it was.
//
// And while the screen is off, the tablet saves power: the processor's governor goes to
// powersave and Wi-Fi to power-save mode, both put back on wake. (Real suspend is unreliable
// on this Intel MID kernel, so the system stays up, idle.)

// drawLock paints the lock screen under the status bar. Caller holds drawMu.
func (c *console) drawLock() {
	c.endGPUScroll()
	f := apple()
	w, h := c.s.W, c.s.H-c.barH
	img := canvas(w, h)
	c.shelf = findBooks()
	reading := c.readingNow()

	// The book's colour, fading into black.
	top := rgb(0x1c1c1e)
	var it *storeItem
	if len(reading) > 0 {
		b := c.shelf[reading[0]]
		it = &storeItem{key: b.path, title: b.title, author: b.author, cover: b.cover}
		if cv := covers.get(b.cover, 120, 180); cv != nil {
			top = coverTint(cv, b.cover)
		} else {
			top = blend(placeholderColour(b.path), rgb(0x000000), 0.35)
		}
	}
	for y := 0; y < h; y++ {
		ui.Fill(img, image.Rect(0, y, w, y+1), blend(top, rgb(0x000000), min(1, float64(y)/float64(h)*1.4)))
	}

	now := time.Now().In(displayZone)
	white := rgb(0xffffff)
	apTextCenter(img, f.headline, w/2, 110, blend(white, top, 0.15), now.Format("Monday 2 January"))
	apTextCenter(img, textFace("inter-semibold", fonts.InterSemiBold, true, 190), w/2, 270, white, now.Format("15:04"))

	if it != nil {
		cr := image.Rect((w-560)/2, 470, (w+560)/2, 470+840)
		shadow(img, cr, 12, 0.35)
		c.drawCover(img, cr, it)
		y := cr.Max.Y + 90
		for i, l := range layoutWords(f.serifTitle, strings.Fields(it.title), 0, w-160, false) {
			if i == 2 {
				break
			}
			lw := 0
			if n := len(l.words); n > 0 {
				lw = l.words[n-1].x + l.words[n-1].w
			}
			drawWords(img, f.serifTitle, l, (w-lw)/2, y, white)
			y += 60
		}
		apTextCenter(img, f.callout, w/2, y+4, blend(white, rgb(0x000000), 0.3), clip(f.callout, it.author, w-160))
		pct := c.lib.Progress[it.key].Pct
		bar := image.Rect(w/2-220, y+60, w/2+220, y+68)
		ui.RoundRect(img, bar, 4, rgb(0x3a3a3c))
		if pct > 0 {
			ui.RoundRect(img, image.Rect(bar.Min.X, bar.Min.Y, bar.Min.X+max(bar.Dx()*pct/100, 8), bar.Max.Y), 4, white)
		}
		apTextCenter(img, f.caption, w/2, y+108, rgb(0x8d8d93), fmt.Sprintf("%d%% read", pct))
	} else {
		apTextCenter(img, f.serifLarge, w/2, 900, white, "condor")
		apTextCenter(img, f.callout, w/2, 980, rgb(0x8d8d93), "Books")
	}
	apTextCenter(img, f.callout, w/2, h-90, rgb(0x8d8d93), "Tap to open")
	ui.RoundRect(img, image.Rect(w/2-90, h-30, w/2+90, h-22), 4, rgb(0x8d8d93)) // iOS's home bar

	c.s.blitRGBA(img, 0, c.barH)
	c.s.Flush()
}

// unlock leaves the lock screen for where condor was. Caller holds drawMu.
func (c *console) unlock() {
	c.locked = false
	c.redrawAll()
}

// --- power saving while the screen is off --------------------------------------------------

var (
	powerMu        sync.Mutex // one switch at a time
	savedGovernors = map[string]string{}
)

// powerSave(true) slows the processor and puts Wi-Fi in power-save mode; false undoes it.
// Best effort: runs on its own, never holding drawMu.
func powerSave(on bool) {
	if _, err := os.Stat("/system/bin/linker"); err != nil {
		return // not on the tablet (tests, the PC): leave this machine's CPU alone
	}
	go func() {
		powerMu.Lock()
		defer powerMu.Unlock()
		govs, _ := filepath.Glob("/sys/devices/system/cpu/cpu[0-9]*/cpufreq/scaling_governor")
		for _, g := range govs {
			if on {
				if cur, err := os.ReadFile(g); err == nil {
					savedGovernors[g] = strings.TrimSpace(string(cur))
				}
				os.WriteFile(g, []byte("powersave"), 0o644)
			} else if old := savedGovernors[g]; old != "" {
				os.WriteFile(g, []byte(old), 0o644)
			}
		}
		state := "off"
		if on {
			state = "on"
		}
		if err := runInAlpine("/usr/sbin/iw", "dev", "wlan0", "set", "power_save", state); err != nil {
			log.Printf("wifi power save %s: %v", state, err)
		}
		log.Printf("power save %s (%d cpu governors)", state, len(govs))
	}()
}
