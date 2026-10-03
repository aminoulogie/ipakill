package main

import (
	"testing"

	"condor-init/ui"
)

func TestHomeTapOpensAppAndBackReturns(t *testing.T) {
	s := newScreen(nil, 1920, 1200, 7680, 32, bitfield{16, 8, 0}, bitfield{8, 8, 0}, bitfield{0, 8, 0}, Rot90)
	l, err := ui.NewLauncher(s.W, s.H, ui.DefaultApps)
	if err != nil {
		t.Fatal(err)
	}
	h := &home{s: s, l: l}
	h.draw()
	h.tap(600, 430) // middle of the first card (Books)
	if h.open == nil || h.open.ID != "books" {
		t.Fatalf("after tapping Books: open = %v", h.open)
	}
	h.tap(600, 1500) // not the back bar: stays open
	if h.open == nil {
		t.Fatal("tap outside the back bar closed the app")
	}
	h.tap(150, 140) // "< Home"
	if h.open != nil {
		t.Fatalf("after tapping Home: open = %v", h.open)
	}
}
