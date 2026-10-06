package main

import (
	"bytes"
	"container/list"
	"context"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // some books' covers

	"condor-init/epub"
	"condor-init/ui"
)

// Book covers: fetched once and kept on the tablet for good, twice: the image as it came
// (/data/condor/covers/<hash>) and, once scaled to a size it's drawn at, that too, ready to
// show (/data/condor/covers/s/<hash>-<w>x<h>: raw pixels, no decoding). A cover seen once
// appears at once from then on, everywhere, even after a restart. In memory, the most
// recently drawn covers are kept, up to coverMemBytes (all of Home's, and more).

var coverDir = condorHome + "/covers"

type coverKey struct {
	url  string
	w, h int
}

type coverCache struct {
	mu      sync.Mutex
	imgs    map[coverKey]*list.Element // values: *coverEntry
	lru     *list.List                 // most recently drawn at the front
	bytes   int
	pending map[coverKey]bool
	failed  map[string]bool
	sem     chan struct{}
	loaded  func(url string) // called (without locks) when a cover is ready
}

type coverEntry struct {
	k   coverKey
	img *image.RGBA
}

const coverMemBytes = 48 << 20

var covers = newCoverCache()

func newCoverCache() *coverCache {
	return &coverCache{imgs: map[coverKey]*list.Element{}, lru: list.New(), pending: map[coverKey]bool{},
		failed: map[string]bool{}, sem: make(chan struct{}, 3)}
}

// get returns the cover scaled to fill w x h, or nil while it loads (or if it can't). A cover
// already scaled on the tablet is read at once (a few milliseconds), so pages draw with it.
func (cc *coverCache) get(url string, w, h int) *image.RGBA {
	if url == "" {
		return nil
	}
	k := coverKey{url, w, h}
	cc.mu.Lock()
	if e := cc.imgs[k]; e != nil {
		cc.lru.MoveToFront(e)
		img := e.Value.(*coverEntry).img
		cc.mu.Unlock()
		return img
	}
	if cc.pending[k] || cc.failed[url] {
		cc.mu.Unlock()
		return nil
	}
	cc.mu.Unlock()
	if img := readScaled(k); img != nil {
		cc.mu.Lock()
		cc.keep(k, img)
		cc.mu.Unlock()
		return img
	}
	cc.mu.Lock()
	defer cc.mu.Unlock()
	if cc.pending[k] {
		return nil
	}
	cc.pending[k] = true
	go cc.load(k)
	return nil
}

// keep puts a cover in memory, dropping the least recently drawn beyond the budget. Caller
// holds cc.mu.
func (cc *coverCache) keep(k coverKey, img *image.RGBA) {
	if e := cc.imgs[k]; e != nil {
		cc.lru.MoveToFront(e)
		return
	}
	cc.imgs[k] = cc.lru.PushFront(&coverEntry{k, img})
	cc.bytes += len(img.Pix)
	for cc.bytes > coverMemBytes && cc.lru.Len() > 1 {
		old := cc.lru.Back().Value.(*coverEntry)
		cc.lru.Remove(cc.lru.Back())
		delete(cc.imgs, old.k)
		cc.bytes -= len(old.img.Pix)
	}
}

func (cc *coverCache) load(k coverKey) {
	cc.sem <- struct{}{}
	img, err := loadCover(k.url, k.w, k.h)
	<-cc.sem
	cc.mu.Lock()
	delete(cc.pending, k)
	if err != nil {
		log.Printf("store: cover %s: %v", k.url, err)
		cc.failed[k.url] = true
	} else {
		cc.keep(k, img)
		writeScaled(k, img)
	}
	loaded := cc.loaded
	cc.mu.Unlock()
	if loaded != nil && err == nil {
		loaded(k.url)
	}
}

// scaledFile is where a cover scaled to w x h is kept.
func scaledFile(k coverKey) string {
	return filepath.Join(coverDir, "s", fmt.Sprintf("%s-%dx%d", hash(k.url), k.w, k.h))
}

func readScaled(k coverKey) *image.RGBA {
	b, err := os.ReadFile(scaledFile(k))
	if err != nil || len(b) != 4*k.w*k.h {
		return nil
	}
	return &image.RGBA{Pix: b, Stride: 4 * k.w, Rect: image.Rect(0, 0, k.w, k.h)}
}

func writeScaled(k coverKey, img *image.RGBA) {
	f := scaledFile(k)
	os.MkdirAll(filepath.Dir(f), 0o755)
	if os.WriteFile(f+".new", img.Pix, 0o644) == nil {
		os.Rename(f+".new", f)
	}
}

func loadCover(url string, w, h int) (*image.RGBA, error) {
	file := filepath.Join(coverDir, hash(url))
	b, err := os.ReadFile(file)
	if strings.HasPrefix(url, "epub:") { // a book on the tablet: the cover is inside it
		path, inside, _ := strings.Cut(strings.TrimPrefix(url, "epub:"), "#")
		eb, err := epub.Open(path)
		if err != nil {
			return nil, err
		}
		b, err = eb.ReadFile(inside)
		eb.Close()
		if err != nil {
			return nil, err
		}
	} else if err != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		resp, err := webGetCtx(ctx, url)
		if err != nil {
			return nil, err
		}
		b, err = io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		os.MkdirAll(coverDir, 0o755)
		os.WriteFile(file, b, 0o644)
	}
	src, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		os.Remove(file)
		return nil, fmt.Errorf("decode: %v", err)
	}
	sb := src.Bounds()
	if sb.Dx() < 8 || sb.Dy() < 8 { // 1x1 "no cover" placeholders
		return nil, fmt.Errorf("no cover (%dx%d)", sb.Dx(), sb.Dy())
	}
	// Fill w x h, cropping the longer side (covers are close to 2:3 anyway).
	crop := sb
	if sb.Dx()*h > sb.Dy()*w {
		cw := sb.Dy() * w / h
		crop.Min.X += (sb.Dx() - cw) / 2
		crop.Max.X = crop.Min.X + cw
	} else {
		ch := sb.Dx() * h / w
		crop.Min.Y += (sb.Dy() - ch) / 2
		crop.Max.Y = crop.Min.Y + ch
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	xdraw.ApproxBiLinear.Scale(dst, dst.Rect, src, crop, xdraw.Src, nil)
	return dst, nil
}

// coverColors are the backgrounds of covers drawn for books without one.
var coverColors = []color.RGBA{
	{0x3b, 0x4a, 0x6b, 255}, {0x6b, 0x3b, 0x4a, 255}, {0x3b, 0x6b, 0x5a, 255},
	{0x6b, 0x5a, 0x3b, 255}, {0x55, 0x3b, 0x6b, 255}, {0x2f, 0x5d, 0x6e, 255},
}

// placeholderColour is the made-up cover's colour for a book (the same every time).
func placeholderColour(key string) color.RGBA {
	h := 0
	for _, ch := range key {
		h = h*31 + int(ch)
	}
	return coverColors[(h&0x7fffffff)%len(coverColors)]
}

// drawCover draws a book's cover in r: the real one if loaded, else a made-up one with the
// title and author (shown while loading, and for books that have none).
func (c *console) drawCover(img *image.RGBA, r image.Rectangle, it *storeItem) {
	if cv := covers.get(it.cover, r.Dx(), r.Dy()); cv != nil {
		xdraw.Draw(img, r, cv, image.Point{}, xdraw.Src)
		return
	}
	bg := placeholderColour(it.key)
	ui.Fill(img, r, bg)
	ui.Fill(img, image.Rect(r.Min.X+r.Dx()/12, r.Min.Y, r.Min.X+r.Dx()/12+6, r.Max.Y), blend(bg, pgDark, 0.35))
	if r.Dx() < 120 { // too small for a title: the colour and the spine say "a book"
		return
	}
	face, small := apple().captionBold, apple().caption
	if r.Dx() > 400 {
		face = apple().headline
	}
	lh := face.Metrics().Height.Ceil() + 4
	pad := r.Dx()/12 + 22
	y := r.Min.Y + r.Dy()/6
	for i, l := range wrapText(face, it.title, r.Dx()-pad-18) {
		if i == 5 || y+lh > r.Max.Y-70 {
			break
		}
		y += lh
		ui.DrawText(img, face, r.Min.X+pad, y, pgText, visual(l))
	}
	ui.DrawText(img, small, r.Min.X+pad, r.Max.Y-36, blend(pgText, bg, 0.3), visual(clip(small, it.author, r.Dx()-pad-18)))
}
