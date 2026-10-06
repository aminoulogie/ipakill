package main

import (
	"encoding/binary"
	"image"
	"log"
	"math"
	"os"
	"time"
)

// gpuFlag turns on drawing through the GPU (Settings > Display & Brightness). gpuTrying
// exists while a start hasn't been confirmed: if condor-init finds it at boot, the last try
// went wrong (the tablet hung or restarted), and the GPU stays off.
const (
	gpuFlag   = condorHome + "/gpu"
	gpuTrying = condorHome + "/gpu.trying"
)

func gpuEnabled() bool { _, err := os.Stat(gpuFlag); return err == nil }

var gpuTriedAtBoot bool

// gpuSafeToTry is false, once, when the previous start was never confirmed.
func gpuSafeToTry() bool {
	if gpuTriedAtBoot {
		return true
	}
	gpuTriedAtBoot = true
	if _, err := os.Stat(gpuTrying); err == nil {
		log.Printf("GPU: the last start wasn't confirmed; GPU drawing is off (turn it on again in Settings)")
		os.Remove(gpuTrying)
		os.Remove(gpuFlag)
		return false
	}
	return true
}

// Drawing through the GPU (gpu_linux.go, os/condor-gl/condorsf.cpp). On this tablet only
// SurfaceFlinger reaches the panel, so with the GPU on, condor runs it and shows its whole
// screen on a SurfaceFlinger layer: it still draws with the processor, into memory the GPU
// helper shares, and the helper shows the rows that changed. On top of that the GPU does what
// the processor can't do 60 times a second: tall pages scroll by moving a picture of the page
// (gpuscroll.go), and screen changes animate as quads of the pictures before and after.

// quad is one rectangle of a frame, in the framebuffer's native pixels (glanim.c's layout).
type quad struct {
	kind                           int32 // 0 the screen before, 1 after, 3 a colour
	x0, y0, x1, y1, u0, v0, u1, v1 float32
	a0, a1                         float32 // colour quads: opacity at y0 and y1
	mul                            float32 // pictures: brightness
	r, g, b                        float32 // the colour, or the tint mixed into a picture
	mix                            float32
}

const (
	gpuOld = 0
	gpuNew = 1
)

// gpuDev is the GPU helper as condor-init sees it (a fake one in tests).
type gpuDev interface {
	images() (old, nu []byte)        // shared with the GPU, the framebuffer's native layout
	load() error                     // upload both pictures
	frame(q []quad, last bool) error // show a frame
	close()
}

// gpuDisplay is the GPU showing the whole screen.
type gpuDisplay interface {
	gpuDev
	screenBuf() []byte                          // the screen, shared: condor draws into it
	present(lo, hi int) error                   // native rows lo..hi changed: show the screen
	pageLoad(img *image.RGBA) error             // the page (logical), for scrolling
	pageRows(img *image.RGBA, y0, y1 int) error // the page's rows y0..y1-1 changed
	overlay(q []quad)                           // drawn over the screen until overlayOff; latest wins
	overlayOff() error
	failed() error
}

// pageMaxRows is the tallest page the GPU scrolls (taller ones scroll on the CPU).
const pageMaxRows = 10000

// gpuPage is a quad of the page: x y in logical screen pixels, u v in the page's pixels.
const gpuPage = 4

// encodeFrame is a frame command for glanim.
func encodeFrame(q []quad, last bool) []byte {
	cmd := uint32(2)
	if last {
		cmd = 3
	}
	b := binary.LittleEndian.AppendUint32(nil, cmd)
	b = binary.LittleEndian.AppendUint32(b, uint32(len(q)))
	for _, k := range q {
		b = binary.LittleEndian.AppendUint32(b, uint32(k.kind))
		for _, f := range []float32{k.x0, k.y0, k.x1, k.y1, k.u0, k.v0, k.u1, k.v1, k.a0, k.a1, k.mul, k.r, k.g, k.b, k.mix} {
			b = binary.LittleEndian.AppendUint32(b, math.Float32bits(f))
		}
	}
	return b
}

// frect is a rectangle in logical (portrait) pixels; x0 > x1 mirrors a picture.
type frect struct{ x0, y0, x1, y1 int }

func fr(r image.Rectangle) frect { return frect{r.Min.X, r.Min.Y, r.Max.X, r.Max.Y} }

// pic places the part src of picture kind at dst, both logical. Logical (x, y) is native
// (y, fbH - x) at pixel edges, so the quad's corners map one to one.
func (c *console) pic(kind int32, dst, src frect) quad {
	h := float32(c.s.fbH)
	return quad{kind: kind,
		x0: float32(dst.y0), y0: h - float32(dst.x0), x1: float32(dst.y1), y1: h - float32(dst.x1),
		u0: float32(src.y0), v0: h - float32(src.x0), u1: float32(src.y1), v1: h - float32(src.x1),
		mul: 1}
}

// shade darkens the logical columns [x0, x1) of rows [y0, y1), from k0 at x0 to k1 at x1.
func (c *console) shade(x0, x1, y0, y1 int, k0, k1 float64) quad {
	h := float32(c.s.fbH)
	return quad{kind: 3, x0: float32(y0), y0: h - float32(x0), x1: float32(y1), y1: h - float32(x1),
		a0: float32(k0), a1: float32(k1)}
}

func (c *console) same(kind int32, r frect) quad { return c.pic(kind, r, r) }

// --- the animations as quads (each matches its CPU frame in anim.go) ---------------------

func (c *console) pushQuads(t float64, forward bool) []quad {
	w, h, bar := c.s.W, c.s.H, c.barH
	top, under := int32(gpuNew), int32(gpuOld)
	if !forward {
		top, under, t = gpuOld, gpuNew, 1-t
	}
	edge := int(float64(w) * (1 - t))
	drift := int(float64(w) * t / 3)
	return []quad{
		c.pic(under, frect{0, bar, edge, h}, frect{drift, bar, edge + drift, h}),
		c.shade(edge-30, edge, bar, h, 0, 0.16),
		c.pic(top, frect{edge, bar, w, h}, frect{0, bar, w - edge, h}),
		c.same(gpuNew, frect{0, 0, w, bar}),
	}
}

// riseQuads: riseFrame's sheet r rising, from pictures old and nu.
func (c *console) riseQuads(old, nu int32, r image.Rectangle, t float64) []quad {
	w, h := c.s.W, c.s.H
	off := int(float64(h-r.Min.Y) * (1 - t))
	y0 := min(r.Min.Y+off, h)
	dim := c.same(old, frect{r.Min.X, r.Min.Y, r.Max.X, y0})
	dim.mul = float32(1 - 0.25*t)
	return []quad{
		c.same(nu, frect{0, 0, w, h}),
		dim,
		c.pic(nu, frect{r.Min.X, y0, r.Max.X, h}, frect{r.Min.X, r.Min.Y, r.Max.X, h - off}),
	}
}

func (c *console) slideQuads(t float64, dir int) []quad {
	w, h, bar := c.s.W, c.s.H, c.barH
	o := int(float64(w) * t)
	q := []quad{c.same(gpuNew, frect{0, 0, w, bar})}
	if dir > 0 {
		return append(q, c.pic(gpuOld, frect{0, bar, w - o, h}, frect{o, bar, w, h}),
			c.pic(gpuNew, frect{w - o, bar, w, h}, frect{0, bar, o, h}))
	}
	return append(q, c.pic(gpuNew, frect{0, bar, o, h}, frect{w - o, bar, w, h}),
		c.pic(gpuOld, frect{o, bar, w, h}, frect{0, bar, w - o, h}))
}

// curlQuads: curlFrame's page a turning over to show b.
func (c *console) curlQuads(a, b int32, t float64) []quad {
	w, h, bar := c.s.W, c.s.H, c.barH
	f := int(float64(w) * (1 - t))
	e := max(2*f-w, 0)
	th := c.theme()
	back := blend(th.bg, rgb(0x000000), 0.05)
	if th.dark {
		back = blend(th.bg, rgb(0xffffff), 0.06)
	}
	flap := c.pic(a, frect{e, bar, f, h}, frect{2*f - e + 1, bar, f + 1, h}) // mirrored
	flap.r, flap.g, flap.b = float32(back.R)/255, float32(back.G)/255, float32(back.B)/255
	flap.mix = 225.0 / 256 // the paper's back, with its print faintly showing through
	return []quad{
		c.same(a, frect{0, bar, e, h}),
		c.shade(e-14, e, bar, h, 0, 0.12),
		flap,
		c.shade(e, f, bar, h, 0, 16.0/256), // darker toward the fold
		c.same(b, frect{f, bar, w, h}),
		c.shade(f, f+46, bar, h, 0.28, 0),
		c.same(b, frect{0, 0, w, bar}),
	}
}

// --- running them --------------------------------------------------------------------------

// gpuHas: old and nu are the GPU's pictures (the GPU was up when they were taken).
func (c *console) gpuHas(old, nu []byte) bool {
	if c.gpu == nil || len(old) == 0 || len(nu) == 0 {
		return false
	}
	a, b := c.gpu.images()
	return &a[0] == &old[0] && &b[0] == &nu[0]
}

// gpuFrames plays an animation on the GPU, one frame of build(t) per vertical blank, time-
// boxed like frames(). False if the GPU failed (it's dropped; the caller just ends).
func (c *console) gpuFrames(d time.Duration, build func(t float64) []quad) bool {
	if err := c.gpu.load(); err != nil {
		c.dropGPU(err)
		return false
	}
	d = time.Duration(float64(d) * animScale)
	start := time.Now()
	for {
		t := float64(time.Since(start)) / float64(d)
		if d <= 0 || t >= 1 {
			break
		}
		if err := c.gpu.frame(build(spring(t)), false); err != nil {
			c.dropGPU(err)
			return false
		}
		animFrames++
	}
	if err := c.gpu.frame([]quad{c.same(gpuNew, frect{0, 0, c.s.W, c.s.H})}, true); err != nil {
		c.dropGPU(err)
		return false
	}
	return true
}

// wantGPU starts drawing through the GPU in the background when it's turned on (once).
// condor keeps drawing on the framebuffer until the GPU's layer is up.
func (c *console) wantGPU() {
	if c.disp == nil && !c.gpuStarting && (!gpuEnabled() || !gpuSafeToTry()) && leftoverSurfaceFlinger() {
		// A SurfaceFlinger from an earlier condor (an update, a rollback) would hide the
		// framebuffer: stop it.
		log.Printf("GPU: off, but SurfaceFlinger is running: stopping it")
		stopSurfaceFlinger()
		reclaimFramebuffer(c.s)
		c.s.markRows(0, c.s.fbH-1)
		c.s.Flush()
	}
	if c.disp != nil || c.gpuStarting || c.gpuFailed || !gpuEnabled() || !gpuSafeToTry() {
		return
	}
	c.gpuStarting = true
	os.WriteFile(gpuTrying, nil, 0o644)
	syncDisks()
	go func() {
		g, err := startGPU(c.s)
		drawMu.Lock()
		defer drawMu.Unlock()
		c.gpuStarting = false
		if err != nil {
			log.Printf("GPU: %v; drawing stays on the framebuffer", err)
			c.gpuFailed = true
			os.Remove(gpuTrying)
			os.Remove(gpuFlag)
			stopSurfaceFlinger()
			reclaimFramebuffer(c.s)
			c.s.markRows(0, c.s.fbH-1)
			c.s.Flush()
			if c.mode == modeSettings {
				c.redrawAll()
			}
			return
		}
		c.useGPU(g)
		if c.mode == modeSettings {
			c.redrawAll()
		}
		log.Printf("GPU: the screen is on the PowerVR, through SurfaceFlinger")
		time.AfterFunc(10*time.Second, func() {
			drawMu.Lock()
			defer drawMu.Unlock()
			if c.disp != nil {
				os.Remove(gpuTrying)
				log.Printf("GPU: confirmed")
			}
		})
	}()
}

// useGPU moves the screen into the GPU's shared memory and shows it there. Caller holds
// drawMu.
func (c *console) useGPU(g gpuDisplay) {
	scr := g.screenBuf()
	copy(scr, c.s.buf)
	c.s.buf = scr
	c.s.dev = &gpuWriter{c: c, g: g}
	c.gpu, c.disp = g, g
	c.s.markRows(0, c.s.fbH-1)
	c.s.Flush()
}

// gpuWriter is the screen's device while the GPU shows it: a Flush shows the rows that
// changed. If the GPU fails, the screen goes back to the framebuffer.
type gpuWriter struct {
	c *console
	g gpuDisplay
}

func (w *gpuWriter) WriteAt(p []byte, off int64) (int, error) {
	s := w.c.s
	lo := int(off) / s.stride
	hi := (int(off)+len(p))/s.stride - 1
	if err := w.g.present(lo, hi); err != nil {
		w.c.gpuLost(err)
		return s.fbDev.WriteAt(p, off)
	}
	return len(p), nil
}

// gpuLost puts the screen back on the framebuffer after a GPU failure, and turns the GPU off
// for the next start. Caller holds drawMu.
func (c *console) gpuLost(err error) {
	if c.disp == nil {
		return
	}
	log.Printf("GPU: %v; back to drawing on the framebuffer", err)
	c.disp.close()
	c.gpu, c.disp = nil, nil
	c.gpuFailed = true
	c.sc.gpuOn = false
	os.Remove(gpuFlag)
	os.Remove(gpuTrying)
	stopSurfaceFlinger()
	c.s.dev = c.s.fbDev
	reclaimFramebuffer(c.s)
	c.s.markRows(0, c.s.fbH-1)
	c.s.Flush()
}

// dropGPU gives up on the GPU after an error in an animation. Caller holds drawMu.
func (c *console) dropGPU(err error) {
	if c.disp != nil {
		c.gpuLost(err)
		return
	}
	log.Printf("GPU: %v; animations back on the CPU", err)
	c.gpu.close()
	c.gpu = nil
	c.gpuFailed = true
}
