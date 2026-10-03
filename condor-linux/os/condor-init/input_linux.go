//go:build linux

package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"
)

// eviocgabs is EVIOCGABS(abs) = _IOR('E', 0x40 + abs, struct input_absinfo), 24 bytes.
func eviocgabs(abs uint16) uintptr { return 0x80184540 + uintptr(abs) }

// findInput returns the /dev/input/eventN whose device name contains name.
func findInput(name string) (string, error) {
	paths, _ := filepath.Glob("/sys/class/input/event*/device/name")
	for _, p := range paths {
		if b, err := os.ReadFile(p); err == nil && strings.Contains(string(b), name) {
			return "/dev/input/" + filepath.Base(filepath.Dir(filepath.Dir(p))), nil
		}
	}
	return "", fmt.Errorf("no input device named %q", name)
}

// absRange reads an axis' min/max (struct input_absinfo: value, minimum, maximum, ...).
func absRange(f *os.File, abs uint16) (axisRange, error) {
	var info [24]byte
	if err := ioctl(f.Fd(), eviocgabs(abs), unsafe.Pointer(&info[0])); err != nil {
		return axisRange{}, err
	}
	le := binary.LittleEndian
	return axisRange{int32(le.Uint32(info[4:])), int32(le.Uint32(info[8:]))}, nil
}

// readTouch opens the touchscreen and calls handle for every frame of finger changes.
// It returns only on error.
func readTouch(name string, fbW, fbH int, rot Rotation, logf func(string, ...any), handle func([]TouchPoint)) error {
	path, err := findInput(name)
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	xr, err := absRange(f, absMTPositionX)
	if err != nil {
		return fmt.Errorf("EVIOCGABS X: %w", err)
	}
	yr, err := absRange(f, absMTPositionY)
	if err != nil {
		return fmt.Errorf("EVIOCGABS Y: %w", err)
	}
	logf("touch: %s, x %d..%d, y %d..%d, mapped to native %dx%d then rotation %d", path, xr.min, xr.max, yr.min, yr.max, fbW, fbH, rot)
	d := newTouchDecoder(xr, yr, fbW, fbH, rot)
	// struct input_event on 32-bit x86: timeval (2 x int32), type u16, code u16, value s32.
	const evSize = 16
	buf := make([]byte, evSize*64)
	le := binary.LittleEndian
	for {
		n, err := f.Read(buf)
		if err != nil {
			return err
		}
		for o := 0; o+evSize <= n; o += evSize {
			e := buf[o : o+evSize]
			if pts := d.event(le.Uint16(e[8:]), le.Uint16(e[10:]), int32(le.Uint32(e[12:]))); len(pts) > 0 {
				handle(pts)
			}
		}
	}
}

// readKeys reports every key event (EV_KEY) from the named input device.
func readKeys(name string, handle func(code uint16, value int32)) error {
	path, err := findInput(name)
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	const evSize, evKey = 16, 1 // struct input_event on 32-bit x86
	buf := make([]byte, evSize*16)
	le := binary.LittleEndian
	for {
		n, err := f.Read(buf)
		if err != nil {
			return err
		}
		for o := 0; o+evSize <= n; o += evSize {
			e := buf[o : o+evSize]
			if le.Uint16(e[8:]) == evKey {
				handle(le.Uint16(e[10:]), int32(le.Uint32(e[12:])))
			}
		}
	}
}
