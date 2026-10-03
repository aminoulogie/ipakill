package ui

import (
	"testing"
	"time"
)

func TestHitMapsCardsToApps(t *testing.T) {
	l, err := NewLauncher(1200, 1920, DefaultApps)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range DefaultApps {
		r := l.rowRect(i)
		got, ok := l.Hit((r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2)
		if !ok || got.ID != want.ID {
			t.Errorf("centre of card %d: got %v %v, want %s", i, got, ok, want.ID)
		}
	}
	if _, ok := l.Hit(10, 10); ok {
		t.Error("status bar should not hit an app")
	}
	if !l.HitBack(100, statusH+10) || l.HitBack(100, 1000) {
		t.Error("back bar hit test wrong")
	}
}

func TestRenderSizesAndInk(t *testing.T) {
	l, err := NewLauncher(1200, 1920, DefaultApps)
	if err != nil {
		t.Fatal(err)
	}
	st := Status{Time: time.Date(2026, 10, 3, 18, 30, 0, 0, time.UTC), Battery: 50}
	home := l.Home(st)
	if home.Rect.Dx() != 1200 || home.Rect.Dy() != 1920 {
		t.Fatalf("home is %v", home.Rect)
	}
	// The title must actually put dark pixels on the screen.
	dark := 0
	for y := titleY - 80; y < titleY; y++ {
		for x := margin; x < margin+400; x++ {
			if c := home.RGBAAt(x, y); c.R < 80 && c.G < 80 && c.B < 80 {
				dark++
			}
		}
	}
	if dark < 500 {
		t.Errorf("title drew only %d dark pixels", dark)
	}
	if a := l.AppScreen(DefaultApps[0], st); a.Rect != home.Rect {
		t.Errorf("app screen is %v", a.Rect)
	}
}

func TestBlitVisitsEveryPixel(t *testing.T) {
	l, _ := NewLauncher(120, 192, DefaultApps)
	img := l.canvas()
	n := 0
	Blit(img, func(x, y int, r, g, b uint8) {
		if r != Background.R || g != Background.G || b != Background.B {
			t.Fatalf("pixel %d,%d = %d,%d,%d", x, y, r, g, b)
		}
		n++
	})
	if n != 120*192 {
		t.Fatalf("visited %d pixels", n)
	}
}
