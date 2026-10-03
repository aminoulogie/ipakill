// condor: command-line control of a Condor TRA-901G tablet (Android 4.2.2) over USB.
// Wraps adb/fastboot; downloads Google's platform-tools on first use if they aren't installed.
//
//	condor doctor                  check adb, cable, and that the tablet is authorized
//	condor info                    model, Android, kernel, battery, storage
//	condor books <file|dir>...     copy .epub/.pdf/.mobi/.fb2/.txt to /sdcard/Books
//	condor install <app.apk>...    install or update apps
//	condor apps                    list installed (non-system) apps
//	condor shell [command...]      open a shell on the tablet, or run one command
//	condor screenshot [out.png]    save the tablet's screen to a PNG
//	condor recon                   read-only inspection + backup for the Linux project
//	condor backup [folder]         copy the tablet's internal storage (/sdcard) to the PC
//	condor reboot [bootloader|recovery]
//	condor setup                   install so 'condor' works in any cmd window (also runs on double-click)
package main

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const toolsURL = "https://dl.google.com/android/repository/platform-tools-latest-%s.zip"

var (
	home     = condorHome()
	toolsDir = filepath.Join(home, "platform-tools")
	adbPath  string

	bookExts = map[string]bool{".epub": true, ".pdf": true, ".mobi": true, ".fb2": true, ".txt": true, ".djvu": true, ".cbz": true}
)

func condorHome() string {
	if p := os.Getenv("USERPROFILE"); p != "" {
		return filepath.Join(p, ".condor")
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".condor")
}

func main() {
	if len(os.Args) < 2 {
		// Double-clicked in Explorer: install so 'condor' works in every cmd window.
		if runtime.GOOS == "windows" && !onPath() {
			err := setup()
			if err != nil {
				fmt.Println("error:", err)
			}
			fmt.Print("\nPress Enter to close...")
			fmt.Scanln()
			return
		}
		usage()
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	if cmd == "help" || cmd == "-h" || cmd == "--help" {
		usage()
		return
	}
	if cmd == "setup" {
		if err := setup(); err != nil {
			fail(err)
		}
		return
	}
	var err error
	if adbPath, err = findADB(); err != nil {
		fail(err)
	}
	switch cmd {
	case "doctor":
		err = doctor()
	case "info":
		err = info()
	case "books":
		err = books(args)
	case "install":
		err = install(args)
	case "apps":
		err = apps()
	case "shell":
		err = shell(args)
	case "screenshot":
		err = screenshot(args)
	case "recon":
		err = recon()
	case "backup":
		err = backup(args)
	case "reboot":
		err = reboot(args)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fail(err)
	}
}

func usage() {
	fmt.Print(`condor - control the Condor tablet over USB

  condor doctor                  check adb, cable, and authorization
  condor info                    model, Android, kernel, battery, storage
  condor books <file|dir>...     copy ebooks to /sdcard/Books
  condor install <app.apk>...    install or update apps
  condor apps                    list installed apps
  condor shell [command...]      shell on the tablet (or run one command)
  condor screenshot [out.png]    save the tablet's screen
  condor recon                   read-only inspection + backup (Linux project)
  condor backup [folder]         copy the tablet's files (/sdcard) to the PC
  condor reboot [bootloader|recovery]
  condor setup                   make 'condor' work in any cmd window
`)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

// ---------- setup: put condor on the user's PATH ----------

var binDir = filepath.Join(home, "bin")

func onPath() bool {
	p, err := exec.LookPath("condor")
	return err == nil && p != ""
}

// setup copies this executable to %USERPROFILE%\.condor\bin and adds that folder to the user PATH.
func setup() error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	name := "condor"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	dst := filepath.Join(binDir, name)
	os.MkdirAll(binDir, 0o755)
	if !strings.EqualFold(filepath.Clean(self), filepath.Clean(dst)) {
		data, err := os.ReadFile(self)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dst, data, 0o755); err != nil {
			return fmt.Errorf("copy to %s: %w (close other condor windows and retry)", dst, err)
		}
	}
	fmt.Println("installed", dst)
	if runtime.GOOS != "windows" {
		fmt.Printf("add this to your shell profile:  export PATH=\"%s:$PATH\"\n", binDir)
		return nil
	}
	// The user PATH lives in the registry; setx would truncate it at 1024 chars, so use .NET.
	ps := `$d='` + binDir + `'; $p=[Environment]::GetEnvironmentVariable('Path','User');` +
		`if(-not (($p -split ';') -contains $d)){[Environment]::SetEnvironmentVariable('Path',(($p.TrimEnd(';')+';'+$d).TrimStart(';')),'User'); 'added'} else {'present'}`
	out, err := exec.Command("powershell", "-NoProfile", "-Command", ps).CombinedOutput()
	if err != nil {
		return fmt.Errorf("update PATH: %s", strings.TrimSpace(string(out)))
	}
	if strings.TrimSpace(string(out)) == "added" {
		fmt.Println("added", binDir, "to your PATH")
	}
	fmt.Println("\nDone. Open a NEW cmd window and type:  condor doctor")
	return nil
}

// ---------- adb plumbing ----------

func findADB() (string, error) {
	name := "adb"
	if runtime.GOOS == "windows" {
		name = "adb.exe"
	}
	if p := filepath.Join(toolsDir, name); exists(p) {
		return p, nil
	}
	if p, err := exec.LookPath("adb"); err == nil {
		return p, nil
	}
	fmt.Println("adb not found, downloading Google platform-tools (one time, ~7 MB)...")
	if err := downloadTools(); err != nil {
		return "", fmt.Errorf("download platform-tools: %w", err)
	}
	return filepath.Join(toolsDir, name), nil
}

func downloadTools() error {
	osName := map[string]string{"windows": "windows", "linux": "linux", "darwin": "darwin"}[runtime.GOOS]
	if osName == "" {
		return fmt.Errorf("unsupported OS %s", runtime.GOOS)
	}
	resp, err := http.Get(fmt.Sprintf(toolsURL, osName))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %s", resp.Status)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	for _, f := range zr.File { // entries are "platform-tools/..."
		dst := filepath.Join(home, filepath.FromSlash(f.Name))
		if !strings.HasPrefix(dst, filepath.Clean(home)+string(os.PathSeparator)) {
			continue
		}
		if f.FileInfo().IsDir() {
			os.MkdirAll(dst, 0o755)
			continue
		}
		os.MkdirAll(filepath.Dir(dst), 0o755)
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode()|0o600)
		if err == nil {
			_, err = io.Copy(out, rc)
			out.Close()
		}
		rc.Close()
		if err != nil {
			return err
		}
	}
	fmt.Println("installed to", toolsDir)
	return nil
}

// adb runs adb and returns its output with Android 4.2's CRLF line endings normalized.
func adb(args ...string) (string, error) {
	out, err := exec.Command(adbPath, args...).CombinedOutput()
	return strings.ReplaceAll(string(out), "\r", ""), err
}

// sh runs a command in the tablet's shell. Android 4.2 doesn't pass exit codes through adb.
func sh(cmd string) string {
	out, _ := adb("shell", cmd)
	return strings.TrimRight(out, "\n")
}

// adbLive runs adb attached to this terminal (progress output, interactive shell).
func adbLive(args ...string) error {
	c := exec.Command(adbPath, args...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return c.Run()
}

func state() string {
	out, _ := adb("get-state")
	return strings.TrimSpace(out)
}

// needDevice waits briefly for the tablet and explains what's wrong if it never shows up.
func needDevice() error {
	adb("start-server")
	for i := 0; i < 10; i++ {
		switch state() {
		case "device":
			return nil
		case "unauthorized":
			fmt.Println("Tablet says 'unauthorized': unlock it and tap 'Allow' on the USB debugging prompt...")
		}
		time.Sleep(time.Second)
	}
	out, _ := adb("devices")
	return fmt.Errorf("tablet not ready (state %q).\n%s\nRun 'condor doctor' for help", state(), out)
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// ---------- commands ----------

func doctor() error {
	fmt.Println("adb:      ", adbPath)
	v, _ := adb("version")
	fmt.Println("version:  ", strings.SplitN(v, "\n", 2)[0])
	adb("start-server")
	out, _ := adb("devices", "-l")
	fmt.Print("devices:\n", out)
	switch s := state(); s {
	case "device":
		fmt.Println("OK: tablet connected and authorized.")
		fmt.Println("battery:  ", battery())
	case "unauthorized":
		fmt.Println("Tablet is connected but not authorized: unlock it and tap 'Allow' on the USB debugging prompt.")
		fmt.Println("No prompt? Settings > Developer options > 'Revoke USB debugging authorizations', then replug.")
	default:
		fmt.Println("No tablet seen. Check, in order:")
		fmt.Println("  1. Tablet is on and unlocked (not on the low-battery screen).")
		fmt.Println("  2. USB debugging: Settings > About tablet > tap 'Build number' 7x > Developer options > USB debugging.")
		fmt.Println("  3. Cable carries data (not charge-only). Try another port, ideally on the back of the PC.")
		if runtime.GOOS == "windows" {
			fmt.Println("  4. Driver: Device Manager > look for an unknown 'Android' device > Update driver >")
			fmt.Println("     Browse > Let me pick > 'Android ADB Interface' (Intel's Android USB driver also works).")
		}
	}
	return nil
}

func battery() string {
	d := sh("dumpsys battery")
	var level, status, temp string
	for _, l := range strings.Split(d, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(l), ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch k {
		case "level":
			level = v + "%"
		case "status":
			status = map[string]string{"2": "charging", "3": "discharging", "4": "not charging", "5": "full"}[v]
		case "temperature":
			temp = v
		}
	}
	if len(temp) > 1 {
		temp = ", " + temp[:len(temp)-1] + "." + temp[len(temp)-1:] + "°C"
	}
	return strings.TrimSpace(level + " " + status + temp)
}

func info() error {
	if err := needDevice(); err != nil {
		return err
	}
	prop := func(k string) string { return sh("getprop " + k) }
	rows := [][2]string{
		{"Model", prop("ro.product.manufacturer") + " " + prop("ro.product.model")},
		{"Android", prop("ro.build.version.release") + " (API " + prop("ro.build.version.sdk") + ")"},
		{"Build", prop("ro.build.display.id")},
		{"Platform", prop("ro.board.platform")},
		{"CPU ABI", prop("ro.product.cpu.abi")},
		{"Kernel", sh("cat /proc/version")},
		{"Battery", battery()},
		{"Serial", prop("ro.serialno")},
	}
	for _, r := range rows {
		fmt.Printf("%-9s %s\n", r[0]+":", r[1])
	}
	fmt.Println("\nStorage:")
	fmt.Println(sh("df /data /sdcard /storage/sdcard1 2>/dev/null"))
	return nil
}

func books(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: condor books <file-or-folder>...")
	}
	if err := needDevice(); err != nil {
		return err
	}
	var files []string
	for _, a := range args {
		filepath.Walk(a, func(p string, fi os.FileInfo, err error) error {
			if err == nil && !fi.IsDir() && bookExts[strings.ToLower(filepath.Ext(p))] {
				files = append(files, p)
			}
			return nil
		})
	}
	if len(files) == 0 {
		return fmt.Errorf("no ebooks found (looked for %s)", strings.Join(sortedKeys(bookExts), " "))
	}
	sh("mkdir /sdcard/Books")
	ok := 0
	for i, f := range files {
		fmt.Printf("[%d/%d] %s\n", i+1, len(files), filepath.Base(f))
		if out, err := adb("push", f, "/sdcard/Books/"+filepath.Base(f)); err != nil {
			fmt.Println("   failed:", strings.TrimSpace(out))
			continue
		}
		ok++
	}
	// Ask the media scanner to index the folder so reader apps see the files immediately.
	sh("am broadcast -a android.intent.action.MEDIA_MOUNTED -d file:///sdcard")
	fmt.Printf("%d of %d books copied to /sdcard/Books\n", ok, len(files))
	return nil
}

func sortedKeys(m map[string]bool) []string {
	var k []string
	for s := range m {
		k = append(k, s)
	}
	sort.Strings(k)
	return k
}

func install(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: condor install <app.apk>...")
	}
	if err := needDevice(); err != nil {
		return err
	}
	for _, apk := range args {
		fmt.Println("installing", filepath.Base(apk))
		out, _ := adb("install", "-r", apk)
		out = strings.TrimSpace(out)
		if !strings.Contains(out, "Success") {
			hint := ""
			switch {
			case strings.Contains(out, "OLDER_SDK"):
				hint = " (this app needs a newer Android than 4.2; look for an older version of it)"
			case strings.Contains(out, "NO_MATCHING_ABIS"), strings.Contains(out, "CPU_ABI"):
				hint = " (no x86 build in this APK; try the x86 or 'universal' version)"
			case strings.Contains(out, "INSUFFICIENT_STORAGE"):
				hint = " (tablet storage full)"
			}
			return fmt.Errorf("%s: %s%s", filepath.Base(apk), lastLine(out), hint)
		}
		fmt.Println("   ok")
	}
	return nil
}

func lastLine(s string) string {
	l := strings.Split(strings.TrimSpace(s), "\n")
	return l[len(l)-1]
}

func apps() error {
	if err := needDevice(); err != nil {
		return err
	}
	var pkgs []string
	for _, l := range strings.Split(sh("pm list packages -3"), "\n") {
		if p := strings.TrimPrefix(strings.TrimSpace(l), "package:"); p != "" {
			pkgs = append(pkgs, p)
		}
	}
	sort.Strings(pkgs)
	fmt.Println(strings.Join(pkgs, "\n"))
	fmt.Printf("(%d user-installed apps)\n", len(pkgs))
	return nil
}

func shell(args []string) error {
	if err := needDevice(); err != nil {
		return err
	}
	return adbLive(append([]string{"shell"}, args...)...)
}

func screenshot(args []string) error {
	if err := needDevice(); err != nil {
		return err
	}
	out := "condor-" + time.Now().Format("20060102-150405") + ".png"
	if len(args) > 0 {
		out = args[0]
	}
	// Android 4.2 has no 'adb exec-out', so capture to the tablet first.
	sh("screencap -p /sdcard/condor-shot.png")
	defer sh("rm /sdcard/condor-shot.png")
	if o, err := adb("pull", "/sdcard/condor-shot.png", out); err != nil {
		return fmt.Errorf("pull screenshot: %s", strings.TrimSpace(o))
	}
	fmt.Println("saved", out)
	return nil
}

func backup(args []string) error {
	if err := needDevice(); err != nil {
		return err
	}
	dst := "condor-files-" + time.Now().Format("20060102-150405")
	if len(args) > 0 {
		dst = args[0]
	}
	os.MkdirAll(dst, 0o755)
	fmt.Println("copying /sdcard to", dst, "(this can take a while)...")
	if err := adbLive("pull", "/sdcard/", dst); err != nil {
		return fmt.Errorf("backup incomplete: %w (re-run to retry)", err)
	}
	fmt.Println("done:", dst)
	return nil
}

func reboot(args []string) error {
	if err := needDevice(); err != nil {
		return err
	}
	a := []string{"reboot"}
	if len(args) > 0 {
		if args[0] != "bootloader" && args[0] != "recovery" {
			return fmt.Errorf("reboot target must be 'bootloader' or 'recovery'")
		}
		a = append(a, args[0])
	}
	return adbLive(a...)
}

// ---------- recon: read-only inspection for the Linux project ----------

func recon() error {
	if err := needDevice(); err != nil {
		return err
	}
	out := "condor-recon-" + time.Now().Format("20060102-150405")
	save := func(name, cmd string) string {
		p := filepath.Join(out, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(p), 0o755)
		s := sh(cmd)
		os.WriteFile(p, []byte(s+"\n"), 0o644)
		return s
	}
	step := func(s string) { fmt.Println("==>", s) }

	step("root check")
	root := "no root"
	if strings.Contains(sh("id"), "uid=0") {
		root = "adb runs as root"
	} else if strings.Contains(sh("su -c id"), "uid=0") {
		root = "su"
	}
	fmt.Println("    " + root)
	// asRoot wraps a command in su when that's how we get root; some files are root-only.
	asRoot := func(cmd string) string {
		if root == "su" {
			return "su -c '" + cmd + "'"
		}
		return cmd
	}

	step("system and kernel")
	props := save("system/getprop.txt", "getprop")
	save("system/build.prop", "cat /system/build.prop")
	kver := save("kernel/version.txt", "cat /proc/version")
	cmdline := save("kernel/cmdline.txt", asRoot("cat /proc/cmdline"))
	for name, cmd := range map[string]string{
		"kernel/cpuinfo.txt": "cat /proc/cpuinfo", "kernel/meminfo.txt": "cat /proc/meminfo",
		"kernel/dmesg.txt": asRoot("dmesg"), "kernel/modules-loaded.txt": "cat /proc/modules",
		"kernel/filesystems.txt": "cat /proc/filesystems", "kernel/devices.txt": "cat /proc/devices",
		"kernel/iomem.txt": "cat /proc/iomem", "kernel/interrupts.txt": "cat /proc/interrupts",
	} {
		save(name, cmd)
	}

	step("storage and partitions")
	save("storage/partitions.txt", "cat /proc/partitions")
	save("storage/mounts.txt", "cat /proc/mounts")
	links := save("storage/dev-block-links.txt", asRoot("ls -lR /dev/block"))
	uevents := save("storage/partition-uevents.txt", "cat /sys/block/mmcblk0/mmcblk0p*/uevent")
	parts := partitionNames(uevents, links)
	var ptable strings.Builder
	for _, p := range parts {
		fmt.Fprintf(&ptable, "%-14s %s\n", p.name, p.dev)
	}
	save("storage/fstab-rootfs.txt", "cat /fstab.*")
	save("storage/df.txt", "df")

	step("display, touch, sound, wifi, buses")
	fb := save("display/fb.txt", "cat /proc/fb")
	save("display/graphics-sysfs.txt", "ls -lR /sys/class/graphics")
	save("display/drm-sysfs.txt", "ls -lR /sys/class/drm")
	save("input/getevent-p.txt", "getevent -p")
	inputs := save("input/devices.txt", "cat /proc/bus/input/devices")
	save("sound/cards.txt", "cat /proc/asound/cards")
	nets := save("wifi/net-sysfs.txt", "ls -l /sys/class/net")
	save("wifi/system-etc-wifi.txt", "ls -lR /system/etc/wifi")
	for _, b := range []string{"i2c", "sdio", "usb", "pci", "platform"} {
		save("bus/"+b+".txt", "ls -l /sys/bus/"+b+"/devices")
	}

	step("pulling driver modules, firmware, kernel config, init scripts")
	for _, d := range []string{"/system/lib/modules", "/lib/modules", "/system/etc/firmware", "/system/vendor/firmware", "/vendor/firmware", "/system/etc/wifi"} {
		if l := sh("ls " + d); l == "" || strings.Contains(l, "No such file") {
			continue
		}
		dst := filepath.Join(out, "pulled", filepath.FromSlash(d))
		os.MkdirAll(dst, 0o755)
		if _, err := adb("pull", d, dst); err == nil {
			fmt.Println("    pulled", d)
		}
	}
	if _, err := adb("pull", "/proc/config.gz", filepath.Join(out, "kernel", "config.gz")); err == nil {
		fmt.Println("    pulled /proc/config.gz (full kernel config)")
	}
	for _, f := range strings.Fields(sh("ls /")) {
		if strings.HasSuffix(f, ".rc") || strings.HasPrefix(f, "fstab") || strings.HasPrefix(f, "ueventd") {
			os.MkdirAll(filepath.Join(out, "pulled", "rootfs"), 0o755)
			adb("pull", "/"+f, filepath.Join(out, "pulled", "rootfs", f))
		}
	}

	if root != "no root" {
		step("backing up boot partitions (read-only dd from the tablet)")
		dump := func(name, ddArgs string) {
			tmp := "/sdcard/condor-" + name + ".img"
			sh(asRoot("dd " + ddArgs + " of=" + tmp + " bs=4096"))
			dst := filepath.Join(out, "dumps", name+".img")
			os.MkdirAll(filepath.Dir(dst), 0o755)
			if _, err := adb("pull", tmp, dst); err == nil {
				if fi, err := os.Stat(dst); err == nil && fi.Size() > 0 {
					fmt.Printf("    %-12s -> dumps/%s.img (%d KB)\n", name, name, fi.Size()/1024)
				}
			}
			sh("rm " + tmp)
		}
		// The first 4 MB of the eMMC hold the partition table and Intel's OSIP boot header.
		dump("mmcblk0-head", "if=/dev/block/mmcblk0 count=1024")
		found := 0
		for _, p := range parts {
			switch strings.ToLower(p.name) {
			case "boot", "recovery", "fastboot", "droidboot", "misc", "osloader", "esp", "panic":
				dump(strings.ToLower(p.name), "if="+p.dev)
				found++
			}
		}
		if found == 0 {
			fmt.Println("    no named boot partitions found; send storage/partition-uevents.txt and dev-block-links.txt")
		}
	} else {
		fmt.Println("    no root: skipping partition dumps (boot.img can come from the stock firmware instead)")
	}

	var sum strings.Builder
	fmt.Fprintf(&sum, "== Condor recon %s ==\n", time.Now().Format(time.RFC1123))
	fmt.Fprintf(&sum, "Kernel:   %s\nAndroid:  %s\nBuild:    %s\nPlatform: %s\nRoot:     %s\nCmdline:  %s\n",
		kver, propOf(props, "ro.build.version.release"), propOf(props, "ro.build.display.id"),
		propOf(props, "ro.board.platform"), root, cmdline)
	fmt.Fprintf(&sum, "\n-- Partitions --\n%s\n-- Framebuffers --\n%s\n\n-- Input devices --\n", ptable.String(), fb)
	for _, l := range strings.Split(inputs, "\n") {
		if strings.HasPrefix(l, "N:") || strings.HasPrefix(l, "H:") {
			sum.WriteString(l + "\n")
		}
	}
	sum.WriteString("\n-- Loaded modules --\n")
	for _, l := range strings.Split(save("kernel/modules-loaded.txt", "cat /proc/modules"), "\n") {
		if f := strings.Fields(l); len(f) > 0 {
			sum.WriteString(f[0] + "\n")
		}
	}
	fmt.Fprintf(&sum, "\n-- Network interfaces --\n%s\n", nets)
	os.WriteFile(filepath.Join(out, "SUMMARY.txt"), []byte(sum.String()), 0o644)
	fmt.Print("\n", sum.String())

	if err := zipDir(out, out+".zip"); err != nil {
		return err
	}
	fmt.Printf("\nDone. Paste %s here; keep %s.zip as your backup.\n", filepath.Join(out, "SUMMARY.txt"), out)
	return nil
}

type partition struct{ name, dev string }

// partitionNames maps partition names to /dev/block nodes, from sysfs uevents
// (PARTNAME=/DEVNAME= pairs) and, failing that, from by-name/by-label symlinks.
func partitionNames(uevents, links string) []partition {
	var parts []partition
	seen := map[string]bool{}
	add := func(name, dev string) {
		if name != "" && dev != "" && !seen[name] {
			seen[name] = true
			parts = append(parts, partition{name, dev})
		}
	}
	var name, dev string
	for _, l := range strings.Split(uevents+"\n", "\n") {
		k, v, _ := strings.Cut(strings.TrimSpace(l), "=")
		switch k {
		case "PARTNAME":
			name = v
		case "DEVNAME":
			dev = "/dev/block/" + strings.TrimPrefix(v, "block/")
		case "MAJOR", "":
			add(name, dev)
			name, dev = "", ""
		}
	}
	add(name, dev)
	for _, l := range strings.Split(links, "\n") {
		if i := strings.Index(l, " -> /dev/block/"); i >= 0 {
			f := strings.Fields(l[:i])
			if len(f) > 0 {
				add(f[len(f)-1], strings.TrimSpace(l[i+4:]))
			}
		}
	}
	return parts
}

func propOf(getprop, key string) string {
	for _, l := range strings.Split(getprop, "\n") {
		if strings.HasPrefix(l, "["+key+"]") {
			_, v, _ := strings.Cut(l, ": ")
			return strings.Trim(v, "[]")
		}
	}
	return ""
}

func zipDir(dir, dst string) error {
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	err = filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(filepath.Dir(dir), p)
		w, err := zw.Create(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		src, err := os.Open(p)
		if err != nil {
			return err
		}
		defer src.Close()
		_, err = io.Copy(w, src)
		return err
	})
	if err != nil {
		return err
	}
	return zw.Close()
}
