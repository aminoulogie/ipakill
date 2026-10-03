# Knowledge base: Condor TRA-901G

Everything learned so far, with sources. Treat "verified" items as facts; "likely" items
need confirming with `condor recon`.

## 1. Hardware

| | | |
|---|---|---|
| Model | Condor TRA-901G (also sold as "CTAB 890 3G"), made in Algeria | verified (back label) |
| SoC | Intel Atom Z2580, "Clover Trail+" / Cloverview, 2 cores / 4 threads, 2.0 GHz | verified |
| GPU | PowerVR SGX544 MP2 (Android-only drivers) | verified |
| RAM / storage | 2 GB / 16 GB eMMC, microSD ≤32 GB | verified |
| Display | 8.9" IPS 1920×1200 | verified |
| Cameras | 5 MP rear, 2 MP front | verified |
| Radios | Wi-Fi, Bluetooth, GPS, 2G/3G (micro-SIM) | verified |
| Port / power | micro-USB 2.0, DC 5V 2A | verified (label) |
| OS | Android 4.2.2 Jelly Bean | verified |
| OEM twin | Ramos i9 (same SoC, same 8.9" FHD panel) | likely |
| Wi-Fi chip | Broadcom bcmdhd or TI wl12xx | unknown, recon |
| Kernel | ~3.4 / 3.10 Intel MID downstream, 32-bit | likely, recon |

Sources: DeviceAtlas TRA-901G https://deviceatlas.com/device-data/devices/condor/tra-901g/15265437 ·
Algérie360 launch article https://www.algerie360.com/condor-lance-de-nouveaux-produits-bases-sur-la-technologie-intel-tablettes-et-appareils-2-en-1-sur-le-marche-en-janvier-2014/ ·
Notebookcheck Z2580 https://www.notebookcheck.net/Intel-Atom-Z2580-Notebook-Processor.81250.0.html ·
Liliputing Ramos i-series https://liliputing.com/?p=58986

## 2. What can't run here, and why

- **Windows 8/10**: Windows tablets used Atom **Z2760 (Clover Trail)**. The Z2580 is the
  Android-only Clover Trail+ line: no Windows-capable UEFI firmware, no Windows drivers for
  PowerVR/touch/Wi-Fi/modem, and 16 GB is below Windows' minimum.
  Intel forum: https://forums.intel.com/s/question/0D50P0000490Tep/possibly-install-windows-to-intel-z2580
- **Kindle OS**: Amazon hardware only.
- **Android 5+**: no ROM for TRA-901G or Ramos i9. Only Zenfone 5 (Z2560/Z2580 phone) got
  official Lollipop; porting it needs Condor's kernel/drivers.
- **Mainline Linux / Ubuntu / Debian / Arch**: Clover Trail+ was never upstreamed (mainline has
  Medfield/Merrifield/Moorefield, not Cloverview). Modern systemd won't run on a 3.x kernel.
  Phoronix https://www.phoronix.com/news/MTE4NDY

## 3. What can run

- **Android 4.4 KitKat** via a full flash of the **Ramos i9** firmware with Intel's flash tool.
  Untested on Condor hardware. OTA files from Ramos fail Condor's signature check; a full
  flash bypasses that. Condor said no official update is planned.
  ForumDZ thread https://www.forumdz.com/topic/37825-condor-ctab-890-3g-tra-901g-firmware-update/
- **postmarketOS-style Linux on the downstream kernel**: proven on the Asus Zenfone 5 (same
  chip family): USB networking, Xorg on fbdev, touch. Intel phones use a different boot image
  format, so flashing is manual.
  pmOS package https://pkgs.postmarketos.org/package/main/postmarketos/x86/device-asus-t00f ·
  initial port commit https://ayakael.net/forge/pmaports/commit/301f732c5c9218ec3a124aa64cb072b7b687d31c ·
  Intel MID wiki https://wiki.postmarketos.org/wiki/Intel_MID ·
  XDA Clover Trail+ dev thread https://xdaforums.com/t/calling-intel-atom-clover-trail-zxxxx-device-owners-collaboration-on-developement.3780361/
- **Linux inside Android** (chroot + VNC): needs root, slow, no boot changes.
- **Android 4.2 + reader app**: KOReader x86 needs Android **4.3+**
  (https://f-droid.org/en/packages/org.koreader.launcher.fdroid/); use older KOReader,
  FBReader or Moon+ Reader versions on 4.2.

## 4. Firmware and recovery kit

- Stock TRA-901G ROM (4.2.2): https://www.needrom.com/download/condor-tra-901g/ ·
  http://firmwarecondor.blogspot.com/2015/06/tra-901g.html ·
  http://gofirmware.com/download/condor-ctab-890-3g-tra-901g ·
  https://www.flashtool.org/android/firmware-condor/
- Flashing: Intel **Manufacturing Flash Tool** → File → Open → `Flash.xml` → connect USB;
  takes ~4–5 min. Intel Platform Flash Tool / xFSTK for the chip's USB recovery (DnX) mode.
- USB drivers: https://www.androidusbdrivers.com/condor-tra-901g-3g-usb-drivers/ (Intel
  Android USB driver also works).
- Download the stock ROM **before** any flash; it's the unbrick kit.

## 5. Boot chain (Clover Trail+)

```
chip ROM → IFWI (firmware, SCU/PMIC) → OSIP header on eMMC → droidboot (fastboot)
        → boot.img (Intel format: OSIP + cmdline + bootstub + bzImage + initrd) → /init
        └ key combo → recovery.img
```
- Standard `mkbootimg`/`abootimg` won't handle Intel images; `condor bootimg` does (verified
  byte-identical on this tablet's and the Ramos i9 images). Layout and OSIP slot map: CLAUDE.md.
- `fastboot boot`: **not supported** (droidboot 0.5: "boot command stubbed on this platform").
- Signatures: every image carries a signed Intel manifest covering the exact payload size
  (header + bootstub + kernel + ramdisk). Whether the firmware enforces it is the open
  question; hints toward yes: OSIP attribute 0x00 = "signed kernel", Ramos twin ships a
  PROD IFWI, `fastboot boot` deliberately stubbed. Test: modified recovery slot via dd.
- The Ramos i9 factory package (Intel flash tool: IFWI, FW/OS DnX, droidboot, boot, recovery,
  system, partition.tbl) matches this tablet's GPT exactly. Its flash.xml erases `factory`.
- Boot menu keys (unconfirmed): power off, then **Vol Up + Power** or **Vol Down + Power**.
- Android 4.2 has no Factory Reset Protection: reset → straight to setup.

## 6. Practical notes from the session

- **Low-battery screen** (battery outline + yellow ⚠ + red bar): battery too empty to boot.
  PC USB gives ~0.5 A vs 2 A rated; use a wall charger, wait 1–2 h, don't spam power.
- **Cable test with no other device**: run the PowerShell device watcher (below) and plug the
  tablet in; any device appearing = cable carries data and tablet is alive.
  ```powershell
  $before = Get-PnpDevice -PresentOnly | ? InstanceId -like 'USB*'
  while ($true) { $now = Get-PnpDevice -PresentOnly | ? InstanceId -like 'USB*'
    Compare-Object $before $now -Property FriendlyName,InstanceId | % { "$(Get-Date -f T) $($_.SideIndicator) $($_.FriendlyName)" }
    $before = $now; Start-Sleep 1 }
  ```
- **Pattern lock**: adb on 4.2.2 needs on-screen "Allow", so adb is blocked while locked.
  MTP file copy usually still works locked on 4.2. After 5 wrong patterns, "Forgot pattern?"
  → Google login (needs Wi-Fi). Otherwise factory reset from recovery.
- adb on Android 4.2: no `exec-out`, CRLF output, exit codes not passed through, toolbox
  has no `grep` (filter on the PC side).
- Root on x86 Android 4.2 is not easy (most one-click exploits are ARM-only). Without root,
  get boot.img/recovery.img from the stock ROM package instead.

## 7. Realistic outcome estimates (from planning)

| Milestone | Chance |
|---|---|
| Own EPUB reader working on the PC | ~95% |
| Bootloader accepts our images | ~60–70% |
| Our code running on tablet, terminal over USB | ~55–65% |
| Our reader on the tablet screen with touch | ~35–45% |
| Full Kindle-like system incl. Wi-Fi | ~20–30% |

Biggest risks: bootloader signature checks (Phase 2), display driver needing Android's
graphics stack (Phase 5), Wi-Fi needing Android services (Phase 7), worn battery.
