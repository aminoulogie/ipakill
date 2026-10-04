package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Looking words up and translating, as Soma's src/lib/lookup.ts, translate.ts and
// word-capture.ts: Wiktionary's definitions first, WordNet through Datamuse second,
// MyMemory for translations; a selection is tidied before it's filed, and the sentence
// it came from goes with it into the word book.

var (
	wiktionaryURL = "https://en.wiktionary.org/api/rest_v1/page/definition"
	datamuseURL   = "https://api.datamuse.com/words"
	translateURL  = "https://api.mymemory.translated.net/get"
	lookupTimeout = 8 * time.Second // Soma's LOOKUP_TIMEOUT_MS
)

const (
	maxWords          = 6   // beyond this it is a passage, not a word
	maxChars          = 90  //
	maxSentence       = 220 // room for the sentence, without keeping half a chapter
	maxPassage        = 400
	maxTranslateChars = 480
)

type language struct{ code, label string }

// Soma's LANGUAGES, Arabic and French first. Japanese and Chinese are left out: this
// tablet has no font for them.
var languages = []language{
	{"ar", "Arabic"}, {"fr", "French"}, {"en", "English"}, {"es", "Spanish"}, {"de", "German"},
	{"it", "Italian"}, {"pt", "Portuguese"}, {"tr", "Turkish"}, {"ru", "Russian"},
}

func languageLabel(code string) string {
	for _, l := range languages {
		if l.code == code {
			return l.label
		}
	}
	return strings.ToUpper(code)
}

func isLanguage(code string) bool {
	for _, l := range languages {
		if l.code == code {
			return true
		}
	}
	return false
}

// edge is punctuation and quote marks that belong to the page, not to the word.
var edge = regexp.MustCompile(`^[\s"'“”‘’«»\-–—.,;:!?()\[\]{}…*_]+|[\s"'“”‘’«»\-–—.,;:!?()\[\]{}…*_]+$`)

func cleanSelection(raw string) string {
	return strings.TrimSpace(edge.ReplaceAllString(strings.Join(strings.Fields(raw), " "), ""))
}

func hasLetter(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

// isCapturable: a word (or a few) worth a dictionary and the word book.
func isCapturable(text string) bool {
	c := cleanSelection(text)
	n := len([]rune(c))
	return n >= 2 && n <= maxChars && len(strings.Fields(c)) <= maxWords && hasLetter(c)
}

// isSelectable: worth a menu at all (highlight, translate).
func isSelectable(text string) bool {
	c := cleanSelection(text)
	n := len([]rune(c))
	return n >= 2 && n <= maxPassage && hasLetter(c)
}

// sentenceAround is the sentence the selection sits in, out of its paragraph.
func sentenceAround(block, selected string) string {
	text := strings.Join(strings.Fields(block), " ")
	needle := cleanSelection(selected)
	if text == "" || needle == "" {
		return ""
	}
	at := strings.Index(strings.ToLower(text), strings.ToLower(needle))
	if at < 0 {
		return ""
	}
	before := text[:at]
	start := max(strings.LastIndex(before, ". "), strings.LastIndex(before, "! "), strings.LastIndex(before, "? "))
	from := 0
	if start >= 0 {
		from = start + 2
	}
	after := text[at+len(needle):]
	to := len(text)
	for _, p := range []string{".", "!", "?"} {
		if i := strings.Index(after, p); i >= 0 {
			to = min(to, at+len(needle)+i+1)
		}
	}
	s := strings.TrimSpace(text[from:to])
	if s == "" || strings.EqualFold(cleanSelection(s), needle) {
		return ""
	}
	if r := []rune(s); len(r) > maxSentence {
		s = strings.TrimRight(string(r[:maxSentence-1]), " ") + "…"
	}
	return s
}

type sense struct{ pos, definition, example string }

type wordLookup struct {
	word   string
	senses []sense
	source string
}

func lookupJSON(ctx context.Context, u string, v any) error {
	ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()
	return getJSON(ctx, u, v)
}

// parseWiktionary reads rest_v1/page/definition: {"en": [{partOfSpeech, definitions:
// [{definition, examples}]}], "fr": [...]} — keyed by the language of the WORD, with
// English definitions. Up to three senses; definitions are HTML.
func parseWiktionary(raw []byte, lang, word string) *wordLookup {
	var root map[string][]struct {
		PartOfSpeech string `json:"partOfSpeech"`
		Definitions  []struct {
			Definition string   `json:"definition"`
			Examples   []string `json:"examples"`
		} `json:"definitions"`
	}
	if json.Unmarshal(raw, &root) != nil {
		return nil
	}
	var out []sense
	for _, e := range root[lang] {
		for _, d := range e.Definitions {
			def := stripTags(d.Definition)
			if def == "" {
				continue
			}
			ex := ""
			if len(d.Examples) > 0 {
				ex = stripTags(d.Examples[0])
			}
			out = append(out, sense{e.PartOfSpeech, def, ex})
			if len(out) == 3 {
				return &wordLookup{word, out, "Wiktionary"}
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return &wordLookup{word, out, "Wiktionary"}
}

// parseDatamuse reads md=dp results: defs are "pos\tdefinition".
func parseDatamuse(raw []byte, word string) *wordLookup {
	var list []struct {
		Word string   `json:"word"`
		Defs []string `json:"defs"`
		Tags []string `json:"tags"`
	}
	if json.Unmarshal(raw, &list) != nil || len(list) == 0 {
		return nil
	}
	e := list[0]
	var out []sense
	for _, d := range e.Defs[:min(len(e.Defs), 3)] {
		pos, def, ok := strings.Cut(d, "\t")
		if !ok {
			def, pos = pos, ""
			if len(e.Tags) > 0 {
				pos = e.Tags[0]
			}
		}
		if def = strings.TrimSpace(def); def != "" {
			out = append(out, sense{strings.TrimSpace(pos), def, ""})
		}
	}
	if len(out) == 0 {
		return nil
	}
	if e.Word != "" {
		word = e.Word
	}
	return &wordLookup{word, out, "WordNet via Datamuse"}
}

func fetchRaw(ctx context.Context, u string) ([]byte, error) {
	var raw json.RawMessage
	if err := lookupJSON(ctx, u, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// lookupWord finds what a word means. lang is the book's language: Wiktionary has English
// definitions of words in most languages; Datamuse (the fallback) is English only.
func lookupWord(ctx context.Context, word, lang string) (*wordLookup, error) {
	term := cleanSelection(word)
	if term == "" {
		return nil, errors.New("select a word first")
	}
	if lang == "" {
		lang = "en"
	}
	reached := false
	var lastErr error
	for _, t := range lookupForms(term) {
		raw, err := fetchRaw(ctx, wiktionaryURL+"/"+url.PathEscape(t))
		if answered(err) { // a 404 is the dictionary saying "no such page", not a lost network
			reached = true
		} else {
			lastErr = err
		}
		if err == nil {
			if r := parseWiktionary(raw, lang, t); r != nil {
				return r, nil
			}
		}
	}
	if lang == "en" {
		raw, err := fetchRaw(ctx, datamuseURL+"?sp="+url.QueryEscape(term)+"&md=dp&max=1")
		if answered(err) {
			reached = true
		} else {
			lastErr = err
		}
		if err == nil {
			if r := parseDatamuse(raw, term); r != nil {
				return r, nil
			}
		}
	}
	if !reached {
		log.Printf("lookup %q: %v", term, lastErr)
		return nil, errUnreachable
	}
	return nil, &noDefinitionError{term}
}

// errUnreachable: no dictionary answered (no internet).
var errUnreachable = errors.New("could not reach the dictionary: is Wi-Fi on?")

// noDefinitionError: the dictionary answered, and has no entry for the word.
type noDefinitionError struct{ term string }

func (e *noDefinitionError) Error() string {
	return fmt.Sprintf("no definition found for \"%s\"", e.term)
}

// elision: French and Italian articles and pronouns run into the next word ("l'asile",
// "qu'il", "dell'anno"): the dictionary has the word without them.
var elision = regexp.MustCompile(`(?i)^(l|d|j|m|n|s|t|c|qu|jusqu|lorsqu|puisqu|quoiqu|dell|all|nell|sull|un)['’]`)

// lookupForms are the spellings to ask the dictionary for, best first: as selected, in lower
// case, without an elided article, without an English possessive.
func lookupForms(term string) []string {
	forms := []string{term, strings.ToLower(term)}
	if bare := elision.ReplaceAllString(term, ""); bare != term && bare != "" {
		forms = append(forms, bare, strings.ToLower(bare))
	}
	for _, s := range []string{"'s", "’s"} {
		if b, ok := strings.CutSuffix(term, s); ok && b != "" {
			forms = append(forms, b, strings.ToLower(b))
		}
	}
	return uniq(forms...)
}

func uniq(xs ...string) []string {
	var out []string
	for _, x := range xs {
		dup := false
		for _, o := range out {
			dup = dup || o == x
		}
		if !dup {
			out = append(out, x)
		}
	}
	return out
}

type translation struct {
	text    string
	quality float64 // 0..1, or -1 when not given
}

var shouted = regexp.MustCompile(`^[A-Z '"]+$`)

// parseTranslation reads MyMemory's {responseStatus, responseData: {translatedText, match}}.
func parseTranslation(raw []byte) *translation {
	var r struct {
		Status json.RawMessage `json:"responseStatus"`
		Data   struct {
			Text  string          `json:"translatedText"`
			Match json.RawMessage `json:"match"`
		} `json:"responseData"`
	}
	if json.Unmarshal(raw, &r) != nil {
		return nil
	}
	if st, err := strconv.Atoi(strings.Trim(string(r.Status), `"`)); err == nil && st != 200 {
		return nil
	}
	text := strings.TrimSpace(r.Data.Text)
	if text == "" || (shouted.MatchString(text) && len(text) > 12) { // its own complaints, shouted
		return nil
	}
	q, err := strconv.ParseFloat(strings.Trim(string(r.Data.Match), `"`), 64)
	if err != nil || q < 0 || q > 1 {
		q = -1
	}
	return &translation{text, q}
}

func translateText(ctx context.Context, text, from, to string) (*translation, error) {
	term := strings.TrimSpace(text)
	if term == "" {
		return nil, errors.New("nothing to translate")
	}
	if len([]rune(term)) > maxTranslateChars {
		return nil, errors.New("that is too much text to translate at once")
	}
	if from == "" {
		from = "en"
	}
	if from == to {
		return nil, fmt.Errorf("that is already %s", languageLabel(to))
	}
	raw, err := fetchRaw(ctx, translateURL+"?q="+url.QueryEscape(term)+"&langpair="+url.QueryEscape(from+"|"+to))
	if err != nil {
		return nil, errors.New("could not reach the translator: is Wi-Fi on?")
	}
	t := parseTranslation(raw)
	if t == nil {
		return nil, fmt.Errorf("no %s for \"%s\"", languageLabel(to), term)
	}
	return t, nil
}
