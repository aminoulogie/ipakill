#!/bin/sh
# gl-run.sh: runs gltest on the tablet (from Alpine, started by gl-test.ps1). gltest runs in
# Android's own root through condor-init's root shell (port 2324), which passes on Android's
# environment. Tried twice: framebuffer window first, then GPU driver first. After each run
# it prints Android's log (Info and up) and the crash report (tombstone) if there is one.
R=/proc/1/root
cp /tmp/gltest $R/data/local/tmp/gltest && chmod 755 $R/data/local/tmp/gltest || exit 1
/system/bin/stop surfaceflinger
sleep 1
for mode in "" eglfirst; do
	echo "=== gltest ${mode:-(window first)}"
	/system/bin/logcat -c
	touch /tmp/gl-mark
	printf 'echo props=$ANDROID_PROPERTY_WORKSPACE; /data/local/tmp/gltest %s 2>&1; echo exit code $?; exit\n' "$mode" |
		nc 127.0.0.1 2324 | grep -v -e 'tty' -e 'job control' -e 'condor-init shell'
	sleep 2
	echo "--- Android log"
	/system/bin/logcat -d -v brief '*:I' 2>&1 | tail -n 40
	for t in $(find $R/data/tombstones -type f -newer /tmp/gl-mark 2>/dev/null); do
		echo "--- crash report $t"
		head -n 70 "$t"
	done
# A crash can leave the display panned away from where condor draws (a grey screen).
printf '/data/local/tmp/gltest unpan; exit\n' | nc 127.0.0.1 2324 | grep framebuffer
done
# A crash can leave the display panned away from where condor draws (a grey screen).
printf '/data/local/tmp/gltest unpan; exit\n' | nc 127.0.0.1 2324 | grep framebuffer
