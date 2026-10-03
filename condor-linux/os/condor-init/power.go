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
		step := 10
		if code == keyVolumeDown {
			step = -10
		}
		c.brightness = min(max(c.brightness+step, 10), 100)
		setBacklight(c.brightness)
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
	c.redrawAll()
	setBacklight(c.brightness)
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
