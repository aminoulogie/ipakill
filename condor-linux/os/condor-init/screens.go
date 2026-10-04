package main

import (
	"fmt"
	"image"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"condor-init/ui"
)

// The launcher (home) and settings pages, and what their buttons do.

func kernelRelease() string {
	b, _ := os.ReadFile("/proc/sys/kernel/osrelease")
	return strings.TrimSpace(string(b))
}

func uptime() string {
	b, _ := os.ReadFile("/proc/uptime")
	var secs float64
	fmt.Sscan(string(b), &secs)
	d := time.Duration(secs) * time.Second
	return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
}

var reSSID = regexp.MustCompile(`(?m)^\s*ssid="([^"]*)"`)

// savedSSID is the network `wifi connect` saved, or "".
func savedSSID() string {
	b, err := os.ReadFile(alpineRoot + "/etc/condor/wpa.conf")
	if err != nil {
		return ""
	}
	if m := reSSID.FindSubmatch(b); m != nil {
		return string(m[1])
	}
	return ""
}

// batteryInfo returns e.g. "62%  charging  3.91 V".
func batteryInfo() string {
	pct, charging := ui.ReadBattery()
	if pct < 0 {
		return "no battery found"
	}
	s := fmt.Sprintf("%d%%", pct)
	if charging {
		s += "  charging"
	}
	if b, err := os.ReadFile("/sys/class/power_supply/cw2015_battery/voltage_now"); err == nil {
		var uv float64
		fmt.Sscan(strings.TrimSpace(string(b)), &uv)
		s += fmt.Sprintf("  %.2f V", uv/1e6)
	}
	return s
}

// showPage draws the current page (launcher or settings) under the bar. Caller holds drawMu.
func (c *console) showPage() {
	if !c.screenOn {
		return
	}
	switch {
	case c.mode == modeSettings:
		c.page = c.settingsPage()
	case c.mode == modeBooks:
		c.page = c.shelfPage()
	case c.mode == modeReader && c.book != nil:
		c.page = c.readerPage()
	case c.mode == modeStore:
		c.page = c.storePage()
	case c.mode == modeWords:
		c.page = c.wordsPage()
	default:
		c.page = c.booksHomePage()
	}
	c.blitPage()              // the window of a tall page that's scrolled to
	if c.mode != modeReader { // the reader draws its own, under its overlays
		c.drawToast()
	}
	if (c.mode == modeStore && c.store.typing) || (c.mode == modeWords && c.wui.edit) {
		c.skb.draw()
	}
	c.s.Flush()
}

// setMode switches screens. Caller holds drawMu.
func (c *console) setMode(m mode) {
	c.mode, c.confirm = m, ""
	c.redrawAll()
}

// pageTap handles a finger lifting on the launcher or settings. Caller holds drawMu.
func (c *console) pageTap(x, y int) {
	if c.page == nil {
		return
	}
	id := c.page.hit(x, c.pageY(y))
	if id == "" {
		return
	}
	if id != c.confirm {
		c.confirm = ""
	}
	if c.booksTap(id) || c.homeTap(id) || c.storeTap(id) || c.wordsTap(id) || c.readerTap(id) {
		return
	}
	switch {
	case strings.HasPrefix(id, "set:pane:"):
		c.setPane = strings.TrimPrefix(id, "set:pane:")
		c.showPage()
		return
	}
	switch id {
	case "store":
		c.store.sel = nil
		c.transition("push", image.Rectangle{}, func() { c.setMode(modeStore) })
		return
	case "terminal":
		c.transition("push", image.Rectangle{}, func() { c.setMode(modeTerminal) })
		return
	case "settings":
		c.transition("push", image.Rectangle{}, func() { c.setMode(modeSettings) })
		return
	case "home":
		c.transition("pop", image.Rectangle{}, func() { c.setMode(modeBooksHome) })
		return
	case "dark":
		c.setAppearance(c.cfg.Light) // switches: light becomes dark and back
		c.cfg.save()
		c.redrawAll()
		return
	case "bright-", "bright+":
		step := 10
		if id == "bright-" {
			step = -10
		}
		c.cfg.Brightness = min(max(c.cfg.Brightness+step, 10), 100)
		setBacklight(c.cfg.Brightness)
		c.cfg.save()
	case "lock":
		c.cfg.NoLock = !c.cfg.NoLock
		c.cfg.save()
	case "anim":
		c.cfg.Animations = !c.cfg.Animations
		c.cfg.save()
		c.wantGPU()
	case "off0", "off1", "off5", "off10":
		fmt.Sscanf(id, "off%d", &c.cfg.ScreenOff)
		c.cfg.save()
	case "wifi":
		if !c.wifiBusy {
			c.wifiBusy = true
			go c.reconnectWifi()
		}
	case "restart", "poweroff", "android":
		if c.confirm != id {
			c.confirm = id // first tap: ask for a second one
			go func() {
				time.Sleep(4 * time.Second)
				drawMu.Lock()
				if c.confirm == id {
					c.confirm = ""
					if c.mode == modeSettings {
						c.showPage()
					}
				}
				drawMu.Unlock()
			}()
		} else {
			c.powerAction(id)
			return
		}
	}
	c.showPage()
}

func (c *console) reconnectWifi() {
	err := runInAlpine("/usr/local/bin/wifi", "boot")
	log.Printf("wifi reconnect from settings: %v", err)
	drawMu.Lock()
	c.wifiBusy = false
	if c.mode == modeSettings {
		c.showPage()
	}
	c.drawBar()
	c.s.Flush()
	drawMu.Unlock()
}

// powerAction restarts, powers off, or goes back to Android. Android's init does the clean
// shutdown (sync, unmount) when sys.powerctl is set. Caller holds drawMu.
func (c *console) powerAction(id string) {
	what := map[string]string{"restart": "reboot", "poweroff": "shutdown", "android": "reboot"}[id]
	if id == "android" {
		os.Remove(condorHome + "/autostart")
		os.Remove(condorHome + "/takeover")
	}
	log.Printf("power: %s (%s)", id, what)
	msg := map[string]string{"restart": "restarting...", "poweroff": "powering off...", "android": "restarting into Android..."}[id]
	pn := newPen(c.s.W, c.s.H-c.barH, c.pf)
	pn.text(c.pf.title, pgText, pn.mx, 400, msg)
	c.s.blitRGBA(pn.p.img, 0, c.barH)
	c.s.Flush()
	syncDisks()
	if err := exec.Command("/system/bin/setprop", "sys.powerctl", what).Run(); err != nil {
		log.Printf("setprop sys.powerctl %s: %v", what, err)
	}
}
