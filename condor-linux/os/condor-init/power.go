package main

import (
	"log"
	"time"
)

// Hardware keys: the power button (mid_powerbtn) turns the screen off and on; the volume
// keys (gpio-keys) change the brightness. Real suspend is unreliable on Intel MID kernels,
// so "off" means backlight off and the panel blanked; Wi-Fi and SSH keep working.

const (
	keyVolumeDown = 114
	keyVolumeUp   = 115
	keyPower      = 116
)

// key handles one key event (value 1 = press, 2 = auto-repeat, 0 = release).
func (c *console) key(code uint16, value int32) {
	if value == 0 {
		return
	}
	drawMu.Lock()
	defer drawMu.Unlock()
	c.lastInput = time.Now()
	switch code {
	case keyPower:
		if value != 1 {
			return
		}
		c.setScreen(!c.screenOn)
	case keyVolumeUp, keyVolumeDown:
		if !c.screenOn {
			return
		}
		if c.mode == modeReader && c.book != nil { // in a book the volume keys turn pages (or lines)
			if c.rd.view == "" && !c.overlayOpen() {
				if code == keyVolumeDown {
					c.readerTap("next")
				} else {
					c.readerTap("prev")
				}
			}
			return
		}
		step := 10
		if code == keyVolumeDown {
			step = -10
		}
		c.cfg.Brightness = min(max(c.cfg.Brightness+step, 10), 100)
		setBacklight(c.cfg.Brightness)
		c.cfg.save()
		if c.mode == modeSettings {
			c.showPage()
		}
	}
}

// setScreen turns the display off (backlight 0, panel blanked) or back on, redrawn.
// Caller holds drawMu.
func (c *console) setScreen(on bool) {
	if on == c.screenOn {
		return
	}
	c.screenOn = on
	if !on {
		setBacklight(0)
		blankScreen(c.s, true)
		log.Printf("screen off")
		return
	}
	blankScreen(c.s, false)
	c.lastInput = time.Now()
	c.redrawAll()
	setBacklight(c.cfg.Brightness)
	log.Printf("screen on")
}

// keysLoop reads one input device's keys. It doesn't return.
func (c *console) keysLoop(device string) {
	for {
		err := readKeys(device, c.key)
		log.Printf("keys %s: %v; retrying in 5s", device, err)
		time.Sleep(5 * time.Second)
	}
}

// idleLoop turns the screen off after the configured minutes without a touch or key.
// It doesn't return.
func (c *console) idleLoop() {
	for range time.Tick(5 * time.Second) {
		drawMu.Lock()
		if c.screenOn && c.cfg.ScreenOff > 0 && time.Since(c.lastInput) > time.Duration(c.cfg.ScreenOff)*time.Minute {
			log.Printf("idle %d min: screen off", c.cfg.ScreenOff)
			c.setScreen(false)
		}
		drawMu.Unlock()
	}
}
