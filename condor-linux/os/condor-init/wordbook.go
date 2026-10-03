package main

import (
	"encoding/json"
	"fmt"
	"image"
	"os"
	"sort"
	"strings"
	"time"

	"condor-init/ui"
)

// The word book, as Soma's WordBook and src/lib/review-queue.ts: words kept from books with
// what they mean and the sentence they were met in, coming back for review at two days, a
// week and a month after the previous pass. After the third pass a word is learned and
// leaves the queue; a late review isn't punished, it just pushes the next one out.

var reviewIntervals = []int{2, 7, 30} // days after the previous pass

type wordEntry struct {
	ID       string   `json:"id"`
	Word     string   `json:"word"`
	Meaning  string   `json:"meaning"`
	Sentence string   `json:"sentence"`
	Book     string   `json:"book"`
	Lang     string   `json:"lang"`
	Added    string   `json:"added"` // 2006-01-02
	Reviews  []string `json:"reviews"`
}

type wordBook struct {
	Words []wordEntry `json:"words"`
}

var wordsPath = condorHome + "/words.json"

func loadWords() *wordBook {
	wb := &wordBook{}
	if b, err := os.ReadFile(wordsPath); err == nil {
		json.Unmarshal(b, wb)
	}
	return wb
}

func (wb *wordBook) save() {
	b, _ := json.MarshalIndent(wb, "", "  ")
	os.MkdirAll(condorHome, 0o755)
	tmp := wordsPath + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		os.Rename(tmp, wordsPath)
	}
}

func today() string { return time.Now().Format("2006-01-02") }

func addDays(date string, days int) string {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return date
	}
	return t.AddDate(0, 0, days).Format("2006-01-02")
}

func (w *wordEntry) stage() int { return min(len(w.Reviews), len(reviewIntervals)) }

// due is when the next pass falls due, or "" once learned.
func (w *wordEntry) due() string {
	s := w.stage()
	if s >= len(reviewIntervals) || strings.TrimSpace(w.Meaning) == "" {
		return ""
	}
	from := w.Added
	if len(w.Reviews) > 0 {
		from = w.Reviews[len(w.Reviews)-1]
	}
	return addDays(from, reviewIntervals[s])
}

// dueToday is every word due on or before day, most overdue first.
func (wb *wordBook) dueToday(day string) []*wordEntry {
	var out []*wordEntry
	for i := range wb.Words {
		if d := wb.Words[i].due(); d != "" && d <= day {
			out = append(out, &wb.Words[i])
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].due() < out[j].due() })
	return out
}

// keep files a word (or fills in the meaning of one already kept). It reports whether the
// word was new.
func (wb *wordBook) keep(e wordEntry) bool {
	for i := range wb.Words {
		if strings.EqualFold(wb.Words[i].Word, e.Word) {
			if wb.Words[i].Meaning == "" {
				wb.Words[i].Meaning = e.Meaning
			}
			if wb.Words[i].Sentence == "" {
				wb.Words[i].Sentence = e.Sentence
			}
			wb.save()
			return false
		}
	}
	e.ID = fmt.Sprintf("w%d", time.Now().UnixNano())
	if e.Added == "" {
		e.Added = today()
	}
	wb.Words = append(wb.Words, e)
	wb.save()
	return true
}

func (wb *wordBook) remove(id string) {
	var out []wordEntry
	for _, w := range wb.Words {
		if w.ID != id {
			out = append(out, w)
		}
	}
	wb.Words = out
	wb.save()
}

func (wb *wordBook) find(id string) *wordEntry {
	for i := range wb.Words {
		if wb.Words[i].ID == id {
			return &wb.Words[i]
		}
	}
	return nil
}

// --- the words app ---------------------------------------------------------------------

type wordsUI struct {
	view     string // "" list, "word", "review"
	sel      string // the open word's ID
	from     int    // first word on the list page
	shown    bool   // review: meaning revealed
	skipped  map[string]bool
	confirm  bool // delete asked once
	edit     bool // typing a meaning
	editText string
}

const wordsPerPage = 7

func (c *console) uiFonts() *readerFonts { return newReaderFonts("sans", 32, 1.45) }

func (c *console) wordsPage() *page {
	wb, st := c.words, &c.wui
	f := apple()
	h := c.s.H - c.barH
	img := canvas(c.s.W, h)
	ui.Fill(img, img.Rect, apBG)
	p := &page{img: img}
	mx := 48
	tw := c.s.W - 2*mx
	pill := func(id, label string, r image.Rectangle, primary bool) {
		bg, fg := apCard2, apBlue
		if primary {
			bg, fg = apBlue, rgb(0xffffff)
		}
		ui.RoundRect(img, r, r.Dy()/2, bg)
		apTextCenter(img, f.headline, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, fg, label)
		p.buttons = append(p.buttons, button{id, r})
	}
	back := func(label string) {
		iconBack(img, mx, 64, apBlue)
		apText(img, f.body, mx+30, 76, apBlue, label)
		p.buttons = append(p.buttons, button{"w:back", image.Rect(0, 10, 300, 120)})
	}
	switch st.view {
	case "word":
		w := wb.find(st.sel)
		if w == nil {
			st.view = ""
			return c.wordsPage()
		}
		back("Words")
		y := 150
		for _, l := range layoutWords(f.serifLarge, strings.Fields(w.Word), 0, tw, false) {
			y += 80
			drawWords(img, f.serifLarge, l, mx, y, apLabel)
		}
		meta := "Kept " + w.Added
		if w.Book != "" {
			meta = "From " + w.Book + "  ·  " + w.Added
		}
		apText(img, f.caption, mx, y+50, apSecondary, clip(f.caption, meta, tw))
		y += 120
		section := func(title string) {
			apText(img, f.captionBold, mx, y, apSecondary, strings.ToUpper(title))
			y += 20
		}
		section("Meaning")
		meaning := w.Meaning
		if st.edit {
			meaning = st.editText + "|"
		}
		col := apLabel
		if meaning == "" {
			meaning, col = "No meaning yet. Tap Write Meaning to add one.", apSecondary
		}
		y = drawParagraphs(img, f.body, meaning, mx, y, tw, 48, y+8*48, col) + 40
		if w.Sentence != "" {
			section("In the book")
			y = drawParagraphs(img, f.callout, "“"+w.Sentence+"”", mx, y, tw, 44, y+6*44, apSecondary) + 40
		}
		section("Review")
		status := "Learned: all three reviews done."
		if d := w.due(); d != "" {
			status = fmt.Sprintf("Review %d of 3, due %s.", w.stage()+1, d)
		} else if w.Meaning == "" {
			status = "Comes up for review once it has a meaning."
		}
		apText(img, f.body, mx, y+30, apLabel, status)
		y += 80
		half := (tw - 24) / 2
		if st.edit {
			pill("w:editdone", "Save Meaning", image.Rect(mx, y, mx+tw, y+96), true)
		} else {
			pill("w:edit", "Write Meaning", image.Rect(mx, y, mx+half, y+96), false)
			del := "Delete"
			if st.confirm {
				del = "Tap Again to Delete"
			}
			r := image.Rect(mx+half+24, y, mx+tw, y+96)
			ui.RoundRect(img, r, 48, apCard2)
			apTextCenter(img, f.headline, (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, apRed, del)
			p.buttons = append(p.buttons, button{"w:delete", r})
		}
		return p

	case "review":
		back("Words")
		var queue []*wordEntry
		for _, w := range wb.dueToday(today()) {
			if !st.skipped[w.ID] {
				queue = append(queue, w)
			}
		}
		apText(img, f.serifLarge, mx, 228, apLabel, "Review")
		if len(queue) == 0 {
			apText(img, f.title, mx, 360, apLabel, "All done for today")
			if next := wb.nextUp(); next != nil {
				apText(img, f.callout, mx, 420, apSecondary, fmt.Sprintf("Next: “%s” on %s", next.Word, next.due()))
			}
			return p
		}
		w := queue[0]
		apText(img, f.caption, mx, 280, apSecondary, fmt.Sprintf("%d to go  ·  review %d of 3", len(queue), w.stage()+1))
		card := image.Rect(mx, 320, c.s.W-mx, h-250)
		shadow(img, card, 30, 0.10)
		ui.RoundRect(img, card, 30, apGrouped)
		y := card.Min.Y + 70
		for _, l := range layoutWords(f.serifTitle, strings.Fields(w.Word), 0, card.Dx()-80, false) {
			y += 40
			drawWords(img, f.serifTitle, l, card.Min.X+40, y, apLabel)
			y += 24
		}
		if w.Sentence != "" {
			y = drawParagraphs(img, f.callout, "“"+w.Sentence+"”", card.Min.X+40, y+20, card.Dx()-80, 44, y+20+5*44, apSecondary)
		}
		if st.shown {
			ui.Fill(img, image.Rect(card.Min.X+40, y+24, card.Max.X-40, y+26), apSeparator)
			drawParagraphs(img, f.body, w.Meaning, card.Min.X+40, y+54, card.Dx()-80, 48, card.Max.Y-30, apLabel)
			half := (tw - 24) / 2
			pill("w:notyet", "Not Yet", image.Rect(mx, h-200, mx+half, h-104), false)
			pill("w:gotit", "Got It", image.Rect(mx+half+24, h-200, mx+tw, h-104), true)
		} else {
			pill("w:show", "Show Meaning", image.Rect(mx, h-200, mx+tw, h-104), true)
		}
		p.buttons = append(p.buttons, button{"w:show", card})
		return p
	}

	// The list.
	c.booksTabs(p, "tab:words")
	apText(img, f.serifLarge, mx, 228, apLabel, "Words")
	due := len(wb.dueToday(today()))
	apText(img, f.caption, mx, 280, apSecondary, fmt.Sprintf("%d words kept  ·  %d to review today", len(wb.Words), due))
	y := 310
	if due > 0 {
		pill("w:review", fmt.Sprintf("Review Now (%d)", due), image.Rect(mx, y, mx+tw, y+96), true)
		y += 126
	}
	if len(wb.Words) == 0 {
		apText(img, f.title, mx, y+100, apLabel, "No words yet")
		drawParagraphs(img, f.callout, "In a book, touch and hold a word, then tap Keep Word. It comes back for review after two days, a week and a month.",
			mx, y+130, tw, 44, y+400, apSecondary)
		return p
	}
	list := make([]*wordEntry, len(wb.Words))
	for i := range wb.Words {
		list[len(list)-1-i] = &wb.Words[i] // newest first
	}
	per := wordsPerPage
	st.from = min(st.from, (len(list)-1)/per*per)
	grp := image.Rect(mx, y, c.s.W-mx, y+min(per, len(list)-st.from)*170)
	ui.RoundRect(img, grp, 26, apGrouped)
	for i := st.from; i < len(list) && i < st.from+per; i++ {
		w := list[i]
		r := image.Rect(mx, y, c.s.W-mx, y+170)
		if i > st.from {
			ui.Fill(img, image.Rect(mx+32, r.Min.Y, c.s.W-mx, r.Min.Y+1), apSeparator)
		}
		if l := layoutWords(f.headline, strings.Fields(w.Word), 0, r.Dx()-110, false); len(l) > 0 {
			drawWords(img, f.headline, l[0], r.Min.X+32, r.Min.Y+56, apLabel)
		}
		meaning := w.Meaning
		if meaning == "" {
			meaning = "No meaning yet"
		}
		if l := layoutWords(f.callout, strings.Fields(meaning), 0, r.Dx()-110, false); len(l) > 0 {
			drawWords(img, f.callout, l[0], r.Min.X+32, r.Min.Y+102, apSecondary)
		}
		state := "Learned"
		if d := w.due(); d != "" {
			state = "Next review " + d
		} else if strings.TrimSpace(w.Meaning) == "" {
			state = "Needs a meaning"
		}
		if w.Book != "" {
			state += "  ·  " + w.Book
		}
		apText(img, f.caption, r.Min.X+32, r.Min.Y+144, apSecondary, clip(f.caption, state, r.Dx()-110))
		iconChevronRight(img, r.Max.X-44, (r.Min.Y+r.Max.Y)/2, apSecondary)
		p.buttons = append(p.buttons, button{"w:open:" + w.ID, r})
		y += 170
	}
	if len(list) > per {
		by := h - 100
		apText(img, f.body, mx, by+48, apBlue, "‹ Previous")
		p.buttons = append(p.buttons, button{"w:prev", image.Rect(0, by, 360, by+90)})
		apTextCenter(img, f.caption, c.s.W/2, by+38, apSecondary, fmt.Sprintf("%d–%d of %d", st.from+1, min(st.from+per, len(list)), len(list)))
		apTextRight(img, f.body, c.s.W-mx, by+48, apBlue, "Next ›")
		p.buttons = append(p.buttons, button{"w:next", image.Rect(c.s.W-360, by, c.s.W, by+90)})
	}
	return p
}

// nextUp is the next word coming for review after today.
func (wb *wordBook) nextUp() *wordEntry {
	var best *wordEntry
	for i := range wb.Words {
		d := wb.Words[i].due()
		if d == "" || d <= today() {
			continue
		}
		if best == nil || d < best.due() {
			best = &wb.Words[i]
		}
	}
	return best
}

// wordsTap handles the words app. Caller holds drawMu.
func (c *console) wordsTap(id string) bool {
	st := &c.wui
	if id == "words" {
		*st = wordsUI{}
		c.transition("push", image.Rectangle{}, func() { c.setMode(modeWords) })
		return true
	}
	if !strings.HasPrefix(id, "w:") {
		return false
	}
	switch { // moving between the list, a word and the review: slide
	case strings.HasPrefix(id, "w:open:") || id == "w:review":
		c.transition("push", image.Rectangle{}, func() { c.wordsTapNow(id) })
		return true
	case id == "w:back" && !(st.view == "word" && st.edit):
		c.transition("pop", image.Rectangle{}, func() { c.wordsTapNow(id) })
		return true
	}
	return c.wordsTapNow(id)
}

func (c *console) wordsTapNow(id string) bool {
	st, wb := &c.wui, c.words
	if !strings.HasPrefix(id, "w:") {
		return false
	}
	if id != "w:delete" {
		st.confirm = false
	}
	switch {
	case id == "w:back":
		if st.view == "word" && st.edit {
			st.edit = false
		} else {
			st.view, st.shown = "", false
		}
	case strings.HasPrefix(id, "w:open:"):
		st.view, st.sel, st.edit = "word", strings.TrimPrefix(id, "w:open:"), false
	case id == "w:review":
		st.view, st.shown, st.skipped = "review", false, map[string]bool{}
	case id == "w:show":
		st.shown = true
	case id == "w:gotit" || id == "w:notyet":
		var queue []*wordEntry
		for _, w := range wb.dueToday(today()) {
			if !st.skipped[w.ID] {
				queue = append(queue, w)
			}
		}
		if len(queue) > 0 {
			if id == "w:gotit" {
				queue[0].Reviews = append(queue[0].Reviews, today())
				wb.save()
			} else {
				st.skipped[queue[0].ID] = true
			}
		}
		st.shown = false
	case id == "w:delete":
		if !st.confirm {
			st.confirm = true
		} else {
			wb.remove(st.sel)
			st.view, st.confirm = "", false
		}
	case id == "w:edit":
		if w := wb.find(st.sel); w != nil {
			st.edit, st.editText = true, w.Meaning
			c.skb.visible = true
		}
	case id == "w:editdone":
		if w := wb.find(st.sel); w != nil {
			w.Meaning = strings.TrimSpace(st.editText)
			wb.save()
		}
		st.edit = false
	case id == "w:prev":
		st.from = max(st.from-wordsPerPage, 0)
	case id == "w:next":
		if st.from+wordsPerPage < len(wb.Words) {
			st.from += wordsPerPage
		}
	}
	c.showPage()
	return true
}

// wordsKey types a meaning with the keyboard. Caller holds drawMu.
func (c *console) wordsKey(b []byte) {
	st := &c.wui
	if len(b) == 0 || b[0] == 0x1b {
		return
	}
	for _, ch := range string(b) {
		switch {
		case ch == '\r' || ch == '\n':
			c.wordsTap("w:editdone")
			return
		case ch == 0x7f || ch == 0x08:
			if r := []rune(st.editText); len(r) > 0 {
				st.editText = string(r[:len(r)-1])
			}
		case ch >= ' ' && len(st.editText) < 400:
			st.editText += string(ch)
		}
	}
	c.showPage()
}
