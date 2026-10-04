package main

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// countingDictionary is a Wiktionary that knows "asile" and "road", says 404 to everything
// else, and counts the questions.
func countingDictionary(t *testing.T) *atomic.Int32 {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		switch {
		case strings.HasSuffix(r.URL.Path, "/asile"):
			io.WriteString(w, `{"fr": [{"partOfSpeech": "Noun", "definitions": [{"definition": "asylum, refuge"}]}]}`)
		case strings.HasSuffix(r.URL.Path, "/road"):
			io.WriteString(w, `{"en": [{"partOfSpeech": "Noun", "definitions": [{"definition": "A way."}]}]}`)
		case strings.HasPrefix(r.URL.Path, "/datamuse"):
			io.WriteString(w, `[]`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	oldW, oldD, oldC := wiktionaryURL, datamuseURL, webClient
	wiktionaryURL, datamuseURL = srv.URL+"/wiktionary", srv.URL+"/datamuse"
	webClient = func() *http.Client { return srv.Client() }
	t.Cleanup(func() { wiktionaryURL, datamuseURL, webClient = oldW, oldD, oldC })
	return &n
}

func TestOfflineDictionary(t *testing.T) {
	c := testConsole(t) // a fresh, empty dictionary
	_ = c
	n := countingDictionary(t)
	ctx := t.Context()

	r, err := lookupSaved(ctx, "l'asile", "fr")
	if err != nil || r.senses[0].definition != "asylum, refuge" {
		t.Fatalf("first lookup: %+v %v", r, err)
	}
	asked := n.Load()
	if r, err = lookupSaved(ctx, "L'asile", "fr"); err != nil || r.senses[0].definition != "asylum, refuge" || n.Load() != asked {
		t.Fatalf("second lookup should come from the tablet: %+v %v (asked %d more)", r, err, n.Load()-asked)
	}
	if _, err = lookupSaved(ctx, "zzyzx", "fr"); err == nil {
		t.Fatal("an unknown word should say so")
	}
	asked = n.Load()
	var nd *noDefinitionError
	if _, err = lookupSaved(ctx, "zzyzx", "fr"); !errors.As(err, &nd) || n.Load() != asked {
		t.Fatalf("an unknown word should be remembered as unknown: %v (asked %d more)", err, n.Load()-asked)
	}

	// Offline: saved words still answer, others say why.
	wiktionaryURL, datamuseURL = "http://127.0.0.1:1/w", "http://127.0.0.1:1/d"
	if r, err = lookupSaved(ctx, "asile", "fr"); err != nil || r.senses[0].definition != "asylum, refuge" {
		t.Fatalf("offline, saved: %+v %v", r, err)
	}
	if _, err = lookupSaved(ctx, "maison", "fr"); !errors.Is(err, errOffline) {
		t.Fatalf("offline, not saved: %v", err)
	}

	// After a restart (a fresh store reading the file), the words are still there.
	dict = newDictStore()
	if e, ok := dict.get("fr", "asile"); !ok || e.None || dict.count("fr") < 2 {
		t.Fatalf("after a restart: %+v %v, %d words", e, ok, dict.count("fr"))
	}
}

// Opening a book saves its words in the background, so the whole book works offline.
func TestBookWordsSavedForOffline(t *testing.T) {
	c := readerConsole(t)
	countingDictionary(t)
	oldBg, oldPause := dictBackground, dictPause
	dictBackground, dictPause = true, 0
	t.Cleanup(func() { dictBackground, dictPause = oldBg, oldPause })
	openFirstBook(t, c)
	drawMu.Unlock()
	waitFor(t, "the book's words", func() bool { return c.df.total > 0 && c.df.done == c.df.total })
	drawMu.Lock()
	if got := dict.count("en"); got < c.df.total {
		t.Fatalf("%d words saved, the book has %d", got, c.df.total)
	}
	if e, ok := dict.get("en", "road"); !ok || e.None {
		t.Fatal(`"road" should be saved with its meaning`)
	}
	if e, ok := dict.get("en", "rivers"); !ok || !e.None {
		t.Fatal(`"rivers" should be saved as having no entry`)
	}
	if !strings.Contains(c.dictStatus(), "work offline") {
		t.Errorf("status: %q", c.dictStatus())
	}
	tapChrome(t, c, "settings")
	tapButton(t, c, "r:custom")
	shot(t, c, "reader-customize-offline")
	tapButton(t, c, "r:set:offdict")
	if !c.lib.Prefs.NoOfflineDict {
		t.Fatal("the switch should turn it off")
	}
}
