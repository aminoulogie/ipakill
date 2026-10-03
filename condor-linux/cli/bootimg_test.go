package main

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// fakeBootImg builds an Intel-layout image: signature, header, 4 KB stub, bzImage, gzip ramdisk.
func fakeBootImg(withOSIP bool) []byte {
	sig := make([]byte, sigSize)
	sig[0x80] = 0x52
	hdr := make([]byte, hdrSize)
	copy(hdr, "init=/init console=ttyS0\n") // Condor's images end the cmdline with a newline
	kernel := make([]byte, 0x3000)
	copy(kernel[0x202:], "HdrS")
	ramdisk := append([]byte{0x1f, 0x8b, 0x08, 0x00}, bytes.Repeat([]byte{7}, 1000)...)
	binary.LittleEndian.PutUint32(hdr[cmdlineSize:], uint32(len(kernel)))
	binary.LittleEndian.PutUint32(hdr[cmdlineSize+4:], uint32(len(ramdisk)))
	var b bytes.Buffer
	for _, p := range [][]byte{sig, hdr, bytes.Repeat([]byte{0x90}, 0x1000), kernel, ramdisk} {
		b.Write(p)
	}
	b.Write(make([]byte, 512-b.Len()%512))
	if !withOSIP {
		return b.Bytes()
	}
	osip := make([]byte, 512)
	copy(osip, tra901gSector0[:osiiOffset+osiiSize])
	osip[8], osip[10] = 1, 0x38 // one entry, 0x38-byte header, as in Intel flash files
	im := &bootImg{osip: osip}
	im.tail = b.Bytes()
	im.fixOSIP()
	return append(osip, b.Bytes()...)
}

func TestBootImgRoundTrip(t *testing.T) {
	for _, withOSIP := range []bool{false, true} {
		orig := fakeBootImg(withOSIP)
		im, err := parseBootImg(orig)
		if err != nil {
			t.Fatal(err)
		}
		if im.cmdline != "init=/init console=ttyS0\n" || len(im.stub) != 0x1000 || len(im.kernel) != 0x3000 {
			t.Fatalf("parsed %q stub %d kernel %d", im.cmdline, len(im.stub), len(im.kernel))
		}
		dir := t.TempDir()
		src := filepath.Join(dir, "in.img")
		os.WriteFile(src, orig, 0o644)
		if err := bootimgUnpack(src, filepath.Join(dir, "x")); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(dir, "out.img")
		if err := bootimgPack(filepath.Join(dir, "x"), out); err != nil {
			t.Fatal(err)
		}
		got, _ := os.ReadFile(out)
		if !bytes.Equal(got, orig) {
			t.Fatalf("osip=%v: unchanged repack differs (%d vs %d bytes)", withOSIP, len(got), len(orig))
		}

		// A changed cmdline must land in the header, and the OSIP checksum must stay valid.
		os.WriteFile(filepath.Join(dir, "x", "cmdline.txt"), []byte("init=/init quiet\n"), 0o644)
		if err := bootimgPack(filepath.Join(dir, "x"), out); err != nil {
			t.Fatal(err)
		}
		got, _ = os.ReadFile(out)
		im2, err := parseBootImg(got)
		if err != nil || im2.cmdline != "init=/init quiet" {
			t.Fatalf("modified repack: %v %q", err, im2.cmdline)
		}
		if withOSIP {
			var x byte
			for _, c := range got[:0x38] {
				x ^= c
			}
			if x != 0 {
				t.Fatalf("OSIP checksum broken: xor %#x", x)
			}
		}
	}
}
