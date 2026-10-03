package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Intel MID boot image (boot / recovery / droidboot), as stored in an OSIP slot on the eMMC:
//
//	0x0000  signature block, 480 bytes (zeros + Intel manifest + RSA signature)
//	0x01E0  boot header, 4096 bytes: cmdline[1024], bzImage size u32, initrd size u32,
//	        SPI UART suppression u32, SPI type u32, padding
//	0x11E0  bootstub (Intel's 32-bit stub that sets up boot_params and jumps to the kernel)
//	        bzImage
//	        initrd (gzip'd cpio)
//	        padding up to the OSIP slot size
//
// The firmware loads everything after the signature block at the OSII load address
// (0x01100000 here) and jumps to load+0x1000, i.e. the bootstub.
//
// Files made for Intel's flash tool and 'fastboot flash' (e.g. RAMOSI9-boot.PV.bin) carry
// one extra 512-byte OSIP sector in front, describing the single image that follows.
const (
	sigSize     = 0x1E0
	hdrSize     = 0x1000
	cmdlineSize = 1024
)

type bootImg struct {
	osip, sig, hdr, stub, kernel, ramdisk, tail []byte
	cmdline                                     string
}

func parseBootImg(b []byte) (*bootImg, error) {
	im := &bootImg{}
	if len(b) >= 512 && string(b[:4]) == osipMagic {
		im.osip, b = b[:512], b[512:]
	}
	if len(b) < sigSize+hdrSize+512 {
		return nil, fmt.Errorf("too small for an Intel boot image (%d bytes)", len(b))
	}
	im.sig, im.hdr = b[:sigSize], b[sigSize:sigSize+hdrSize]
	cl := im.hdr[:cmdlineSize]
	if i := bytes.IndexByte(cl, 0); i >= 0 {
		cl = cl[:i]
	}
	im.cmdline = string(cl)
	kSize := int(binary.LittleEndian.Uint32(im.hdr[cmdlineSize:]))
	rSize := int(binary.LittleEndian.Uint32(im.hdr[cmdlineSize+4:]))
	rest := b[sigSize+hdrSize:]
	// The bootstub is 4 KB on Medfield and 8 KB on some later trees: find the bzImage's
	// "HdrS" magic (offset 0x202 of the setup header) to tell.
	stub := -1
	for off := 0x1000; off <= 0x10000 && off+0x206 <= len(rest); off += 0x1000 {
		if string(rest[off+0x202:off+0x206]) == "HdrS" {
			stub = off
			break
		}
	}
	if stub < 0 {
		return nil, fmt.Errorf("no bzImage (HdrS) found after the boot header; not an Intel boot image?")
	}
	if kSize <= 0 || stub+kSize+rSize > len(rest) {
		return nil, fmt.Errorf("header sizes (kernel %d, ramdisk %d) don't fit in %d bytes", kSize, rSize, len(rest))
	}
	im.stub = rest[:stub]
	im.kernel = rest[stub : stub+kSize]
	im.ramdisk = rest[stub+kSize : stub+kSize+rSize]
	im.tail = rest[stub+kSize+rSize:]
	return im, nil
}

// payload is what the signature covers (everything after the signature block).
func (im *bootImg) payloadHash() string {
	h := sha256.New()
	for _, p := range [][]byte{im.hdr, im.stub, im.kernel, im.ramdisk} {
		h.Write(p)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (im *bootImg) bytes() []byte {
	var out bytes.Buffer
	for _, p := range [][]byte{im.osip, im.sig, im.hdr, im.stub, im.kernel, im.ramdisk, im.tail} {
		out.Write(p)
	}
	return out.Bytes()
}

// fixOSIP updates the single-entry OSIP sector of a flash file to the image's new size
// and recomputes its checksum (the XOR of all header bytes must be 0).
func (im *bootImg) fixOSIP() {
	if len(im.osip) == 0 {
		return
	}
	n := len(im.sig) + len(im.hdr) + len(im.stub) + len(im.kernel) + len(im.ramdisk) + len(im.tail)
	binary.LittleEndian.PutUint32(im.osip[osiiOffset+16:], uint32(n/512))
	im.osip[7] = 0
	var x byte
	for _, c := range im.osip[:binary.LittleEndian.Uint16(im.osip[10:])] {
		x ^= c
	}
	im.osip[7] = x
}

func (im *bootImg) describe() string {
	var s strings.Builder
	if len(im.osip) > 0 {
		e, _ := parseOSIP(im.osip)
		if len(e) > 0 {
			fmt.Fprintf(&s, "OSIP sector flash-file header: %s image, attribute %#x, %d blocks\n", e[0].name(), e[0].attr, e[0].blocks)
		}
	}
	fmt.Fprintf(&s, "signature   %d bytes, signed: %v\n", len(im.sig), !allZero(im.sig))
	fmt.Fprintf(&s, "cmdline     %s\n", im.cmdline)
	fmt.Fprintf(&s, "SPI fields  uart-suppression=%d type=%d\n",
		binary.LittleEndian.Uint32(im.hdr[cmdlineSize+8:]), binary.LittleEndian.Uint32(im.hdr[cmdlineSize+12:]))
	fmt.Fprintf(&s, "bootstub    %d bytes\n", len(im.stub))
	fmt.Fprintf(&s, "kernel      %d bytes (bzImage, protocol %d.%02d)\n", len(im.kernel), im.kernel[0x207], im.kernel[0x206])
	kind := "unknown format"
	if bytes.HasPrefix(im.ramdisk, []byte{0x1f, 0x8b}) {
		kind = "gzip"
	}
	fmt.Fprintf(&s, "ramdisk     %d bytes (%s)\n", len(im.ramdisk), kind)
	fmt.Fprintf(&s, "padding     %d bytes after the ramdisk (all zero: %v)\n", len(im.tail), allZero(im.tail))
	fmt.Fprintf(&s, "payload     sha256 %s\n", im.payloadHash())
	return s.String()
}

func allZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}

func bootimg(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: condor bootimg info <img> | unpack <img> [dir] | pack <dir> <out.img>")
	}
	switch args[0] {
	case "info":
		b, err := os.ReadFile(args[1])
		if err != nil {
			return err
		}
		im, err := parseBootImg(b)
		if err != nil {
			return err
		}
		fmt.Print(im.describe())
		return nil
	case "unpack":
		dir := strings.TrimSuffix(args[1], filepath.Ext(args[1]))
		if len(args) > 2 {
			dir = args[2]
		}
		return bootimgUnpack(args[1], dir)
	case "pack":
		if len(args) < 3 {
			return fmt.Errorf("usage: condor bootimg pack <dir> <out.img>")
		}
		return bootimgPack(args[1], args[2])
	}
	return fmt.Errorf("unknown bootimg action %q (info, unpack, pack)", args[0])
}

// Files in an unpacked boot image folder. cmdline.txt, kernel and ramdisk.cpio.gz are the
// ones meant to be edited; the others are copied back byte for byte.
var bootFiles = []struct{ name, what string }{
	{"osip.bin", "512-byte OSIP sector of a flash file (absent for eMMC dumps)"},
	{"signature.bin", "480-byte signature block, copied as is"},
	{"header.bin", "4 KB boot header; pack rewrites the cmdline and sizes in it"},
	{"cmdline.txt", "kernel command line"},
	{"bootstub.bin", "Intel bootstub"},
	{"kernel", "bzImage"},
	{"ramdisk.cpio.gz", "initramfs"},
	{"padding.bin", "bytes after the ramdisk up to the slot size"},
}

func bootimgUnpack(src, dir string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	im, err := parseBootImg(b)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data := [][]byte{im.osip, im.sig, im.hdr, []byte(strings.TrimRight(im.cmdline, "\r\n") + "\n"), im.stub, im.kernel, im.ramdisk, im.tail}
	for i, f := range bootFiles {
		os.Remove(filepath.Join(dir, f.name)) // don't leave a stale osip.bin from an earlier unpack
		if len(data[i]) == 0 && f.name == "osip.bin" {
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, f.name), data[i], 0o644); err != nil {
			return err
		}
	}
	info := im.describe()
	os.WriteFile(filepath.Join(dir, "bootimg.txt"), []byte("source      "+src+"\n"+info), 0o644)
	fmt.Print(info)
	fmt.Println("unpacked to", dir)
	return nil
}

func bootimgPack(dir, out string) error {
	read := func(name string) ([]byte, error) { return os.ReadFile(filepath.Join(dir, name)) }
	im := &bootImg{}
	var err error
	for _, f := range []struct {
		name string
		dst  *[]byte
	}{{"signature.bin", &im.sig}, {"header.bin", &im.hdr}, {"bootstub.bin", &im.stub}, {"kernel", &im.kernel}, {"ramdisk.cpio.gz", &im.ramdisk}} {
		if *f.dst, err = read(f.name); err != nil {
			return err
		}
	}
	if len(im.sig) != sigSize || len(im.hdr) != hdrSize {
		return fmt.Errorf("signature.bin must be %d bytes and header.bin %d bytes", sigSize, hdrSize)
	}
	cl, err := read("cmdline.txt")
	if err != nil {
		return err
	}
	im.cmdline = strings.TrimRight(string(cl), "\r\n")
	if len(im.cmdline) >= cmdlineSize {
		return fmt.Errorf("cmdline is %d bytes, the limit is %d", len(im.cmdline), cmdlineSize-1)
	}
	// Leave the header untouched when the cmdline didn't change, so unchanged repacks are
	// byte-identical even if the original has bytes after the cmdline's terminating NUL.
	old := im.hdr[:cmdlineSize]
	// (Condor's cmdline ends in "\n" before the NUL; compare without trailing newlines.)
	if i := bytes.IndexByte(old, 0); i < 0 || strings.TrimRight(string(old[:i]), "\r\n") != im.cmdline {
		copy(old, make([]byte, cmdlineSize))
		copy(old, im.cmdline)
	}
	binary.LittleEndian.PutUint32(im.hdr[cmdlineSize:], uint32(len(im.kernel)))
	binary.LittleEndian.PutUint32(im.hdr[cmdlineSize+4:], uint32(len(im.ramdisk)))
	im.tail, _ = read("padding.bin")
	if n := (sigSize + hdrSize + len(im.stub) + len(im.kernel) + len(im.ramdisk) + len(im.tail)) % 512; n != 0 {
		im.tail = append(im.tail, make([]byte, 512-n)...) // OSIP sizes are in 512-byte blocks
	}
	if im.osip, err = read("osip.bin"); err == nil {
		if len(im.osip) != 512 {
			return fmt.Errorf("osip.bin must be 512 bytes")
		}
		im.fixOSIP()
	}
	if err := os.WriteFile(out, im.bytes(), 0o644); err != nil {
		return err
	}
	fmt.Print(im.describe())
	fmt.Println("packed", out)
	if info, err := read("bootimg.txt"); err == nil {
		if strings.Contains(string(info), im.payloadHash()) {
			fmt.Println("payload unchanged: the original signature still matches it.")
		} else {
			fmt.Println("payload CHANGED: the signature block no longer matches. This only boots if the")
			fmt.Println("firmware/bootloader doesn't enforce signatures (test with 'fastboot boot' first).")
		}
	}
	return nil
}
