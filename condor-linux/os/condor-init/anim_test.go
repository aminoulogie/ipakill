package main

import (
	"bytes"
	"testing"
	"time"
)

// Every transition ends exactly on the screen it leads to, after drawing frames.
// sameBelowBar compares two native-layout screens below the status bar: the bar's clock can
// tick between two draws.
func sameBelowBar(c *console, a, b []byte) bool {
	for fy := 0; fy < c.s.fbH; fy++ {
		row := fy*c.s.stride + 4*c.barH // logical y >= barH is native x >= barH
		if !bytes.Equal(a[row:fy*c.s.stride+4*c.s.fbW], b[row:fy*c.s.stride+4*c.s.fbW]) {
			return false
		}
	}
	return true
}

func TestTransitionsEndOnTheNewScreen(t *testing.T) {
	c := testConsole(t)
	drawMu.Lock() // like the touch loop: background loaders wait
	defer drawMu.Unlock()
	c.cfg.Animations = true
	c.showPage()
	for _, tc := range []struct {
		kind string
		to   mode
	}{{"push", modeSettings}, {"pop", modeBooksHome}, {"rise", modeSettings}, {"fall", modeBooksHome}} {
		before := animFrames
		c.transition(tc.kind, c.fullSheet(), func() { c.setMode(tc.to) })
		if animFrames == before {
			t.Errorf("%s: no frames", tc.kind)
		}
		end := append([]byte(nil), c.s.buf...)
		c.redrawAll()
		if !sameBelowBar(c, end, c.s.buf) {
			t.Errorf("%s: ended off the new screen", tc.kind)
		}
	}
	// A mid-push frame, to look at.
	old, nu := c.animBufs()
	copy(old, c.s.buf)
	c.s.hold = true
	c.setMode(modeSettings)
	c.s.hold = false
	copy(nu, c.s.buf)
	c.pushFrame(old, nu, 0.5, true)
	shot(t, c, "anim-push")
	c.riseFrame(old, nu, c.lookupSheet(), 0.6)
	shot(t, c, "anim-rise")
}

func TestAnimationsAreTimeBoxed(t *testing.T) {
	c := testConsole(t)
	c.cfg.Animations = true
	c.showPage()
	start := time.Now()
	c.transition("push", c.fullSheet(), func() { c.setMode(modeSettings) })
	if d := time.Since(start); d > durPush+300*time.Millisecond {
		t.Errorf("push took %v", d)
	}
	animScale = 0
	defer func() { animScale = 1 }()
	before := animFrames
	c.transition("pop", c.fullSheet(), func() { c.setMode(modeBooksHome) })
	if animFrames != before {
		t.Error("with animations off there should be no frames")
	}
}

func benchFrame(b *testing.B, draw func(c *console, old, nu []byte, t float64)) {
	c := benchConsole(b)
	c.showPage()
	old, nu := c.animBufs()
	copy(old, c.s.buf)
	c.setMode(modeSettings)
	copy(nu, c.s.buf)
	b.SetBytes(int64(len(c.s.buf)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		draw(c, old, nu, float64(i%10)/10)
		c.s.markRows(0, c.s.fbH-1)
		c.s.Flush()
	}
}

func BenchmarkFramePush(b *testing.B) {
	benchFrame(b, func(c *console, old, nu []byte, t float64) { c.pushFrame(old, nu, t, true) })
}
func BenchmarkFrameSlide(b *testing.B) {
	benchFrame(b, func(c *console, old, nu []byte, t float64) { c.slideFrame(old, nu, t, 1) })
}
func BenchmarkFrameCurl(b *testing.B) {
	benchFrame(b, func(c *console, old, nu []byte, t float64) { c.curlFrame(old, nu, t) })
}
func BenchmarkFrameRise(b *testing.B) {
	benchFrame(b, func(c *console, old, nu []byte, t float64) { c.riseFrame(old, nu, c.lookupSheet(), t) })
}

// Off by default: screens change at once, no frames.
func TestAnimationsOffByDefault(t *testing.T) {
	c := testConsole(t)
	c.cfg = loadSettings()
	c.showPage()
	before := animFrames
	c.transition("push", c.fullSheet(), func() { c.setMode(modeSettings) })
	if c.cfg.Animations || animFrames != before {
		t.Error("animations should be off unless turned on")
	}
	c.setPane = "display"
	c.showPage()
	tapButton(t, c, "anim")
	if !c.cfg.Animations {
		t.Error("the switch should turn them on")
	}
}
