# condor-linux

Boot a from-scratch Linux (Alpine x86, Arch-style minimal build) on a **Condor TRA-901G**
tablet (Intel Atom Z2580 "Clover Trail+", 2 GB RAM, 8.9" 1920×1200, Android 4.2.2),
reusing Condor's own kernel, which already contains the touch, display and Wi-Fi drivers.
Android stays on internal storage; Linux runs from microSD.

## Plan

| Phase | What | Risk |
|---|---|---|
| 1 | `recon.sh`: inspect tablet, pull modules/firmware, back up boot partitions | none (read-only) |
| 2 | `fastboot boot` a test image from RAM, which tells us if the bootloader accepts our images | none (nothing written) |
| 3 | Custom initramfs, Alpine rootfs on microSD, USB serial/SSH shell | none |
| 4 | Display console, then Xorg (fbdev) | none |
| 5 | Touch (evdev) + Wi-Fi (Condor modules + firmware + wpa_supplicant) | none |
| 6 | KOReader for EPUBs | none |

Why Alpine, not Arch: Condor's kernel is old (3.x). Modern systemd (Arch, Debian, Fedora)
refuses to boot on it; Alpine (OpenRC + musl) doesn't care.

## Phase 1: run it

On a Linux PC (or WSL with usbipd):

```
sudo apt install adb fastboot        # Arch: sudo pacman -S android-tools
./recon.sh                           # read-only inspection
./recon.sh --fastboot                # also check the bootloader (reboots the tablet once)
```

On the tablet: Settings → About tablet → tap *Build number* 7× → Developer options →
USB debugging ON, then accept the prompt when you plug in.

Output: `condor-recon-<date>/SUMMARY.txt` (send this) and a `.tar.gz` backup to keep.
