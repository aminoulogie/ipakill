package main

import (
	"image"
	"testing"
)

func testKeyboard(t *testing.T) (*keyboard, *[]string, *[]bool) {
	s := newScreen(nil, 1920, 1200, 7680, 32, bitfield{16, 8, 0}, bitfield{8, 8, 0}, bitfield{0, 8, 0}, Rot90)
	var sent []string
	var shown []bool
	kb, err := newKeyboard(s, func(b []byte) { sent = append(sent, string(b)) }, func(v bool) { shown = append(shown, v) })
	if err != nil {
		t.Fatal(err)
	}
	return kb, &sent, &shown
}

// tap presses and lifts a finger on the key with this label in the current layer.
func tap(t *testing.T, kb *keyboard, label string) {
	t.Helper()
	for _, keys := range kb.layers[kb.layer] {
		for _, key := range keys {
			if key.label == label {
				c := key.r.Min.Add(key.r.Max).Div(2).Add(image.Pt(0, kb.y0))
				kb.touch(TouchPoint{Slot: 0, X: c.X, Y: c.Y, Down: true})
				kb.touch(TouchPoint{Slot: 0, X: c.X, Y: c.Y, Up: true})
				return
			}
		}
	}
	t.Fatalf("no key %q in layer %d", label, kb.layer)
}

func TestKeyboardRowsFitTheScreen(t *testing.T) {
	kb, _, _ := testKeyboard(t)
	for li, layer := range kb.layers {
		if len(layer) != kbRows {
			t.Fatalf("layer %d has %d rows", li, len(layer))
		}
		for ri, keys := range layer {
			w := 0.0
			for _, k := range keys {
				w += k.w
			}
			if w != 10 {
				t.Errorf("layer %d row %d is %.2f units wide, want 10", li, ri, w)
			}
			last := keys[len(keys)-1].r
			if last.Max.X > kb.s.W-kbPad {
				t.Errorf("layer %d row %d overflows: %v", li, ri, last)
			}
		}
	}
	if kb.y0+kbHeight != kb.s.H {
		t.Errorf("keyboard spans %d..%d, screen is %d", kb.y0, kb.y0+kbHeight, kb.s.H)
	}
}

func TestKeyboardTypes(t *testing.T) {
	kb, sent, shown := testKeyboard(t)
	tap(t, kb, "l")
	tap(t, kb, "s")
	tap(t, kb, "enter")
	tap(t, kb, "shift")
	tap(t, kb, "a") // shifted once, then shift is released
	tap(t, kb, "a")
	tap(t, kb, "ctrl")
	tap(t, kb, "c")
	tap(t, kb, "↑")
	tap(t, kb, "bksp")
	tap(t, kb, "sym")
	tap(t, kb, "|")
	tap(t, kb, "sym") // label stays "sym" in the key list; drawn as "abc"
	tap(t, kb, "alt")
	tap(t, kb, "b")
	want := []string{"l", "s", "\r", "A", "a", "\x03", "\x1b[A", "\x7f", "|", "\x1bb"}
	if len(*sent) != len(want) {
		t.Fatalf("sent %q, want %q", *sent, want)
	}
	for i := range want {
		if (*sent)[i] != want[i] {
			t.Fatalf("key %d: sent %q, want %q (all: %q)", i, (*sent)[i], want[i], *sent)
		}
	}
	tap(t, kb, "hide")
	if kb.visible || len(*shown) != 1 || (*shown)[0] {
		t.Fatalf("hide: visible=%v shown=%v", kb.visible, *shown)
	}
	kb.touch(TouchPoint{Slot: 0, X: 600, Y: 300, Down: true})
	kb.touch(TouchPoint{Slot: 0, X: 600, Y: 300, Up: true})
	if !kb.visible || len(*shown) != 2 || !(*shown)[1] {
		t.Fatalf("tap to show: visible=%v shown=%v", kb.visible, *shown)
	}
}

func TestKeyboardCommitsWhereTheFingerLifts(t *testing.T) {
	kb, sent, _ := testKeyboard(t)
	var q, w *kbKey
	for _, k := range kb.layers[0][2] {
		switch k.label {
		case "q":
			q = k
		case "w":
			w = k
		}
	}
	cq := q.r.Min.Add(q.r.Max).Div(2).Add(image.Pt(0, kb.y0))
	cw := w.r.Min.Add(w.r.Max).Div(2).Add(image.Pt(0, kb.y0))
	kb.touch(TouchPoint{Slot: 1, X: cq.X, Y: cq.Y, Down: true})
	kb.touch(TouchPoint{Slot: 1, X: cw.X, Y: cw.Y, Moved: true})
	kb.touch(TouchPoint{Slot: 1, X: cw.X, Y: cw.Y, Up: true})
	if len(*sent) != 1 || (*sent)[0] != "w" {
		t.Fatalf("sent %q, want [w]", *sent)
	}
}
