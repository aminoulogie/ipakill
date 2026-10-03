package main

// wifiScript is installed into Alpine as /usr/local/bin/wifi. Android's Wi-Fi service is
// stopped in takeover mode, so we drive the Broadcom bcmdhd driver (already loaded by the
// kernel at boot) ourselves. /system is bound into the Alpine root (see alpineMounts), so the
// driver finds its firmware and Android's own binaries run here.
//
// Verified on the tablet (2026-10-03): Alpine's wpa_supplicant 2.11 can't use this 3.4
// kernel's nl80211 ("Could not allocate genl cache"), and wext scans fail. Condor's
// /system/bin/wpa_supplicant works, but it runs as Android's wifi user (uid 1010): its config
// and control-socket directory must belong to that uid, and wpa_cli must create its reply
// socket with umask 0 so the supplicant can answer. busybox udhcpc then gets the address.
const wifiScript = `#!/bin/sh
# wifi: Wi-Fi for condor (Broadcom bcmdhd on the Condor TRA-901G)
#   wifi scan                     list networks
#   wifi connect SSID [password]  join (saved: reconnects at every boot)
#   wifi status | off | forget | debug
CONF=/etc/condor/wpa.conf
RUNCONF=/etc/condor/wpa-android.conf
SOCK=/run/wpa_supplicant
WIFI_UID=1010
IF=wlan0
P=/sys/module/bcmdhd/parameters
LOG=/var/log/wifi.log

# Condor's supplicant speaks this kernel's nl80211; Alpine's can't (see wifi.go).
SUP=/system/bin/wpa_supplicant
[ -x $SUP ] || SUP=wpa_supplicant

cli() { (umask 0; wpa_cli -p $SOCK -i $IF "$@"); }

need() {
	for c in wpa_cli iw; do
		command -v $c >/dev/null 2>&1 && continue
		echo "missing tools: run  apk add wpa_supplicant iw  (with 'condor net' running on the PC)"
		exit 1
	done
}

# Point the driver at its firmware and calibration files if Android didn't already.
firmware() {
	[ -d $P ] || { echo "the bcmdhd Wi-Fi driver isn't loaded (wifi debug)"; return 1; }
	cur=$(cat $P/firmware_path 2>/dev/null)
	if [ -z "$cur" ] || [ ! -f "$cur" ]; then
		fw=$(ls /system/etc/firmware/fw_bcm*.bin /system/vendor/firmware/fw_bcm*.bin /system/etc/wifi/fw_bcm*.bin 2>/dev/null | grep -v -e apsta -e p2p | head -n1)
		[ -n "$fw" ] && echo "$fw" > $P/firmware_path
	fi
	cur=$(cat $P/nvram_path 2>/dev/null)
	if [ -e $P/nvram_path ] && { [ -z "$cur" ] || [ ! -f "$cur" ]; }; then
		nv=$(ls /system/etc/wifi/*.cal /system/etc/wifi/*nvram*.txt /system/etc/wifi/bcmdhd*.txt /system/etc/firmware/*nvram*.txt /system/etc/firmware/*.cal 2>/dev/null | head -n1)
		[ -n "$nv" ] && echo "$nv" > $P/nvram_path
	fi
	return 0
}

up() {
	firmware || return 1
	ip link set $IF up 2>/dev/null || { sleep 2; ip link set $IF up; } || {
		echo "can't bring $IF up (wifi debug shows why)"; return 1; }
}

join() {
	pkill wpa_supplicant 2>/dev/null; pkill -f "udhcpc -i $IF" 2>/dev/null; sleep 1
	# The supplicant runs as the wifi user: give it its config and socket directory.
	rm -rf $SOCK; mkdir -p $SOCK
	chown $WIFI_UID:$WIFI_UID $SOCK; chmod 770 $SOCK
	sed "s#^ctrl_interface=.*#ctrl_interface=$SOCK#" $CONF > $RUNCONF
	chown $WIFI_UID:$WIFI_UID $RUNCONF; chmod 600 $RUNCONF
	$SUP -B -i $IF -Dnl80211 -c $RUNCONF >>$LOG 2>&1 || {
		echo "wpa_supplicant failed (wifi debug)"; return 1; }
	echo "joining..."
	i=0
	while [ $i -lt 25 ]; do
		cli status 2>/dev/null | grep -q '^wpa_state=COMPLETED' && break
		sleep 1; i=$((i+1))
	done
	if ! cli status 2>/dev/null | grep -q '^wpa_state=COMPLETED'; then
		echo "couldn't join: wrong password, or the network is out of range (wifi debug)"; return 1
	fi
	echo "getting an address..."
	udhcpc -i $IF -n -q -t 8 >>$LOG 2>&1 || { echo "no address from DHCP"; return 1; }
	echo "online: $(ip -4 addr show $IF | awk '/inet /{print $2}')  ssid: $(cli status | sed -n 's/^ssid=//p')"
}

case "$1" in
scan)
	need; up || exit 1
	sleep 1
	out=$(iw dev $IF scan 2>&1) || { echo "scan failed: $out (wifi debug)"; exit 1; }
	echo "$out" | awk '
		/^BSS/ {sig=""}
		/signal:/ {sig=$2}
		/SSID: / {sub(/^[ \t]*SSID: /,""); if ($0!="" && !seen[$0]++) printf "%6s dBm  %s\n", sig, $0}' | sort -rn
	;;
connect)
	[ -n "$2" ] || { echo "usage: wifi connect SSID [password]"; exit 1; }
	need
	mkdir -p /etc/condor
	{
		echo "ctrl_interface=/run/wpa_supplicant"
		echo "update_config=1"
		if [ -n "$3" ]; then
			wpa_passphrase "$2" "$3" | grep -v '#psk='
		else
			printf 'network={\n\tssid="%s"\n\tkey_mgmt=NONE\n}\n' "$2"
		fi
	} > $CONF
	chmod 600 $CONF
	up && join
	;;
boot)  # run by condor-init at startup: reconnect to the saved network, quietly
	[ -f $CONF ] || exit 0
	command -v wpa_cli >/dev/null 2>&1 || exit 0
	{ up && join; } >>$LOG 2>&1
	;;
status)
	if pgrep wpa_supplicant >/dev/null; then
		cli status 2>/dev/null | grep -e '^ssid=' -e '^wpa_state=' -e '^ip_address='
	else
		echo "not connected"
	fi
	ip -4 addr show $IF 2>/dev/null | awk '/inet /{print "address: "$2}'
	;;
off)
	pkill wpa_supplicant; pkill -f "udhcpc -i $IF"; ip link set $IF down; echo "wifi off"
	;;
forget)
	rm -f $CONF; echo "saved network removed"
	;;
debug)
	echo "== driver"; ls $P 2>/dev/null && grep . $P/firmware_path $P/nvram_path 2>/dev/null
	echo "== firmware files"; ls -l /system/etc/firmware /system/etc/wifi 2>/dev/null | grep -i -e bcm -e nvram -e cal -e '^/'
	echo "== interface"; ip link show $IF
	echo "== log"; tail -n 15 $LOG 2>/dev/null
	echo "== supplicant (Android log)"; /system/bin/logcat -d -s wpa_supplicant 2>/dev/null | tail -n 15
	echo "== kernel"; dmesg | grep -i -e dhd -e wl_ -e wlan -e sdio | tail -n 25
	;;
*)
	echo "usage: wifi scan | connect SSID [password] | status | off | forget | debug"
	;;
esac
`
