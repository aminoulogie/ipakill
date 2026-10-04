#!/bin/sh
# update: condor updates itself, from the tablet alone (Terminal tab, or ssh). It gets the
# latest code from GitHub (an archive, with curl: new git can't run on this kernel, it needs
# getrandom(), which 3.4 doesn't have), builds condor-init here with Go, then installs it the way
# wifi-update does: a backup, a restart, and the old version put back if the new one
# doesn't run. condor-init installs this as /usr/local/bin/update in Alpine; the first time
# it can also be fetched:
#   U=https://raw.githubusercontent.com/aminoulogie/ipakill/condor-linux
#   curl -LO $U/condor-linux/os/condor-init/update.sh
#   sh update.sh
REPO=aminoulogie/ipakill
BRANCH=condor-linux
SRC=/root/condor-src
D=/proc/1/root/data/condor # /data/condor as init sees it (we're in Alpine's chroot)

say() { printf '\033[1;36m==> %s\033[0m\n' "$*"; }
die() { printf '\033[1;31m%s\033[0m\n' "$*"; exit 1; }

[ "$(id -u)" = 0 ] || die "run it as root"
[ -f $D/condor-init ] || die "can't see $D: run it on the tablet"

if ! command -v go >/dev/null || ! command -v curl >/dev/null; then
	say "installing Go and curl (once, about 200 MB)"
	apk add go curl || die "apk failed: is Wi-Fi on?"
fi

say "getting the latest code"
commit=$(curl -fsSL -H "Accept: application/vnd.github.sha" https://api.github.com/repos/$REPO/commits/$BRANCH)
rm -rf $SRC.new && mkdir -p $SRC.new
curl -fsSL https://codeload.github.com/$REPO/tar.gz/refs/heads/$BRANCH | tar -xzf - -C $SRC.new ||
	die "download failed: is Wi-Fi on?"
rm -rf $SRC && mv $SRC.new/* $SRC && rm -rf $SRC.new || die "couldn't unpack the code"
echo "    $BRANCH at ${commit:-?}"

say "building condor-init (the first time takes several minutes)"
mkdir -p /root/.cache/gotmp
rm -f /tmp/condor-init.new # a leftover (an earlier update) would stop the build
cd $SRC/condor-linux/os/condor-init || die "no condor-init in the code"
# GOTMPDIR on flash: /tmp is memory. GOTOOLCHAIN=auto fetches a newer Go if go.mod needs one.
GOTMPDIR=/root/.cache/gotmp GOTOOLCHAIN=auto GOOS=linux GOARCH=386 CGO_ENABLED=0 \
	go build -o /tmp/condor-init.new . || die "the build failed (nothing was changed)"
want=$(md5sum /tmp/condor-init.new | cut -d' ' -f1)
if [ "$want" = "$(md5sum $D/condor-init | cut -d' ' -f1)" ]; then
	say "already up to date"
	exit 0
fi

# The install runs detached: restarting condor-init closes this terminal.
cat >/tmp/condor-install.sh <<'EOF'
want=$1
D=/proc/1/root/data/condor
log() { echo "$(date +%T) $*"; }
restart() {
	touch $D/takeover
	for p in $(pidof condor-init); do kill $p; done
	i=0
	while pidof condor-init >/dev/null && [ $i -lt 20 ]; do sleep 0.5; i=$((i+1)); done
	/system/bin/start flash_recovery
}
cp $D/condor-init $D/condor-init.bak || { log "FAILED: couldn't keep a backup"; exit 1; }
cp /tmp/condor-init.new $D/condor-init.new && chmod 755 $D/condor-init.new && mv -f $D/condor-init.new $D/condor-init || {
	log "FAILED: couldn't install"; exit 1; }
[ "$(md5sum $D/condor-init | cut -d' ' -f1)" = "$want" ] || {
	cp $D/condor-init.bak $D/condor-init; log "FAILED: wrong md5 after install, old one kept"; exit 1; }
log "installed, restarting condor-init"
restart
sleep 15
if pidof condor-init >/dev/null; then
	rm -f /tmp/condor-init.new
	log "DONE: the new condor-init is running (pid $(pidof condor-init))"
	exit 0
fi
log "the new condor-init didn't start: putting the old one back"
cp $D/condor-init.bak $D/condor-init
restart
sleep 10
if pidof condor-init >/dev/null; then log "ROLLED BACK: the old version is running again"; else
	log "FAILED: nothing running. Plug in USB and run update.cmd on the PC"; fi
EOF
say "installing: the screen restarts in a few seconds"
echo "    afterwards, in Terminal: cat /tmp/condor-update.log"
sleep 2
setsid sh /tmp/condor-install.sh "$want" >/tmp/condor-update.log 2>&1 </dev/null &
