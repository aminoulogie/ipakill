# condor-linux: project memory

Turning an old **Condor TRA-901G** tablet into an e-reader running our own Linux userspace,
with our own code written from scratch on top of Condor's kernel. Full plan: `PLAN.md`.
Everything we've learned, with sources: `docs/KNOWLEDGE.md` (read it before suggesting
anything about OS options, firmware or flashing).

## The machine you're running on
- Windows PC, PowerShell. The tablet is attached over **micro-USB**. A WSL Linux environment
  is available but USB devices aren't visible in WSL without `usbipd`, so use Windows tools.
- `condor` CLI (Go, `cli/main.go`) is on PATH after `condor setup`; it wraps adb/fastboot
  from `%USERPROFILE%\.condor\platform-tools` (auto-downloaded). Use `condor` commands
  first; call `adb`/`fastboot` directly from that folder when you need something it lacks,
  then consider adding it as a `condor` subcommand.
- Rebuild the CLI: `cd cli; go build -o condor.exe .; .\condor.exe setup`.

## Device facts (verified)
- Condor TRA-901G, made in Algeria, ~2014. Intel Atom **Z2580** (Clover Trail+, 32-bit x86
  Android build), PowerVR SGX544MP2, 2 GB RAM, 16 GB eMMC + microSD, 8.9" 1920×1200 IPS,
  micro-USB, DC 5V 2A. Ships Android **4.2.2**. Likely an OEM twin of the **Ramos i9**.
- Root: setuid `/system/bin/su` (mgyun, "16 com.mgyun.shua.su") + manager app
  `/data/app/Superuser.apk` (com.mgyun.superuser). Defender blocks the APK on the PC.
- Battery is old; it showed the "battery too low" screen and needed a long charge.
  It boots (Condor logo → Android). Screen is protected by a **pattern lock**.

## Recon findings (2026-10-03)
- Serial CLV13E2A6BD, build `Condor_TRA-901G_20151127`, platform `clovertrail`, ABI x86, API 17.
- Kernel **3.4.34** SMP PREEMPT (built 2015-11-27, gcc 4.6) → Go (needs ≥3.2) and Alpine OK.
- Root: `su` works over adb. `/proc/cmdline` and dmesg need su.
- Display: framebuffer `psbfb` (fb0, Intel MID PowerVR/psb driver), GPU module `sgx`.
  **Native scanout is 1920x1200 landscape** (mode `U:1920x1200p-0`, 32 bpp XRGB: r16 g8 b0,
  stride 7680, one page, fb rotate 0, panel mm unknown), but the tablet is used **portrait**:
  framebuffer x=0 is the top edge, its bottom rows are the left edge. condor-init draws in
  logical portrait 1200x1920 via one setting, `rotation` = Rot90 (`os/condor-init/screen.go`,
  override in /data/condor/rotation). Goodix touch reports native X 0–1920, Y 0–1200 (protocol
  B, slots 0–254), so touch maps through the same setting.
  Backlight: /sys/class/backlight/psb-bl, max 100.
- Touch: **Goodix Capacitive TouchScreen** (event2). Keys: `gpio-keys` (event1, volume),
  `mid_powerbtn` (event3). Accelerometer `bma250` (event0). Audio jack event4.
- Wi-Fi/BT: Broadcom **bcmdhd** (SDIO, mmc2, wlan0 + p2p0), `cfg80211`, `bcm_bt_lpm`.
  Modules live in `/lib/modules` (ramdisk); firmware in `/system/etc/firmware`, `/system/etc/wifi`.
- Cameras: ov5645 (rear), ov2675 (front) via atomisp.
- Storage: ~11 GB /data (internal storage shares /data). No `/dev/block/by-name`.
- Windows adb returns `\r\r\n` from this tablet; the CLI strips every `\r`.

## Boot chain findings (2026-10-03, verified)
- GPT on mmcblk0: reserved(p1, sector 40) panic factory misc config cache logs system data.
  Identical sector layout to the Ramos i9 `partition.tbl` (p4 is "spare" there).
- **No boot/recovery/fastboot partitions.** Sector 0 holds Intel's OSIP header; its 4 entries
  point into `reserved`: fastboot 4050 (20164 blocks, attr 0x0E), recovery 26050 (18530, 0x0C),
  splash 48050 (1642, 0x04), boot 70050 (15014, 0x00). Load 0x01100000, entry +0x1000.
- Image layout: 480-byte signature block (Intel manifest + RSA, records payload size) +
  4 KB header (cmdline, kernel size, ramdisk size) + 4 KB bootstub + bzImage + gzip ramdisk.
  Flash-tool files add a 512-byte single-entry OSIP sector in front. `condor bootimg` handles
  both; all tablet and Ramos images repack byte-identical.
- droidboot 0.5 (`product: clovertrail`): **`fastboot boot` is disabled** ("boot command
  stubbed on this platform"), `getvar all` returns nothing. Windows needs the Google driver's
  "Android Bootloader Interface" bound manually to USB\VID_8087&PID_09EF.
- Read raw eMMC without writing anything: `su -c 'hd -b <byte offset> -c <bytes> /dev/block/mmcblk0'`
  through `cmd /c ... > file` (PowerShell adds a BOM), decoded and checksum-verified on the PC.
- Dumps of boot/recovery/fastboot from this tablet: `C:\Users\pro\condor-backup\dumps\`.
- `/sdcard/update.zip` = full Ramos i9 Intel flash-tool package (PROD IFWI, DnX, flash.xml);
  its flash.xml **erases factory**. `/sdcard/safeupdate.zip` = DigiflipPro XT911 ROM that also
  writes IFWI: don't use.

## Decisions already made (don't re-litigate)
- Windows 8/10, Kindle OS, Android 5+: **impossible** on this chip (see KNOWLEDGE.md §2).
- Plan: keep Condor's kernel + drivers, replace everything above it. Current direction is
  **"terminal first, then write our own code from scratch"**: our own `/init`, our own
  framebuffer UI and EPUB reader in **Go** (`GOOS=linux GOARCH=386`), developed first on the
  PC with a fake-screen window, then on `/dev/fb0` + `/dev/input/event*` on the tablet.
- ~~Our Linux image goes in the recovery slot~~: impossible, the firmware rejects any
  modified boot/recovery image. New route: keep the signed kernel + ramdisk, hook early boot
  from **/system**, stop Android's zygote/surfaceflinger, start our own userspace.
- Fallback ladder in PLAN.md if a phase is blocked.

## Current status
- [x] Phase 0: `condor` CLI built (doctor, info, books, install, apps, shell, screenshot, recon, reboot, setup, term)
- [x] Tablet charges and boots to Android
- [x] USB data works (needed reseating/another cable; first tries gave "Device Descriptor Request Failed")
- [x] adb already enabled + PC authorized, works through the pattern lock; **su root available**
- [x] First `condor recon` (2026-10-03): kernel, modules, firmware pulled. Partition dumps
      were skipped because /dev/block/by-name doesn't exist. Fixed in CLI; re-run recon.
- [x] `condor backup`: internal storage (1.55 GB) in `C:\Users\pro\condor-backup\sdcard`
      (Defender quarantined `rootauthor_8.1.1.3.apk`). microSD backup skipped by the user.
- [x] Boot/recovery/fastboot dumped (read-only) and partition table + OSIP mapped (see above).
      `condor recon` fixed to dump OSIP images, reserved, factory, eMMC boot areas, and to read
      root-only init*.rc/fstab via su; not re-run yet.
- [x] `condor bootimg info|unpack|pack`: byte-identical repacks verified
- [x] `fastboot boot`: not available on this droidboot
- [x] Signature test (2026-10-03): **signatures are enforced.** Recovery slot (sector 26050)
      overwritten via `dd` with a 1-byte-changed cmdline (same size): `reboot recovery` fell
      back to droidboot ("RESULT: OKAY", no error text). Control with the original image:
      stock recovery booted ("Aucune commande"). Original restored and verified; sector 0,
      boot and fastboot verified unchanged before and after. → No custom kernel/ramdisk.
- [x] No factory reset (root is too valuable). Pattern lock removed instead: gesture.key deleted,
      locksettings password_type/autolock set to 0; originals on microSD `condor/` + PC `condor-backup\lock`.
      Safety copy of Superuser.apk on microSD `condor/`.
- [x] /system backed up: `condor-backup\dumps\system.img` + microSD `condor/system.img`
      (md5 dd3e7835e36b8120435a62b099ed404a, equals the partition)
- [x] Takeover hook installed: `/system/etc/install-recovery.sh` (stock flash_recovery service
      runs it as root at class main). Verified harmless when not armed.
- [x] **Milestone 1** (2026-10-03): `condor takeover arm` + reboot → zygote/system_server
      stopped, `/data/condor/condor-init` (Go, linux/386) as root draws a test pattern on
      psbfb (colours correct, but drawn in native coordinates it appeared rotated 90°) and
      serves a root shell on 127.0.0.1:2323 (`adb forward`).
      adbd keeps running. Backlight: /sys/class/backlight/psb-bl (was 23).
- [x] Display fix: the panel scans out the read()/write() view of fb0; the mmap view is shifted
      299 rows and ends early. condor-init draws into a RAM back buffer and flushes dirty rows
      with write(); verified by read-back. Backlight set to 80/100 at start.
- [x] `condor takeover push <bin>` / `restart` (restart = touch trigger + `start flash_recovery`,
      so init launches it detached; `su ... &` doesn't survive).
- [x] **Milestone 2** (2026-10-03): touch works. Goodix event2, protocol B, raw X 0..1920 /
      Y 0..1200 in native fb orientation, mapped through the same Rot90 → strokes follow the
      finger. Follow-up: some touch-downs arrive with raw Y=0 in their first frame (logged as
      logical x=1199) → stray dot at the right edge; wait for both axes before drawing.
- [x] `condor term` (2026-10-03): bridges to condor-init's root shell (127.0.0.1:2323) via
      `adb forward`; requires takeover mode (condor-init running). Windows sends CRLF; term.go
      strips `\r` from stdin or every command arrives as "id\r" (": not found"). `cli/term.go`.
- [x] Clock sync (2026-10-03): tablet boots at **2013** (no RTC battery), breaking apk/TLS.
      `condor shell "su -c 'date -s YYYYMMDD.HHMMSS'"` with the arg as **local** time (tablet is
      fixed CET = UTC+1, no DST), so feed UTC+1h. Verify with `date +%s` (CLOCK_REALTIME, == PC)
      or the `date -u` string; **`date -u +%s` is a toolbox bug** (double-applies the offset, reads
      1h off). Resets on every reboot → re-run each takeover (candidate to automate in condor-init).
- [ ] Alpine next: minirootfs (x86) onto microSD, chroot from takeover (apk needs the clock set).
- [ ] Milestone 3: real text with a font
- [ ] Reader code: `os/` folder, PC fake screen, milestone 1 (pixels + text)

Update this checklist as things are done.

## Safety rules (always)
1. **Never write to the tablet without explicit approval for that exact command**:
   `fastboot flash|erase|format|oem|update`, `dd of=/dev/block/...`, wipe/reset actions.
   Reading (`getvar`, `dd if=`, `pull`, `getprop`) is fine.
2. Before the first flash: we must have the **stock TRA-901G 4.2.2 firmware** + Intel
   flash tool downloaded and a recon backup (`condor-recon-*.zip`) saved off the tablet.
3. `fastboot boot` is unavailable here. Don't use `fastboot flash` (it rewrites the OSIP in
   sector 0). Write only the **recovery** OSIP slot, with `dd` from Android at the exact OSIP
   offset, same size, read back and verified. Never touch sector 0, boot or fastboot slots.
4. Explain risk in plain words before anything that could fail to boot.
5. The user is a beginner-to-intermediate: short steps, exact commands, PowerShell syntax.

## Conventions
- Go code in `cli/` (Windows tool) and `os/` (tablet userspace). `gofmt`, `go vet` before commit.
- Commit to the `condor-linux` branch. No model names in commits.
