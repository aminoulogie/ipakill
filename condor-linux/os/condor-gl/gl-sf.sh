#!/bin/sh
# gl-sf.sh: the SurfaceFlinger GPU test (started by gl-sf.ps1, runs in Alpine). glsf asks
# SurfaceFlinger for a full-screen layer and draws into it with OpenGL ES, the way Android's
# boot animation does: the one GPU route that reaches this Intel tablet's panel. It shows
# condor's screen moved down (6 s), then red (4 s), then a moving gradient (4 s), and prints
# the frame rate and a read-back of what the GPU actually drew.
#
# glsf runs in Android's own root through condor-init's root shell (port 2324), which passes
# on Android's environment (ANDROID_PROPERTY_WORKSPACE, so the GPU blobs can read properties).
# Unlike gltest/glanim, this needs SurfaceFlinger running, so it starts it first.
#
# After the test the layer goes away and the panel may stay black until a reboot: restart the
# tablet when it's done.
R=/proc/1/root
cp /tmp/glsf $R/data/local/tmp/glsf && chmod 755 $R/data/local/tmp/glsf || exit 1
pkill -x condor-gl 2>/dev/null # condor's own GPU helper, if animations are on

A() { printf '%s\nexit\n' "$1" | nc 127.0.0.1 2324 2>/dev/null | tr -d '\r' | sed 's/^root@android:[^#]*# //' | grep -v -e tty -e 'job control' -e 'condor-init shell'; }
sf_up() { A 'ps' | grep -q '/system/bin/surfaceflinger'; }
echo "surfaceflinger service state: '$(A 'getprop init.svc.surfaceflinger')'"
echo "binary: $(A 'ls -l /system/bin/surfaceflinger')"
echo "init.rc: $(A 'grep -n -A8 "service surfaceflinger" /init.rc')"
if ! sf_up; then
	# Android's service, started through init (the root shell has Android's property socket).
	echo "start: $(A '/system/bin/start surfaceflinger 2>&1')"
	for i in 1 2 3 4 5 6; do sleep 1; sf_up && break; done
fi
if ! sf_up; then
	# Not a service on this build: run the program itself, detached.
	echo "start did nothing; running /system/bin/surfaceflinger directly"
	A '/system/bin/surfaceflinger </dev/null >/data/local/tmp/sf.out 2>&1 &' >/dev/null
	sleep 1
	for i in 1 2 3 4 5 6; do sleep 1; sf_up && break; done
fi
if ! sf_up; then
	echo "SurfaceFlinger did not start; not running glsf. Diagnostics:"
	echo "--- sf.out"; A 'cat /data/local/tmp/sf.out'
	echo "--- processes: $(A 'ps' | grep -i -E 'surface|system_server|zygote')"
	echo "--- logcat"; /system/bin/logcat -d -v brief '*:I' 2>&1 | tail -n 25
	exit 1
fi
echo "SurfaceFlinger is running"
sleep 2

/system/bin/logcat -c 2>/dev/null
touch /tmp/gl-mark
echo ">>> $(date +%T) WATCH THE TABLET: condor's screen moved down, then red, then a gradient"
printf '/data/local/tmp/glsf 2>&1; echo exit code $?; exit\n' |
	nc 127.0.0.1 2324 | grep -v -e 'tty' -e 'job control' -e 'condor-init shell'

sleep 2
echo "--- Android log (SurfaceFlinger, EGL, libc)"
/system/bin/logcat -d -v brief '*:I' 2>&1 | grep -iE 'surfaceflinger|egl|gralloc|pvr|sgx|hwcomposer|DEBUG|glsf|libc|fatal' | tail -n 40
for t in $(find $R/data/tombstones -type f -newer /tmp/gl-mark 2>/dev/null); do
	echo "--- crash report $t"
	head -n 70 "$t"
done

echo "--- libgui symbols glsf needs (confirm they exist on this tablet)"
command -v nm >/dev/null 2>&1 || apk add -q binutils >/dev/null 2>&1
for l in libgui libutils libbinder; do
	nm -D --defined-only /system/lib/$l.so 2>/dev/null | awk '{print $3}' |
		grep -E 'SurfaceComposerClientC1Ev|createSurfaceERKNS_7String8|openGlobalTransaction|closeGlobalTransaction|SurfaceControl8setLayer|SurfaceControl4show|SurfaceControl10getSurface|ProcessState4self|startThreadPool|String8C1EPKc|RefBase9incStrong' |
		sed "s/^/$l /"
done | sort -u
