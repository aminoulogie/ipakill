package main

import (
	"bytes"
	"testing"
	"time"
)

// Every transition ends exactly on the screen it leads to, after drawing frames.
func TestTransitionsEndOnTheNewScreen(t *testing.T) {
	c := testConsole(t)
	c.showPage()
	for _, tc := range []struct {
		kind string
		to   mode
	}{{"push", modeSettings}, {"pop", modeLauncher}, {"rise", modeSettings}, {"fall", modeLauncher}} {
		before := animFrames
		c.transition(tc.kind, c.fullSheet(), func() { c.setMode(tc.to) })
		if animFrames == before {
			t.Errorf("%s: no frames", tc.kind)
		}
		end := append([]byte(nil), c.s.buf...)
		c.redrawAll()
		if !bytes.Equal(end, c.s.buf) {
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
	c.showPage()
	start := time.Now()
	c.transition("push", c.fullSheet(), func() { c.setMode(modeSettings) })
	if d := time.Since(start); d > durPush+300*time.Millisecond {
		t.Errorf("push took %v", d)
	}
	animScale = 0
	defer func() { animScale = 1 }()
	before := animFrames
	c.transition("pop", c.fullSheet(), func() { c.setMode(modeLauncher) })
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
