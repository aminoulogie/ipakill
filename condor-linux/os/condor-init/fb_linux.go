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
	f, ok := s.dev.(*os.File)
	if !ok {
		return
	}
	mode := uintptr(fbBlankUnblank)
	if off {
		mode = 4 // FB_BLANK_POWERDOWN
	}
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), fbioBlank, mode); e != 0 {
		log.Printf("FBIOBLANK %d: %v", mode, e)
	}
}
