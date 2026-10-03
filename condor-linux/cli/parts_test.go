package main

import "testing"

func TestPartitionNames(t *testing.T) {
	ue := "MAJOR=179\nMINOR=1\nDEVNAME=mmcblk0p1\nDEVTYPE=partition\nPARTN=1\nPARTNAME=boot\n" +
		"MAJOR=179\nMINOR=2\nDEVNAME=mmcblk0p2\nPARTNAME=recovery\n"
	links := "lrwxrwxrwx root root 2013-08-22 02:02 fastboot -> /dev/block/mmcblk0p3\n" +
		"lrwxrwxrwx root root 2013-08-22 02:02 boot -> /dev/block/mmcblk0p1\n"
	got := partitionNames(ue, links)
	want := []partition{{"boot", "/dev/block/mmcblk0p1"}, {"recovery", "/dev/block/mmcblk0p2"}, {"fastboot", "/dev/block/mmcblk0p3"}}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestPropOfCRLF(t *testing.T) {
	// adb on Windows + Android 4.2 can return \r\r\n; adb() strips every \r before parsing.
	if v := propOf("[ro.build.version.release]: [4.2.2]\n", "ro.build.version.release"); v != "4.2.2" {
		t.Fatalf("got %q", v)
	}
}
