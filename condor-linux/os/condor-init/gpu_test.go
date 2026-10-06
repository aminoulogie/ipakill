package main

import (
	"image"
	"math"
	"testing"
)

// softGPU does what glanim does, in software: the quads of a frame drawn onto a native-
// layout screen, nearest pixel, as the GPU samples them.
type softGPU struct {
	s           *Screen
	old, nu     []byte
	screen      []byte // what the GPU shows
	frames      int
	last, loads int

	// As the screen's display (gpuDisplay): condorsf.cpp's screen image, page and overlay.
	scr      []byte
	page     *image.RGBA
	over     []quad
	presents int
	uploads  int // pages uploaded whole
}

func newSoftGPU(s *Screen) *softGPU {
	n := len(s.buf)
	return &softGPU{s: s, old: make([]byte, n), nu: make([]byte, n), screen: make([]byte, n), scr: make([]byte, n)}
}

func (g *softGPU) screenBuf() []byte { return g.scr }
func (g *softGPU) failed() error     { return nil }

func (g *softGPU) present(lo, hi int) error {
	g.presents++
	copy(g.screen, g.scr)
	for _, k := range g.over {
		g.draw(k)
	}
	return nil
}

func (g *softGPU) pageLoad(img *image.RGBA) error {
	g.uploads++
	g.page = image.NewRGBA(img.Rect)
	copy(g.page.Pix, img.Pix)
	return nil
}

func (g *softGPU) pageRows(img *image.RGBA, y0, y1 int) error {
	for y := y0; y < y1; y++ {
		copy(g.page.Pix[g.page.PixOffset(0, y):g.page.PixOffset(0, y+1)], img.Pix[img.PixOffset(0, y):img.PixOffset(0, y+1)])
	}
	return nil
}

func (g *softGPU) overlay(q []quad)  { g.over = q; g.present(0, 0) }
func (g *softGPU) overlayOff() error { g.over = nil; return nil }

func (g *softGPU) images() (old, nu []byte) { return g.old, g.nu }
func (g *softGPU) load() error              { g.loads++; return nil }
func (g *softGPU) close()                   {}

func (g *softGPU) frame(q []quad, last bool) error {
	for i := range g.screen {
		g.screen[i] = 0
	}
	for _, k := range q {
		g.draw(k)
	}
	g.frames++
	if last {
		g.last++
	}
	return nil
}

func (g *softGPU) draw(k quad) {
	s := g.s
	if k.kind == gpuPage { // logical coordinates: each native pixel's centre, turned back
		for py := 0; py < s.fbH; py++ {
			lx := float32(s.fbH) - (float32(py) + 0.5)
			if lx < k.x0 || lx >= k.x1 {
				continue
			}
			for px := 0; px < s.fbW; px++ {
				ly := float32(px) + 0.5
				if ly < k.y0 || ly >= k.y1 {
					continue
				}
				u := int(k.u0 + (lx-k.x0)*(k.u1-k.u0)/(k.x1-k.x0))
				v := int(k.v0 + (ly-k.y0)*(k.v1-k.v0)/(k.y1-k.y0))
				p := g.page.Pix[g.page.PixOffset(u, v):]
				d := g.screen[py*s.stride+4*px:]
				d[0], d[1], d[2] = p[2], p[1], p[0]
			}
		}
		return
	}
	lo := func(a, b float32) float32 { return min(a, b) }
	hi := func(a, b float32) float32 { return max(a, b) }
	for py := max(int(math.Ceil(float64(lo(k.y0, k.y1)-0.5))), 0); py < s.fbH && float32(py)+0.5 < hi(k.y0, k.y1); py++ {
		fy := (float32(py) + 0.5 - k.y0) / (k.y1 - k.y0)
		for px := max(int(math.Ceil(float64(lo(k.x0, k.x1)-0.5))), 0); px < s.fbW && float32(px)+0.5 < hi(k.x0, k.x1); px++ {
			fx := (float32(px) + 0.5 - k.x0) / (k.x1 - k.x0)
			d := g.screen[py*s.stride+4*px:]
			if k.kind == 3 {
				a := k.a0 + fy*(k.a1-k.a0)
				for i, c := range []float32{k.b, k.g, k.r} {
					d[i] = uint8(float32(d[i])*(1-a) + c*255*a + 0.5)
				}
				continue
			}
			src := g.old
			switch k.kind {
			case gpuNew:
				src = g.nu
			case 2:
				src = g.scr
			}
			u := min(max(int(math.Floor(float64(k.u0+fx*(k.u1-k.u0)))), 0), s.fbW-1)
			v := min(max(int(math.Floor(float64(k.v0+fy*(k.v1-k.v0)))), 0), s.fbH-1)
			p := src[v*s.stride+4*u:]
			for i, c := range []float32{k.b, k.g, k.r} {
				d[i] = uint8((float32(p[i])*(1-k.mix)+c*255*k.mix)*k.mul + 0.5)
			}
		}
	}
}

// differ counts pixels more than tol apart in any colour.
func differ(a, b []byte, s *Screen, tol int) int {
	n := 0
	for y := 0; y < s.fbH; y++ {
		for x := 0; x < s.fbW; x++ {
			i := y*s.stride + 4*x
			for j := 0; j < 3; j++ {
				if d := int(a[i+j]) - int(b[i+j]); d > tol || d < -tol {
					n++
					break
				}
			}
		}
	}
	return n
}

// The GPU's frames are the CPU's frames: every animation's quads, drawn, match anim.go's
// frame for the same moment.
func TestGPUQuadsMatchCPUFrames(t *testing.T) {
	c := testConsole(t)
	c.cfg.Animations = true
	c.showPage()
	g := newSoftGPU(c.s)
	copy(g.old, c.s.buf)
	c.s.hold = true
	c.setMode(modeSettings)
	c.s.hold = false
	copy(g.nu, c.s.buf)
	sheet := c.lookupSheet()
	total := c.s.fbW * c.s.fbH
	for _, tc := range []struct {
		name  string
		cpu   func(t float64)
		gpu   func(t float64) []quad
		tol   int
		allow int // pixels allowed off (curl's darkening is a straight ramp on the GPU)
	}{
		{"push", func(t float64) { c.pushFrame(g.old, g.nu, t, true) }, func(t float64) []quad { return c.pushQuads(t, true) }, 2, 0},
		{"pop", func(t float64) { c.pushFrame(g.old, g.nu, t, false) }, func(t float64) []quad { return c.pushQuads(t, false) }, 2, 0},
		{"rise", func(t float64) { c.riseFrame(g.old, g.nu, sheet, t) }, func(t float64) []quad { return c.riseQuads(gpuOld, gpuNew, sheet, t) }, 2, 0},
		{"fall", func(t float64) { c.riseFrame(g.nu, g.old, sheet, 1-t) }, func(t float64) []quad { return c.riseQuads(gpuNew, gpuOld, sheet, 1-t) }, 2, 0},
		{"slide", func(t float64) { c.slideFrame(g.old, g.nu, t, 1) }, func(t float64) []quad { return c.slideQuads(t, 1) }, 0, 0},
		{"slide back", func(t float64) { c.slideFrame(g.old, g.nu, t, -1) }, func(t float64) []quad { return c.slideQuads(t, -1) }, 0, 0},
		{"curl", func(t float64) { c.curlFrame(g.old, g.nu, t) }, func(t float64) []quad { return c.curlQuads(gpuOld, gpuNew, t) }, 8, total / 100},
		{"curl back", func(t float64) { c.curlFrame(g.nu, g.old, 1-t) }, func(t float64) []quad { return c.curlQuads(gpuNew, gpuOld, 1-t) }, 8, total / 100},
	} {
		for _, at := range []float64{0, 0.13, 0.5, 0.77, 1} {
			tc.cpu(at)
			g.frame(tc.gpu(at), false)
			if n := differ(c.s.buf, g.screen, c.s, tc.tol); n > tc.allow {
				t.Errorf("%s at %.2f: %d pixels differ from the CPU frame", tc.name, at, n)
			}
		}
	}
}

// With a GPU, transitions and page turns run on it and still end on the new screen.
func TestTransitionsOnTheGPU(t *testing.T) {
	c := testConsole(t)
	c.cfg.Animations = true
	c.showPage()
	g := newSoftGPU(c.s)
	c.gpu = g
	before := animFrames
	c.transition("push", c.fullSheet(), func() { c.setMode(modeSettings) })
	if g.loads != 1 || g.last != 1 || animFrames == before {
		t.Fatalf("push: %d loads, %d last frames, %d frames", g.loads, g.last, animFrames-before)
	}
	if differ(g.screen, c.s.buf, c.s, 0) != 0 {
		t.Error("the GPU's last frame isn't the new screen")
	}
	end := append([]byte(nil), c.s.buf...)
	c.redrawAll()
	if differ(end, c.s.buf, c.s, 0) != 0 {
		t.Error("push ended off the new screen")
	}
}
