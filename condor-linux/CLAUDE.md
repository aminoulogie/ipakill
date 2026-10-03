# condor-linux: project memory

Turning an old **Condor TRA-901G** tablet into its own little Linux machine: it boots straight
into our userspace (Alpine Linux + our Go `condor-init`) on top of Condor's signed kernel, with
Wi-Fi, SSH and a console on its screen. Plan and what's next: `PLAN.md`. Everything learned,
with sources: `docs/KNOWLEDGE.md` (read it before suggesting anything about OS options,
firmware or flashing). **Start with "How the system works" below.**

## How the system works (2026-10-03)
```
power on → signed kernel + ramdisk (stock, can't change) → Android init
  → init runs /system/etc/install-recovery.sh as root (service flash_recovery, class main)
      = our hook (cli/takeover.go: takeoverHook). If /data/condor/autostart (every boot) or
        /data/condor/takeover (one-shot) exists: stop bootanim + zygote, exec condor-init.
        Crash-loop guard: /data/condor/bootfail counter; at 3 the hook boots Android and
        deletes autostart. condor-init clears it after 25 s (bootguard.go).
  → /data/condor/condor-init (os/condor-init, Go, linux/386, runs as root, logs to init.log)
      - fixes the clock (no RTC battery: floor = its own install mtime), hostname "condor"
      - screen: Linux console on fb0 (console.go + vt/), Go Mono, 73x57 cells, portrait
      - shell: Alpine login shell in a chroot of /data/alpine on a pty (pty_linux.go),
        with /proc /sys /dev /tmp(tmpfs) /system(bind) mounted (alpine_linux.go)
      - alpineBoot: writes Alpine config (prompt, proxy env, /usr/local/bin/wifi), runs
        `wifi boot` (rejoin saved network), starts sshd if openssh is installed
      - ports (all 127.0.0.1 except sshd):
          2323 console: `condor term` joins the screen's session (PC + tablet share it)
          2324 plain root shell (no pty), back door for tools
          2325 tunnel: `condor net` control/data connections (internet over USB)
          3128 HTTP proxy: via the PC when condor net runs, else direct over Wi-Fi
          22   sshd inside Alpine (keys only), reachable over Wi-Fi
  adbd keeps running the whole time, so the PC can always fix things over USB.
```
- On the tablet: `/data/condor/` (condor-init, autostart, bootfail, hook.log, init.log, shrc),
  `/data/alpine/` (Alpine 3.24 x86 root; `/etc/condor/wpa.conf` = saved Wi-Fi network).
- Without Alpine installed, the console falls back to Android's `/system/bin/sh`; if the
  fonts fail, to the `ui` launcher; if the console fails entirely, the touch test screen.

## Daily workflow (PowerShell on the PC)
```
.\condor-linux\dev.cmd           build condor-init for linux/386, push + restart (or arm + reboot)
condor term                      the tablet's console from the PC (Ctrl+] leaves)
ssh root@<tablet ip> / condor ssh  same over Wi-Fi (keys set up by condor ssh setup)
condor alpine run <cmd>          run one command inside Alpine (exit code checked)
condor net                       internet over USB for the tablet (leave running)
condor takeover status           hook, autostart, condor-init md5, last log lines
condor takeover auto on|off      boot into condor every time / back to Android
condor takeover install-hook     update the /system hook (one file, md5-checked, ro after)
condor reboot
```
Rebuild the CLI after pulling CLI changes: `cd cli; go build -o condor.exe .; .\condor.exe setup`
(close `condor term` windows first, the exe is locked while they run).
GitHub is often unreachable from this PC: pull with a retry loop, or use a git bundle.

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
- **Wi-Fi works in takeover mode (2026-10-03)**: `iw` scans fine, but Alpine's
  wpa_supplicant 2.11 fails on this kernel's nl80211 ("Could not allocate genl cache") and
  wext scans fail. Condor's `/system/bin/wpa_supplicant -Dnl80211` works from inside Alpine
  (/system is bind-mounted); it runs as uid 1010 (wifi), so its config + socket dir must be
  owned by 1010 and `wpa_cli` needs `umask 0`. busybox `udhcpc` gets the lease. All of this is
  the `wifi` command (`os/condor-init/wifi.go`); the saved network reconnects at boot.

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
  modified boot/recovery image. Route in use: keep the signed kernel + ramdisk, hook early
  boot from **/system**, stop Android's zygote, run condor-init + Alpine (chroot).
- The user wants a **Linux-style system** (console, terminal-first, Arch-like "build it
  yourself" with apk), not an iPad-style UI. Alpine, not Arch (old kernel; Alpine's musl and
  busybox are fine with 3.4).
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
- [x] Clock: tablet boots at **2013** (no RTC battery), which breaks TLS. condor-init sets the
      clock forward to its own install time at start (timefix_linux.go); `condor takeover push`
      refreshes that. Manual alternative: `date -s` takes **local** time (fixed CET = UTC+1);
      `date -u +%s` is a toolbox bug (1 h off), verify with `date +%s`.
- [x] Text + launcher (`os/condor-init/ui`, Go fonts): status bar, app list. Kept as a fallback;
      the user preferred a Linux console. `go run ./cmd/ui-preview` renders it to PNG.
- [x] **Linux console on the screen** (console.go, vt/): pty shell, VT100/xterm subset, colours,
      Go Mono; `condor term` shares the session (raw mode on Windows, VT output, Ctrl+] leaves).
      Known: htop etc. render for 73x57, so a smaller PowerShell window scrolls; condor-init
      redraws glyphs from the font each time (CPU-heavy under htop; a glyph cache would fix it).
- [x] **Alpine 3.24 (x86)** in /data/alpine: `condor alpine install` downloads the minirootfs
      (sha256-checked), `condor-init untar` unpacks it (Android has no tar). Console = Alpine
      login shell. apk uses http mirrors through condor-init's proxy.
- [x] **Internet over USB**: `condor net` (no adb reverse on 4.2: reverse tunnel, see tunnel.go).
- [x] **Autostart**: `condor takeover auto on` → every boot goes into condor, with the
      crash-loop fallback. Needed `install-hook` (the old one-shot hook ignored autostart).
- [x] **Wi-Fi**: `wifi scan|connect|status|off|forget|debug` (see Recon findings for why it uses
      Condor's wpa_supplicant). Rejoins at boot; give it ~30-40 s after boot. Proxy retries once.
- [x] **SSH over Wi-Fi**: `condor ssh setup` (openssh, PC's ed25519 key, sshd), sshd at boot.
      Don't run `setup-alpine` (it's for real installs).
- [x] **On-screen keyboard** (keyboard.go): bottom 616 px, console above it (73x38), terminal
      layout (esc tab ctrl alt arrows / numbers / letters / sym layer), one-shot shift/ctrl/alt,
      commits on finger lift, bksp/arrows repeat; `hide` gives the console the full screen
      (73x56), any tap brings it back; the pty is resized (SIGWINCH) both ways.
- [x] Daily-use basics (2026-10-03): glyph cache (glyphs.go), status bar with time, Wi-Fi
      address and battery (bar.go; battery = cw2015_battery, charger = smb347-mains/usb),
      power button toggles the screen (backlight 0 + FBIOBLANK powerdown; touches ignored
      while off), volume keys = brightness (power.go), NTP at boot after Wi-Fi (busybox ntpd),
      shell TZ=WAT-1.
- [x] **Launcher + settings** (pages.go, screens.go): boots to the launcher (terminal,
      settings, books "coming soon"); "≡ condor" corner of the status bar = home from anywhere;
      the terminal keeps running in the background. Settings: brightness, auto screen-off
      (never/1/5/10 min, idleLoop), Wi-Fi status + reconnect, battery, system, power (restart,
      power off, android = autostart off + reboot; two taps; via `setprop sys.powerctl`).
      Saved in /data/condor/settings.json. Ready-made launchers don't fit: they need X/Wayland
      or a GPU, and fbdev mmap is shifted 299 rows on this panel.
- [x] **Books** (reader.go, epub/): EPUB parsing ported from Soma's reader
      (aminoulogie/kite-bay-otter-topaz src/lib/epub.ts: container.xml → OPF, percent-decoded
      paths, linear="no" skipped, dc: metadata, 3 cover conventions; Soma's resolvePath tests
      ported). Shelf scans /data/media/0/Books, microSD Books, /data/alpine/root/books,
      /data/condor/books. Paged reader: Soma's themes (night default, paper, sepia), A-/A+,
      line height 1.7, tap left third = back, else forward, volume keys turn pages; progress
      per book as (chapter, first word) in /data/condor/books.json. Soma itself can't run here
      (React web app; no browser engine on the framebuffer).
- [ ] microSD bind into Alpine; update over Wi-Fi

Update this checklist as things are done.

## Safety rules (always)
1. **Never write to the tablet without explicit approval for that exact command**:
   `fastboot flash|erase|format|oem|update`, `dd of=/dev/block/...`, wipe/reset actions.
   Reading (`getvar`, `dd if=`, `pull`, `getprop`) is fine.
2. Before the first flash: we must have the **stock TRA-901G 4.2.2 firmware** + Intel
   flash tool downloaded and a recon backup (`condor-recon-*.zip`) saved off the tablet.
3. `fastboot boot` is unavailable here. Don't use `fastboot flash` (it rewrites the OSIP in
   sector 0). Never touch sector 0 or the boot/fastboot/recovery OSIP slots: the firmware
   enforces signatures, so changing them gains nothing. The only /system change is the hook
   file, written with `condor takeover install-hook` (md5-checked, /system back to ro).
   /system backup: `C:\Users\pro\condor-backup\dumps\system.img` (+ microSD `condor/`).
4. Explain risk in plain words before anything that could fail to boot.
5. The user is a beginner-to-intermediate: short steps, exact commands, PowerShell syntax.

## Conventions
- Go code in `cli/` (Windows tool) and `os/` (tablet userspace). `gofmt`, `go vet` before commit.
- Commit to the `condor-linux` branch. No model names in commits.
