//go:build linux

package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"runtime"
	"strings"
	"unsafe"
)

// Read-only DRM queries: which framebuffer each CRTC is scanning out, so we can tell
// whether the panel shows psbfb's buffer (fb0) or something else (e.g. the boot logo).
// Struct layouts from include/drm/drm_mode.h (Linux 3.4), 32-bit x86.
const (
	drmIoctlModeGetResources = 0xC04064A0 // _IOWR('d', 0xA0, struct drm_mode_card_res), 64 bytes
	drmIoctlModeGetCrtc      = 0xC06864A1 // _IOWR('d', 0xA1, struct drm_mode_crtc), 104 bytes
	drmIoctlModeGetFB        = 0xC01C64AD // _IOWR('d', 0xAD, struct drm_mode_fb_cmd), 28 bytes
)

func drmInfo() string {
	var out strings.Builder
	f, err := os.OpenFile("/dev/dri/card0", os.O_RDWR, 0)
	if err != nil {
		return "open /dev/dri/card0: " + err.Error()
	}
	defer f.Close()
	fd := f.Fd()
	le := binary.LittleEndian

	// drm_mode_card_res: 4 u64 array pointers, then counts and size limits.
	var res [64]byte
	if err := ioctl(fd, drmIoctlModeGetResources, unsafe.Pointer(&res[0])); err != nil {
		return "GETRESOURCES: " + err.Error()
	}
	nFB, nCrtc := le.Uint32(res[32:]), le.Uint32(res[36:])
	fbs := make([]uint32, nFB+1)
	crtcs := make([]uint32, nCrtc+1)
	conns := make([]uint32, le.Uint32(res[40:])+1)
	encs := make([]uint32, le.Uint32(res[44:])+1)
	le.PutUint64(res[0:], uint64(uintptr(unsafe.Pointer(&fbs[0]))))
	le.PutUint64(res[8:], uint64(uintptr(unsafe.Pointer(&crtcs[0]))))
	le.PutUint64(res[16:], uint64(uintptr(unsafe.Pointer(&conns[0]))))
	le.PutUint64(res[24:], uint64(uintptr(unsafe.Pointer(&encs[0]))))
	err = ioctl(fd, drmIoctlModeGetResources, unsafe.Pointer(&res[0]))
	runtime.KeepAlive(fbs)
	runtime.KeepAlive(crtcs)
	runtime.KeepAlive(conns)
	runtime.KeepAlive(encs)
	if err != nil {
		return "GETRESOURCES (2): " + err.Error()
	}
	nFB, nCrtc = le.Uint32(res[32:]), le.Uint32(res[36:])
	fmt.Fprintf(&out, "drm: %d fbs %v, %d crtcs %v, %d connectors, size %dx%d..%dx%d\n",
		nFB, fbs[:nFB], nCrtc, crtcs[:nCrtc], le.Uint32(res[40:]),
		le.Uint32(res[48:]), le.Uint32(res[56:]), le.Uint32(res[52:]), le.Uint32(res[60:]))

	seen := map[uint32]bool{}
	for _, id := range crtcs[:nCrtc] {
		// drm_mode_crtc: set_connectors_ptr u64, count_connectors, crtc_id, fb_id, x, y,
		// gamma_size, mode_valid (u32 each), then drm_mode_modeinfo (hdisplay at +4, vdisplay at +14).
		var c [104]byte
		le.PutUint32(c[12:], id)
		if err := ioctl(fd, drmIoctlModeGetCrtc, unsafe.Pointer(&c[0])); err != nil {
			fmt.Fprintf(&out, "crtc %d: %v\n", id, err)
			continue
		}
		fbID := le.Uint32(c[16:])
		m := c[36:]
		fmt.Fprintf(&out, "crtc %d: fb %d at %d,%d, mode valid %d: %dx%d@%d %q\n", id, fbID,
			le.Uint32(c[20:]), le.Uint32(c[24:]), le.Uint32(c[32:]),
			le.Uint16(m[4:]), le.Uint16(m[14:]), le.Uint32(m[24:]), cstr(m[36:68]))
		if fbID != 0 && !seen[fbID] {
			seen[fbID] = true
			out.WriteString(drmFB(fd, fbID))
		}
	}
	for _, id := range fbs[:nFB] {
		if !seen[id] {
			out.WriteString(drmFB(fd, id))
		}
	}
	return out.String()
}

func drmFB(fd uintptr, id uint32) string {
	// drm_mode_fb_cmd: fb_id, width, height, pitch, bpp, depth, handle.
	var b [28]byte
	le := binary.LittleEndian
	le.PutUint32(b[0:], id)
	if err := ioctl(fd, drmIoctlModeGetFB, unsafe.Pointer(&b[0])); err != nil {
		return fmt.Sprintf("  fb %d: %v\n", id, err)
	}
	return fmt.Sprintf("  fb %d: %dx%d pitch %d bpp %d depth %d\n", id,
		le.Uint32(b[4:]), le.Uint32(b[8:]), le.Uint32(b[12:]), le.Uint32(b[16:]), le.Uint32(b[20:]))
}

func cstr(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}
