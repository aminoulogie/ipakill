package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Vectors made by Soma's own crypto.ts (src/lib/sync, run under Node) for the secret
// bytes i*37+11: the tablet must find the same vault, the same record ids, and open what
// the PC and the phone seal (and they must open what it seals: TestSomaSealOpensInSoma).
const (
	somaVecCode  = "1CR5-AYMZ-RKMG-WCTR-FPHC-FV0H-6SDR-19EA-XWA3-JQM3-N36Z-45SW-C630-48W1"
	somaVecVault = "ZB0rBW1gaSvmHMNrYGHzRw"
	somaVecToken = "_mzGSG2f8nlIoiyK82Z5P3v24mV-4TxCvfvhyINyeHg"
	somaVecID    = "IFnsnf8TEQLOhO8O4f5Mhg" // recordId("habits#abc123")
	somaVecBlob  = "lTaP0L730IQJNZoyFXPN0sjjrf6vB3nDuYtMQqdXnpwLjccVdp1sTXrNGtBlKJVnlLDIEnA6BT0bI2YSES5Rfo-i9g6t8WhioS7ETTD1nFD3i7q3MD9BPdizdub3mfqCpFAztbINfc12GUcSq78C8LFRgCEjS6UdMa76IFWzIDCzV-DnK--0xRuw6UYhOvKjPE40Fw"
)

func TestSomaKeysMatchSoma(t *testing.T) {
	secret := somaSecret(somaVecCode)
	if secret == nil {
		t.Fatal("Soma's recovery code was rejected")
	}
	for i, b := range secret {
		if b != byte(i*37+11) {
			t.Fatalf("secret byte %d is %d", i, b)
		}
	}
	if somaSecret("1CR5-AYMZ-RKMG-WCTR-FPHC-FV0H-6SDR-19EA-XWA3-JQM3-N36Z-45SW-C630-48W2") != nil {
		t.Error("a mistyped code was accepted")
	}
	if somaSecret("1cr5 aymz rkmg wctr fphc fv0h 6sdr 19ea xwa3 jqm3 n36z 45sw c630 48w1") == nil {
		t.Error("lower case and spaces should be fine")
	}
	k, err := somaKeysFor(secret)
	if err != nil {
		t.Fatal(err)
	}
	if k.vault != somaVecVault || k.token != somaVecToken {
		t.Fatalf("vault %s token %s: not Soma's", k.vault, k.token)
	}
	if id := k.recordID("habits#abc123"); id != somaVecID {
		t.Fatalf("record id %s, Soma's is %s", id, somaVecID)
	}
	e, err := k.open(somaVecBlob)
	if err != nil {
		t.Fatalf("can't open Soma's record: %v", err)
	}
	if e.Key != "todos#x1" || e.Value == nil || *e.Value != `{"id":"x1","text":"café ✓","done":false}` || e.T != 1700000000000 || e.Device != "pc000001" {
		t.Fatalf("opened %+v", e)
	}
	v := "hello"
	blob, _ := k.seal(somaEnvelope{Key: "a", Value: &v, T: 5, Device: "tab"})
	if back, err := k.open(blob); err != nil || *back.Value != "hello" {
		t.Fatalf("round trip: %v", err)
	}
}

// fakeRelay is Soma's sync server (server-core.ts) in memory: one vault, newest blob per id,
// a sequence number per write.
type fakeRelay struct {
	mu    sync.Mutex
	token string
	seq   int64
	recs  map[string]struct {
		seq  int64
		blob string
	}
}

func (f *fakeRelay) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+f.token || r.Header.Get("X-Vault") == "" {
		http.Error(w, `{"error":"unauthorized"}`, 401)
		return
	}
	switch r.URL.Path {
	case "/v1/push":
		var body struct{ Items []struct{ ID, Blob string } }
		json.NewDecoder(r.Body).Decode(&body)
		for _, it := range body.Items {
			f.seq++
			f.recs[it.ID] = struct {
				seq  int64
				blob string
			}{f.seq, it.Blob}
		}
		json.NewEncoder(w).Encode(map[string]any{"seq": f.seq})
	case "/v1/pull":
		after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
		type item struct {
			ID   string `json:"id"`
			Seq  int64  `json:"seq"`
			Blob string `json:"blob"`
		}
		var items []item
		for id, rec := range f.recs {
			if rec.seq > after {
				items = append(items, item{id, rec.seq, rec.blob})
			}
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Seq < items[j].Seq })
		json.NewEncoder(w).Encode(map[string]any{"items": items, "more": false})
	default:
		http.NotFound(w, r)
	}
}

// pcPut is the PC writing a record (as Soma's engine does).
func (f *fakeRelay) pcPut(t *testing.T, k *somaKeys, key string, value any, at int64) {
	b, _ := json.Marshal(value)
	v := string(b)
	blob, err := k.seal(somaEnvelope{Key: key, Value: &v, T: at, Device: "pc000001"})
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.seq++
	f.recs[k.recordID(key)] = struct {
		seq  int64
		blob string
	}{f.seq, blob}
	f.mu.Unlock()
}

// pcGet is the PC reading a record back: its value and which device wrote it.
func (f *fakeRelay) pcGet(t *testing.T, k *somaKeys, key string) (obj, string) {
	f.mu.Lock()
	rec, ok := f.recs[k.recordID(key)]
	f.mu.Unlock()
	if !ok {
		return nil, ""
	}
	e, err := k.open(rec.blob)
	if err != nil || e.Value == nil {
		t.Fatalf("%s: %v", key, err)
	}
	var o obj
	json.Unmarshal([]byte(*e.Value), &o)
	return o, e.Device
}

func somaTestLink(t *testing.T) (*console, *fakeRelay, *somaKeys) {
	c := testConsole(t)
	oldDir, oldSoma, oldClient := somaDir, soma, webClient
	somaDir, soma = t.TempDir(), &somaStore{Recs: map[string]somaRec{}}
	k, _ := somaKeysFor(somaSecret(somaVecCode))
	relay := &fakeRelay{token: k.token, recs: map[string]struct {
		seq  int64
		blob string
	}{}}
	srv := httptest.NewServer(relay)
	webClient = func() *http.Client { return srv.Client() }
	t.Cleanup(func() { srv.Close(); somaDir, soma, webClient = oldDir, oldSoma, oldClient })
	today := somaToday()
	now := time.Now().UnixMilli() - 60000
	relay.pcPut(t, k, "habits#h1", obj{"id": "h1", "name": "Stretch", "color": "#30d158", "desc": "", "goalDaysPerWeek": 7, "history": obj{}}, now)
	relay.pcPut(t, k, "habits#h2", obj{"id": "h2", "name": "Brush", "color": "#0a84ff", "history": obj{},
		"steps": []any{obj{"id": "am", "name": "Morning", "target": 1}, obj{"id": "pm", "name": "Night", "target": 2}}}, now)
	relay.pcPut(t, k, "habits@order", []string{"h2", "h1"}, now)
	relay.pcPut(t, k, "todos#t1", obj{"id": "t1", "text": "Buy milk", "done": false, "date": today, "scope": "day"}, now)
	relay.pcPut(t, k, "todos@order", []string{"t1"}, now)
	relay.pcPut(t, k, "dayPlans/"+today, []any{obj{"id": "b1", "label": "Sleep", "hours": 8, "color": "#5e5ce6", "fixed": true, "start": 23},
		obj{"id": "b2", "label": "Work", "hours": 16, "color": "#ff9f0a", "fixed": false}}, now)
	relay.pcPut(t, k, "history/"+today, obj{"split": "Push", "durationFormatted": "52:10", "totalSets": 18, "totalVol": 7400, "caloriesBurned": 380}, now)
	relay.pcPut(t, k, "nutrition/"+today, obj{"items": []any{obj{"name": "Eggs", "cals": 300, "p": 25, "c": 2, "f": 20}}, "goals": obj{"cals": 2400, "protein": 160}, "water": 1500}, now)
	relay.pcPut(t, k, "reading/"+today, 20, now)
	if err := soma.setLink(strings.TrimPrefix(srv.URL, "https://"), "1cr5 aymz rkmg wctr fphc fv0h 6sdr 19ea xwa3 jqm3 n36z 45sw c630 48w1"); err != nil {
		t.Fatal(err)
	}
	return c, relay, k
}

// The tablet joins the PC's vault, shows its data, and what it changes reaches the PC.
func TestSomaSyncsWithThePC(t *testing.T) {
	c, relay, k := somaTestLink(t)
	soma.sync()
	if s := soma.status(); !strings.HasPrefix(s, "Synced") {
		t.Fatalf("status %q", s)
	}
	hs := somaHabits()
	if len(hs) != 2 || hs[0].Name != "Brush" || hs[1].Name != "Stretch" {
		t.Fatalf("habits %+v (in Soma's order)", hs)
	}
	today := somaToday()
	if plan := somaPlan(today); len(plan) != 2 || clockOf(plan[0].Start) != "23:00" || clockOf(plan[1].Start) != "07:00" {
		t.Fatalf("plan %+v", plan)
	}
	if s := somaTraining(today); s == nil || s.Split != "Push" || s.Sets != 18 {
		t.Fatalf("training %+v", s)
	}
	if n := somaNutrition(today); n.Cals != 300 || n.GoalCals != 2400 {
		t.Fatalf("nutrition %+v", n)
	}

	// The tab draws, all three parts.
	drawMu.Lock()
	c.setMode(modeSoma)
	for _, v := range []string{"so:view:calendar", "so:view:reports", "so:view:today"} {
		c.somaTap(v)
	}
	// Tick both habits, read 15 minutes, tick the to-do, add one.
	c.somaTap("so:habit:h1")
	c.somaTap("so:habit:h2")
	c.somaTap("so:read:15")
	c.somaTap("so:todo:t1")
	c.somaTap("so:type:todo")
	c.somaKey([]byte("Call mum\r"))
	drawMu.Unlock()
	soma.sync()

	h1, dev := relay.pcGet(t, k, "habits#h1")
	if dev != soma.Device || h1["history"].(obj)[today] != true || h1["name"] != "Stretch" || h1["goalDaysPerWeek"] != float64(7) {
		t.Fatalf("the PC sees habit h1 as %v from %q", h1, dev)
	}
	h2, _ := relay.pcGet(t, k, "habits#h2")
	steps := h2["stepLog"].(obj)[today].(obj)
	if h2["history"].(obj)[today] != true || steps["am"] != float64(1) || steps["pm"] != float64(2) {
		t.Fatalf("a habit with steps: %v", h2)
	}
	if t1, _ := relay.pcGet(t, k, "todos#t1"); t1["done"] != true {
		t.Fatalf("to-do %v", t1)
	}
	order := soma.get("todos@order")
	var ids []string
	json.Unmarshal([]byte(order), &ids)
	if len(ids) != 2 || ids[0] != "t1" {
		t.Fatalf("order %v", ids)
	}
	if nt, _ := relay.pcGet(t, k, "todos#"+ids[1]); nt["text"] != "Call mum" || nt["date"] != today || nt["scope"] != "day" {
		t.Fatalf("new to-do %v", nt)
	}
	if somaReading(today) != 35 {
		t.Fatalf("reading %d", somaReading(today))
	}

	// The PC changes the to-do back later: the tablet takes it.
	relay.pcPut(t, k, "todos#t1", obj{"id": "t1", "text": "Buy oat milk", "done": false, "date": today, "scope": "day"}, time.Now().UnixMilli()+1000)
	soma.sync()
	for _, td := range somaTodos() {
		if td.ID == "t1" && (td.Text != "Buy oat milk" || td.Done) {
			t.Fatalf("the PC's newer edit didn't win: %+v", td)
		}
	}

	// A restart: everything is still there, and still linked.
	dev = soma.Device
	soma = &somaStore{Recs: map[string]somaRec{}}
	if !soma.linked() || soma.Device != dev || len(somaHabits()) != 2 {
		t.Fatal("the tablet forgot Soma after a restart")
	}
}

// A link.txt sent from the PC links the tablet, even after condor started.
func TestSomaLinkFromThePC(t *testing.T) {
	oldDir, oldSoma := somaDir, soma
	somaDir, soma = t.TempDir(), &somaStore{Recs: map[string]somaRec{}}
	t.Cleanup(func() { somaDir, soma = oldDir, oldSoma })
	if soma.linked() {
		t.Fatal("linked with nothing")
	}
	os.WriteFile(filepath.Join(somaDir, "link.txt"), []byte("soma-sync.example.workers.dev\r\n"+somaVecCode+"\r\n"), 0o600)
	if !soma.linked() {
		t.Fatal("link.txt wasn't taken")
	}
	if soma.link.URL != "https://soma-sync.example.workers.dev" || soma.keys.vault != somaVecVault {
		t.Fatalf("linked to %q, vault %q", soma.link.URL, soma.keys.vault)
	}
	if _, err := os.Stat(filepath.Join(somaDir, "link.txt")); err == nil {
		t.Error("link.txt should be replaced by link.json (readable by root only)")
	}
}
