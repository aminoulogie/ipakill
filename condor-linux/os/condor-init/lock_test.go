package main

import "testing"

// Waking shows the lock screen with the book being read; a tap opens condor where it was.
// With the switch off, waking goes straight back.
func TestLockScreen(t *testing.T) {
	c := readerConsole(t)
	openFirstBook(t, c)
	c.saveProgress()
	c.setScreen(false)
	c.setScreen(true)
	if !c.locked {
		t.Fatal("waking should show the lock screen")
	}
	shot(t, c, "lock-screen")
	page := c.book.page
	pressKey(c, keyVolumeDown) // brightness, not a page turn behind the lock screen
	if c.book.page != page {
		t.Error("the volume keys turned a page behind the lock screen")
	}
	c.unlock()
	if c.locked || c.mode != modeReader {
		t.Fatalf("after the tap: locked %v, mode %v", c.locked, c.mode)
	}
	c.cfg.NoLock = true
	c.setScreen(false)
	c.setScreen(true)
	if c.locked {
		t.Fatal("with the lock screen off, waking should open condor directly")
	}
}
