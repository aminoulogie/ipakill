#!/bin/sh
# gl-probe.sh (started by gl-probe.ps1, runs in Alpine): does Android's own GPU route work
# with condor running? Android's boot animation draws with the GPU through SurfaceFlinger,
# the way condor would; this starts it for 8 s while someone watches the tablet. Then it
# lists the SurfaceFlinger client functions the tablet's libraries offer (for condor-gl).
A() { printf '%s; exit\n' "$1" | nc 127.0.0.1 2324 2>/dev/null | grep -v -e tty -e 'job control' -e 'condor-init shell' -e '^root@android:/ # $'; }
echo "surfaceflinger was: $(A 'getprop init.svc.surfaceflinger') / boot animation: $(A 'getprop init.svc.bootanim')"
/system/bin/start surfaceflinger
sleep 3
echo ">>> $(date +%T) WATCH THE TABLET: boot animation for 8 seconds"
/system/bin/start bootanim
sleep 8
echo ">>> $(date +%T) stopping the boot animation"
/system/bin/stop bootanim
sleep 4
echo ">>> $(date +%T) stopping surfaceflinger (is condor back? tap the screen)"
/system/bin/stop surfaceflinger
sleep 4
echo ">>> $(date +%T) test over"
echo "--- functions"
command -v nm >/dev/null 2>&1 || apk add -q binutils >/dev/null 2>&1
for l in libgui libutils libui libbinder; do
	nm -D --defined-only /system/lib/$l.so 2>/dev/null | awk '{print $3}' |
		grep -E 'SurfaceComposerClient|SurfaceControl|android7Surface|android11Surface|RefBase9(inc|dec)Strong|String8C[12]EPKc|getBuiltInDisplay|DisplayInfo|ProcessState|IPCThreadState4self|startThreadPool' |
		sed "s/^/$l /"
done | sort -u
