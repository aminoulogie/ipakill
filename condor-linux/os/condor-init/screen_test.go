package main

import "testing"

const fbW, fbH = 1920, 1200 // TRA-901G native scanout

func TestRotationRoundTrip(t *testing.T) {
	for _, r := range []Rotation{Rot0, Rot90, Rot180, Rot270} {
		w, h := r.size(fbW, fbH)
		for _, p := range [][2]int{{0, 0}, {w - 1, 0}, {0, h - 1}, {w - 1, h - 1}, {123, 456}} {
			fx, fy := r.toFB(p[0], p[1], fbW, fbH)
			if fx < 0 || fy < 0 || fx >= fbW || fy >= fbH {
				t.Fatalf("rot %d: %v maps outside the framebuffer: %d,%d", r, p, fx, fy)
			}
			if x, y := r.fromFB(fx, fy, fbW, fbH); x != p[0] || y != p[1] {
				t.Fatalf("rot %d: %v -> %d,%d -> %d,%d", r, p, fx, fy, x, y)
			}
		}
	}
}

// What was seen on the tablet with native drawing: framebuffer x=0 appeared at the top and
// the framebuffer's bottom rows on the left. Rot90 must put logical top-left there.
func TestRot90MatchesPanel(t *testing.T) {
	w, h := Rot90.size(fbW, fbH)
	if w != 1200 || h != 1920 {
		t.Fatalf("logical size %dx%d, want portrait 1200x1920", w, h)
	}
	if fx, fy := Rot90.toFB(0, 0, fbW, fbH); fx != 0 || fy != fbH-1 { // top-left
		t.Fatalf("top-left -> %d,%d", fx, fy)
	}
	if fx, _ := Rot90.toFB(0, h-1, fbW, fbH); fx != fbW-1 { // bottom edge = framebuffer right
		t.Fatalf("bottom-left -> fx %d", fx)
	}
}

// fakeFB records WriteAt calls like the framebuffer device would receive them.
type fakeFB struct{ mem []byte }

func (f *fakeFB) WriteAt(p []byte, off int64) (int, error) { return copy(f.mem[off:], p), nil }

func xrgb() (bitfield, bitfield, bitfield) {
	return bitfield{16, 8, 0}, bitfield{8, 8, 0}, bitfield{0, 8, 0}
}

func TestScreenSetFlush(t *testing.T) {
	dev := &fakeFB{mem: make([]byte, fbW*4*fbH)}
	r, g, b := xrgb()
	s := newScreen(dev, fbW, fbH, fbW*4, 32, r, g, b, Rot90)
	s.Set(0, 0, 255, 0, 0) // logical top-left, red → native bottom-left
	o := (fbH - 1) * fbW * 4
	if dev.mem[o+2] != 0 {
		t.Fatal("Set must not touch the device before Flush")
	}
	if s.dirtyLo != fbH-1 || s.dirtyHi != fbH-1 {
		t.Fatalf("dirty rows %d..%d", s.dirtyLo, s.dirtyHi)
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if dev.mem[o+2] != 255 || dev.mem[o+1] != 0 || dev.mem[o] != 0 {
		t.Fatalf("pixel bytes after flush % x", dev.mem[o:o+4])
	}
	if s.dirtyLo <= s.dirtyHi {
		t.Fatal("Flush must reset the dirty range")
	}
	s.Set(-1, 5, 1, 1, 1) // off screen: ignored, no panic
	s.Set(s.W, 0, 1, 1, 1)
	s.Dot(5, 5, 20, 1, 1, 1) // partly off screen
	s.Line(0, 0, s.W-1, s.H-1, 3, 9, 9, 9)
}

func TestScreenClear(t *testing.T) {
	dev := &fakeFB{mem: make([]byte, fbW*4*fbH)}
	for i := range dev.mem {
		dev.mem[i] = 0x77
	}
	r, g, b := xrgb()
	s := newScreen(dev, fbW, fbH, fbW*4, 32, r, g, b, Rot90)
	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	for i, v := range dev.mem {
		if v != 0 {
			t.Fatalf("byte %d is %#x after Clear", i, v)
		}
	}
}
