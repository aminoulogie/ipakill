#!/usr/bin/env bash
# Phase 1: read-only inspection of a Condor TRA-901G (Intel Atom Z2580, Android 4.2.2).
# Collects everything needed to boot our own Linux on Condor's kernel:
# kernel version/config, partitions, driver modules, firmware, input/display/Wi-Fi info,
# and (if root is available) raw dumps of the boot/recovery partitions.
#
# Nothing on the tablet is modified. Dumps go to /sdcard temporarily, then are pulled and deleted.
#
# Usage: ./recon.sh             inspect over adb
#        ./recon.sh --fastboot  also reboot to the bootloader and read `fastboot getvar all`
set -u

OUT="condor-recon-$(date +%Y%m%d-%H%M%S)"
WANT_FASTBOOT=0
[ "${1:-}" = "--fastboot" ] && WANT_FASTBOOT=1

say()  { printf '\n\033[1;36m==> %s\033[0m\n' "$*"; }
warn() { printf '\033[1;33m!! %s\033[0m\n' "$*"; }
die()  { printf '\033[1;31mxx %s\033[0m\n' "$*"; exit 1; }

command -v adb >/dev/null || die "adb not found. Debian/Ubuntu: sudo apt install adb fastboot   Arch: sudo pacman -S android-tools"

# Android 4.2's adb shell returns CRLF line endings; strip them.
sh_()  { adb shell "$@" 2>/dev/null | tr -d '\r'; }
grab() { # grab <file-in-OUT> <shell command...>
    local f="$OUT/$1"; shift
    mkdir -p "$(dirname "$f")"
    sh_ "$@" > "$f"
}

say "Waiting for the tablet (enable USB debugging and accept the prompt on screen)"
adb start-server >/dev/null
adb wait-for-device
[ "$(adb get-state 2>/dev/null)" = "device" ] || die "Tablet not authorized. Unlock it and tap 'Allow' on the USB debugging prompt."
mkdir -p "$OUT"

say "System info"
grab system/getprop.txt      getprop
grab system/build.prop       cat /system/build.prop
grab kernel/version.txt      cat /proc/version
grab kernel/cmdline.txt      cat /proc/cmdline
grab kernel/cpuinfo.txt      cat /proc/cpuinfo
grab kernel/meminfo.txt      cat /proc/meminfo
grab kernel/dmesg.txt        dmesg
grab kernel/modules-loaded.txt cat /proc/modules
grab kernel/filesystems.txt  cat /proc/filesystems
grab kernel/devices.txt      cat /proc/devices
grab kernel/iomem.txt        cat /proc/iomem
grab kernel/interrupts.txt   cat /proc/interrupts
cat "$OUT/kernel/version.txt"

say "Storage and partitions"
grab storage/partitions.txt  cat /proc/partitions
grab storage/mounts.txt      cat /proc/mounts
grab storage/by-name.txt     ls -l /dev/block/by-name
grab storage/by-name-platform.txt ls -lR /dev/block/platform
grab storage/fstab.txt       sh -c 'cat /fstab.* /etc/vold.fstab /system/etc/vold.fstab 2>/dev/null'
grab storage/df.txt          df

say "Display, input (touch), sound"
grab display/fb.txt          cat /proc/fb
grab display/graphics-sysfs.txt ls -lR /sys/class/graphics
grab display/drm-sysfs.txt   ls -lR /sys/class/drm
grab input/getevent-p.txt    getevent -p
grab input/getevent-il.txt   getevent -il
grab input/devices.txt       cat /proc/bus/input/devices
grab sound/cards.txt         cat /proc/asound/cards

say "Wi-Fi, Bluetooth, buses"
grab wifi/net-sysfs.txt      ls -l /sys/class/net
grab wifi/props.txt          sh -c 'getprop | grep -i -e wifi -e wlan 2>/dev/null || getprop'
grab wifi/system-etc-wifi.txt ls -lR /system/etc/wifi
grab bus/i2c.txt             ls -lR /sys/bus/i2c/devices
grab bus/sdio.txt            ls -lR /sys/bus/sdio/devices
grab bus/usb.txt             ls -lR /sys/bus/usb/devices
grab bus/pci.txt             ls -lR /sys/bus/pci/devices
grab bus/platform.txt        ls /sys/bus/platform/devices

say "Pulling driver modules, firmware, init scripts and kernel config"
for d in /system/lib/modules /lib/modules /system/etc/firmware /system/vendor/firmware /vendor/firmware /system/etc/wifi; do
    if [ -n "$(sh_ ls "$d" | head -1)" ]; then
        mkdir -p "$OUT/pulled$d"
        adb pull "$d" "$OUT/pulled$d" >/dev/null 2>&1 && echo "  pulled $d" || warn "could not pull $d"
    fi
done
adb pull /proc/config.gz "$OUT/kernel/config.gz" >/dev/null 2>&1 \
    && echo "  pulled /proc/config.gz (full kernel config!)" \
    || warn "no /proc/config.gz; we'll read the config out of boot.img later"
mkdir -p "$OUT/pulled/rootfs"
for f in $(sh_ ls / | grep -E '\.rc$|^fstab|^ueventd'); do
    adb pull "/$f" "$OUT/pulled/rootfs/$f" >/dev/null 2>&1
done

say "Checking for root"
ROOT=""
if [ "$(sh_ id | grep -c 'uid=0')" = 1 ]; then ROOT="plain"
elif [ "$(sh_ su -c id | grep -c 'uid=0')" = 1 ]; then ROOT="su"
fi
echo "${ROOT:-no root}" > "$OUT/root-status.txt"

dump_part() { # dump_part <name> : dd the partition to /sdcard, pull, delete
    local name="$1" dev
    dev=$(awk -v n="$name" '$0 ~ (" " n " ->") {print $NF}' "$OUT/storage/by-name.txt" | head -1)
    [ -z "$dev" ] && dev=$(grep -h " $name -> " "$OUT/storage/by-name-platform.txt" | awk '{print $NF}' | head -1)
    [ -z "$dev" ] && { warn "no partition named '$name'"; return; }
    local cmd="dd if=$dev of=/sdcard/condor-$name.img bs=4096"
    if [ "$ROOT" = "su" ]; then sh_ su -c "$cmd" >/dev/null; else sh_ "$cmd" >/dev/null; fi
    mkdir -p "$OUT/dumps"
    if adb pull "/sdcard/condor-$name.img" "$OUT/dumps/$name.img" >/dev/null 2>&1; then
        echo "  $name ($dev) -> dumps/$name.img  $(du -h "$OUT/dumps/$name.img" | cut -f1)"
    else
        warn "dump of $name failed"
    fi
    sh_ rm -f "/sdcard/condor-$name.img" >/dev/null
}

if [ -n "$ROOT" ]; then
    say "Root available ($ROOT): backing up boot-related partitions"
    for p in boot recovery fastboot droidboot misc; do dump_part "$p"; done
else
    warn "No root. Skipping partition dumps. That's OK: we can extract boot.img from the official"
    warn "Condor TRA-901G 4.2.2 firmware package instead."
fi

if [ "$WANT_FASTBOOT" = 1 ]; then
    command -v fastboot >/dev/null || die "fastboot not found"
    say "Rebooting to bootloader to read its variables (read-only)"
    adb reboot bootloader
    echo "  waiting up to 60s for fastboot..."
    for i in $(seq 60); do fastboot devices 2>/dev/null | grep -q . && break; sleep 1; done
    if fastboot devices | grep -q .; then
        fastboot getvar all > "$OUT/fastboot-getvar.txt" 2>&1
        cat "$OUT/fastboot-getvar.txt"
        say "Rebooting back to Android"
        fastboot reboot >/dev/null 2>&1 || fastboot continue >/dev/null 2>&1
    else
        warn "fastboot didn't see the tablet. On some Intel tablets: power off, then hold Volume Down + Power."
        warn "Note in the summary what screen appeared."
    fi
fi

say "Summary"
{
    echo "== Condor TRA-901G recon $(date) =="
    echo "Kernel:     $(cat "$OUT/kernel/version.txt")"
    echo "Android:    $(grep -m1 'ro.build.version.release' "$OUT/system/getprop.txt")"
    echo "Build:      $(grep -m1 'ro.build.display.id' "$OUT/system/getprop.txt")"
    echo "Platform:   $(grep -m1 -e 'ro.board.platform' "$OUT/system/getprop.txt")"
    echo "Bootloader: $(grep -m1 -e 'ro.bootloader' "$OUT/system/getprop.txt")"
    echo "Root:       $(cat "$OUT/root-status.txt")"
    echo "Cmdline:    $(cat "$OUT/kernel/cmdline.txt")"
    echo; echo "-- Partitions by name --"; cat "$OUT/storage/by-name.txt"
    echo; echo "-- Framebuffers --"; cat "$OUT/display/fb.txt"
    echo; echo "-- Input devices --"; grep -E '^N:|^H:' "$OUT/input/devices.txt"
    echo; echo "-- Loaded modules --"; awk '{print $1}' "$OUT/kernel/modules-loaded.txt"
    echo; echo "-- Network interfaces --"; cat "$OUT/wifi/net-sysfs.txt"
} | tee "$OUT/SUMMARY.txt"

tar czf "$OUT.tar.gz" "$OUT"
say "Done. Send me $OUT/SUMMARY.txt (paste it), and keep $OUT.tar.gz safe: it's your backup."
