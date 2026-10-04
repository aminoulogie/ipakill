package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"condor-init/epub"
)

// The offline dictionary. Every definition Look Up gets from Wiktionary is kept on the
// tablet (one file per language, /data/condor/dict/<lang>.jsonl, a line per word), so a word
// looked up once answers at once, with or without Wi-Fi. And while a book is open, its
// words are fetched in the background, a few a second from where the reader is, so the
// whole book can be read offline. Words with no entry are remembered too, so they aren't
// asked again.

var dictDir = condorHome + "/dict"

// dictPause is the time between background fetches: gentle on Wiktionary and the battery.
var dictPause = 300 * time.Millisecond

type dictEntry struct {
	W    string      `json:"w"`
	S    [][3]string `json:"s,omitempty"` // part of speech, definition, example
	Src  string      `json:"src,omitempty"`
	None bool        `json:"none,omitempty"` // the dictionary has no entry
}

type dictStore struct {
	mu     sync.Mutex
	loaded map[string]bool
	m      map[string]*dictEntry // lang + "\x00" + lowercase word
	n      map[string]int        // entries per language
}

var dict = newDictStore()

func newDictStore() *dictStore {
	return &dictStore{loaded: map[string]bool{}, m: map[string]*dictEntry{}, n: map[string]int{}}
}

// dictBackground: fetch the open book's words in the background (tests turn it off).
var dictBackground = true

func dictKey(lang, w string) string { return lang + "\x00" + strings.ToLower(w) }

// dictFile is the language's file; lang is kept to letters, so it's always a plain name.
func dictFile(lang string) string {
	clean := strings.Map(func(r rune) rune {
		if r < 128 && unicode.IsLetter(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, lang)
	if clean == "" || len(clean) > 8 {
		clean = "other"
	}
	return filepath.Join(dictDir, clean+".jsonl")
}

// load reads a language's saved words once. Caller holds d.mu.
func (d *dictStore) load(lang string) {
	if d.loaded[lang] {
		return
	}
	d.loaded[lang] = true
	f, err := os.Open(dictFile(lang))
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var e dictEntry
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.W != "" {
			if _, ok := d.m[dictKey(lang, e.W)]; !ok {
				d.n[lang]++
			}
			d.m[dictKey(lang, e.W)] = &e
		}
	}
}

func (d *dictStore) get(lang, w string) (*dictEntry, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.load(lang)
	e, ok := d.m[dictKey(lang, w)]
	return e, ok
}

// put keeps an entry, in memory and on flash (appended, so a crash loses one line at most).
func (d *dictStore) put(lang string, e dictEntry) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.load(lang)
	k := dictKey(lang, e.W)
	if _, ok := d.m[k]; !ok {
		d.n[lang]++
	}
	d.m[k] = &e
	os.MkdirAll(dictDir, 0o755)
	f, err := os.OpenFile(dictFile(lang), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("dictionary: %v", err)
		return
	}
	b, _ := json.Marshal(e)
	f.Write(append(b, '\n'))
	f.Close()
}

// count is how many words of a language are saved.
func (d *dictStore) count(lang string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.load(lang)
	return d.n[lang]
}

func (e *dictEntry) lookup() *wordLookup {
	r := &wordLookup{word: e.W, source: e.Src}
	for _, s := range e.S {
		r.senses = append(r.senses, sense{s[0], s[1], s[2]})
	}
	return r
}

func entryOf(word string, r *wordLookup) dictEntry {
	e := dictEntry{W: word, Src: r.source}
	for _, s := range r.senses {
		e.S = append(e.S, [3]string{s.pos, s.definition, s.example})
	}
	return e
}

// errOffline: no internet, and the word isn't saved.
var errOffline = errors.New("this word isn't saved for offline reading yet, and there's no internet")

// lookupSaved looks a word up in the saved dictionary first, then online (keeping what it
// finds, and the fact that there's nothing to find).
func lookupSaved(ctx context.Context, word, lang string) (*wordLookup, error) {
	term := cleanSelection(word)
	if term == "" {
		return nil, errors.New("select a word first")
	}
	if lang == "" {
		lang = "en"
	}
	known := true // every spelling is saved, all as "no entry"
	for _, f := range lookupForms(term) {
		e, ok := dict.get(lang, f)
		switch {
		case ok && !e.None:
			return e.lookup(), nil
		case !ok:
			known = false
		}
	}
	if known {
		return nil, &noDefinitionError{term}
	}
	r, err := lookupWord(ctx, term, lang)
	var nd *noDefinitionError
	switch {
	case err == nil:
		dict.put(lang, entryOf(term, r))
		if !strings.EqualFold(r.word, term) {
			dict.put(lang, entryOf(r.word, r))
		}
	case errors.As(err, &nd):
		dict.put(lang, dictEntry{W: term, None: true})
	case errors.Is(err, errUnreachable):
		return nil, errOffline
	}
	return r, err
}

// --- fetching a book's words in the background ---------------------------------------------

// dictFetch is the background fetch for the open book. Fields are read and written under
// drawMu.
type dictFetch struct {
	gen         int
	path        string
	total, done int
	waiting     bool // no internet: retrying now and then
}

// bookWords is a book's distinct words worth a dictionary, from chapter start onwards and
// then the chapters before, lowercase, without elided articles.
func bookWords(b *epub.Book, start int) []string {
	seen := map[string]bool{}
	var words []string
	n := len(b.Chapters)
	for k := 0; k < n; k++ {
		ch := (start + k) % n
		x, err := b.ReadFile(b.Chapters[ch].Path)
		if err != nil {
			continue
		}
		for _, blk := range epub.ChapterText(x) {
			for _, f := range strings.Fields(blk.Text) {
				w := strings.ToLower(cleanSelection(f))
				w = elision.ReplaceAllString(w, "")
				w = strings.Trim(w, `"'“”‘’«»-–—.,;:!?()[]{}…*_`)
				if len([]rune(w)) < 3 || seen[w] || strings.ContainsAny(w, "0123456789/@") || !hasLetter(w) {
					continue
				}
				seen[w] = true
				words = append(words, w)
			}
		}
	}
	return words
}

// startDictFetch begins saving the open book's words for offline reading, unless it's
// switched off. Caller holds drawMu.
func (c *console) startDictFetch() {
	ob := c.book
	if ob == nil || c.lib.Prefs.NoOfflineDict || !dictBackground {
		return
	}
	c.df.gen++
	c.df.path, c.df.total, c.df.done, c.df.waiting = ob.path, 0, 0, false
	go c.fetchBookWords(c.df.gen, ob.path, c.bookLang(), ob.chapter)
}

func (c *console) fetchBookWords(gen int, path, lang string, start int) {
	b, err := epub.Open(path) // its own copy: the reader keeps using c.book's
	if err != nil {
		return
	}
	words := bookWords(b, start)
	b.Close()
	stale := func() bool { drawMu.Lock(); defer drawMu.Unlock(); return c.df.gen != gen }
	drawMu.Lock()
	c.df.total = len(words)
	drawMu.Unlock()
	done := 0
	for i := 0; i < len(words); i++ {
		if stale() {
			return
		}
		w := words[i]
		if _, ok := dict.get(lang, w); !ok {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			_, err := lookupSaved(ctx, w, lang)
			cancel()
			if errors.Is(err, errOffline) { // no internet: wait and try this word again
				drawMu.Lock()
				c.df.waiting = true
				drawMu.Unlock()
				time.Sleep(time.Minute)
				i--
				continue
			}
			time.Sleep(dictPause)
		}
		done++
		drawMu.Lock()
		if c.df.gen == gen {
			c.df.done, c.df.waiting = done, false
		}
		drawMu.Unlock()
	}
	log.Printf("dictionary: %s: all %d words saved", filepath.Base(path), len(words))
}

// dictStatus is the offline dictionary's line in Customize.
func (c *console) dictStatus() string {
	switch {
	case c.lib.Prefs.NoOfflineDict:
		return "Off: Look Up needs the internet, except for words already saved."
	case c.df.total == 0:
		return fmt.Sprintf("%d words saved on the tablet.", dict.count(c.bookLang()))
	case c.df.done >= c.df.total:
		return fmt.Sprintf("This book's %d words work offline.", c.df.total)
	case c.df.waiting:
		return fmt.Sprintf("%d of this book's %d words saved; waiting for the internet.", c.df.done, c.df.total)
	}
	return fmt.Sprintf("Saving this book's words for offline reading: %d of %d.", c.df.done, c.df.total)
}
