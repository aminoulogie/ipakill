//go:build linux

package main

import (
	"fmt"
	"os"
	"strings"
	"syscall"
)

// alpineMounts makes the kernel's filesystems visible inside the Alpine root (once per boot):
// /proc, /sys, and the tablet's /dev (with /dev/pts, so the shell has its terminal).
func alpineMounts() error {
	mounted, _ := os.ReadFile("/proc/mounts")
	isMounted := func(p string) bool { return strings.Contains(string(mounted), " "+p+" ") }
	for _, m := range []struct {
		src, target, fstype string
		flags               uintptr
	}{
		{"proc", alpineRoot + "/proc", "proc", 0},
		{"sysfs", alpineRoot + "/sys", "sysfs", 0},
		{"/dev", alpineRoot + "/dev", "", syscall.MS_BIND | syscall.MS_REC},
		{"tmpfs", alpineRoot + "/tmp", "tmpfs", 0},
	} {
		if isMounted(m.target) {
			continue
		}
		os.MkdirAll(m.target, 0o755)
		if err := syscall.Mount(m.src, m.target, m.fstype, m.flags, ""); err != nil {
			return fmt.Errorf("mount %s: %w", m.target, err)
		}
	}
	return nil
}

// setHostname names the machine "condor" (shown in the prompt).
func setHostname() { syscall.Sethostname([]byte("condor")) }
