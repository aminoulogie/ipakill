package main

import (
	"fmt"
	"image/color"
	"sort"
	"strings"
	"time"
)

// Highlights, as Soma's src/lib/marks.ts: five pastels, no colour picker; marking words that
// touch an existing highlight swallows it, so the same words end up in ONE ink, the last
// one picked. Spans are chapter word indices [Start, End], inclusive.

type bookMark struct {
	ID      string `json:"id"`
	Chapter int    `json:"chapter"`
	Start   int    `json:"start"`
	End     int    `json:"end"`
	Colour  string `json:"colour"`
	Text    string `json:"text"`
	Added   string `json:"added"`
}

type inkColour struct {
	id, label     string
	chip          color.RGBA
	light, dark   color.RGBA // the ink over a pale page and over a dark one
	lightA, darkA float64
}

// MARK_COLOURS: each with a thinner, brighter ink for the night page.
var markColours = []inkColour{
	{"butter", "Butter", rgb(0xffd98a), color.RGBA{255, 209, 112, 255}, color.RGBA{255, 203, 90, 255}, 0.58, 0.30},
	{"mint", "Mint", rgb(0xa6e3c4), color.RGBA{140, 224, 189, 255}, color.RGBA{104, 226, 176, 255}, 0.55, 0.26},
	{"sky", "Sky", rgb(0xa8d2ff), color.RGBA{146, 198, 255, 255}, color.RGBA{116, 178, 255, 255}, 0.58, 0.30},
	{"blossom", "Blossom", rgb(0xffb6cd), color.RGBA{255, 170, 200, 255}, color.RGBA{255, 140, 184, 255}, 0.55, 0.26},
	{"lilac", "Lilac", rgb(0xcdbaff), color.RGBA{198, 176, 255, 255}, color.RGBA{176, 146, 255, 255}, 0.55, 0.30},
}

func markColour(id string) inkColour {
	for _, m := range markColours {
		if m.id == id {
			return m
		}
	}
	return markColours[0]
}

// addMark adds a highlight, absorbing any it touches.
func addMark(list []bookMark, m bookMark) []bookMark {
	var kept []bookMark
	for _, o := range list {
		if o.Chapter == m.Chapter && o.Start <= m.End && m.Start <= o.End {
			m.Start, m.End = min(m.Start, o.Start), max(m.End, o.End)
			continue
		}
		kept = append(kept, o)
	}
	kept = append(kept, m)
	sort.Slice(kept, func(i, j int) bool {
		if kept[i].Chapter != kept[j].Chapter {
			return kept[i].Chapter < kept[j].Chapter
		}
		return kept[i].Start < kept[j].Start
	})
	return kept
}

func removeMark(list []bookMark, id string) []bookMark {
	var out []bookMark
	for _, m := range list {
		if m.ID != id {
			out = append(out, m)
		}
	}
	return out
}

// markAt is the highlight covering a word, if any.
func markAt(list []bookMark, chapter, word int) *bookMark {
	for i := range list {
		if m := &list[i]; m.Chapter == chapter && word >= m.Start && word <= m.End {
			return m
		}
	}
	return nil
}

// highlight marks the selection in a colour. Caller holds drawMu.
func (c *console) highlight(colour string) {
	ob, s := c.book, c.rd.sel
	if ob == nil || s == nil {
		return
	}
	m := bookMark{ID: fmt.Sprintf("m%d", time.Now().UnixNano()), Chapter: ob.chapter, Start: s.from, End: s.to,
		Colour: colour, Added: time.Now().Format("2006-01-02")}
	list := addMark(c.lib.Marks[ob.path], m)
	for i := range list { // the joined span's text, from the chapter's words
		if list[i].ID == m.ID {
			list[i].Text = strings.Join(ob.words[list[i].Start:list[i].End+1], " ")
		}
	}
	c.lib.Marks[ob.path] = list
	c.lib.save()
	c.marksVersion++
}

func (c *console) unmark(id string) {
	ob := c.book
	c.lib.Marks[ob.path] = removeMark(c.lib.Marks[ob.path], id)
	c.lib.save()
	c.marksVersion++
}
