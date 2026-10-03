#!/bin/sh
# gl-show.sh: the visual GPU test (started by gl-show.ps1, runs in Alpine). glanim shows the
# screen as condor drew it through the GPU, moved down, for 6 s, then red for 4 s. It runs
# in Android's root through condor-init's root shell (port 2324), like gl-run.sh.
R=/proc/1/root
cp /tmp/glanim $R/data/local/tmp/glanim && chmod 755 $R/data/local/tmp/glanim || exit 1
pkill -x condor-gl 2>/dev/null # condor's own GPU helper, if animations are on
/system/bin/stop surfaceflinger
sleep 1
printf '/data/local/tmp/glanim show 1920 1200 2>&1; echo exit code $?; exit\n' |
	nc 127.0.0.1 2324 | grep -v -e 'tty' -e 'job control' -e 'condor-init shell'
