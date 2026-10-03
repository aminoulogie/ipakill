package main

import (
	"encoding/binary"
	"fmt"
)

// Intel MID (Medfield / Clover Trail+) eMMC sector 0 starts with an OSIP header ("$OS$")
// instead of a boot code area. Each 24-byte OSII entry after it points at one OS image
// stored raw on the eMMC; the protective MBR partition entries follow at 0x1BE as usual.
//
//	0x00 "$OS$"  0x04 reserved, rev minor, rev major, checksum (XOR of header = 0)
//	0x08 num_pointers, num_images, header_size u16   0x0C..0x1F reserved
//	0x20 OSII[n]: rev minor u16, rev major u16, start LBA u32, load addr u32,
//	              entry u32, size in 512-byte blocks u32, attribute u8, 3 reserved
const (
	osipMagic     = "$OS$"
	osiiOffset    = 0x20
	osiiSize      = 24
	osipMBROffset = 0x1BE
)

type osipEntry struct {
	start, blocks, load, entry uint32
	attr                       byte
}

// name maps an OSII attribute to its image, per Intel droidboot's osip.h
// (ATTR_SIGNED_KERNEL 0, ATTR_UNSIGNED_KERNEL 1, ROS 0x0C, POS 0x0E, COS 0x0A, SPLASH 0x04).
func (e osipEntry) name() string {
	switch e.attr {
	case 0x00, 0x01:
		return "boot"
	case 0x0C:
		return "recovery"
	case 0x0E:
		return "fastboot"
	case 0x0A:
		return "charging"
	case 0x04:
		return "splash"
	}
	return fmt.Sprintf("osii-%02x", e.attr)
}

func parseOSIP(b []byte) ([]osipEntry, error) {
	if len(b) < 512 || string(b[:4]) != osipMagic {
		return nil, fmt.Errorf("no OSIP header in sector 0")
	}
	n := int(b[8])
	if osiiOffset+n*osiiSize > osipMBROffset {
		return nil, fmt.Errorf("OSIP claims %d entries, more than fit before the MBR", n)
	}
	var es []osipEntry
	for i := 0; i < n; i++ {
		o := b[osiiOffset+i*osiiSize:]
		es = append(es, osipEntry{
			start:  binary.LittleEndian.Uint32(o[4:]),
			load:   binary.LittleEndian.Uint32(o[8:]),
			entry:  binary.LittleEndian.Uint32(o[12:]),
			blocks: binary.LittleEndian.Uint32(o[16:]),
			attr:   o[20],
		})
	}
	return es, nil
}
