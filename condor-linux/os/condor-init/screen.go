package main

import (
	"encoding/binary"
	"os"
	"strconv"
	"strings"
)

// Rotation is how our logical screen is turned relative to the framebuffer's native scanout.
//
// TRA-901G (verified on screen 2026-10-03): psbfb scans out 1920x1200 with fb rotate=0, but
// the tablet is used in portrait. A pattern drawn in native coordinates showed the
// framebuffer's left edge (x=0) at the top and its bottom rows on the left, so the logical
// portrait screen (1200x1920) is the framebuffer turned by Rot90. The Goodix touch panel
// reports in the same native 1920x1200 space, so touch uses the same setting (fromFB).
type Rotation int

const (
	Rot0   Rotation = 0
	Rot90  Rotation = 90
	Rot180 Rotation = 180
	Rot270 Rotation = 270

	defaultRotation = Rot90
	rotationFile    = "/data/condor/rotation" // optional override: 0, 90, 180 or 270
)

// rotation is the single orientation setting used for drawing, text and touch.
var rotation = loadRotation()

func loadRotation() Rotation {
	b, err := os.ReadFile(rotationFile)
	if err != nil {
		return defaultRotation
	}
	switch v, _ := strconv.Atoi(strings.TrimSpace(string(b))); Rotation(v) {
	case Rot0, Rot90, Rot180, Rot270:
		return Rotation(v)
	}
	return defaultRotation
}

// size returns the logical width and height for a native fbW x fbH framebuffer.
func (r Rotation) size(fbW, fbH int) (int, int) {
	if r == Rot90 || r == Rot270 {
		return fbH, fbW
	}
	return fbW, fbH
}

// toFB maps logical (x, y) to native framebuffer coordinates.
func (r Rotation) toFB(x, y, fbW, fbH int) (int, int) {
	switch r {
	case Rot90:
		return y, fbH - 1 - x
	case Rot180:
		return fbW - 1 - x, fbH - 1 - y
	case Rot270:
		return fbW - 1 - y, x
	}
	return x, y
}

// fromFB maps native framebuffer coordinates (e.g. a touch point) to logical (x, y).
func (r Rotation) fromFB(fx, fy, fbW, fbH int) (int, int) {
	switch r {
	case Rot90:
		return fbH - 1 - fy, fx
	case Rot180:
		return fbW - 1 - fx, fbH - 1 - fy
	case Rot270:
		return fy, fbW - 1 - fx
	}
	return fx, fy
}

// bitfield is struct fb_bitfield: where a colour channel sits in a pixel.
type bitfield struct{ offset, length, msbRight uint32 }

// Screen is the framebuffer seen in logical (rotated) coordinates.
type Screen struct {
	mem              []byte
	stride, bpp      int
	fbW, fbH         int // native
	W, H             int // logical
	rot              Rotation
	red, green, blue bitfield
}

func newScreen(mem []byte, fbW, fbH, stride, bpp int, red, green, blue bitfield, rot Rotation) *Screen {
	w, h := rot.size(fbW, fbH)
	return &Screen{mem: mem, stride: stride, bpp: bpp, fbW: fbW, fbH: fbH, W: w, H: h, rot: rot,
		red: red, green: green, blue: blue}
}

func (s *Screen) pack(r, g, b uint8) uint32 {
	c := func(v uint8, f bitfield) uint32 { return (uint32(v) >> (8 - f.length)) << f.offset }
	return c(r, s.red) | c(g, s.green) | c(b, s.blue)
}

// Set colours one logical pixel; points off the screen are ignored.
func (s *Screen) Set(x, y int, r, g, b uint8) {
	if x < 0 || y < 0 || x >= s.W || y >= s.H {
		return
	}
	fx, fy := s.rot.toFB(x, y, s.fbW, s.fbH)
	o := fy*s.stride + fx*s.bpp/8
	if s.bpp == 32 {
		binary.LittleEndian.PutUint32(s.mem[o:], s.pack(r, g, b))
	} else {
		binary.LittleEndian.PutUint16(s.mem[o:], uint16(s.pack(r, g, b)))
	}
}

// Fill colours the logical rectangle [x0,x1) x [y0,y1).
func (s *Screen) Fill(x0, y0, x1, y1 int, r, g, b uint8) {
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			s.Set(x, y, r, g, b)
		}
	}
}

// TestPattern draws, in logical coordinates: a white border, 8 vertical colour bars over the
// top two thirds, a grey ramp (black left → white right) along the bottom, and a red square
// in the top-left corner so the orientation can be checked at a glance.
func (s *Screen) TestPattern() {
	bars := [][3]uint8{{255, 255, 255}, {255, 255, 0}, {0, 255, 255}, {0, 255, 0},
		{255, 0, 255}, {255, 0, 0}, {0, 0, 255}, {0, 0, 0}}
	for y := 0; y < s.H; y++ {
		for x := 0; x < s.W; x++ {
			var c [3]uint8
			switch {
			case x < 8 || x >= s.W-8 || y < 8 || y >= s.H-8:
				c = [3]uint8{255, 255, 255}
			case y < s.H*2/3:
				c = bars[x*len(bars)/s.W]
			default:
				v := uint8(x * 255 / s.W)
				c = [3]uint8{v, v, v}
			}
			s.Set(x, y, c[0], c[1], c[2])
		}
	}
	s.Fill(24, 24, 104, 104, 255, 0, 0)
}
