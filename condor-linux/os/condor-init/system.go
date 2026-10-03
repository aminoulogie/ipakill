package main

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"strings"
	"time"

	"github.com/go-fonts/liberation/liberationserifbold"

	"condor-init/fonts"
	"condor-init/ui"
)

// The system's own screens in iPadOS's look: the home screen (wallpaper, widgets, app icons,
// dock) and Settings (a sidebar of panes with coloured icons, grouped lists on the right).

// --- wallpaper --------------------------------------------------------------------------------

var (
	wallpaperTop = rgb(0x1d3473)
	wallpaperMid = rgb(0x5a3ea6)
	wallpaperBot = rgb(0xe6809f)
	wallpaper    *image.RGBA
)

// wallpaperFor draws (once) a soft gradient with two glows, like iPadOS's default.
func wallpaperFor(w, h int) *image.RGBA {
	if wallpaper != nil && wallpaper.Rect.Dx() == w && wallpaper.Rect.Dy() == h {
		return wallpaper
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		t := float64(y) / float64(h)
		var c color.RGBA
		if t < 0.55 {
			c = blend(wallpaperTop, wallpaperMid, t/0.55)
		} else {
			c = blend(wallpaperMid, wallpaperBot, (t-0.55)/0.45)
		}
		ui.Fill(img, image.Rect(0, y, w, y+1), c)
	}
	glow := func(cx, cy, rad int, c color.RGBA, a float64) {
		for y := max(cy-rad, 0); y < min(cy+rad, h); y++ {
			row := img.Pix[img.PixOffset(0, y):]
			for x := max(cx-rad, 0); x < min(cx+rad, w); x++ {
				d := math.Hypot(float64(x-cx), float64(y-cy)) / float64(rad)
				if d >= 1 {
					continue
				}
				k := a * (1 - d) * (1 - d)
				i := 4 * x
				row[i] = uint8(float64(row[i])*(1-k) + float64(c.R)*k)
				row[i+1] = uint8(float64(row[i+1])*(1-k) + float64(c.G)*k)
				row[i+2] = uint8(float64(row[i+2])*(1-k) + float64(c.B)*k)
			}
		}
	}
	glow(w*3/4, h/4, w*2/3, rgb(0x3fa0ff), 0.55)
	glow(w/5, h*2/3, w*3/5, rgb(0xff6fb0), 0.45)
	wallpaper = img
	return img
}

// --- app icons -----------------------------------------------------------------------------

// squircle fills an app-icon shape with a vertical gradient.
func squircle(img *image.RGBA, r image.Rectangle, top, bot color.RGBA) {
	rad := float64(r.Dx()) * 0.225
	for y := r.Min.Y; y < r.Max.Y; y++ {
		dy := 0.0
		if fy := float64(y-r.Min.Y) + 0.5; fy < rad {
			dy = rad - fy
		} else if fy := float64(r.Max.Y-y) - 0.5; fy < rad {
			dy = rad - fy
		}
		inset := 0
		if dy > 0 {
			inset = int(rad - math.Sqrt(max(rad*rad-dy*dy, 0)) + 0.5)
		}
		c := blend(top, bot, float64(y-r.Min.Y)/float64(r.Dy()))
		ui.Fill(img, image.Rect(r.Min.X+inset, y, r.Max.X-inset, y+1), c)
	}
}

type appIcon struct{ id, name string }

// drawAppIcon draws an app's icon in r.
func drawAppIcon(img *image.RGBA, r image.Rectangle, id string) {
	f := apple()
	cx, cy := (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2
	s := float64(r.Dx()) / 150
	sc := func(v float64) int { return int(v * s) }
	white := rgb(0xffffff)
	// A shadow under every icon.
	for i := 1; i <= 8; i++ {
		blendRect(img, image.Rect(r.Min.X+sc(14), r.Max.Y+i-4, r.Max.X-sc(14), r.Max.Y+i-3), rgb(0x000000), 0.05)
	}
	switch id {
	case "books": // Apple Books: an open book on orange
		squircle(img, r, rgb(0xffa733), rgb(0xff6e00))
		for side := -1; side <= 1; side += 2 {
			for i := 0; i < sc(48); i++ {
				x := cx + side*(sc(4)+i)
				dy := int(float64(i) * 0.18)
				ui.Fill(img, image.Rect(min(x, x+side), cy-sc(30)-dy, max(x, x+side)+1, cy+sc(30)-dy), white)
			}
		}
	case "store": // the Book Store: a bag on blue
		squircle(img, r, rgb(0x2fb6ff), rgb(0x0a6cff))
		ui.RoundRect(img, image.Rect(cx-sc(36), cy-sc(18), cx+sc(36), cy+sc(42)), sc(10), white)
		ring(img, cx, cy-sc(20), sc(20), sc(8), 0.5, white, white)
		ui.Fill(img, image.Rect(cx-sc(36), cy-sc(18), cx+sc(36), cy-sc(14)), white)
		apTextCenter(img, f.headline, cx, cy+sc(14), rgb(0x0a84ff), "B")
	case "words": // the word book: "Aa" on indigo
		squircle(img, r, rgb(0x7d7aff), rgb(0x4b3fd9))
		apTextCenter(img, textFace("libserif-bold", liberationserifbold.TTF, true, 62*s), cx, cy+sc(2), white, "Aa")
	case "terminal": // macOS Terminal: a prompt on black
		squircle(img, r, rgb(0x4a4a4e), rgb(0x2a2a2c))
		squircle(img, r.Inset(sc(8)), rgb(0x1c1c1e), rgb(0x0b0b0c))
		ui.DrawText(img, apple().headline, r.Min.X+sc(28), r.Min.Y+sc(66), rgb(0xe8e8e8), ">_")
	case "settings": // a gear on grey
		squircle(img, r, rgb(0x9a9aa0), rgb(0x5f5f64))
		dark := rgb(0x3a3a3c)
		for a := 0; a < 360; a += 30 {
			t := float64(a) * math.Pi / 180
			line(img, cx+int(float64(sc(30))*math.Cos(t)), cy+int(float64(sc(30))*math.Sin(t)),
				cx+int(float64(sc(48))*math.Cos(t)), cy+int(float64(sc(48))*math.Sin(t)), sc(12), dark)
		}
		ui.Circle(img, cx, cy, sc(38), dark)
		ui.Circle(img, cx, cy, sc(30), rgb(0xd1d1d6))
		ui.Circle(img, cx, cy, sc(13), dark)
	}
}

// blendRoundRect lays c at alpha a over a rounded rectangle (the dock's frosted glass).
func blendRoundRect(img *image.RGBA, r image.Rectangle, rad int, c color.RGBA, a float64) {
	fr := float64(rad)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		dy := 0.0
		if fy := float64(y-r.Min.Y) + 0.5; fy < fr {
			dy = fr - fy
		} else if fy := float64(r.Max.Y-y) - 0.5; fy < fr {
			dy = fr - fy
		}
		inset := 0
		if dy > 0 {
			inset = int(fr - math.Sqrt(max(fr*fr-dy*dy, 0)) + 0.5)
		}
		blendRect(img, image.Rect(r.Min.X+inset, y, r.Max.X-inset, y+1), c, a)
	}
}

// --- the home screen -------------------------------------------------------------------------

func (c *console) launcherPage() *page {
	f := apple()
	w, h := c.s.W, c.s.H-c.barH
	img := canvas(w, h)
	copy(img.Pix, wallpaperFor(w, h).Pix)
	p := &page{img: img}
	white := rgb(0xffffff)
	c.shelf = findBooks()

	// Widgets: a calendar, and the book being read.
	now := time.Now().In(displayZone)
	cal := image.Rect(60, 40, 400, 380)
	shadow(img, cal, 44, 0.10)
	ui.RoundRect(img, cal, 44, white)
	apText(img, f.captionBold, cal.Min.X+36, cal.Min.Y+62, rgb(0xff3b30), strings.ToUpper(now.Format("Monday")))
	apText(img, textFace("inter-bold", fonts.InterBold, true, 130), cal.Min.X+30, cal.Min.Y+200, apLabel, fmt.Sprint(now.Day()))
	apText(img, f.callout, cal.Min.X+36, cal.Max.Y-44, apSecondary, now.Format("January 2006"))

	bw := image.Rect(440, 40, w-60, 380)
	shadow(img, bw, 44, 0.10)
	ui.RoundRect(img, bw, 44, white)
	if now := c.readingNow(); len(now) > 0 {
		b := c.shelf[now[0]]
		cr := image.Rect(bw.Min.X+36, bw.Min.Y+36, bw.Min.X+36+180, bw.Min.Y+36+268)
		shadowRect(img, cr)
		c.drawCover(img, cr, &storeItem{key: b.path, title: b.title, author: b.author, cover: b.cover})
		x, tw := cr.Max.X+32, bw.Max.X-cr.Max.X-68
		apText(img, f.captionBold, x, bw.Min.Y+70, rgb(0xff8a00), "CONTINUE READING")
		ty := bw.Min.Y + 120
		for i, l := range layoutWords(f.headline, strings.Fields(b.title), 0, tw, false) {
			if i == 2 {
				break
			}
			drawWords(img, f.headline, l, x, ty, apLabel)
			ty += 42
		}
		apText(img, f.callout, x, ty+4, apSecondary, clip(f.callout, b.author, tw))
		pct := c.lib.Progress[b.path].Pct
		bar := image.Rect(x, bw.Max.Y-74, x+tw-80, bw.Max.Y-66)
		ui.RoundRect(img, bar, 4, apCard2)
		ui.RoundRect(img, image.Rect(bar.Min.X, bar.Min.Y, bar.Min.X+max(bar.Dx()*pct/100, 8), bar.Max.Y), 4, apLabel)
		apTextRight(img, f.caption, x+tw, bw.Max.Y-60, apSecondary, fmt.Sprintf("%d%%", pct))
		p.buttons = append(p.buttons, button{fmt.Sprintf("book%d", now[0]), bw})
	} else {
		mins, goal := c.lib.readingToday(), c.lib.Prefs.GoalMinutes
		ring(img, bw.Min.X+150, (bw.Min.Y+bw.Max.Y)/2, 90, 22, float64(mins)/float64(max(goal, 1)), rgb(0xd7eef8), apRingBlue)
		apTextCenter(img, f.title, bw.Min.X+150, (bw.Min.Y+bw.Max.Y)/2, apLabel, fmt.Sprint(mins))
		apText(img, f.captionBold, bw.Min.X+290, bw.Min.Y+130, rgb(0xff8a00), "READING GOAL")
		tw := bw.Max.X - bw.Min.X - 320
		apText(img, f.headline, bw.Min.X+290, bw.Min.Y+184, apLabel, clip(f.headline, fmt.Sprintf("%d of %d minutes today", mins, goal), tw))
		apText(img, f.callout, bw.Min.X+290, bw.Min.Y+232, apSecondary, clip(f.callout, "Open Books to read.", tw))
		p.buttons = append(p.buttons, button{"books", bw})
	}

	// Apps.
	apps := []appIcon{{"books", "Books"}, {"store", "Book Store"}, {"words", "Words"}, {"terminal", "Terminal"}, {"settings", "Settings"}}
	const size = 150
	gap := (w - 5*size) / 6
	for i, a := range apps {
		x := gap + i*(size+gap)
		r := image.Rect(x, 470, x+size, 470+size)
		drawAppIcon(img, r, a.id)
		// Labels in white with a soft shadow, as on a wallpaper.
		apTextCenter(img, f.caption, r.Min.X+size/2+1, r.Max.Y+38, blend(wallpaperMid, rgb(0x000000), 0.5), a.name)
		apTextCenter(img, f.caption, r.Min.X+size/2, r.Max.Y+36, white, a.name)
		p.buttons = append(p.buttons, button{a.id, image.Rect(r.Min.X-20, r.Min.Y-10, r.Max.X+20, r.Max.Y+56)})
	}

	// The dock.
	dock := []string{"books", "store", "terminal", "settings"}
	const ds = 130
	dw := len(dock)*ds + (len(dock)+1)*36
	dr := image.Rect((w-dw)/2, h-230, (w+dw)/2, h-60)
	blendRoundRect(img, dr, 44, white, 0.28)
	for i, id := range dock {
		x := dr.Min.X + 36 + i*(ds+36)
		r := image.Rect(x, dr.Min.Y+20, x+ds, dr.Min.Y+20+ds)
		drawAppIcon(img, r, id)
		p.buttons = append(p.buttons, button{id, r})
	}
	// The page dot.
	ui.Circle(img, w/2, dr.Min.Y-40, 7, white)
	return p
}

// --- Settings ------------------------------------------------------------------------------

type settingsPane struct {
	id, name string
	icon     color.RGBA
}

var settingsPanes = []settingsPane{
	{"wifi", "Wi-Fi", rgb(0x0a84ff)},
	{"battery", "Battery", rgb(0x34c759)},
	{"display", "Display & Brightness", rgb(0x0a84ff)},
	{"general", "General", rgb(0x8e8e93)},
	{"power", "Restart & Shut Down", rgb(0xff3b30)},
}

// paneGlyph draws a pane's white symbol on its coloured square.
func paneGlyph(img *image.RGBA, id string, cx, cy int) {
	white := rgb(0xffffff)
	switch id {
	case "wifi":
		for _, rad := range []int{6, 12, 18} {
			for a := 225.0; a <= 315; a += 4 {
				t := a * math.Pi / 180
				ui.Circle(img, cx+int(float64(rad)*math.Cos(t)), cy+8+int(float64(rad)*math.Sin(t)), 2, white)
			}
		}
		ui.Circle(img, cx, cy+8, 3, white)
	case "battery":
		ui.RoundRect(img, image.Rect(cx-16, cy-8, cx+12, cy+8), 3, white)
		ui.Fill(img, image.Rect(cx+13, cy-3, cx+16, cy+3), white)
	case "display":
		ui.DrawText(img, apple().captionBold, cx-15, cy+9, white, "AA")
	case "general":
		ring(img, cx, cy, 11, 6, 1, white, white)
		for a := 0; a < 360; a += 45 {
			t := float64(a) * math.Pi / 180
			ui.Circle(img, cx+int(15*math.Cos(t)), cy+int(15*math.Sin(t)), 3, white)
		}
	case "power":
		ring(img, cx, cy+2, 12, 4, 0.85, white, white)
		ui.Fill(img, image.Rect(cx-2, cy-16, cx+2, cy), white)
	}
}

// groupRows draws an inset grouped list section and returns the rows' rectangles.
func groupRows(img *image.RGBA, x, y, w, n, rh int) []image.Rectangle {
	ui.RoundRect(img, image.Rect(x, y, x+w, y+n*rh), 24, apBG)
	var rows []image.Rectangle
	for i := 0; i < n; i++ {
		r := image.Rect(x, y+i*rh, x+w, y+(i+1)*rh)
		if i > 0 {
			ui.Fill(img, image.Rect(x+30, r.Min.Y, x+w, r.Min.Y+1), apSeparator)
		}
		rows = append(rows, r)
	}
	return rows
}

func (c *console) settingsPage() *page {
	f := apple()
	w, h := c.s.W, c.s.H-c.barH
	img := canvas(w, h)
	ui.Fill(img, img.Rect, apGrouped)
	p := &page{img: img}
	if c.setPane == "" {
		c.setPane = "wifi"
	}

	// The sidebar.
	const sw = 500
	ui.Fill(img, image.Rect(sw, 0, sw+1, h), apSeparator)
	iconBack(img, 32, 64, apBlue)
	apText(img, f.body, 60, 76, apBlue, "Home")
	p.buttons = append(p.buttons, button{"home", image.Rect(0, 10, 220, 120)})
	apText(img, f.largeTitle, 32, 200, apLabel, "Settings")
	// The device card, like the Apple Account row.
	dc := image.Rect(24, 240, sw-24, 360)
	ui.RoundRect(img, dc, 24, apBG)
	ui.Circle(img, dc.Min.X+60, (dc.Min.Y+dc.Max.Y)/2, 40, rgb(0x8e8e93))
	apTextCenter(img, f.headline, dc.Min.X+60, (dc.Min.Y+dc.Max.Y)/2, rgb(0xffffff), "C")
	apText(img, f.headline, dc.Min.X+120, dc.Min.Y+54, apLabel, "condor")
	apText(img, f.caption, dc.Min.X+120, dc.Min.Y+92, apSecondary, "Condor TRA-901G")
	y := 400
	ui.RoundRect(img, image.Rect(24, y, sw-24, y+len(settingsPanes)*84), 24, apBG)
	for i, pn := range settingsPanes {
		r := image.Rect(24, y+i*84, sw-24, y+(i+1)*84)
		on := pn.id == c.setPane
		fg := apLabel
		if on {
			ui.RoundRect(img, r.Inset(4), 18, apBlue)
			fg = rgb(0xffffff)
		} else if i > 0 {
			ui.Fill(img, image.Rect(r.Min.X+92, r.Min.Y, r.Max.X, r.Min.Y+1), apSeparator)
		}
		ir := image.Rect(r.Min.X+20, r.Min.Y+20, r.Min.X+64, r.Min.Y+64)
		ui.RoundRect(img, ir, 11, pn.icon)
		paneGlyph(img, pn.id, (ir.Min.X+ir.Max.X)/2, (ir.Min.Y+ir.Max.Y)/2)
		apText(img, f.callout, r.Min.X+86, r.Min.Y+52, fg, clip(f.callout, pn.name, r.Dx()-110))
		p.buttons = append(p.buttons, button{"set:pane:" + pn.id, r})
	}

	// The pane.
	x, pw := sw+40, w-sw-80
	pane := settingsPanes[0]
	for _, pn := range settingsPanes {
		if pn.id == c.setPane {
			pane = pn
		}
	}
	apTextCenter(img, f.headline, sw+(w-sw)/2, 70, apLabel, pane.name)
	y = 150
	header := func(s string) {
		apText(img, f.caption, x+30, y+40, apSecondary, strings.ToUpper(s))
		y += 56
	}
	footer := func(s string) {
		y = drawParagraphs(img, f.caption, s, x+30, y+14, pw-60, 34, y+200, apSecondary) + 30
	}
	value := func(r image.Rectangle, name, v string) {
		apText(img, f.body, r.Min.X+30, r.Min.Y+56, apLabel, name)
		apTextRight(img, f.body, r.Max.X-30, r.Min.Y+56, apSecondary, clip(f.body, v, r.Dx()/2))
	}
	const rh = 88
	switch pane.id {
	case "wifi":
		ssid, ip := savedSSID(), wifiAddr()
		rows := groupRows(img, x, y, pw, 1, rh)
		apText(img, f.body, rows[0].Min.X+30, rows[0].Min.Y+56, apLabel, "Wi-Fi")
		iosSwitch(img, rows[0].Max.X-24, (rows[0].Min.Y+rows[0].Max.Y)/2, ip != "" || c.wifiBusy, false)
		if ip == "" && ssid != "" {
			p.buttons = append(p.buttons, button{"wifi", rows[0]}) // on: join the saved network
		}
		y += rh + 20
		if ssid != "" {
			header("Network")
			rows = groupRows(img, x, y, pw, 2, rh)
			if ip != "" {
				iconCheck(img, rows[0].Min.X+26, (rows[0].Min.Y+rows[0].Max.Y)/2, apBlue)
			}
			apText(img, f.body, rows[0].Min.X+76, rows[0].Min.Y+56, apLabel, ssid)
			state := "Not Connected"
			if ip != "" {
				state = "Connected"
			}
			apTextRight(img, f.body, rows[0].Max.X-30, rows[0].Min.Y+56, apSecondary, state)
			label := "Reconnect"
			if c.wifiBusy {
				label = "Connecting…"
			}
			apText(img, f.body, rows[1].Min.X+30, rows[1].Min.Y+56, apBlue, label)
			p.buttons = append(p.buttons, button{"wifi", rows[1]})
			y += 2 * rh
		}
		if ip != "" {
			header("Address")
			rows = groupRows(img, x, y, pw, 2, rh)
			value(rows[0], "IP Address", ip)
			value(rows[1], "SSH", "root@"+ip)
			y += 2 * rh
		}
		footer("To join a network, open Terminal and type: wifi connect \"name\" \"password\". The network is joined again at every start.")
	case "battery":
		pct, charging := ui.ReadBattery()
		rows := groupRows(img, x, y, pw, 1, 260)
		r := rows[0]
		if pct >= 0 {
			col := rgb(0x34c759)
			if pct <= 20 && !charging {
				col = rgb(0xff3b30)
			}
			ring(img, r.Min.X+130, (r.Min.Y+r.Max.Y)/2, 80, 22, float64(pct)/100, apCard2, col)
			apTextCenter(img, f.title, r.Min.X+130, (r.Min.Y+r.Max.Y)/2, apLabel, fmt.Sprintf("%d%%", pct))
			state := "On Battery"
			if charging {
				state = "Charging"
			}
			apText(img, f.title, r.Min.X+260, r.Min.Y+120, apLabel, state)
			apText(img, f.callout, r.Min.X+260, r.Min.Y+170, apSecondary, batteryInfo())
		} else {
			apText(img, f.body, r.Min.X+30, r.Min.Y+80, apSecondary, "No battery found.")
		}
		y += 280
		footer("The battery is the original one from 2014. Charge it with the 5 V 2 A adapter for the best results.")
	case "display":
		header("Brightness")
		rows := groupRows(img, x, y, pw, 1, 120)
		r := rows[0]
		tx0, tx1, ty := r.Min.X+110, r.Max.X-110, (r.Min.Y+r.Max.Y)/2
		ui.RoundRect(img, image.Rect(tx0, ty-4, tx1, ty+4), 4, apCard2)
		kx := tx0 + (tx1-tx0)*(c.cfg.Brightness-10)/90
		ui.RoundRect(img, image.Rect(tx0, ty-4, kx, ty+4), 4, apBlue)
		ui.Circle(img, kx, ty+2, 23, apSeparator)
		ui.Circle(img, kx, ty, 22, rgb(0xffffff))
		apTextCenter(img, f.headline, r.Min.X+56, ty, apSecondary, "−")
		apTextCenter(img, f.headline, r.Max.X-56, ty, apSecondary, "+")
		p.buttons = append(p.buttons, button{"bright-", image.Rect(r.Min.X, r.Min.Y, r.Min.X+r.Dx()/2, r.Max.Y)},
			button{"bright+", image.Rect(r.Min.X+r.Dx()/2, r.Min.Y, r.Max.X, r.Max.Y)})
		y += 140
		footer(fmt.Sprintf("%d%%. The volume buttons change it too, outside a book.", c.cfg.Brightness))
		header("Auto-Lock")
		opts := []struct {
			id, name string
			min      int
		}{{"off1", "1 Minute", 1}, {"off5", "5 Minutes", 5}, {"off10", "10 Minutes", 10}, {"off0", "Never", 0}}
		rows = groupRows(img, x, y, pw, len(opts), rh)
		for i, o := range opts {
			apText(img, f.body, rows[i].Min.X+30, rows[i].Min.Y+56, apLabel, o.name)
			if c.cfg.ScreenOff == o.min {
				iconCheck(img, rows[i].Max.X-60, (rows[i].Min.Y+rows[i].Max.Y)/2, apBlue)
			}
			p.buttons = append(p.buttons, button{o.id, rows[i]})
		}
		y += len(opts) * rh
		footer("The power button turns the screen off and on at any time.")
	case "general":
		header("About")
		ver := alpineVersion()
		if ver == "" {
			ver = "not installed"
		}
		host, _ := os.Hostname()
		items := [][2]string{{"Name", host}, {"Model", "Condor TRA-901G"}, {"Processor", "Intel Atom Z2580"},
			{"Software", "condor on Alpine " + ver}, {"Kernel", "Linux " + kernelRelease()}, {"Up For", uptime()}}
		rows := groupRows(img, x, y, pw, len(items), rh)
		for i, it := range items {
			value(rows[i], it[0], it[1])
		}
		y += len(items) * rh
		footer("Fonts: Inter (the system font), Liberation and DejaVu. Books from Project Gutenberg, the Internet Archive, Open Library and Google Books.")
	case "power":
		header("Power")
		items := []struct{ id, name string }{{"restart", "Restart"}, {"poweroff", "Shut Down"}, {"android", "Restart into Android"}}
		rows := groupRows(img, x, y, pw, len(items), rh)
		for i, it := range items {
			name, col := it.name, apBlue
			if it.id == "poweroff" || it.id == "android" {
				col = apRed
			}
			if c.confirm == it.id {
				name, col = "Tap Again to "+it.name, apRed
			}
			apText(img, f.body, rows[i].Min.X+30, rows[i].Min.Y+56, col, name)
			p.buttons = append(p.buttons, button{it.id, rows[i]})
		}
		y += len(items) * rh
		footer("Restart into Android turns condor's autostart off. To come back, run condor takeover auto on from the PC.")
	}
	return p
}
