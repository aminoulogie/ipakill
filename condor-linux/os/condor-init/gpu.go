package main

import (
	"encoding/binary"
	"image"
	"log"
	"math"
	"os"
	"time"
)

func gpuEnabled() bool { _, err := os.Stat("/data/condor/gpu"); return err == nil }

// Animations on the GPU. The tablet's PowerVR SGX544 works only through Android's own
// drivers, so a small bionic program (os/condor-gl/glanim.c, carried inside condor-init)
// opens the framebuffer with them, and condor-init drives it: the screen before and after a
// change go into memory both share, and each frame is a handful of quads, rectangles of
// those two pictures put somewhere on the screen, which the GPU composes and shows at the
// panel's vertical blank (60 a second). The CPU then only works out where the rectangles
// go. Anything wrong with the GPU and the animations fall back to the CPU frames (anim.go).

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
	frame(q []quad, last bool) error // show a frame; the last one also brings the display home
	close()
}

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

// wantGPU starts the GPU helper in the background when animations are on (once).
//
// Off unless /data/condor/gpu exists: tested on the tablet (gl-show), the GPU draws
// correctly but its frames never reach the panel (black screen). Android shows them through
// Intel's hardware composer, which only SurfaceFlinger drives; without it the display
// plane the driver flips to isn't shown. Kept for experiments.
func (c *console) wantGPU() {
	if !c.cfg.Animations || c.gpu != nil || c.gpuStarting || c.gpuFailed || !gpuEnabled() {
		return
	}
	c.gpuStarting = true
	go func() {
		g, err := startGPU(c.s)
		drawMu.Lock()
		defer drawMu.Unlock()
		c.gpuStarting = false
		if err != nil {
			log.Printf("GPU: %v; animations stay on the CPU", err)
			c.gpuFailed = true
			return
		}
		log.Printf("GPU: animations on the PowerVR")
		c.gpu = g
	}()
}

// dropGPU gives up on the GPU after an error. Caller holds drawMu.
func (c *console) dropGPU(err error) {
	log.Printf("GPU: %v; animations back on the CPU", err)
	c.gpu.close()
	c.gpu = nil
	c.gpuFailed = true
}
