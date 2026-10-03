# Plan: our own Linux on the Condor TRA-901G

Goal: the tablet is its own small Linux machine. It boots straight into a minimal system we
built (Alpine Linux plus our own Go program, `condor-init`), with the screen, touch, Wi-Fi
and SSH working, and grows from there: on-screen keyboard, an EPUB reader, and whatever else
we build on top.

## The idea

We don't write drivers. Android runs a Linux kernel that already has every driver this tablet
needs. We keep that kernel and replace everything above it:

```
 ┌──────────────────────────────┐        ┌──────────────────────────────────────────┐
 │ Android apps + framework     │  ───►  │ Alpine Linux 3.24 (apk, busybox, sshd)   │
 │ (zygote, system_server, ...) │        │ condor-init: console on the screen,      │
 │                              │        │ touch, Wi-Fi, internet over USB, boot    │
 ├──────────────────────────────┤        ├──────────────────────────────────────────┤
 │ Android init + /system       │  keep  │ same (our hook in /system starts us)     │
 │ Linux 3.4 kernel + drivers   │  keep  │ same kernel, same drivers                │
 │ Intel bootloader (droidboot) │  keep  │ same bootloader                          │
 └──────────────────────────────┘        └──────────────────────────────────────────┘
```

Why Alpine: the kernel is 3.4. systemd-based distributions (Arch, Debian, Fedora) won't run
on it; Alpine's musl and busybox are fine. It's minimal and "build it yourself" with `apk`.

## What we tried, what's fixed, and the route that works

1. **Own kernel/ramdisk in the recovery slot (original plan): impossible.** The firmware
   verifies signed manifests on the boot/recovery images (tested 2026-10-03 with a one-byte
   change), and `fastboot boot` is disabled. The kernel, its cmdline and the ramdisk are fixed.
2. **Take over from /system (in use).** The signed ramdisk's init runs
   `/system/etc/install-recovery.sh` as root, and stock /system doesn't ship that file. Ours
   stops Android's zygote and starts `condor-init`. With autostart on, that's every boot; a
   crash-loop guard falls back to Android after 3 failed starts. Details: CLAUDE.md, "How the
   system works".

## Done (2026-10-03)

| # | Milestone | Where |
|---|---|---|
| 0 | `condor` CLI (Windows): adb wrapper, recon, backups, boot image tools | `cli/` |
| 1 | Recon + backups: boot/recovery/fastboot dumps, OSIP map, /system image | CLAUDE.md |
| 2 | Signature test: our own kernel/ramdisk is impossible → /system route | CLAUDE.md |
| 3 | Takeover hook + `condor-init`: framebuffer, test pattern, root shell over USB | `os/condor-init` |
| 4 | Display fixed (read/write view, 299-row mmap shift), portrait rotation | `screen.go`, `fb_linux.go` |
| 5 | Touch (Goodix, protocol B, same rotation) | `touch.go`, `input_linux.go` |
| 6 | Text with real fonts + a simple launcher (kept as fallback) | `ui/` |
| 7 | Linux console on the screen, shared with `condor term` | `console.go`, `vt/` |
| 8 | Alpine 3.24 in a chroot, `apk` | `alpine*.go`, `untar.go`, `cli/alpine.go` |
| 9 | Internet over USB (`condor net`, reverse tunnel + proxy) | `tunnel.go`, `cli/net.go` |
| 10 | Boot into condor every time, with crash-loop fallback | `cli/takeover.go`, `bootguard.go` |
| 11 | Wi-Fi with Condor's wpa_supplicant, rejoins at boot | `wifi.go`, `proxy.go` |
| 12 | SSH over Wi-Fi (keys only), sshd at boot | `cli/ssh.go`, `alpine.go` |
| 13 | One-step dev loop: `dev.cmd` builds, pushes, restarts | `dev.ps1` |
| 14 | On-screen keyboard under the console (hide/show, resizes the shell) | `keyboard.go` |
| 15 | Status bar, power button (screen off/on), volume = brightness, glyph cache, NTP | `bar.go`, `power.go`, `glyphs.go` |

## Next

| # | Step | Done when | Notes |
|---|---|---|---|
| E | **Books app** (the original goal) | open an EPUB from /data/alpine/root/books or the microSD, page through it by tap/volume keys | Go: EPUB = zip + XHTML; render with the `ui` text code; remember the page. |
| G | microSD in Alpine | the card mounted at /mnt/sd | It's `/storage/sdcard_ext` in Android's namespace; bind it in. |

## Fallback ladder (if something breaks)

1. A bad `condor-init` build: `dev.cmd` again with a fix; if the tablet can't start it 3 times
   in a row, it boots Android and turns autostart off by itself.
2. Back to Android on purpose: `condor takeover auto off`, then `condor reboot`.
3. Remove our hook entirely: delete `/system/etc/install-recovery.sh` (remount /system rw).
4. Full restore: `/system` image backed up in `C:\Users\pro\condor-backup\dumps\system.img`.
