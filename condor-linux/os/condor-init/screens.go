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

// launcherPage is the home screen: one card per app.
func (c *console) launcherPage() *page {
	pn := newPen(c.s.W, c.s.H-c.barH, c.pf)
	pn.y = 150
	pn.text(c.pf.title, pgText, pn.mx, pn.y, "condor")
	ver := alpineVersion()
	if ver == "" {
		ver = "-"
	}
	pn.y += 56
	pn.text(c.pf.small, pgMuted, pn.mx, pn.y, "alpine "+ver+"  ·  kernel "+kernelRelease())
	pn.y += 50
	apps := []struct{ id, name, desc string }{
		{"terminal", "terminal", "Alpine shell with keyboard"},
		{"settings", "settings", "display, wi-fi, battery, power"},
		{"books", "books", "read EPUB books"},
		{"store", "store", "75,000 free books from Project Gutenberg"},
	}
	for _, a := range apps {
		r := image.Rect(pn.mx, pn.y, c.s.W-pn.mx, pn.y+170)
		ui.RoundRect(pn.p.img, r, 24, pgCard)
		nameColor, descColor := pgText, pgMuted
		if a.id == "" {
			nameColor = pgMuted
		}
		pn.text(c.pf.bold, nameColor, r.Min.X+44, r.Min.Y+72, "> "+a.name)
		pn.text(c.pf.small, descColor, r.Min.X+44+ui.TextWidth(c.pf.bold, "> "), r.Min.Y+124, a.desc)
		if a.id != "" {
			pn.p.buttons = append(pn.p.buttons, button{a.id, r})
		}
		pn.y += 200
	}
	return pn.p
}

// settingsPage draws settings with the current values.
func (c *console) settingsPage() *page {
	pn := newPen(c.s.W, c.s.H-c.barH, c.pf)
	back := image.Rect(pn.mx-12, 24, pn.mx+260, 124)
	pn.btn("home", "< home", back, pgBtn, pgText)
	pn.y = 230
	pn.text(c.pf.title, pgText, pn.mx, pn.y, "settings")

	pn.heading("DISPLAY")
	pn.line(c.pf.body, pgText, fmt.Sprintf("brightness  %d%%", c.cfg.Brightness))
	pn.row([]string{"bright-", "bright+"}, []string{"-", "+"}, "", false)
	pn.line(c.pf.body, pgText, "screen off after")
	sel := fmt.Sprintf("off%d", c.cfg.ScreenOff)
	pn.row([]string{"off0", "off1", "off5", "off10"}, []string{"never", "1 min", "5 min", "10 min"}, sel, false)

	pn.heading("WI-FI")
	ssid, ip := savedSSID(), wifiAddr()
	switch {
	case ip != "":
		pn.line(c.pf.body, pgText, "connected  "+ssid)
		pn.line(c.pf.small, pgMuted, "address "+ip+"   ssh root@"+ip)
	case ssid != "":
		pn.line(c.pf.body, pgText, "not connected  (saved: "+ssid+")")
	default:
		pn.line(c.pf.body, pgText, "no network saved")
		pn.line(c.pf.small, pgMuted, "in the terminal: wifi connect \"name\" \"password\"")
	}
	label := "reconnect"
	if c.wifiBusy {
		label = "connecting..."
	}
	pn.row([]string{"wifi"}, []string{label}, "", false)

	pn.heading("BATTERY")
	pn.line(c.pf.body, pgText, batteryInfo())

	pn.heading("SYSTEM")
	pn.line(c.pf.body, pgText, "alpine "+alpineVersion()+"  ·  kernel "+kernelRelease())
	pn.line(c.pf.small, pgMuted, "up "+uptime()+"   ·   hostname condor")

	pn.heading("POWER")
	ids := []string{"restart", "poweroff", "android"}
	labels := []string{"restart", "power off", "android"}
	for i, id := range ids {
		if c.confirm == id {
			labels[i] = "tap again"
		}
	}
	pn.row(ids, labels, c.confirm, true)
	pn.line(c.pf.small, pgMuted, "android: turns autostart off and restarts into Android")
	return pn.p
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
	default:
		c.page = c.launcherPage()
	}
	c.s.blitRGBA(c.page.img, 0, c.barH)
	if c.mode == modeStore && c.store.typing {
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
	id := c.page.hit(x, y-c.barH)
	if id == "" {
		return
	}
	if id != c.confirm {
		c.confirm = ""
	}
	if c.storeTap(id) || c.readerTap(id) {
		return
	}
	switch id {
	case "terminal":
		c.setMode(modeTerminal)
		return
	case "settings":
		c.setMode(modeSettings)
		return
	case "home":
		c.setMode(modeLauncher)
		return
	case "bright-", "bright+":
		step := 10
		if id == "bright-" {
			step = -10
		}
		c.cfg.Brightness = min(max(c.cfg.Brightness+step, 10), 100)
		setBacklight(c.cfg.Brightness)
		c.cfg.save()
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
