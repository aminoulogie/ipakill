# Plan: our own Linux on the Condor TRA-901G

Goal: tablet boots into a minimal Linux we built ourselves, with screen, touch and Wi-Fi
working, that opens straight into an EPUB reader, like a Kindle.

## The big idea

We don't write drivers. Android already runs a Linux kernel that has every driver this
tablet needs. We keep that kernel and replace everything above it:

```
 ┌──────────────────────────────┐        ┌──────────────────────────────┐
 │ Android apps                 │        │ KOReader (EPUB reader)       │
 │ Android framework (Java)     │  ───►  │ Xorg + on-screen keyboard    │
 │ Android init + /system       │        │ Alpine Linux (OpenRC, musl)  │
 ├──────────────────────────────┤        ├──────────────────────────────┤
 │ Linux 3.x kernel + drivers   │  keep  │ same kernel + same drivers   │
 │ Intel bootloader (droidboot) │  keep  │ same bootloader              │
 └──────────────────────────────┘        └──────────────────────────────┘
```

Why Alpine: the kernel is ~3.x. systemd (Arch, Debian, Ubuntu, Fedora) won't run on it.
Alpine uses OpenRC and works on old kernels. postmarketOS, which is built on Alpine,
already ported the Asus Zenfone 5, which has the same Intel chip family, using this exact
approach. We use its tooling (`pmbootstrap`) and the Zenfone port as a template.

## How this tablet boots (Intel Clover Trail+)

```
power on → chip ROM → IFWI firmware → droidboot (fastboot) → boot.img → kernel → /init
                                                     └─ Vol keys → recovery.img
```

- `boot.img` is **Intel's own format** (OSIP header + cmdline + bootstub + bzImage +
  initrd), not the standard Android one. We need Intel-aware unpack/repack tools.
- **Safe dual-boot trick:** put our Linux in the **recovery** partition and leave Android
  in **boot**. A normal power-on starts Android; holding the recovery key combo starts
  Linux. If our image is broken, the tablet still boots Android.
- **Last-resort unbrick:** flash the official Condor 4.2.2 firmware with Intel's
  Manufacturing/Platform Flash Tool. The chip's USB recovery mode makes this almost
  always possible.

## Phases

Each phase has a clear "done when" test. We don't move on until it passes.

| # | Phase | Done when | Risk |
|---|---|---|---|
| 0 | **Tools**: `condor.exe` CLI | `condor doctor` says OK | none ✅ built |
| 1 | **Recon**: read hardware + back up | `condor recon` SUMMARY.txt collected | none |
| 2 | **Boot gate**: can we boot our own images? | ❌ **no**: signatures enforced (see below) | done |
| 3 | **Hello userspace** via /system hook (replaces "hello initramfs") | shell over USB from our own process, test pattern on fb0, Android stopped | low–medium |
| 4 | **Root filesystem**: Alpine on microSD | `ssh user@172.16.42.1` gives an Alpine shell | low |
| 5 | **Display**: framebuffer, then Xorg | text console and an xterm visible on screen | medium |
| 6 | **Touch + buttons** | tapping moves the pointer correctly, power/volume keys work | medium |
| 7 | **Wi-Fi** | `apk update` works over Wi-Fi | medium |
| 8 | **Reader UI**: boots into KOReader | power on → bookshelf, opens an EPUB, page turns by tap | medium |
| 9 | **Polish**: backlight, battery %, sleep, rotation | usable daily as an e-reader | low |

### Phase 1: Recon (next step)
- Run `condor recon` and read `SUMMARY.txt`.
- Learn: kernel version, which partitions exist (`boot`, `recovery`, ...), loaded modules
  (touch, Wi-Fi), framebuffer name, Wi-Fi chip (likely Broadcom `bcmdhd` or TI `wl12xx`),
  whether the kernel config is available, root status.
- Get **boot.img + recovery.img**: dumped from the tablet if rooted, or extracted from the
  official TRA-901G 4.2.2 firmware package (we keep this package forever as the unbrick kit).

### Phase 2: Boot gate (the go/no-go)
1. Unpack boot.img → kernel, ramdisk, cmdline. Repack it **unchanged**.
2. `condor reboot bootloader`, then `fastboot boot repacked.img` (boots from RAM, writes nothing).
3. Android starts → our tooling works and the bootloader accepts our images. **Go.**
4. If `fastboot boot` isn't supported by Condor's droidboot: flash it to **recovery** instead
   (`fastboot flash recovery`), and boot it with the key combo. Android stays untouched.
5. If the bootloader rejects any modified image (signature check), stop and reassess.
   Options would be Ramos i9 firmware, whose bootloader may be unlocked, or Linux-inside-Android.

New CLI commands: `condor bootimg unpack|pack`, `condor testboot <img>`.

**Result (2026-10-03): blocked.** `fastboot boot` is stubbed in droidboot, and the firmware
verifies the signed manifest of boot/recovery: a recovery with a single changed cmdline byte,
written to its OSIP slot with dd, silently falls back to droidboot, while the original boots.
So the kernel, its cmdline and the ramdisk (`/init`, `init.rc`) are fixed. The recovery-slot
dual boot is off the table.

### New route: take over from /system (stock kernel kept)
Android's signed ramdisk mounts `/system` (ext4, not verified on 4.2) and starts services
from it. We add an early hook there, guarded by a trigger so Android still boots normally
without it. When triggered, the hook stops zygote/surfaceflinger and starts our own
userspace: first a shell over USB and a test pattern on `/dev/fb0`, later Alpine on the
microSD (chroot instead of switch_root) and our reader. Details and exact writes get
planned and approved before anything is changed; `/system` is backed up first.

### Phase 3: Hello initramfs
A ~2 MB initramfs: busybox and a hand-written `/init` script that:
1. mounts `/proc`, `/sys`, `/dev`
2. loads Condor's modules (`insmod` from the files pulled in Phase 1)
3. turns the USB port into a network card (`/sys/class/android_usb/android0` → `rndis`)
4. gives itself `172.16.42.1` and starts `telnetd`

On the PC, Windows sees a new "RNDIS" network adapter, and `telnet 172.16.42.1` gives a shell.
**Our own code is now running on the tablet.** If the screen stays black, that's expected here.

### Phase 4: Root filesystem on microSD
- Partition the microSD: `ext4`, label `condor-root`.
- Build Alpine (x86) with `pmbootstrap` using a new device package
  `device-condor-tra901g` (copied from `device-asus-t00f`, the Zenfone 5 port).
- `/init` mounts the microSD and `switch_root`s into Alpine. OpenRC starts, then `sshd`.
- Kernel modules + firmware from Android go into `/lib/modules` and `/lib/firmware`.

### Phase 5: Display
- Find the framebuffer (`/dev/fb0`). Show a text console on it (`fbcon`, or `fbterm` if the
  kernel lacks fbcon).
- Xorg with the `fbdev` driver, software rendering. The PowerVR GPU stays unused: its
  drivers only work with Android's libraries. Software rendering is fine for reading.
- Risk: some Intel MID display drivers only light the panel after Android's graphics
  service talks to them. Workaround: poke the same sysfs/ioctls Android does (strace from Android).

### Phase 6: Touch and buttons
- Touch is an I2C device (from recon: `getevent -p`) → appears as `/dev/input/eventN`.
- Xorg `evdev`/`libinput` driver. Fix the axes with a transformation matrix if
  taps land in the wrong place or rotated.
- Power and volume keys via `gpio-keys`: volume = page turn, power = screen off.

### Phase 7: Wi-Fi
- Load the Wi-Fi module with the firmware + nvram files from `/system/etc/firmware` and
  `/system/etc/wifi` (exact names come from Android's `init.*.rc`, pulled in Phase 1).
- `wpa_supplicant` (nl80211 or wext, depending on the driver) + `udhcpc`.
- Add `condor wifi <ssid>` to the CLI to write the Wi-Fi config over USB.

### Phase 8: Reader UI (the Kindle part)
- Auto-login → minimal X session → **KOReader** fullscreen, no desktop.
- `condor books` copies books straight to the Linux side over USB networking (scp).
- Risk: KOReader doesn't ship official 32-bit x86 Linux builds; we'd build it from source
  (it supports Linux SDL). Fallback readers: `foliate` (GTK, heavier) or `fbreader`.

### Phase 9: Polish
- Backlight slider (`/sys/class/backlight`), battery % (`/sys/class/power_supply`).
- Power button: screen off + CPU idle. Real suspend on downstream Intel MID kernels is
  often broken, so we blank the screen instead.
- Rotation (portrait for reading), on-screen keyboard (`onboard`/`matchbox-keyboard`).
- Package everything: one `condor flash-linux` command that builds and installs it.

## Fallback ladder

If we get stuck, each lower option is easier and still gives something useful:

1. Full Linux on microSD, booted from recovery (the plan above)
2. Linux without our own display: run Alpine, but drive the screen from Android
3. Linux inside Android: chroot + VNC (needs root, no boot changes at all)
4. Android 4.2 cleaned up with an EPUB reader (`condor install`, `condor books`)

## What could stop us, and when we'll know

| Blocker | Found in |
|---|---|
| Bootloader rejects modified images | Phase 2: **confirmed**, route changed to /system hook |
| No root and no stock firmware package available to get boot.img | Phase 1 |
| Display driver needs Android's graphics stack | Phase 5 |
| Wi-Fi driver needs Android-only services | Phase 7 |
| Battery is dead and won't hold a charge | now (needs a replacement battery) |

## Time estimate

Phases 1–4: a few evenings. Phases 5–7: one to three weeks of trial and error, mostly
waiting for your test results. Phases 8–9: a few evenings.
