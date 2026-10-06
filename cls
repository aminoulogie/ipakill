[1mdiff --git a/condor-linux/gl-sf.ps1 b/condor-linux/gl-sf.ps1[m
[1mindex 234c8a9..0bff844 100644[m
[1m--- a/condor-linux/gl-sf.ps1[m
[1m+++ b/condor-linux/gl-sf.ps1[m
[36m@@ -19,6 +19,7 @@[m [mfor ($i = 1; $i -le 10; $i++) {[m
     Start-Sleep 5[m
 }[m
 if (-not (Test-Path $bin)) { Write-Host "glsf isn't built. Run build.sh in os\condor-gl first." -ForegroundColor Red; exit 1 }[m
[32m+[m[32mWrite-Host ("glsf to send: " + (Get-Item $bin).Length + " bytes, built " + (Get-Item $bin).LastWriteTime) -ForegroundColor Cyan[m
 & scp @ssh $bin (Join-Path $PSScriptRoot 'os\condor-gl\gl-sf.sh') "root@${Ip}:/tmp/"[m
 if ($LASTEXITCODE -ne 0) { Write-Host "copy failed: is the tablet on Wi-Fi at ${Ip}?" -ForegroundColor Red; exit 1 }[m
 Write-Host "Watch the tablet now: condor's screen moved down (6 s), then red (4 s), then a moving gradient (4 s)." -ForegroundColor Cyan[m
[1mdiff --git a/condor-linux/os/condor-gl/gl-sf.sh b/condor-linux/os/condor-gl/gl-sf.sh[m
[1mindex ea9b905..1e84933 100644[m
[1m--- a/condor-linux/os/condor-gl/gl-sf.sh[m
[1m+++ b/condor-linux/os/condor-gl/gl-sf.sh[m
[36m@@ -16,13 +16,18 @@[m [mcp /tmp/glsf $R/data/local/tmp/glsf && chmod 755 $R/data/local/tmp/glsf || exit[m
 pkill -x condor-gl 2>/dev/null # condor's own GPU helper, if animations are on[m
 [m
 echo "surfaceflinger was: $(printf 'getprop init.svc.surfaceflinger; exit\n' | nc 127.0.0.1 2324 2>/dev/null | tr -d '\r' | grep -E '^(running|stopped)$')"[m
[31m-/system/bin/start surfaceflinger[m
[31m-sleep 3[m
[32m+[m[32mprintf '/system/bin/start surfaceflinger; exit\n' | nc 127.0.0.1 2324 >/dev/null 2>&1[m
[32m+[m[32msleep 4[m
[32m+[m[32mif ! printf 'ps; exit\n' | nc 127.0.0.1 2324 | grep -q surfaceflinger; then[m
[32m+[m[32m        echo "SurfaceFlinger did not start (not in the process list): not running glsf"[m
[32m+[m[32m        exit 1[m
[32m+[m[32mfi[m
[32m+[m[32mecho "SurfaceFlinger is running"[m
 [m
 /system/bin/logcat -c 2>/dev/null[m
 touch /tmp/gl-mark[m
 echo ">>> $(date +%T) WATCH THE TABLET: condor's screen moved down, then red, then a gradient"[m
[31m-printf '/data/local/tmp/glsf 2>&1; echo exit code $?; exit\n' |[m
[32m+[m[32mprintf '/data/local/tmp/glsf hold 30 2>&1; echo exit code $?; exit\n' |[m
 	nc 127.0.0.1 2324 | grep -v -e 'tty' -e 'job control' -e 'condor-init shell'[m
 [m
 sleep 2[m
