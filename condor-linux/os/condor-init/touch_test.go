package main

import "testing"

type ev struct {
	typ, code uint16
	val       int32
}

func feed(d *touchDecoder, evs ...ev) [][]TouchPoint {
	var frames [][]TouchPoint
	for _, e := range evs {
		if f := d.event(e.typ, e.code, e.val); f != nil || (e.typ == evSyn && e.code == synReport) {
			frames = append(frames, f)
		}
	}
	return frames
}

func TestTouchDownMoveUp(t *testing.T) {
	// Goodix on the TRA-901G: X 0..1920, Y 0..1200 in native framebuffer orientation.
	d := newTouchDecoder(axisRange{0, 1920}, axisRange{0, 1200}, fbW, fbH, Rot90)
	frames := feed(d,
		ev{evAbs, absMTSlot, 0}, ev{evAbs, absMTTrackingID, 7},
		ev{evAbs, absMTPositionX, 0}, ev{evAbs, absMTPositionY, 1200}, ev{evSyn, synReport, 0},
		ev{evAbs, absMTPositionX, 960}, ev{evSyn, synReport, 0},
		ev{evAbs, absMTTrackingID, -1}, ev{evSyn, synReport, 0},
	)
	if len(frames) != 3 {
		t.Fatalf("got %d frames", len(frames))
	}
	down := frames[0][0]
	// native (0, 1199) = framebuffer bottom-left = logical top-left under Rot90
	if !down.Down || down.X != 0 || down.Y != 0 {
		t.Fatalf("down: %+v", down)
	}
	mv := frames[1][0]
	if !mv.Moved || mv.PrevX != 0 || mv.PrevY != 0 || mv.X != 0 || mv.Y != 959 {
		t.Fatalf("move: %+v", mv)
	}
	up := frames[2][0]
	if !up.Up || up.X != 0 || up.Y != 959 {
		t.Fatalf("up: %+v", up)
	}
}

func TestTouchTwoFingers(t *testing.T) {
	d := newTouchDecoder(axisRange{0, 1920}, axisRange{0, 1200}, fbW, fbH, Rot0)
	frames := feed(d,
		ev{evAbs, absMTSlot, 0}, ev{evAbs, absMTTrackingID, 1}, ev{evAbs, absMTPositionX, 100}, ev{evAbs, absMTPositionY, 100},
		ev{evAbs, absMTSlot, 1}, ev{evAbs, absMTTrackingID, 2}, ev{evAbs, absMTPositionX, 500}, ev{evAbs, absMTPositionY, 600},
		ev{evSyn, synReport, 0},
		ev{evAbs, absMTSlot, 1}, ev{evAbs, absMTPositionY, 650}, ev{evSyn, synReport, 0}, // only finger 1 moves
	)
	if len(frames) != 2 || len(frames[0]) != 2 || len(frames[1]) != 1 || frames[1][0].Slot != 1 {
		t.Fatalf("frames %+v", frames)
	}
}

func TestAxisScaleClamps(t *testing.T) {
	a := axisRange{0, 1920}
	if a.scale(-5, 1920) != 0 || a.scale(5000, 1920) != 1919 || a.scale(1920, 1920) != 1919 {
		t.Fatal("scale must clamp to the screen")
	}
}
