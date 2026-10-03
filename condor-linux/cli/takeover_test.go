package main

import (
	"strings"
	"testing"
)

func TestTakeoverHook(t *testing.T) {
	if !strings.HasPrefix(takeoverHook, "#!/system/bin/sh\n") || strings.Contains(takeoverHook, "\r") {
		t.Fatal("hook must start with a /system/bin/sh shebang and use LF line endings")
	}
	// One-shot: the trigger must be removed before Android is stopped.
	if strings.Index(takeoverHook, "rm $T") > strings.Index(takeoverHook, "stop zygote") {
		t.Fatal("trigger must be deleted before stopping zygote")
	}
}

func TestMountOpts(t *testing.T) {
	m := "rootfs / rootfs ro,relatime 0 0\n/dev/block/mmcblk0p8 /system ext4 ro,noatime,data=ordered 0 0\n"
	if got := mountOpts(m, "/system"); got != "/dev/block/mmcblk0p8 ro,noatime,data=ordered" {
		t.Fatalf("got %q", got)
	}
	if got := mountOpts(m, "/data"); got != "not mounted" {
		t.Fatalf("got %q", got)
	}
}
