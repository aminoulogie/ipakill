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
- [ ] Back up user files (MTP / microSD), since the pattern lock blocks adb
- [ ] Factory reset (recovery menu), photograph the boot/droidboot menu
- [ ] Enable USB debugging → `condor doctor` → `condor recon` → read SUMMARY.txt
- [ ] Phase 2 boot gate: unpack/repack boot.img, `fastboot boot` unchanged image
- [ ] Reader code: `os/` folder, PC fake screen, milestone 1 (pixels + text)

Update this checklist as things are done.

## Safety rules (always)
1. **Never write to the tablet without explicit approval for that exact command**:
   `fastboot flash|erase|format|oem|update`, `dd of=/dev/block/...`, wipe/reset actions.
   Reading (`getvar`, `dd if=`, `pull`, `getprop`) is fine.
2. Before the first flash: we must have the **stock TRA-901G 4.2.2 firmware** + Intel
   flash tool downloaded and a recon backup (`condor-recon-*.zip`) saved off the tablet.
3. Prefer `fastboot boot <img>` (RAM only) over flashing. Flash **recovery**, never **boot**,
   unless the user explicitly decides otherwise.
4. Explain risk in plain words before anything that could fail to boot.
5. The user is a beginner-to-intermediate: short steps, exact commands, PowerShell syntax.

## Conventions
- Go code in `cli/` (Windows tool) and `os/` (tablet userspace). `gofmt`, `go vet` before commit.
- Commit to the `condor-linux` branch. No model names in commits.
