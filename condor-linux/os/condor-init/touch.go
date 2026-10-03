package main

// Multitouch protocol B decoding (Documentation/input/multi-touch-protocol.txt): the Goodix
// driver reports ABS_MT_SLOT to pick a finger, ABS_MT_TRACKING_ID (-1 = lifted) and
// ABS_MT_POSITION_X/Y for it, then SYN_REPORT closes the frame.
const (
	evSyn     = 0x00
	evAbs     = 0x03
	synReport = 0

	absMTSlot       = 0x2f
	absMTPositionX  = 0x35
	absMTPositionY  = 0x36
	absMTTrackingID = 0x39

	maxSlots = 10
)

// axisRange is the [min, max] the driver reports for a touch axis (EVIOCGABS).
type axisRange struct{ min, max int32 }

// scale maps a raw axis value onto 0..size-1.
func (a axisRange) scale(v int32, size int) int {
	if a.max <= a.min {
		return int(v)
	}
	p := int(int64(v-a.min) * int64(size-1) / int64(a.max-a.min))
	return min(max(p, 0), size-1)
}

// TouchPoint is one finger in a frame, in logical screen coordinates.
type TouchPoint struct {
	Slot     int
	X, Y     int   // logical
	RawX     int32 // as reported by the driver, for calibration logs
	RawY     int32
	Down     bool // finger touched down in this frame
	Up       bool // finger lifted in this frame (X, Y are its last position)
	Moved    bool
	PrevX    int // logical position in the previous frame (valid when Moved)
	PrevY    int
	tracking bool
}

// touchDecoder turns raw input events into per-frame finger states.
type touchDecoder struct {
	xr, yr   axisRange
	fbW, fbH int
	rot      Rotation
	slot     int
	fingers  [maxSlots]TouchPoint
	changed  [maxSlots]bool
}

func newTouchDecoder(xr, yr axisRange, fbW, fbH int, rot Rotation) *touchDecoder {
	d := &touchDecoder{xr: xr, yr: yr, fbW: fbW, fbH: fbH, rot: rot}
	for i := range d.fingers {
		d.fingers[i].Slot = i
	}
	return d
}

// event feeds one input event. At SYN_REPORT it returns the fingers that changed in the
// frame (touched down, moved or lifted); otherwise nil.
func (d *touchDecoder) event(typ, code uint16, val int32) []TouchPoint {
	switch {
	case typ == evAbs && code == absMTSlot:
		if val >= 0 && val < maxSlots {
			d.slot = int(val)
		}
	case typ == evAbs && code == absMTTrackingID:
		f := &d.fingers[d.slot]
		if val < 0 {
			if f.tracking {
				f.Up, f.tracking = true, false
				d.changed[d.slot] = true
			}
		} else if !f.tracking {
			f.Down, f.tracking = true, true
			d.changed[d.slot] = true
		}
	case typ == evAbs && code == absMTPositionX:
		d.fingers[d.slot].RawX = val
		d.changed[d.slot] = true
	case typ == evAbs && code == absMTPositionY:
		d.fingers[d.slot].RawY = val
		d.changed[d.slot] = true
	case typ == evSyn && code == synReport:
		return d.frame()
	}
	return nil
}

func (d *touchDecoder) frame() []TouchPoint {
	var out []TouchPoint
	for i := range d.fingers {
		if !d.changed[i] {
			continue
		}
		d.changed[i] = false
		f := &d.fingers[i]
		x, y := d.rot.fromFB(d.xr.scale(f.RawX, d.fbW), d.yr.scale(f.RawY, d.fbH), d.fbW, d.fbH)
		f.Moved = !f.Down && !f.Up && (x != f.X || y != f.Y)
		f.PrevX, f.PrevY = f.X, f.Y
		if !f.Up {
			f.X, f.Y = x, y
		}
		if f.Down || f.Up || f.Moved {
			out = append(out, *f)
		}
		f.Down, f.Up = false, false
	}
	return out
}
