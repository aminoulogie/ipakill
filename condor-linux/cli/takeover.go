package main

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
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
	triggerPath   = condorDir + "/takeover"  // one-shot: next boot only
	autostartPath = condorDir + "/autostart" // persistent: every boot
	bootfailPath  = condorDir + "/bootfail"  // crash-loop counter
	initPath      = condorDir + "/condor-init"
	systemDev     = "/dev/block/mmcblk0p8"
	shellTmp      = "/data/local/tmp"
	takeoverUsage = "usage: condor takeover status | arm [binary] | auto on|off [binary] | install-hook | disarm | push <binary> | restart | hook"
)

const takeoverHook = `#!/system/bin/sh
# condor-linux takeover hook. init runs this as root at boot (service flash_recovery).
# It starts condor-init instead of Android when EITHER:
#   /data/condor/autostart exists  (persistent: every boot), or
#   /data/condor/takeover  exists  (one-shot: deleted here, so only the next boot).
# Neither present -> normal Android.
#
# Crash-loop guard: a counter is bumped before condor-init starts and cleared by condor-init
# once it has run a while. If condor-init keeps dying early, the counter reaches the limit and
# this hook boots Android and turns autostart off, so the tablet is never trapped: at worst it
# returns to Android on its own after a few reboots.
C=/data/condor
[ -f $C/autostart ] || [ -f $C/takeover ] || exit 0
rm -f $C/takeover
N=$(cat $C/bootfail 2>/dev/null)
N=$((N+0+1))
echo $N > $C/bootfail
sync
if [ $N -ge 3 ]; then
  rm -f $C/bootfail $C/autostart
  echo "fallback to android after $N tries $(date)" >> $C/hook.log
  exit 0
fi
echo "takeover try $N $(date)" >> $C/hook.log
stop bootanim
stop zygote
exec $C/condor-init >> $C/init.log 2>&1
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
	case "auto":
		return takeoverAuto(args[1:])
	case "install-hook":
		return installHook()
	case "disarm":
		sh("su -c 'rm -f " + triggerPath + " " + autostartPath + " " + bootfailPath + "'")
		fmt.Println("disarmed: autostart off, next boot is normal Android.")
		return nil
	case "push": // install a new condor-init without arming
		if len(args) < 2 {
			return fmt.Errorf("usage: condor takeover push <condor-init binary>")
		}
		return pushRoot(args[1], initPath, "755")
	case "restart": // replace the running condor-init without rebooting
		return takeoverRestart()
	}
	return fmt.Errorf(takeoverUsage)
}

// takeoverAuto turns persistent boot-into-condor on or off. "on" installs condor-init (if a
// binary is given), checks the hook is present, and creates /data/condor/autostart so every
// boot starts condor-init. "off" removes it so the next boot is Android.
func takeoverAuto(args []string) error {
	if len(args) == 0 || (args[0] != "on" && args[0] != "off") {
		return fmt.Errorf("usage: condor takeover auto on|off [condor-init binary]")
	}
	if args[0] == "off" {
		sh("su -c 'rm -f " + autostartPath + " " + bootfailPath + "'")
		fmt.Println("autostart off: the next boot is normal Android (condor takeover auto on to re-enable).")
		return nil
	}
	if len(args) > 1 {
		if err := pushRoot(args[1], initPath, "755"); err != nil {
			return err
		}
	}
	if !hookCurrent() {
		return fmt.Errorf("the startup hook in /system is missing or an older version that ignores autostart.\n" +
			"Update it first (writes one file in /system):  condor takeover install-hook")
	}
	if strings.Contains(sh("su -c 'ls "+initPath+"'"), "No such") {
		return fmt.Errorf("%s is missing: run 'condor takeover auto on <condor-init binary>'", initPath)
	}
	sh("su -c 'mkdir " + condorDir + "; rm -f " + bootfailPath + "; touch " + autostartPath + "'")
	if strings.Contains(sh("su -c 'ls "+autostartPath+"'"), "No such") {
		return fmt.Errorf("couldn't create %s", autostartPath)
	}
	fmt.Println("autostart ON: every boot now goes straight into condor.")
	fmt.Println("to go back to Android:  condor takeover auto off   (then condor reboot)")
	fmt.Println("safety net: if condor-init ever fails to start 3 boots running, the tablet")
	fmt.Println("falls back to Android and turns autostart off by itself.")
	return nil
}

// hookCurrent reports whether /system holds exactly this CLI's takeoverHook.
func hookCurrent() bool {
	sum := md5.Sum([]byte(takeoverHook))
	return strings.HasPrefix(sh("md5 "+hookPath), hex.EncodeToString(sum[:]))
}

// installHook writes takeoverHook to /system/etc/install-recovery.sh: /system is remounted
// read-write only for this one file, the result is md5-checked, and /system goes back to
// read-only. If anything fails, the old file (or none) stays and Android still boots.
func installHook() error {
	if hookCurrent() {
		fmt.Println("hook is already the current version; nothing to do.")
		return nil
	}
	f, err := os.CreateTemp("", "condor-hook-*")
	if err != nil {
		return err
	}
	f.WriteString(takeoverHook)
	f.Close()
	defer os.Remove(f.Name())
	tmp := shellTmp + "/install-recovery.sh"
	if out, err := adb("push", f.Name(), tmp); err != nil {
		return fmt.Errorf("push: %s", strings.TrimSpace(out))
	}
	defer sh("rm " + tmp)
	sum := md5.Sum([]byte(takeoverHook))
	want := hex.EncodeToString(sum[:])
	if got := sh("md5 " + tmp); !strings.HasPrefix(got, want) {
		return fmt.Errorf("pushed hook is damaged (%q); nothing was written to /system", got)
	}
	fmt.Println("==> writing", hookPath, "(the only change to /system)")
	sh("su -c 'mount -o remount,rw -t ext4 " + systemDev + " /system'")
	sh("su -c 'cat " + tmp + " > " + hookPath + "; chmod 755 " + hookPath + "; sync'")
	sh("su -c 'mount -o remount,ro -t ext4 " + systemDev + " /system'")
	if !hookCurrent() {
		return fmt.Errorf("the hook in /system doesn't match after writing: %s", sh("md5 "+hookPath))
	}
	m := mountOpts(sh("cat /proc/mounts"), "/system")
	fmt.Println("hook updated and verified; /system:", m)
	if f := strings.Fields(m); len(f) < 2 || !strings.HasPrefix(f[1], "ro") {
		fmt.Println("warning: /system is still read-write; run: condor shell \"su -c 'mount -o remount,ro -t ext4 " + systemDev + " /system'\"")
	}
	return nil
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

// condorInitPids returns the pids of running condor-init processes.
func condorInitPids() []string {
	var pids []string
	for _, l := range strings.Split(sh("ps"), "\n") {
		if f := strings.Fields(l); len(f) > 1 && strings.HasSuffix(strings.TrimSpace(l), "condor-init") {
			pids = append(pids, f[1])
		}
	}
	return pids
}

// takeoverRestart stops condor-init (it clears the screen on SIGTERM) and starts the
// installed binary again, detached. Only in takeover mode: with Android running it would
// draw over SurfaceFlinger.
func takeoverRestart() error {
	if strings.Contains(sh("ps"), "system_server") {
		return fmt.Errorf("Android is running; restart only works in takeover mode (arm + reboot)")
	}
	for _, pid := range condorInitPids() {
		sh("su -c 'kill " + pid + "'")
	}
	for i := 0; i < 10 && len(condorInitPids()) > 0; i++ {
		time.Sleep(300 * time.Millisecond)
	}
	if p := condorInitPids(); len(p) > 0 {
		return fmt.Errorf("condor-init still running (pid %v)", p)
	}
	// Let init start it, exactly like at boot: the hook *is* the flash_recovery service, and
	// that service stopped when condor-init (which the hook exec'd into) exited. Starting it
	// from 'su ... &' doesn't work: su tears the child down when it returns.
	sh("su -c 'touch " + triggerPath + "; start flash_recovery'")
	time.Sleep(2 * time.Second)
	p := condorInitPids()
	if len(p) == 0 {
		return fmt.Errorf("condor-init didn't start; see: condor takeover status")
	}
	fmt.Println("condor-init restarted, pid", p[0])
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
	auto := "off (next boot is Android unless armed)"
	if !strings.Contains(sh("su -c 'ls "+autostartPath+"'"), "No such") {
		auto = "ON (every boot goes into condor)"
	}
	fmt.Println("autostart:   " + auto)
	armed := "no"
	if !strings.Contains(sh("su -c 'ls "+triggerPath+"'"), "No such") {
		armed = "YES (next boot takes over, one-shot)"
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
