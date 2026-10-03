package main

import (
	"log"
	"time"

	"condor-init/ui"
)

// displayZone is the tablet owner's time zone (Africa/Lagos, WAT = UTC+1, no DST). Android's
// zone database isn't available to us, so it's fixed here.
var displayZone = time.FixedZone("WAT", 3600)

// home is the simple shell: a home screen with the app list, and one screen per app.
type home struct {
	s    *Screen
	l    *ui.Launcher
	open *ui.App // nil on the home screen
}

func status() ui.Status {
	pct, charging := ui.ReadBattery()
	return ui.Status{Time: time.Now().In(displayZone), Battery: pct, Charging: charging}
}

// draw renders the current screen and writes it out. Callers hold drawMu.
func (h *home) draw() {
	start := time.Now()
	img := h.l.Home(status())
	if h.open != nil {
		img = h.l.AppScreen(*h.open, status())
	}
	ui.Blit(img, h.s.Set)
	if err := h.s.Flush(); err != nil {
		log.Printf("flush: %v", err)
	}
	log.Printf("drew %s in %v", h.name(), time.Since(start).Round(time.Millisecond))
}

func (h *home) name() string {
	if h.open == nil {
		return "home"
	}
	return h.open.ID
}

// tap handles a finger touching down at logical (x, y). Callers hold drawMu.
func (h *home) tap(x, y int) {
	if h.open == nil {
		if a, ok := h.l.Hit(x, y); ok {
			log.Printf("open %v", a)
			h.open = &a
			h.draw()
		}
		return
	}
	if h.l.HitBack(x, y) {
		log.Printf("back to home from %s", h.open.ID)
		h.open = nil
		h.draw()
	}
}

// runHome shows the home screen, redraws it every minute (clock, battery) and handles taps.
// It doesn't return.
func runHome(s *Screen) {
	l, err := ui.NewLauncher(s.W, s.H, ui.DefaultApps)
	if err != nil {
		log.Printf("launcher: %v; falling back to the touch test", err)
		drawTouchScreen(s)
		touchLoop(s)
		return
	}
	h := &home{s: s, l: l}
	drawMu.Lock()
	h.draw()
	drawMu.Unlock()

	go func() {
		for range time.Tick(time.Minute) {
			drawMu.Lock()
			h.draw()
			drawMu.Unlock()
		}
	}()

	for {
		err := readTouch("Goodix", s.fbW, s.fbH, s.rot, log.Printf, func(pts []TouchPoint) {
			for _, p := range pts {
				if p.Down {
					drawMu.Lock()
					h.tap(p.X, p.Y)
					drawMu.Unlock()
					return // one action per frame, even with several fingers down
				}
			}
		})
		log.Printf("touch: %v; retrying in 2s", err)
		time.Sleep(2 * time.Second)
	}
}
