package main

import (
	"image"
	"image/color"
	"strings"
	"unicode"

	"golang.org/x/image/font"

	"condor-init/ui"
)

// Text layout shared by the reader and its panels: words placed on lines with their boxes
// known, so a tap can find a word and a highlight can be drawn behind it. Arabic (and
// Persian) is shaped here with the Unicode presentation forms (DejaVu has them all) and laid
// out right to left; there's no HarfBuzz on this tablet, and the forms cover modern text.
// Vowel marks (tashkeel) are dropped from the display: drawn without a shaper they'd land
// on the wrong letter.

// arabicForms: isolated, final, initial, medial; 0 = the letter has no such form.
var arabicForms = map[rune][4]rune{
	0x0621: {0xFE80, 0, 0, 0},
	0x0622: {0xFE81, 0xFE82, 0, 0},
	0x0623: {0xFE83, 0xFE84, 0, 0},
	0x0624: {0xFE85, 0xFE86, 0, 0},
	0x0625: {0xFE87, 0xFE88, 0, 0},
	0x0626: {0xFE89, 0xFE8A, 0xFE8B, 0xFE8C},
	0x0627: {0xFE8D, 0xFE8E, 0, 0},
	0x0628: {0xFE8F, 0xFE90, 0xFE91, 0xFE92},
	0x0629: {0xFE93, 0xFE94, 0, 0},
	0x062A: {0xFE95, 0xFE96, 0xFE97, 0xFE98},
	0x062B: {0xFE99, 0xFE9A, 0xFE9B, 0xFE9C},
	0x062C: {0xFE9D, 0xFE9E, 0xFE9F, 0xFEA0},
	0x062D: {0xFEA1, 0xFEA2, 0xFEA3, 0xFEA4},
	0x062E: {0xFEA5, 0xFEA6, 0xFEA7, 0xFEA8},
	0x062F: {0xFEA9, 0xFEAA, 0, 0},
	0x0630: {0xFEAB, 0xFEAC, 0, 0},
	0x0631: {0xFEAD, 0xFEAE, 0, 0},
	0x0632: {0xFEAF, 0xFEB0, 0, 0},
	0x0633: {0xFEB1, 0xFEB2, 0xFEB3, 0xFEB4},
	0x0634: {0xFEB5, 0xFEB6, 0xFEB7, 0xFEB8},
	0x0635: {0xFEB9, 0xFEBA, 0xFEBB, 0xFEBC},
	0x0636: {0xFEBD, 0xFEBE, 0xFEBF, 0xFEC0},
	0x0637: {0xFEC1, 0xFEC2, 0xFEC3, 0xFEC4},
	0x0638: {0xFEC5, 0xFEC6, 0xFEC7, 0xFEC8},
	0x0639: {0xFEC9, 0xFECA, 0xFECB, 0xFECC},
	0x063A: {0xFECD, 0xFECE, 0xFECF, 0xFED0},
	0x0640: {0x0640, 0x0640, 0x0640, 0x0640}, // tatweel
	0x0641: {0xFED1, 0xFED2, 0xFED3, 0xFED4},
	0x0642: {0xFED5, 0xFED6, 0xFED7, 0xFED8},
	0x0643: {0xFED9, 0xFEDA, 0xFEDB, 0xFEDC},
	0x0644: {0xFEDD, 0xFEDE, 0xFEDF, 0xFEE0},
	0x0645: {0xFEE1, 0xFEE2, 0xFEE3, 0xFEE4},
	0x0646: {0xFEE5, 0xFEE6, 0xFEE7, 0xFEE8},
	0x0647: {0xFEE9, 0xFEEA, 0xFEEB, 0xFEEC},
	0x0648: {0xFEED, 0xFEEE, 0, 0},
	0x0649: {0xFEEF, 0xFEF0, 0, 0},
	0x064A: {0xFEF1, 0xFEF2, 0xFEF3, 0xFEF4},
	0x067E: {0xFB56, 0xFB57, 0xFB58, 0xFB59}, // peh
	0x0686: {0xFB7A, 0xFB7B, 0xFB7C, 0xFB7D}, // tcheh
	0x0698: {0xFB8A, 0xFB8B, 0, 0},           // jeh
	0x06A9: {0xFB8E, 0xFB8F, 0xFB90, 0xFB91}, // keheh
	0x06AF: {0xFB92, 0xFB93, 0xFB94, 0xFB95}, // gaf
	0x06CC: {0xFBFC, 0xFBFD, 0xFBFE, 0xFBFF}, // farsi yeh
}

// lamAlef: the ligature of lam with each alef, isolated and final.
var lamAlef = map[rune][2]rune{
	0x0622: {0xFEF5, 0xFEF6}, 0x0623: {0xFEF7, 0xFEF8}, 0x0625: {0xFEF9, 0xFEFA}, 0x0627: {0xFEFB, 0xFEFC},
}

func isArabic(r rune) bool {
	return (r >= 0x0600 && r <= 0x06FF) || (r >= 0x0750 && r <= 0x077F) || (r >= 0xFB50 && r <= 0xFDFF) || (r >= 0xFE70 && r <= 0xFEFF)
}

// isTashkeel: vowel marks and other combining marks of Arabic.
func isTashkeel(r rune) bool {
	return (r >= 0x064B && r <= 0x065F) || r == 0x0670 || (r >= 0x06D6 && r <= 0x06ED) || r == 0x0610 || (r >= 0x0611 && r <= 0x061A)
}

// joinsBoth: the letter connects to the next one too (not only to the previous).
func joinsBoth(r rune) bool {
	f, ok := arabicForms[r]
	return ok && f[2] != 0
}

func joins(r rune) bool { _, ok := arabicForms[r]; return ok && r != 0x0621 }

var mirror = map[rune]rune{'(': ')', ')': '(', '[': ']', ']': '[', '{': '}', '}': '{', '«': '»', '»': '«', '<': '>', '>': '<'}

// shapeArabic turns one word of logical-order Arabic into the presentation forms in visual
// (left-to-right drawing) order.
func shapeArabic(word string) string {
	var rs []rune
	for _, r := range word {
		if !isTashkeel(r) {
			rs = append(rs, r)
		}
	}
	out := make([]rune, 0, len(rs))
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		forms, ok := arabicForms[r]
		if !ok {
			out = append(out, r)
			continue
		}
		prev := i > 0 && joinsBoth(rs[i-1])
		if r == 0x0644 && i+1 < len(rs) { // lam + alef ligature
			if lig, ok := lamAlef[rs[i+1]]; ok {
				if prev {
					out = append(out, lig[1])
				} else {
					out = append(out, lig[0])
				}
				i++
				continue
			}
		}
		next := i+1 < len(rs) && joinsBoth(r) && joins(rs[i+1])
		form := 0 // isolated
		switch {
		case prev && next:
			form = 3
		case prev:
			form = 1
		case next:
			form = 2
		}
		if forms[form] == 0 {
			if form == 3 && forms[1] != 0 {
				form = 1
			} else {
				form = 0
			}
		}
		out = append(out, forms[form])
	}
	// Visual order: reversed, with brackets mirrored.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	for i, r := range out {
		if m, ok := mirror[r]; ok {
			out[i] = m
		}
	}
	return string(out)
}

// visual is a line of interface text as drawn: Arabic words shaped and, in a right-to-left
// line, the words in right-to-left order. Anything else comes back unchanged.
func visual(s string) string {
	has := false
	for _, r := range s {
		if isArabic(r) {
			has = true
			break
		}
	}
	if !has {
		return s
	}
	ws := strings.Fields(s)
	for i, w := range ws {
		ws[i] = displayWord(w)
	}
	if isRTL(s) {
		for i, j := 0, len(ws)-1; i < j; i, j = i+1, j-1 {
			ws[i], ws[j] = ws[j], ws[i]
		}
	}
	return strings.Join(ws, " ")
}

// displayWord is how a word is drawn: shaped if it has Arabic in it.
func displayWord(w string) string {
	for _, r := range w {
		if isArabic(r) {
			return shapeArabic(w)
		}
	}
	return w
}

// isRTL: the text's first strong letter is Arabic (or Hebrew).
func isRTL(s string) bool {
	for _, r := range s {
		if isArabic(r) || (r >= 0x0590 && r <= 0x05FF) {
			return true
		}
		if unicode.IsLetter(r) {
			return false
		}
	}
	return false
}

// tword is a word placed on a line: x is its left edge from the line's start.
type tword struct {
	text string // as drawn
	x, w int
	idx  int // index of the word in its source (chapter or paragraph)
}

// tline is a line of words.
type tline struct {
	words []tword
	rtl   bool
}

// layoutWords wraps words to width. Lines other than a paragraph's last are justified when
// justify is set; right-to-left paragraphs are placed from the right edge. first is the
// index of words[0] in its source.
func layoutWords(face font.Face, words []string, first, width int, justify bool) []tline {
	if len(words) == 0 {
		return nil
	}
	rtl := isRTL(strings.Join(words[:min(len(words), 8)], " "))
	space := font.MeasureString(face, " ").Ceil()
	type m struct {
		text string
		w    int
	}
	ms := make([]m, len(words))
	for i, w := range words {
		d := displayWord(w)
		ms[i] = m{d, font.MeasureString(face, d).Ceil()}
	}
	var lines []tline
	start := 0
	for start < len(ms) {
		end, used := start+1, ms[start].w
		for end < len(ms) && used+space+ms[end].w <= width {
			used += space + ms[end].w
			end++
		}
		// One word wider than the line: it stands alone (and is clipped by the page).
		gap := float64(space)
		if justify && end < len(ms) && end-start > 1 {
			extra := width - used
			if extra < width/3 { // don't stretch a line to a few words across the page
				gap += float64(extra) / float64(end-start-1)
			}
		}
		l := tline{rtl: rtl}
		x := 0.0
		for i := start; i < end; i++ {
			wx := int(x + 0.5)
			if rtl {
				wx = width - int(x+0.5) - ms[i].w
			}
			l.words = append(l.words, tword{ms[i].text, wx, ms[i].w, first + i})
			x += float64(ms[i].w) + gap
		}
		lines = append(lines, l)
		start = end
	}
	return lines
}

// drawWords draws a line's words with their left edge at x0 and baseline y.
func drawWords(img *image.RGBA, face font.Face, l tline, x0, baseline int, c color.Color) {
	for _, w := range l.words {
		ui.DrawText(img, face, x0+w.x, baseline, c, w.text)
	}
}

// drawParagraphs draws plain text (any script) wrapped to width from the top-left (x, y)
// with line height lh, stopping before maxY; it returns the y below the last line.
func drawParagraphs(img *image.RGBA, face font.Face, text string, x, y, width, lh, maxY int, c color.Color) int {
	asc := face.Metrics().Ascent.Ceil()
	for _, para := range strings.Split(text, "\n") {
		for _, l := range layoutWords(face, strings.Fields(para), 0, width, false) {
			if y+lh > maxY {
				return y
			}
			drawWords(img, face, l, x, y+asc, c)
			y += lh
		}
	}
	return y
}
