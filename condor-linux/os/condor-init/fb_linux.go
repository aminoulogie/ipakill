//go:build linux

package main

import (
	"encoding/binary"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

const (
	fbioGetVScreenInfo = 0x4600
	fbioGetFScreenInfo = 0x4602
	fbioBlank          = 0x4611
	fbBlankUnblank     = 0

	backlightDir = "/sys/class/backlight/psb-bl"
)

func ioctl(fd uintptr, req uintptr, arg unsafe.Pointer) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(arg)); e != 0 {
		return e
	}
	return nil
}

// openScreen opens the framebuffer, unblanks it, and wraps it in the logical (rotated)
// Screen, which draws through write() (see Screen for why not mmap). The device stays open
// for the life of the process.
func openScreen(rot Rotation) (*Screen, error) {
	var f *os.File
	var err error
	for _, p := range []string{"/dev/graphics/fb0", "/dev/fb0"} {
		if f, err = os.OpenFile(p, os.O_RDWR, 0); err == nil {
			break
		}
	}
	if err != nil {
		return nil, err
	}
	fd := f.Fd()

	// struct fb_var_screeninfo is 160 bytes; struct fb_fix_screeninfo is 68 bytes on 386.
	var vinfo [160]byte
	var finfo [68]byte
	if err := ioctl(fd, fbioGetVScreenInfo, unsafe.Pointer(&vinfo[0])); err != nil {
		return nil, fmt.Errorf("FBIOGET_VSCREENINFO: %w", err)
	}
	if err := ioctl(fd, fbioGetFScreenInfo, unsafe.Pointer(&finfo[0])); err != nil {
		return nil, fmt.Errorf("FBIOGET_FSCREENINFO: %w", err)
	}
	le := binary.LittleEndian
	fbW, fbH := int(le.Uint32(vinfo[0:])), int(le.Uint32(vinfo[4:]))
	bpp := int(le.Uint32(vinfo[24:]))
	bf := func(o int) bitfield {
		return bitfield{le.Uint32(vinfo[o:]), le.Uint32(vinfo[o+4:]), le.Uint32(vinfo[o+8:])}
	}
	smemLen := int(le.Uint32(finfo[20:]))
	stride := int(le.Uint32(finfo[44:]))
	log.Printf("fb: native %dx%d, %d bpp, stride %d, mem %d, fb rotate %d; using rotation %d",
		fbW, fbH, bpp, stride, smemLen, le.Uint32(vinfo[136:]), rot)
	if bpp != 32 && bpp != 16 {
		return nil, fmt.Errorf("unsupported %d bpp", bpp)
	}
	if stride*fbH > smemLen {
		return nil, fmt.Errorf("stride %d x %d rows exceeds framebuffer memory %d", stride, fbH, smemLen)
	}
	// Unblank: SurfaceFlinger normally does this, and it's gone now.
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, fbioBlank, fbBlankUnblank); e != 0 {
		log.Printf("FBIOBLANK unblank: %v (continuing)", e)
	}
	s := newScreen(f, fbW, fbH, stride, bpp, bf(32), bf(44), bf(56), rot)
	log.Printf("screen: logical %dx%d", s.W, s.H)
	return s, nil
}

// setBacklight sets the panel backlight to percent of its maximum.
func setBacklight(percent int) {
	max := 100
	if b, err := os.ReadFile(backlightDir + "/max_brightness"); err == nil {
		if v, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && v > 0 {
			max = v
		}
	}
	v := max * percent / 100
	if err := os.WriteFile(backlightDir+"/brightness", []byte(strconv.Itoa(v)), 0); err != nil {
		log.Printf("backlight: %v", err)
		return
	}
	log.Printf("backlight %d/%d", v, max)
}

// blankScreen powers the panel down (FB_BLANK_POWERDOWN) or back up.
func blankScreen(s *Screen, off bool) {
	f, ok := s.fbDev.(*os.File)
	if !ok || s.dev != s.fbDev {
		return // SurfaceFlinger drives the panel while the GPU shows the screen: backlight only
	}
	mode := uintptr(fbBlankUnblank)
	if off {
		mode = 4 // FB_BLANK_POWERDOWN
	}
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), fbioBlank, mode); e != 0 {
		log.Printf("FBIOBLANK %d: %v", mode, e)
	}
}

const fbioWaitForVsync = 0x40044620 // _IOW('F', 0x20, __u32)

var vsyncBroken bool

// waitVsync waits for the panel's next vertical blank, so an animation frame lands whole.
// Drivers without FBIO_WAITFORVSYNC say so once; frames are then paced by the clock.
func waitVsync(s *Screen) bool {
	f, ok := s.fbDev.(*os.File)
	if !ok || vsyncBroken {
		return false
	}
	var crtc uint32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), fbioWaitForVsync, uintptr(unsafe.Pointer(&crtc))); e != 0 {
		log.Printf("FBIO_WAITFORVSYNC: %v (frames paced by the clock)", e)
		vsyncBroken = true
		return false
	}
	return true
}

// refreshHz is the panel's refresh rate from its timings (0 if the driver doesn't say).
func refreshHz(s *Screen) float64 {
	f, ok := s.fbDev.(*os.File)
	if !ok {
		return 0
	}
	var v [40]uint32 // struct fb_var_screeninfo
	if ioctl(f.Fd(), fbioGetVScreenInfo, unsafe.Pointer(&v[0])) != nil || v[25] == 0 {
		return 0
	}
	htotal := float64(v[0] + v[26] + v[27] + v[30]) // xres + left + right + hsync
	vtotal := float64(v[1] + v[28] + v[29] + v[31]) // yres + upper + lower + vsync
	return 1e12 / float64(v[25]) / htotal / vtotal  // pixclock is in picoseconds
}
