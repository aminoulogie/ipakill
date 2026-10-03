package main

import "testing"

func TestPartitionNames(t *testing.T) {
	ue := "MAJOR=179\nMINOR=1\nDEVNAME=mmcblk0p1\nDEVTYPE=partition\nPARTN=1\nPARTNAME=boot\n" +
		"MAJOR=179\nMINOR=2\nDEVNAME=mmcblk0p2\nPARTNAME=recovery\n"
	links := "lrwxrwxrwx root root 2013-08-22 02:02 fastboot -> /dev/block/mmcblk0p3\n" +
		"lrwxrwxrwx root root 2013-08-22 02:02 boot -> /dev/block/mmcblk0p1\n"
	got := partitionNames(ue, links)
	want := []partition{{name: "boot", dev: "/dev/block/mmcblk0p1"}, {name: "recovery", dev: "/dev/block/mmcblk0p2"}, {name: "fastboot", dev: "/dev/block/mmcblk0p3"}}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestAddGeometry(t *testing.T) {
	parts := []partition{{name: "reserved", dev: "/dev/block/mmcblk0p1"}}
	addGeometry(parts, "mmcblk0p1 40 335872\nmmcblk0p2 335912 16384")
	if parts[0].start != 40 || parts[0].sectors != 335872 {
		t.Fatalf("got %+v", parts[0])
	}
}

func TestPropOfCRLF(t *testing.T) {
	// adb on Windows + Android 4.2 can return \r\r\n; adb() strips every \r before parsing.
	if v := propOf("[ro.build.version.release]: [4.2.2]\n", "ro.build.version.release"); v != "4.2.2" {
		t.Fatalf("got %q", v)
	}
}

// Sector 0 of the TRA-901G eMMC (first 0x80 bytes; the rest is 0xFF and the MBR).
var tra901gSector0 = []byte{
	0x24, 0x4f, 0x53, 0x24, 0x00, 0x00, 0x01, 0x0f, 0x04, 0x01, 0x80, 0x00, 0, 0, 0, 0,
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	0x00, 0x00, 0x00, 0x00, 0xa2, 0x11, 0x01, 0x00, 0x00, 0x00, 0x10, 0x01, 0x00, 0x10, 0x10, 0x01,
	0xa6, 0x3a, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xc2, 0x65, 0x00, 0x00,
	0x00, 0x00, 0x10, 0x01, 0x00, 0x10, 0x10, 0x01, 0x62, 0x48, 0x00, 0x00, 0x0c, 0x00, 0x00, 0x00,
	0x00, 0x00, 0x00, 0x00, 0xd2, 0x0f, 0x00, 0x00, 0x00, 0x00, 0x10, 0x01, 0x00, 0x10, 0x10, 0x01,
	0xc4, 0x4e, 0x00, 0x00, 0x0e, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xb2, 0xbb, 0x00, 0x00,
	0x00, 0x00, 0x10, 0x01, 0x00, 0x10, 0x10, 0x01, 0x6a, 0x06, 0x00, 0x00, 0x04, 0x00, 0x00, 0x00,
}

func TestParseOSIP(t *testing.T) {
	sec := make([]byte, 512)
	copy(sec, tra901gSector0)
	es, err := parseOSIP(sec)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		name          string
		start, blocks uint32
	}{{"boot", 70050, 15014}, {"recovery", 26050, 18530}, {"fastboot", 4050, 20164}, {"splash", 48050, 1642}}
	if len(es) != len(want) {
		t.Fatalf("got %d entries", len(es))
	}
	for i, w := range want {
		if e := es[i]; e.name() != w.name || e.start != w.start || e.blocks != w.blocks || e.load != 0x01100000 || e.entry != 0x01101000 {
			t.Errorf("entry %d: got %s %+v, want %+v", i, e.name(), e, w)
		}
	}
	if _, err := parseOSIP(make([]byte, 512)); err == nil {
		t.Error("expected error for a sector without $OS$")
	}
}
