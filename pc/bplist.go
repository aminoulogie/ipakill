package main

// Minimal reader for Apple binary plists ("bplist00"), enough for pairing
// records: dicts, arrays, strings, data, integers and booleans.

import (
	"encoding/binary"
	"fmt"
	"unicode/utf16"
)

type bplist struct {
	buf     []byte
	offsets []uint64
	refSize int
}

func parseBplist(b []byte) (any, error) {
	if len(b) < 40 || string(b[:8]) != "bplist00" {
		return nil, fmt.Errorf("not a binary plist")
	}
	t := b[len(b)-32:]
	offSize, refSize := int(t[6]), int(t[7])
	num := binary.BigEndian.Uint64(t[8:])
	top := binary.BigEndian.Uint64(t[16:])
	tableAt := binary.BigEndian.Uint64(t[24:])
	if offSize == 0 || refSize == 0 || tableAt+num*uint64(offSize) > uint64(len(b)) {
		return nil, fmt.Errorf("corrupt binary plist")
	}
	p := &bplist{buf: b, refSize: refSize}
	for i := uint64(0); i < num; i++ {
		p.offsets = append(p.offsets, readUint(b[tableAt+i*uint64(offSize):], offSize))
	}
	return p.object(top, 0)
}

func readUint(b []byte, n int) uint64 {
	var v uint64
	for i := 0; i < n; i++ {
		v = v<<8 | uint64(b[i])
	}
	return v
}

// length returns the element count for marker at off and where the payload starts.
func (p *bplist) length(off uint64) (uint64, uint64) {
	n := uint64(p.buf[off] & 0x0f)
	if n != 0x0f {
		return n, off + 1
	}
	size := 1 << (p.buf[off+1] & 0x0f) // follows as an int object
	return readUint(p.buf[off+2:], size), off + 2 + uint64(size)
}

func (p *bplist) object(ref uint64, depth int) (any, error) {
	if ref >= uint64(len(p.offsets)) || depth > 32 {
		return nil, fmt.Errorf("corrupt binary plist")
	}
	off := p.offsets[ref]
	m := p.buf[off]
	switch m >> 4 {
	case 0x0:
		return m == 0x09, nil
	case 0x1:
		return readUint(p.buf[off+1:], 1<<(m&0x0f)), nil
	case 0x4:
		n, at := p.length(off)
		return p.buf[at : at+n], nil
	case 0x5:
		n, at := p.length(off)
		return string(p.buf[at : at+n]), nil
	case 0x6:
		n, at := p.length(off)
		u := make([]uint16, n)
		for i := range u {
			u[i] = binary.BigEndian.Uint16(p.buf[at+uint64(i)*2:])
		}
		return string(utf16.Decode(u)), nil
	case 0xA:
		n, at := p.length(off)
		arr := make([]any, n)
		for i := uint64(0); i < n; i++ {
			v, err := p.object(readUint(p.buf[at+i*uint64(p.refSize):], p.refSize), depth+1)
			if err != nil {
				return nil, err
			}
			arr[i] = v
		}
		return arr, nil
	case 0xD:
		n, at := p.length(off)
		d := make(map[string]any, n)
		for i := uint64(0); i < n; i++ {
			k, err := p.object(readUint(p.buf[at+i*uint64(p.refSize):], p.refSize), depth+1)
			if err != nil {
				return nil, err
			}
			v, err := p.object(readUint(p.buf[at+(n+i)*uint64(p.refSize):], p.refSize), depth+1)
			if err != nil {
				return nil, err
			}
			if ks, ok := k.(string); ok {
				d[ks] = v
			}
		}
		return d, nil
	}
	return nil, nil // reals, dates, uids: not needed here
}
