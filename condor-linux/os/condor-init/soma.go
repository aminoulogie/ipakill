package main

import (
	"fmt"
	"image"
	"image/color"
	"strconv"
	"strings"
	"time"

	"condor-init/ui"
)

// The Soma tab: Soma (the PC and iPhone app) on the tablet, kept in step with them through
// Soma's own sync (somasync.go). A beta, kept simple: Today (habits, reading, to-dos),
// Calendar (a month; a day's plan, to-dos, training and food), Reports (training,
// nutrition, habits, reading). Ticking a habit, reading time and to-dos can be changed here;
// the rest is shown.

type somaUI struct {
	view    string    // "today", "calendar", "reports"
	month   time.Time // the calendar's month
	sel     string    // the calendar's day
	typing  bool
	field   string // what's being typed: "todo", "week", "day" (a to-do), "url", "code"
	editing string
	url     string // the sync address, typed before the code
	msg     string // the last thing that went wrong
}

func somaColour(hex string, def color.RGBA) color.RGBA {
	h := strings.TrimPrefix(strings.TrimSpace(hex), "#")
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	v, err := strconv.ParseUint(h, 16, 32)
	if len(h) != 6 || err != nil {
		return def
	}
	return rgb(uint32(v))
}

func (c *console) somaPage() *page {
	f := apple()
	w, mx := c.s.W, 48
	tw := w - 2*mx
	su := &c.somaUI
	if su.view == "" {
		su.view = "today"
	}
	soma.load()
	soma.mu.Lock()
	soma.changed = func() {
		drawMu.Lock()
		defer drawMu.Unlock()
		if c.mode == modeSoma && !c.somaUI.typing {
			c.redrawSoon(func() {
				if c.mode == modeSoma {
					c.showPage()
				}
			})
		}
	}
	soma.mu.Unlock()

	h := 3000
	if su.view == "today" {
		h = 2400
	}
	img := canvas(w, max(h, c.viewH()))
	ui.Fill(img, img.Rect, apBG)
	p := &page{img: img, header: tabsH}
	c.booksTabs(p, "tab:soma")
	btn := func(id string, r image.Rectangle) { p.buttons = append(p.buttons, button{id, r}) }
	pill := func(id, label string, r image.Rectangle, primary bool) {
		bg, fg := apCard2, apBlue
		if primary {
			bg, fg = apBlue, rgb(0xffffff)
		}
		ui.RoundRect(img, r, r.Dy()/2, bg)
		apTextCenter(img, f.headline, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, fg, label)
		btn(id, r)
	}
	field := func(id, label, value string, y int) {
		r := image.Rect(mx, y, w-mx, y+96)
		ui.RoundRect(img, r, 22, apCard)
		on := su.typing && su.field == id
		text, col := value, apLabel
		if text == "" && !on {
			text, col = label, apSecondary
		}
		if on {
			text += "|"
		}
		apText(img, f.body, r.Min.X+30, r.Min.Y+60, col, clip(f.body, text, r.Dx()-60))
		btn("so:type:"+id, r)
	}

	apText(img, f.serifLarge, mx, 228, apLabel, "Soma")
	if !soma.linked() {
		y := 300
		drawParagraphs(img, f.callout, "Link the tablet to your Soma: on the PC or the phone, open Soma > Settings > Sync, and type its sync address and recovery code here. The tablet then keeps your habits, to-dos, plans and reports in step with them.",
			mx, y, tw, 44, y+300, apSecondary)
		y += 240
		url := su.url
		if su.typing && su.field == "url" {
			url = su.editing
		}
		field("url", "Sync address (https://...workers.dev)", url, y)
		code := ""
		if su.typing && su.field == "code" {
			code = su.editing
		}
		field("code", "Recovery code (56 characters)", code, y+120)
		pill("so:link", "Link", image.Rect(mx, y+260, w-mx, y+356), true)
		if su.msg != "" {
			drawParagraphs(img, f.callout, su.msg, mx, y+420, tw, 44, y+560, apRed)
		}
		return p
	}

	apText(img, f.caption, mx, 280, apSecondary, time.Now().In(displayZone).Format("Monday 2 January")+"  ·  "+soma.status())
	apText(img, f.body, w-mx-150, 280, apBlue, "Sync now")
	btn("so:sync", image.Rect(w-mx-180, 230, w, 310))
	// The three parts, as a segmented control.
	seg := image.Rect(mx, 310, w-mx, 386)
	ui.RoundRect(img, seg, 20, apCard2)
	views := []struct{ id, label string }{{"today", "Today"}, {"calendar", "Calendar"}, {"reports", "Reports"}}
	for i, v := range views {
		r := image.Rect(seg.Min.X+i*seg.Dx()/3, seg.Min.Y, seg.Min.X+(i+1)*seg.Dx()/3, seg.Max.Y)
		if su.view == v.id {
			ui.RoundRect(img, r.Inset(6), 16, apCard)
		}
		apTextCenter(img, f.headline, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, apLabel, v.label)
		btn("so:view:"+v.id, r)
	}
	if su.msg != "" {
		apText(img, f.caption, mx, 430, apRed, clip(f.caption, su.msg, tw))
	}
	y := 460
	switch su.view {
	case "calendar":
		y = c.somaCalendar(p, y)
	case "reports":
		y = c.somaReports(p, y)
	default:
		y = c.somaToday(p, y)
	}
	if y+200 < img.Rect.Dy() && img.Rect.Dy() > c.viewH() {
		// Shorter than drawn: crop the page so it doesn't scroll into nothing.
		p.img = img.SubImage(image.Rect(0, 0, w, max(y+200, c.viewH()))).(*image.RGBA)
	}
	return p
}

// section draws a section title; returns the y under it.
func somaSection(img *image.RGBA, x, y int, title, note string) int {
	f := apple()
	apText(img, f.title, x, y+50, apLabel, title)
	if note != "" {
		apText(img, f.caption, x+ui.TextWidth(f.title, title)+24, y+50, apSecondary, note)
	}
	return y + 80
}

func (c *console) somaToday(p *page, y int) int {
	f := apple()
	img := p.img
	w, mx := c.s.W, 48
	date := somaToday()
	btn := func(id string, r image.Rectangle) { p.buttons = append(p.buttons, button{id, r}) }

	habits := somaHabits()
	done := 0
	for _, h := range habits {
		if h.done(date) {
			done++
		}
	}
	y = somaSection(img, mx, y, "Habits", fmt.Sprintf("%d of %d today", done, len(habits)))
	if len(habits) == 0 {
		apText(img, f.callout, mx, y+30, apSecondary, "No habits yet: add them in Soma.")
		y += 60
	}
	week := days(date, 7)
	for _, h := range habits {
		r := image.Rect(mx, y, w-mx, y+104)
		ui.RoundRect(img, r, 22, apCard)
		col := somaColour(h.Color, apBlue)
		cx, cy := r.Min.X+56, (r.Min.Y+r.Max.Y)/2
		if h.done(date) {
			ui.Circle(img, cx, cy, 26, col)
			iconCheck(img, cx-14, cy, rgb(0xffffff))
		} else {
			ui.Circle(img, cx, cy, 26, col)
			ui.Circle(img, cx, cy, 21, apCard)
		}
		apText(img, f.headline, r.Min.X+110, cy+12, apLabel, clip(f.headline, h.Name, r.Dx()-110-300))
		for i, d := range week { // the last seven days
			dx := r.Max.X - 260 + i*36
			if h.done(d) {
				ui.Circle(img, dx, cy, 11, col)
			} else {
				ui.Circle(img, dx, cy, 11, apCard2)
			}
		}
		btn("so:habit:"+h.ID, r)
		y += 120
	}

	y += 20
	y = somaSection(img, mx, y, "Reading", fmt.Sprintf("%d min today", somaReading(date)))
	for i, m := range []int{10, 15, 30} {
		r := image.Rect(mx+i*190, y, mx+i*190+170, y+84)
		ui.RoundRect(img, r, 42, apCard2)
		apTextCenter(img, f.headline, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, apBlue, fmt.Sprintf("+%d min", m))
		btn(fmt.Sprintf("so:read:%d", m), r)
	}
	y += 120

	todos := somaTodos()
	for _, list := range []struct{ scope, title, add string }{{"day", "To Do Today", "todo"}, {"week", "This Week", "week"}} {
		var items []somaTodo
		left := 0
		for _, t := range todos {
			if t.Scope == list.scope && t.active(date) {
				items = append(items, t)
				if !t.Done {
					left++
				}
			}
		}
		y = somaSection(img, mx, y, list.title, fmt.Sprintf("%d left", left))
		y = c.somaTodoRows(p, items, y)
		y = c.somaAddRow(p, list.add, "Add a to-do", y)
		y += 30
	}
	return y
}

// somaTodoRows draws to-dos as rows to tick.
func (c *console) somaTodoRows(p *page, items []somaTodo, y int) int {
	f := apple()
	img := p.img
	w, mx := c.s.W, 48
	for _, t := range items {
		r := image.Rect(mx, y, w-mx, y+92)
		ui.RoundRect(img, r, 22, apCard)
		cx, cy := r.Min.X+50, (r.Min.Y+r.Max.Y)/2
		ui.Circle(img, cx, cy, 22, apBlue)
		if t.Done {
			iconCheck(img, cx-13, cy, rgb(0xffffff))
		} else {
			ui.Circle(img, cx, cy, 18, apCard)
		}
		col := apLabel
		if t.Done {
			col = apSecondary
		}
		text := t.Text
		if t.SlotDate != "" {
			text = clockOf(float64(t.SlotStart)/60) + "  " + text
		}
		apText(img, f.body, r.Min.X+96, cy+14, col, clip(f.body, text, r.Dx()-130))
		p.buttons = append(p.buttons, button{"so:todo:" + t.ID, r})
		y += 104
	}
	return y
}

func (c *console) somaAddRow(p *page, id, label string, y int) int {
	f := apple()
	img := p.img
	w, mx := c.s.W, 48
	su := &c.somaUI
	r := image.Rect(mx, y, w-mx, y+92)
	ui.RoundRect(img, r, 22, apCard2)
	text, col := "+  "+label, apBlue
	if su.typing && su.field == id {
		text, col = su.editing+"|", apLabel
	}
	apText(img, f.body, r.Min.X+34, (r.Min.Y+r.Max.Y)/2+14, col, clip(f.body, text, r.Dx()-60))
	p.buttons = append(p.buttons, button{"so:type:" + id, r})
	return y + 104
}

func (c *console) somaCalendar(p *page, y int) int {
	f := apple()
	img := p.img
	w, mx := c.s.W, 48
	su := &c.somaUI
	now := time.Now().In(displayZone)
	if su.month.IsZero() {
		su.month = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, displayZone)
	}
	if su.sel == "" {
		su.sel = somaToday()
	}
	btn := func(id string, r image.Rectangle) { p.buttons = append(p.buttons, button{id, r}) }

	apText(img, f.title, mx, y+50, apLabel, su.month.Format("January 2006"))
	apText(img, f.title, w-mx-140, y+50, apBlue, "‹")
	apText(img, f.title, w-mx-40, y+50, apBlue, "›")
	btn("so:month:-1", image.Rect(w-mx-200, y, w-mx-90, y+80))
	btn("so:month:1", image.Rect(w-mx-90, y, w, y+80))
	y += 90
	cw := (w - 2*mx) / 7
	for i, d := range []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"} {
		apTextCenter(img, f.caption, mx+i*cw+cw/2, y+20, apSecondary, d)
	}
	y += 50
	todos := somaTodos()
	habits := somaHabits()
	first := su.month
	lead := (int(first.Weekday()) + 6) % 7
	daysIn := first.AddDate(0, 1, -1).Day()
	ch := 128
	for i := 0; i < lead+daysIn; i++ {
		if i < lead {
			continue
		}
		day := i - lead + 1
		date := time.Date(first.Year(), first.Month(), day, 0, 0, 0, 0, displayZone).Format("2006-01-02")
		r := image.Rect(mx+(i%7)*cw, y+(i/7)*ch, mx+(i%7+1)*cw, y+(i/7+1)*ch).Inset(4)
		bg := apCard
		if date == su.sel {
			bg = apBlue
		}
		ui.RoundRect(img, r, 18, bg)
		fg := apLabel
		if date == su.sel {
			fg = rgb(0xffffff)
		} else if date == somaToday() {
			fg = apBlue
		}
		apText(img, f.headline, r.Min.X+16, r.Min.Y+40, fg, strconv.Itoa(day))
		// What the day holds: training, to-dos, a plan, habits done.
		dots := []color.RGBA{}
		if somaTraining(date) != nil {
			dots = append(dots, rgb(0xff9f0a))
		}
		for _, t := range todos {
			if t.on(date) {
				dots = append(dots, rgb(0x0a84ff))
				break
			}
		}
		if soma.get("dayPlans/"+date) != "" {
			dots = append(dots, rgb(0xbf5af2))
		}
		n := 0
		for _, h := range habits {
			if h.done(date) {
				n++
			}
		}
		if n > 0 {
			apText(img, f.caption, r.Max.X-60, r.Min.Y+40, fg, fmt.Sprintf("%d✓", n))
		}
		for k, d := range dots {
			ui.Circle(img, r.Min.X+22+k*24, r.Max.Y-24, 8, d)
		}
		btn("so:day:"+date, r)
	}
	y += ((lead+daysIn+6)/7)*ch + 30
	lx := mx
	for _, l := range []struct {
		col   color.RGBA
		label string
	}{{rgb(0xff9f0a), "training"}, {rgb(0x0a84ff), "to-dos"}, {rgb(0xbf5af2), "day plan"}} {
		ui.Circle(img, lx+8, y+2, 8, l.col)
		apText(img, f.caption, lx+24, y+10, apSecondary, l.label)
		lx += 24 + ui.TextWidth(f.caption, l.label) + 36
	}
	apText(img, f.caption, lx, y+10, apSecondary, "n✓ habits done")
	y += 50

	// The chosen day.
	sel, _ := time.ParseInLocation("2006-01-02", su.sel, displayZone)
	y = somaSection(img, mx, y, sel.Format("Monday 2 January"), "")
	if plan := somaPlan(su.sel); len(plan) > 0 {
		for _, b := range plan {
			r := image.Rect(mx, y, w-mx, y+76)
			ui.RoundRect(img, r, 18, apCard)
			ui.RoundRect(img, image.Rect(r.Min.X, r.Min.Y, r.Min.X+12, r.Max.Y), 6, somaColour(b.Color, apBlue))
			apText(img, f.headline, r.Min.X+36, r.Min.Y+50, apLabel, clockOf(b.Start)+" – "+clockOf(b.Start+b.Hours))
			apText(img, f.body, r.Min.X+320, r.Min.Y+50, apLabel, clip(f.body, b.Label, r.Dx()-500))
			apText(img, f.caption, r.Max.X-130, r.Min.Y+48, apSecondary, fmt.Sprintf("%.2gh", b.Hours))
			y += 86
		}
	} else {
		apText(img, f.callout, mx, y+30, apSecondary, "No plan for this day (plan it in Soma).")
		y += 60
	}
	y += 10
	if s := somaTraining(su.sel); s != nil {
		apText(img, f.headline, mx, y+40, apLabel, "Training: "+s.Split)
		apText(img, f.callout, mx, y+84, apSecondary, fmt.Sprintf("%s  ·  %d sets  ·  %.0f kg volume  ·  %.0f kcal", s.Duration, s.Sets, s.Volume, s.Calories))
		y += 110
	}
	if n := somaNutrition(su.sel); n.Logged {
		apText(img, f.headline, mx, y+40, apLabel, "Food")
		apText(img, f.callout, mx, y+84, apSecondary, fmt.Sprintf("%.0f / %.0f kcal  ·  protein %.0f / %.0f g  ·  carbs %.0f g  ·  fat %.0f g", n.Cals, n.GoalCals, n.P, n.GoalP, n.C, n.F))
		y += 110
	}
	var items []somaTodo
	for _, t := range todos {
		if t.on(su.sel) && !t.Cleared {
			items = append(items, t)
		}
	}
	y = somaSection(img, mx, y, "To-dos", "")
	y = c.somaTodoRows(p, items, y)
	y = c.somaAddRow(p, "day", "Add a to-do for this day", y)
	return y
}

func (c *console) somaReports(p *page, y int) int {
	f := apple()
	img := p.img
	w, mx := c.s.W, 48
	date := somaToday()

	// Training: the last two weeks.
	var sessions []*somaSession
	for _, d := range days(date, 14) {
		if s := somaTraining(d); s != nil {
			sessions = append(sessions, s)
		}
	}
	vol, sets := 0.0, 0
	for _, s := range sessions {
		vol += s.Volume
		sets += s.Sets
	}
	y = somaSection(img, mx, y, "Training", fmt.Sprintf("%d sessions in 2 weeks  ·  %d sets  ·  %.0f kg", len(sessions), sets, vol))
	if len(sessions) == 0 {
		apText(img, f.callout, mx, y+30, apSecondary, "No training logged in the last two weeks.")
		y += 60
	}
	for i := len(sessions) - 1; i >= 0; i-- {
		s := sessions[i]
		t, _ := time.Parse("2006-01-02", s.Date)
		r := image.Rect(mx, y, w-mx, y+92)
		ui.RoundRect(img, r, 18, apCard)
		apText(img, f.headline, r.Min.X+30, r.Min.Y+40, apLabel, s.Split)
		apText(img, f.caption, r.Min.X+30, r.Min.Y+76, apSecondary, t.Format("Mon 2 Jan"))
		apText(img, f.callout, r.Min.X+420, r.Min.Y+58, apSecondary, fmt.Sprintf("%s  ·  %d sets  ·  %.0f kg  ·  %.0f kcal", s.Duration, s.Sets, s.Volume, s.Calories))
		y += 102
	}

	// Nutrition: the last week, calories against the goal.
	y += 20
	y = somaSection(img, mx, y, "Nutrition", "last 7 days")
	for _, d := range days(date, 7) {
		n := somaNutrition(d)
		t, _ := time.Parse("2006-01-02", d)
		r := image.Rect(mx, y, w-mx, y+92)
		ui.RoundRect(img, r, 18, apCard)
		apText(img, f.headline, r.Min.X+30, r.Min.Y+56, apLabel, t.Format("Mon 2"))
		if !n.Logged {
			apText(img, f.callout, r.Min.X+200, r.Min.Y+56, apSecondary, "nothing logged")
			y += 102
			continue
		}
		bar := image.Rect(r.Min.X+200, r.Min.Y+30, r.Min.X+600, r.Min.Y+62)
		ui.RoundRect(img, bar, 16, apCard2)
		if n.GoalCals > 0 {
			fill := min(n.Cals/n.GoalCals, 1.3) / 1.3
			col := rgb(0x30d158)
			if n.Cals > n.GoalCals*1.1 || n.Cals < n.GoalCals*0.8 {
				col = rgb(0xff9f0a)
			}
			ui.RoundRect(img, image.Rect(bar.Min.X, bar.Min.Y, bar.Min.X+max(int(float64(bar.Dx())*fill), 32), bar.Max.Y), 16, col)
		}
		txt := fmt.Sprintf("%.0f/%.0f kcal  P %.0f/%.0f", n.Cals, n.GoalCals, n.P, n.GoalP)
		if n.Kg > 0 {
			txt += fmt.Sprintf("  %.1f kg", n.Kg)
		}
		apText(img, f.callout, r.Min.X+630, r.Min.Y+58, apSecondary, clip(f.callout, txt, r.Max.X-r.Min.X-660))
		y += 102
	}

	// Habits: this week.
	y += 20
	monday := mondayOf(date)
	var week []string
	for _, d := range days(date, 7) {
		if d >= monday {
			week = append(week, d)
		}
	}
	y = somaSection(img, mx, y, "Habits", "this week")
	for _, h := range somaHabits() {
		n := 0
		for _, d := range week {
			if h.done(d) {
				n++
			}
		}
		r := image.Rect(mx, y, w-mx, y+76)
		ui.RoundRect(img, r, 18, apCard)
		apText(img, f.body, r.Min.X+30, r.Min.Y+50, apLabel, clip(f.body, h.Name, 460))
		bar := image.Rect(r.Min.X+520, r.Min.Y+26, r.Max.X-150, r.Min.Y+50)
		ui.RoundRect(img, bar, 12, apCard2)
		if n > 0 {
			ui.RoundRect(img, image.Rect(bar.Min.X, bar.Min.Y, bar.Min.X+max(bar.Dx()*n/max(len(week), 1), 24), bar.Max.Y), 12, somaColour(h.Color, apBlue))
		}
		apText(img, f.callout, r.Max.X-120, r.Min.Y+50, apSecondary, fmt.Sprintf("%d / %d", n, len(week)))
		y += 86
	}

	// Reading: the last week.
	y += 20
	total := 0
	for _, d := range days(date, 7) {
		total += somaReading(d)
	}
	y = somaSection(img, mx, y, "Reading", fmt.Sprintf("%d min in 7 days", total))
	top := 1
	for _, d := range days(date, 7) {
		top = max(top, somaReading(d))
	}
	bw := (w - 2*mx) / 7
	for i, d := range days(date, 7) {
		m := somaReading(d)
		bh := 200 * m / top
		x := mx + i*bw
		ui.RoundRect(img, image.Rect(x+20, y+220-bh, x+bw-20, y+220), 10, apBlue)
		t, _ := time.Parse("2006-01-02", d)
		apTextCenter(img, f.caption, x+bw/2, y+250, apSecondary, t.Format("Mon"))
		apTextCenter(img, f.caption, x+bw/2, y+200-bh, apSecondary, strconv.Itoa(m))
	}
	return y + 300
}

// somaTap handles the Soma tab's buttons. Caller holds drawMu.
func (c *console) somaTap(id string) bool {
	if !strings.HasPrefix(id, "so:") {
		return false
	}
	su := &c.somaUI
	su.msg = ""
	if su.typing && !strings.HasPrefix(id, "so:type:") {
		c.somaDone() // a tap elsewhere: what was typed is kept
	}
	switch {
	case strings.HasPrefix(id, "so:view:"):
		su.view = strings.TrimPrefix(id, "so:view:")
		c.setScroll(modeSoma, 0)
	case id == "so:sync":
		go soma.sync()
	case strings.HasPrefix(id, "so:habit:"):
		if err := somaToggleHabit(strings.TrimPrefix(id, "so:habit:")); err != nil {
			su.msg = err.Error()
		}
	case strings.HasPrefix(id, "so:todo:"):
		somaToggleTodo(strings.TrimPrefix(id, "so:todo:"))
	case strings.HasPrefix(id, "so:read:"):
		m, _ := strconv.Atoi(strings.TrimPrefix(id, "so:read:"))
		somaAddReading(somaToday(), m)
	case strings.HasPrefix(id, "so:month:"):
		d, _ := strconv.Atoi(strings.TrimPrefix(id, "so:month:"))
		su.month = su.month.AddDate(0, d, 0)
	case strings.HasPrefix(id, "so:day:"):
		su.sel = strings.TrimPrefix(id, "so:day:")
	case strings.HasPrefix(id, "so:type:"):
		f := strings.TrimPrefix(id, "so:type:")
		if !su.typing || su.field != f {
			su.typing, su.field, su.editing = true, f, ""
			if f == "url" {
				su.editing = su.url
			}
		}
		c.skb.visible = true
	case id == "so:link": // what was being typed was just tried (somaDone)
		if !soma.linked() && su.msg == "" {
			su.msg = "Type the sync address, then the recovery code."
		}
	}
	c.showPage()
	return true
}

// somaKey takes the keyboard's output while typing in Soma. Caller holds drawMu.
func (c *console) somaKey(b []byte) {
	su := &c.somaUI
	if len(b) == 0 || b[0] == 0x1b {
		return
	}
	for _, ch := range string(b) {
		switch {
		case ch == '\r' || ch == '\n':
			c.somaDone()
			c.showPage()
			return
		case ch == 0x7f || ch == 0x08:
			if r := []rune(su.editing); len(r) > 0 {
				su.editing = string(r[:len(r)-1])
			}
		case ch == 0x15:
			su.editing = ""
		case ch >= ' ' && len([]rune(su.editing)) < 200:
			su.editing += string(ch)
		}
	}
	c.showPage()
}

// somaDone finishes what was being typed: a to-do is added, the link is tried.
func (c *console) somaDone() {
	su := &c.somaUI
	text := strings.TrimSpace(su.editing)
	su.typing, su.editing = false, ""
	c.skb.visible = false
	switch su.field {
	case "todo":
		somaAddTodo(text, "day", somaToday())
	case "week":
		somaAddTodo(text, "week", somaToday())
	case "day":
		somaAddTodo(text, "day", su.sel)
	case "url":
		su.url = text
		if text != "" { // the code next
			su.typing, su.field = true, "code"
			c.skb.visible = true
		}
	case "code":
		if err := soma.setLink(su.url, text); err != nil {
			su.msg = err.Error()
		} else {
			su.msg = ""
			go soma.sync()
		}
	}
}
