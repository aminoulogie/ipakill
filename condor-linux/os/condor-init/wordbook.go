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
	h := c.s.H - c.barH
	pn := newPen(c.s.W, h, c.pf)
	f := c.uiFonts()
	textW := c.s.W - 2*pn.mx
	switch st.view {
	case "word":
		w := wb.find(st.sel)
		if w == nil {
			st.view = ""
			return c.wordsPage()
		}
		pn.btn("w:back", "< words", image.Rect(pn.mx-12, 20, pn.mx+260, 110), pgBtn, pgText)
		pn.y = 210
		for _, l := range layoutWords(f.bold, strings.Fields(w.Word), 0, textW, false) {
			drawWords(pn.p.img, f.bold, l, pn.mx, pn.y, pgText)
			pn.y += 64
		}
		meta := "from " + w.Book
		if w.Book == "" {
			meta = "kept"
		}
		meta += " · " + w.Added
		pn.text(c.pf.small, pgMuted, pn.mx, pn.y, clip(c.pf.small, meta, textW))
		pn.heading("MEANING")
		pn.y += 20
		meaning := w.Meaning
		if st.edit {
			meaning = st.editText + "_"
		}
		if meaning == "" {
			meaning = "no meaning yet: tap \"write meaning\" to type one"
		}
		pn.y = drawParagraphs(pn.p.img, f.body, meaning, pn.mx, pn.y, textW, f.lh, pn.y+8*f.lh, pgText)
		if w.Sentence != "" {
			pn.heading("IN THE BOOK")
			pn.y += 20
			pn.y = drawParagraphs(pn.p.img, f.body, "“"+w.Sentence+"”", pn.mx, pn.y, textW, f.lh, pn.y+6*f.lh, pgMuted)
		}
		pn.heading("REVIEW")
		status := "learned: all three reviews done"
		if d := w.due(); d != "" {
			status = fmt.Sprintf("review %d of 3 due %s", w.stage()+1, d)
		} else if w.Meaning == "" {
			status = "comes up for review once it has a meaning"
		}
		pn.line(c.pf.body, pgText, status)
		if st.edit {
			pn.row([]string{"w:editdone"}, []string{"save meaning"}, "", false)
		} else {
			del := "delete"
			if st.confirm {
				del = "tap again"
			}
			pn.row([]string{"w:edit", "w:delete"}, []string{"write meaning", del}, map[bool]string{true: "w:delete"}[st.confirm], true)
		}
		return pn.p

	case "review":
		pn.btn("w:back", "< words", image.Rect(pn.mx-12, 20, pn.mx+260, 110), pgBtn, pgText)
		var queue []*wordEntry
		for _, w := range wb.dueToday(today()) {
			if !st.skipped[w.ID] {
				queue = append(queue, w)
			}
		}
		pn.text(c.pf.title, pgText, pn.mx, 220, "review")
		if len(queue) == 0 {
			pn.y = 300
			pn.line(c.pf.body, pgText, "all done for today")
			if next := wb.nextUp(); next != nil {
				pn.line(c.pf.small, pgMuted, fmt.Sprintf("next: \"%s\" on %s", next.Word, next.due()))
			}
			return pn.p
		}
		w := queue[0]
		pn.text(c.pf.small, pgMuted, pn.mx, 280, fmt.Sprintf("%d to go  ·  review %d of 3", len(queue), w.stage()+1))
		card := image.Rect(pn.mx, 320, c.s.W-pn.mx, h-260)
		ui.RoundRect(pn.p.img, card, 28, pgCard)
		y := card.Min.Y + 100
		for _, l := range layoutWords(f.bold, strings.Fields(w.Word), 0, card.Dx()-80, false) {
			drawWords(pn.p.img, f.bold, l, card.Min.X+40, y, pgText)
			y += 64
		}
		if w.Sentence != "" {
			y = drawParagraphs(pn.p.img, f.body, "“"+w.Sentence+"”", card.Min.X+40, y+10, card.Dx()-80, f.lh, y+10+5*f.lh, pgMuted)
		}
		if st.shown {
			ui.Fill(pn.p.img, image.Rect(card.Min.X+40, y+20, card.Max.X-40, y+22), pgBtn)
			drawParagraphs(pn.p.img, f.body, w.Meaning, card.Min.X+40, y+50, card.Dx()-80, f.lh, card.Max.Y-30, pgText)
			pn.y = h - 230
			pn.row([]string{"w:notyet", "w:gotit"}, []string{"not yet", "got it"}, "w:gotit", false)
		} else {
			pn.y = h - 230
			pn.row([]string{"w:show"}, []string{"show meaning"}, "w:show", false)
		}
		pn.p.buttons = append(pn.p.buttons, button{"w:show", card})
		return pn.p
	}

	// The list.
	pn.btn("home", "< home", image.Rect(pn.mx-12, 20, pn.mx+240, 110), pgBtn, pgText)
	pn.text(c.pf.title, pgText, pn.mx+280, 88, "words")
	due := len(wb.dueToday(today()))
	pn.text(c.pf.small, pgMuted, pn.mx, 180, fmt.Sprintf("%d words kept  ·  %d to review today", len(wb.Words), due))
	pn.y = 200
	if due > 0 {
		pn.row([]string{"w:review"}, []string{fmt.Sprintf("review now (%d)", due)}, "w:review", false)
	}
	if len(wb.Words) == 0 {
		pn.y += 40
		pn.line(c.pf.body, pgText, "no words yet")
		pn.line(c.pf.small, pgMuted, "in a book, long-press a word, then tap \"keep\"")
		return pn.p
	}
	list := make([]*wordEntry, len(wb.Words))
	for i := range wb.Words {
		list[len(list)-1-i] = &wb.Words[i] // newest first
	}
	st.from = min(st.from, (len(list)-1)/wordsPerPage*wordsPerPage)
	pn.y += 30
	for i := st.from; i < len(list) && i < st.from+wordsPerPage; i++ {
		w := list[i]
		r := image.Rect(pn.mx, pn.y, c.s.W-pn.mx, pn.y+170)
		ui.RoundRect(pn.p.img, r, 22, pgCard)
		if l := layoutWords(c.uiFonts().bold, strings.Fields(w.Word), 0, r.Dx()-64, false); len(l) > 0 {
			drawWords(pn.p.img, c.uiFonts().bold, l[0], r.Min.X+32, r.Min.Y+60, pgText)
		}
		meaning := w.Meaning
		if meaning == "" {
			meaning = "no meaning yet"
		}
		if l := layoutWords(f.body, strings.Fields(meaning), 0, r.Dx()-64, false); len(l) > 0 {
			drawWords(pn.p.img, f.body, l[0], r.Min.X+32, r.Min.Y+108, pgMuted)
		}
		state := "learned"
		if d := w.due(); d != "" {
			state = "next review " + d
		}
		pn.text(c.pf.small, pgMuted, r.Min.X+32, r.Min.Y+150, clip(c.pf.small, state+"  ·  "+w.Book, r.Dx()-64))
		pn.p.buttons = append(pn.p.buttons, button{"w:open:" + w.ID, r})
		pn.y += 186
	}
	if len(list) > wordsPerPage {
		pn.y = h - 124
		pn.row([]string{"w:prev", "w:count", "w:next"}, []string{"< prev",
			fmt.Sprintf("%d-%d of %d", st.from+1, min(st.from+wordsPerPage, len(list)), len(list)), "next >"}, "", false)
	}
	return pn.p
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
	st, wb := &c.wui, c.words
	if id == "words" {
		*st = wordsUI{}
		c.setMode(modeWords)
		return true
	}
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
