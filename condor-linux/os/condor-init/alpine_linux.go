//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
)

var mountMu sync.Mutex

// alpineMounts makes the kernel's filesystems visible inside the Alpine root (once per boot):
// /proc, /sys, and the tablet's /dev (with /dev/pts, so the shell has its terminal).
func alpineMounts() error {
	mountMu.Lock() // the console and alpineBoot both get here at startup
	defer mountMu.Unlock()
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
		// The Wi-Fi driver opens its firmware by path from the process that raises wlan0, so
		// /system must resolve the same inside the chroot.
		{"/system", alpineRoot + "/system", "", syscall.MS_BIND},
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

// runInAlpine runs a command inside the Alpine root and waits for it (used for wifi boot).
func runInAlpine(args ...string) error {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = "/"
	cmd.Env = []string{"HOME=/root", "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Chroot: alpineRoot}
	return cmd.Run()
}
