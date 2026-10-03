package main

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The /system takeover route. Condor's boot and recovery images are signed and the firmware
// enforces it, so the kernel and ramdisk stay stock. The ramdisk's init.rc runs
// /system/etc/install-recovery.sh as root ('flash_recovery', class main, oneshot), and stock
// /system doesn't ship that file: we install takeoverHook there.
//
// The hook does nothing unless /data/condor/takeover exists, and deletes it before acting,
// so every takeover is one-shot and the next reboot is plain Android again.
const (
	hookPath      = "/system/etc/install-recovery.sh"
	condorDir     = "/data/condor"
	triggerPath   = condorDir + "/takeover"
	initPath      = condorDir + "/condor-init"
	systemDev     = "/dev/block/mmcblk0p8"
	shellTmp      = "/data/local/tmp"
	takeoverUsage = "usage: condor takeover status | arm [condor-init binary] | disarm | hook"
)

const takeoverHook = `#!/system/bin/sh
# condor-linux takeover hook. init runs this as root at boot (service flash_recovery).
# Without the trigger file it does nothing and Android boots normally.
T=/data/condor/takeover
[ -f $T ] || exit 0
rm $T
sync
echo "takeover $(date)" >> /data/condor/hook.log
stop bootanim
stop zygote
exec /data/condor/condor-init >> /data/condor/init.log 2>&1
`

func takeover(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf(takeoverUsage)
	}
	if args[0] == "hook" { // print the script that goes in /system, no tablet needed
		fmt.Print(takeoverHook)
		return nil
	}
	if err := needDevice(); err != nil {
		return err
	}
	if !strings.Contains(sh("su -c id"), "uid=0") {
		return fmt.Errorf("su doesn't give root; takeover needs it")
	}
	switch args[0] {
	case "status":
		return takeoverStatus()
	case "arm":
		if len(args) > 1 {
			if err := pushRoot(args[1], initPath, "755"); err != nil {
				return err
			}
		}
		if strings.Contains(sh("ls "+hookPath), "No such") {
			fmt.Println("warning: the hook isn't installed in /system yet, so arming does nothing")
		}
		if strings.Contains(sh("su -c 'ls "+initPath+"'"), "No such") {
			return fmt.Errorf("%s is missing: run 'condor takeover arm <condor-init binary>'", initPath)
		}
		sh("su -c 'mkdir " + condorDir + "; touch " + triggerPath + "'")
		if strings.Contains(sh("su -c 'ls "+triggerPath+"'"), "No such") {
			return fmt.Errorf("couldn't create %s", triggerPath)
		}
		fmt.Println("armed: the next boot stops Android and starts condor-init (once).")
		fmt.Println("reboot with:  condor reboot")
		return nil
	case "disarm":
		sh("su -c 'rm " + triggerPath + "'")
		fmt.Println("disarmed: next boot is normal Android.")
		return nil
	}
	return fmt.Errorf(takeoverUsage)
}

// pushRoot copies a PC file to a root-owned path on the tablet via /data/local/tmp,
// verifying its md5 on arrival.
func pushRoot(local, remote, mode string) error {
	data, err := os.ReadFile(local)
	if err != nil {
		return err
	}
	sum := md5.Sum(data)
	want := hex.EncodeToString(sum[:])
	tmp := shellTmp + "/" + filepath.Base(remote)
	if out, err := adb("push", local, tmp); err != nil {
		return fmt.Errorf("push: %s", strings.TrimSpace(out))
	}
	defer sh("rm " + tmp)
	// Write next to the target and rename: overwriting a running binary in place fails
	// with "Text file busy", while a rename just replaces the directory entry.
	next := remote + ".new"
	sh("su -c 'mkdir " + filepath.ToSlash(filepath.Dir(remote)) + "; cat " + tmp + " > " + next + "; chmod " + mode + " " + next + "'")
	if got := sh("su -c 'md5 " + next + "'"); !strings.HasPrefix(got, want) {
		sh("su -c 'rm " + next + "'")
		return fmt.Errorf("%s md5 mismatch after copy: %q, want %s", next, got, want)
	}
	sh("su -c 'mv " + next + " " + remote + "'")
	if got := sh("su -c 'md5 " + remote + "'"); !strings.HasPrefix(got, want) {
		return fmt.Errorf("%s md5 mismatch after rename: %q, want %s", remote, got, want)
	}
	fmt.Printf("installed %s (md5 %s)\n", remote, want)
	return nil
}

func takeoverStatus() error {
	sum := md5.Sum([]byte(takeoverHook))
	want := hex.EncodeToString(sum[:])
	hook := sh("md5 " + hookPath)
	switch {
	case strings.Contains(hook, "No such"):
		fmt.Println("hook:        not installed (" + hookPath + " absent: stock)")
	case strings.HasPrefix(hook, want):
		fmt.Println("hook:        installed, matches this CLI's version")
	default:
		fmt.Println("hook:        PRESENT BUT DIFFERENT from this CLI's version: " + hook)
	}
	fmt.Println("/system:     " + mountOpts(sh("cat /proc/mounts"), "/system"))
	ci := sh("su -c 'md5 " + initPath + "'")
	if strings.Contains(ci, "No such") {
		ci = "missing"
	}
	fmt.Println("condor-init: " + ci)
	armed := "no"
	if !strings.Contains(sh("su -c 'ls "+triggerPath+"'"), "No such") {
		armed = "YES (next boot takes over)"
	}
	fmt.Println("armed:       " + armed)
	running := "no"
	for _, l := range strings.Split(sh("ps"), "\n") {
		if strings.HasSuffix(strings.TrimSpace(l), "condor-init") {
			running = "yes: " + strings.Join(strings.Fields(l)[:2], " pid ")
		}
	}
	fmt.Println("running:     " + running)
	for _, f := range []string{"hook.log", "init.log"} {
		if l := sh("su -c 'cat " + condorDir + "/" + f + "'"); !strings.Contains(l, "No such") && l != "" {
			lines := strings.Split(l, "\n")
			if len(lines) > 8 {
				lines = lines[len(lines)-8:]
			}
			fmt.Printf("\n-- %s (last lines) --\n%s\n", f, strings.Join(lines, "\n"))
		}
	}
	return nil
}

// mountOpts returns the device and options of a mount point from /proc/mounts.
func mountOpts(mounts, point string) string {
	for _, l := range strings.Split(mounts, "\n") {
		if f := strings.Fields(l); len(f) >= 4 && f[1] == point {
			return f[0] + " " + f[3]
		}
	}
	return "not mounted"
}
