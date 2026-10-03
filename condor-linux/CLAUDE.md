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
- Battery is old; it showed the "battery too low" screen and needed a long charge.
  It boots (Condor logo → Android). Screen is protected by a **pattern lock**.

## Recon findings (2026-10-03)
- Serial CLV13E2A6BD, build `Condor_TRA-901G_20151127`, platform `clovertrail`, ABI x86, API 17.
- Kernel **3.4.34** SMP PREEMPT (built 2015-11-27, gcc 4.6) → Go (needs ≥3.2) and Alpine OK.
- Root: `su` works over adb. `/proc/cmdline` and dmesg need su.
- Display: framebuffer `psbfb` (fb0, Intel MID PowerVR/psb driver), GPU module `sgx`.
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
- Our Linux image goes in the **recovery** partition; Android stays in **boot** (safe dual boot).
- Fallback ladder in PLAN.md if a phase is blocked.

## Current status
- [x] Phase 0: `condor` CLI built (doctor, info, books, install, apps, shell, screenshot, recon, reboot, setup)
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
- [ ] Signature test: same-size 1-byte cmdline change written to the recovery OSIP slot with
      `dd` from Android (never sector 0, boot or fastboot), then `condor reboot recovery`
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
