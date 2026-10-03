// condor-init: our first userspace program on the Condor TRA-901G.
//
// Started as root by the /system takeover hook (see cli/takeover.go) after Android's zygote
// has been stopped. It unblanks the framebuffer, draws a test pattern, and serves a shell on
// 127.0.0.1:2323, which the PC reaches over USB with:
//
//	adb forward tcp:2323 tcp:2323   then connect to localhost:2323 (e.g. ncat, PuTTY raw)
//
// Build: GOOS=linux GOARCH=386 CGO_ENABLED=0 go build -o condor-init .
package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"syscall"
	"time"
	"unsafe"
)

const (
	fbioGetVScreenInfo = 0x4600
	fbioGetFScreenInfo = 0x4602
	fbioPanDisplay     = 0x4606
	fbioBlank          = 0x4611
	fbBlankUnblank     = 0

	shellAddr = "127.0.0.1:2323"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.Printf("condor-init starting, pid %d", os.Getpid())
	if err := testPattern(); err != nil {
		log.Printf("framebuffer: %v", err)
	}
	serveShell()
}

func ioctl(fd uintptr, req uintptr, arg unsafe.Pointer) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(arg)); e != 0 {
		return e
	}
	return nil
}

// bitfield is struct fb_bitfield: offset, length, msb_right.
type bitfield struct{ offset, length, msbRight uint32 }

func testPattern() error {
	var f *os.File
	var err error
	for _, p := range []string{"/dev/graphics/fb0", "/dev/fb0"} {
		if f, err = os.OpenFile(p, os.O_RDWR, 0); err == nil {
			break
		}
	}
	if err != nil {
		return err
	}
	defer f.Close()
	fd := f.Fd()

	// struct fb_var_screeninfo is 160 bytes; struct fb_fix_screeninfo is 68 bytes on 386.
	var vinfo [160]byte
	var finfo [68]byte
	if err := ioctl(fd, fbioGetVScreenInfo, unsafe.Pointer(&vinfo[0])); err != nil {
		return fmt.Errorf("FBIOGET_VSCREENINFO: %w", err)
	}
	if err := ioctl(fd, fbioGetFScreenInfo, unsafe.Pointer(&finfo[0])); err != nil {
		return fmt.Errorf("FBIOGET_FSCREENINFO: %w", err)
	}
	le := binary.LittleEndian
	xres, yres := int(le.Uint32(vinfo[0:])), int(le.Uint32(vinfo[4:]))
	yvirt := int(le.Uint32(vinfo[12:]))
	bpp := int(le.Uint32(vinfo[24:]))
	bf := func(o int) bitfield {
		return bitfield{le.Uint32(vinfo[o:]), le.Uint32(vinfo[o+4:]), le.Uint32(vinfo[o+8:])}
	}
	red, green, blue := bf(32), bf(44), bf(56)
	id := string(finfo[:16])
	smemLen := int(le.Uint32(finfo[20:]))
	stride := int(le.Uint32(finfo[44:]))
	log.Printf("fb %q: %dx%d (virtual height %d), %d bpp, stride %d, mem %d, r%v g%v b%v",
		id, xres, yres, yvirt, bpp, stride, smemLen, red, green, blue)
	if bpp != 32 && bpp != 16 {
		return fmt.Errorf("unsupported %d bpp", bpp)
	}

	// Unblank: SurfaceFlinger normally does this, and it's gone now.
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, fbioBlank, fbBlankUnblank); e != 0 {
		log.Printf("FBIOBLANK unblank: %v (continuing)", e)
	}

	size := stride * yvirt
	if size > smemLen || size == 0 {
		size = smemLen
	}
	mem, err := syscall.Mmap(int(fd), 0, size, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		return fmt.Errorf("mmap: %w", err)
	}
	defer syscall.Munmap(mem)

	pack := func(r, g, b uint32) uint32 {
		c := func(v uint32, f bitfield) uint32 { return (v >> (8 - f.length)) << f.offset }
		return c(r, red) | c(g, green) | c(b, blue)
	}
	bars := [][3]uint32{{255, 255, 255}, {255, 255, 0}, {0, 255, 255}, {0, 255, 0},
		{255, 0, 255}, {255, 0, 0}, {0, 0, 255}, {0, 0, 0}}
	// Draw every page of the virtual buffer so the pattern shows whichever one is panned in.
	for y := 0; y < yvirt && (y+1)*stride <= len(mem); y++ {
		yy := y % yres
		row := mem[y*stride:]
		for x := 0; x < xres; x++ {
			var r, g, b uint32
			switch {
			case x < 8 || x >= xres-8 || yy < 8 || yy >= yres-8: // white border: shows the visible edges
				r, g, b = 255, 255, 255
			case yy < yres*2/3: // colour bars
				c := bars[x*len(bars)/xres]
				r, g, b = c[0], c[1], c[2]
			default: // grey ramp
				v := uint32(x * 255 / xres)
				r, g, b = v, v, v
			}
			px := pack(r, g, b)
			if bpp == 32 {
				le.PutUint32(row[x*4:], px)
			} else {
				le.PutUint16(row[x*2:], uint16(px))
			}
		}
	}
	// Show page 0.
	le.PutUint32(vinfo[16:], 0) // xoffset
	le.PutUint32(vinfo[20:], 0) // yoffset
	if err := ioctl(fd, fbioPanDisplay, unsafe.Pointer(&vinfo[0])); err != nil {
		log.Printf("FBIOPAN_DISPLAY: %v (continuing)", err)
	}
	log.Printf("test pattern drawn")
	return nil
}

// serveShell gives each connection on 127.0.0.1:2323 a root /system/bin/sh (no pty).
func serveShell() {
	for {
		ln, err := net.Listen("tcp", shellAddr)
		if err != nil {
			log.Printf("listen %s: %v; retrying", shellAddr, err)
			time.Sleep(2 * time.Second)
			continue
		}
		log.Printf("shell listening on %s", shellAddr)
		for {
			c, err := ln.Accept()
			if err != nil {
				log.Printf("accept: %v", err)
				break
			}
			go handle(c)
		}
		ln.Close()
	}
}

func handle(c net.Conn) {
	defer c.Close()
	log.Printf("shell connection from %s", c.RemoteAddr())
	fmt.Fprintf(c, "condor-init shell (pid %d). No Android running. Type 'exit' to close.\n", os.Getpid())
	cmd := exec.Command("/system/bin/sh", "-i")
	cmd.Env = []string{"PATH=/sbin:/system/bin:/system/xbin", "HOME=/data/condor", "PS1=condor# "}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = c, c, c
	if err := cmd.Run(); err != nil && err != io.EOF {
		log.Printf("shell ended: %v", err)
	}
}
