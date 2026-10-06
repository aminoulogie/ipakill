package main

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Soma's sync, on the tablet (Soma: aminoulogie/kite-bay-otter-topaz, src/lib/sync). The
// PC and the iPhone keep Soma's data in step through a relay that can't read it: every
// record is sealed with AES-256-GCM under a key made from one secret (the 56-character
// recovery code), and named by an HMAC of its name, so the server sees opaque ids. The
// tablet joins the same vault with the same code: it pulls every record, keeps them here,
// and pushes the few it changes (a habit ticked, a to-do added or done, reading minutes).
// The newest edit to a record wins; ties go to the higher device id (crypto.ts, newerThan).

var somaDir = condorHome + "/soma"

// somaRec is one record: Soma's JSON text (nil once deleted), when it changed, and where.
type somaRec struct {
	V     *string `json:"v"`
	T     int64   `json:"t"`
	D     string  `json:"d"`
	Dirty bool    `json:"dirty,omitempty"` // changed here, not sent yet
}

type somaLink struct {
	URL  string `json:"url"`
	Code string `json:"code"`
}

type somaStore struct {
	mu      sync.Mutex
	loaded  bool
	link    *somaLink
	keys    *somaKeys
	Device  string             `json:"device"`
	Seq     int64              `json:"seq"`
	Recs    map[string]somaRec `json:"recs"`
	LastOK  time.Time          `json:"last_ok"`
	lastErr string
	running bool
	changed func() // called (without locks) after a pull brought something new
}

var soma = &somaStore{Recs: map[string]somaRec{}}

// --- keys (crypto.ts) -----------------------------------------------------------------------

const somaB32 = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

func somaChecksum(s string) string {
	a, b := 7, 3
	for i := 0; i < len(s); i++ {
		v := strings.IndexByte(somaB32, s[i])
		a = (a + v*(i+1)) % 1021
		b = (b*31 + v) % 1021
	}
	return string([]byte{somaB32[a%32], somaB32[(a>>5)%32], somaB32[b%32], somaB32[(b>>5)%32]})
}

// somaSecret is the secret in a recovery code, or nil if it was mistyped.
func somaSecret(code string) []byte {
	clean := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			r -= 'a' - 'A'
		case (r < '0' || r > '9') && (r < 'A' || r > 'Z'):
			return -1
		}
		switch r {
		case 'O':
			return '0'
		case 'I', 'L':
			return '1'
		}
		return r
	}, code)
	if len(clean) != 56 || somaChecksum(clean[:52]) != clean[52:] {
		return nil
	}
	out := make([]byte, 0, 32)
	acc, bits := 0, 0
	for i := 0; i < 52; i++ {
		v := strings.IndexByte(somaB32, clean[i])
		if v < 0 {
			return nil
		}
		acc = acc<<5 | v
		bits += 5
		if bits >= 8 {
			if len(out) < 32 {
				out = append(out, byte(acc>>(bits-8)))
			}
			bits -= 8
		}
		acc &= 1<<bits - 1
	}
	if len(out) != 32 {
		return nil
	}
	return out
}

// hkdf is RFC 5869 HKDF-SHA256, as WebCrypto's HKDF with salt "soma-sync-v1".
func hkdf(secret []byte, info string, n int) []byte {
	m := hmac.New(sha256.New, []byte("soma-sync-v1"))
	m.Write(secret)
	prk := m.Sum(nil)
	var out, t []byte
	for i := byte(1); len(out) < n; i++ {
		m = hmac.New(sha256.New, prk)
		m.Write(t)
		m.Write([]byte(info))
		m.Write([]byte{i})
		t = m.Sum(nil)
		out = append(out, t...)
	}
	return out[:n]
}

var b64 = base64.RawURLEncoding

type somaKeys struct {
	aead         cipher.AEAD
	mac          []byte
	vault, token string
}

func somaKeysFor(secret []byte) (*somaKeys, error) {
	block, err := aes.NewCipher(hkdf(secret, "records", 32))
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &somaKeys{aead: aead, mac: hkdf(secret, "ids", 32),
		vault: b64.EncodeToString(hkdf(secret, "vault", 16)), token: b64.EncodeToString(hkdf(secret, "auth", 32))}, nil
}

// recordID is the opaque id the server knows a record by: the same on every device.
func (k *somaKeys) recordID(name string) string {
	m := hmac.New(sha256.New, k.mac)
	m.Write([]byte(name))
	return b64.EncodeToString(m.Sum(nil)[:16])
}

type somaEnvelope struct {
	Key    string  `json:"key"`
	Value  *string `json:"value"`
	T      int64   `json:"t"`
	Device string  `json:"device"`
}

func (k *somaKeys) seal(e somaEnvelope) (string, error) {
	pt, err := json.Marshal(e)
	if err != nil {
		return "", err
	}
	iv := make([]byte, 12)
	rand.Read(iv)
	return b64.EncodeToString(k.aead.Seal(iv, iv, pt, nil)), nil
}

func (k *somaKeys) open(blob string) (*somaEnvelope, error) {
	raw, err := b64.DecodeString(strings.TrimRight(blob, "="))
	if err != nil || len(raw) < 12+16 {
		return nil, errors.New("bad blob")
	}
	pt, err := k.aead.Open(nil, raw[:12], raw[12:], nil)
	if err != nil {
		return nil, err
	}
	var e somaEnvelope
	if err := json.Unmarshal(pt, &e); err != nil {
		return nil, err
	}
	return &e, nil
}

// --- the store on the tablet ----------------------------------------------------------------

func somaNewDevice() string {
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	b := make([]byte, 8)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(36))
		b[i] = digits[n.Int64()]
	}
	return string(b)
}

// load reads the records kept on the tablet (once), and the link until there is one: a
// link.txt put there from the PC is taken whenever it appears.
func (s *somaStore) load() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.loaded {
		s.loaded = true
		if b, err := os.ReadFile(filepath.Join(somaDir, "state.json")); err == nil {
			json.Unmarshal(b, s)
		}
		if s.Recs == nil {
			s.Recs = map[string]somaRec{}
		}
		var l somaLink
		if b, err := os.ReadFile(filepath.Join(somaDir, "link.json")); err == nil && json.Unmarshal(b, &l) == nil {
			s.setLinkLocked(l)
		}
	}
	if s.keys != nil {
		return
	}
	if b, err := os.ReadFile(filepath.Join(somaDir, "link.txt")); err == nil {
		// Two lines put there from the PC: the relay's address and the recovery code.
		f := strings.Fields(string(b))
		if len(f) >= 2 && s.setLinkLocked(somaLink{URL: f[0], Code: strings.Join(f[1:], "")}) == nil {
			s.saveLinkLocked()
			s.saveLocked()
		} else {
			log.Printf("soma: link.txt: needs the sync address, then the recovery code")
		}
	}
	if s.link == nil {
		s.link = &somaLink{}
	}
}

func (s *somaStore) setLinkLocked(l somaLink) error {
	l.URL = strings.TrimRight(strings.TrimSpace(l.URL), "/")
	if l.URL != "" && !strings.Contains(l.URL, "://") {
		l.URL = "https://" + l.URL
	}
	secret := somaSecret(l.Code)
	if secret == nil {
		return errors.New("that recovery code doesn't check out: look for a typo")
	}
	if l.URL == "" {
		return errors.New("the sync address is missing")
	}
	k, err := somaKeysFor(secret)
	if err != nil {
		return err
	}
	if s.keys != nil && s.keys.vault != k.vault { // another vault: start over
		s.Recs, s.Seq = map[string]somaRec{}, 0
	}
	s.link, s.keys = &l, k
	if s.Device == "" {
		s.Device = somaNewDevice()
	}
	return nil
}

// setLink links the tablet to a vault (Soma > Settings > Sync on the PC shows both).
func (s *somaStore) setLink(url, code string) error {
	s.load()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.setLinkLocked(somaLink{URL: url, Code: code}); err != nil {
		return err
	}
	s.saveLinkLocked()
	s.saveLocked()
	return nil
}

func (s *somaStore) saveLinkLocked() {
	os.MkdirAll(somaDir, 0o700)
	b, _ := json.Marshal(s.link)
	os.WriteFile(filepath.Join(somaDir, "link.json"), b, 0o600) // the code opens everything: this device only
	os.Remove(filepath.Join(somaDir, "link.txt"))
}

func (s *somaStore) saveLocked() {
	os.MkdirAll(somaDir, 0o700)
	b, err := json.Marshal(s)
	if err != nil {
		return
	}
	f := filepath.Join(somaDir, "state.json")
	if os.WriteFile(f+".new", b, 0o600) == nil {
		os.Rename(f+".new", f)
	}
}

func (s *somaStore) linked() bool {
	s.load()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.keys != nil
}

// get is a record's JSON text ("" if there's none).
func (s *somaStore) get(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.Recs[key]; ok && r.V != nil {
		return *r.V
	}
	return ""
}

// keys lists the records whose names start with prefix.
func (s *somaStore) keysWith(prefix string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for k, r := range s.Recs {
		if strings.HasPrefix(k, prefix) && r.V != nil {
			out = append(out, k)
		}
	}
	return out
}

// put changes a record here (value "" deletes it); it goes out with the next sync.
func (s *somaStore) put(key, value string) {
	s.mu.Lock()
	r := s.Recs[key]
	t := time.Now().UnixMilli()
	if t <= r.T {
		t = r.T + 1 // always after what this record has carried, even with a slow clock
	}
	var v *string
	if value != "" {
		v = &value
	}
	s.Recs[key] = somaRec{V: v, T: t, D: s.Device, Dirty: true}
	s.saveLocked()
	s.mu.Unlock()
	go s.sync()
}

// --- talking to the relay (engine.ts) -------------------------------------------------------

func (s *somaStore) call(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	s.mu.Lock()
	url, k := s.link.URL, s.keys
	s.mu.Unlock()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Vault", k.vault)
	req.Header.Set("Authorization", "Bearer "+k.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := webClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("sync server: %s %s", resp.Status, strings.TrimSpace(string(b)))
	}
	return b, nil
}

// sync pulls what's new from the other devices, then pushes what changed here. One at a time.
func (s *somaStore) sync() {
	if !s.linked() {
		return
	}
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	got, err := s.pull(ctx)
	if err == nil {
		err = s.push(ctx)
	}
	s.mu.Lock()
	s.running = false
	if err != nil {
		s.lastErr = err.Error()
		log.Printf("soma: sync: %v", err)
	} else {
		s.lastErr, s.LastOK = "", time.Now()
	}
	s.saveLocked()
	changed := s.changed
	s.mu.Unlock()
	if got > 0 && changed != nil {
		changed()
	}
}

// newer: envelope e beats what the tablet has for its record.
func newer(e *somaEnvelope, r somaRec, have bool) bool {
	return !have || e.T > r.T || (e.T == r.T && e.Device > r.D)
}

func (s *somaStore) pull(ctx context.Context) (int, error) {
	n := 0
	for {
		s.mu.Lock()
		after := s.Seq
		s.mu.Unlock()
		b, err := s.call(ctx, "GET", fmt.Sprintf("/v1/pull?after=%d", after), nil)
		if err != nil {
			return n, err
		}
		var res struct {
			Items []struct {
				ID   string `json:"id"`
				Seq  int64  `json:"seq"`
				Blob string `json:"blob"`
			} `json:"items"`
			More bool `json:"more"`
		}
		if err := json.Unmarshal(b, &res); err != nil {
			return n, err
		}
		s.mu.Lock()
		for _, it := range res.Items {
			s.Seq = max(s.Seq, it.Seq)
			e, err := s.keys.open(it.Blob)
			if err != nil || e.Device == s.Device {
				continue // not ours to read, or our own echo
			}
			r, have := s.Recs[e.Key]
			if r.Dirty || !newer(e, r, have) {
				continue // a change made here goes out next, newer, and wins
			}
			s.Recs[e.Key] = somaRec{V: e.Value, T: e.T, D: e.Device}
			n++
		}
		s.mu.Unlock()
		if !res.More || len(res.Items) == 0 {
			return n, nil
		}
	}
}

func (s *somaStore) push(ctx context.Context) error {
	type item struct {
		ID   string `json:"id"`
		Blob string `json:"blob"`
	}
	s.mu.Lock()
	var items []item
	var keys []string
	for k, r := range s.Recs {
		if !r.Dirty {
			continue
		}
		blob, err := s.keys.seal(somaEnvelope{Key: k, Value: r.V, T: r.T, Device: s.Device})
		if err != nil {
			s.mu.Unlock()
			return err
		}
		items = append(items, item{s.keys.recordID(k), blob})
		keys = append(keys, k)
	}
	s.mu.Unlock()
	for i := 0; i < len(items); i += 400 {
		j := min(i+400, len(items))
		body, _ := json.Marshal(map[string]any{"items": items[i:j]})
		if _, err := s.call(ctx, "POST", "/v1/push", body); err != nil {
			return err
		}
		s.mu.Lock()
		for _, k := range keys[i:j] {
			r := s.Recs[k]
			r.Dirty = false
			s.Recs[k] = r
		}
		s.mu.Unlock()
	}
	return nil
}

// status is one line on how syncing is going.
func (s *somaStore) status() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.keys == nil:
		return "Not linked"
	case s.running:
		return "Syncing..."
	case s.lastErr != "":
		return "Sync failed: " + s.lastErr
	case s.LastOK.IsZero():
		return "Not synced yet"
	}
	return "Synced " + s.LastOK.In(displayZone).Format("15:04")
}

// somaLoop syncs now and then while condor runs (the PC and the phone change things too).
func somaLoop() {
	for {
		soma.sync()
		time.Sleep(2 * time.Minute)
	}
}
