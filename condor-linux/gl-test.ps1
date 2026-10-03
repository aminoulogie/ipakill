# gl-test.ps1: can condor draw with the tablet's GPU? Runs os/condor-gl/gltest on the tablet
# over Wi-Fi (the IP saved by wifi-update) and prints what it measured. Harmless: it stops
# Android's SurfaceFlinger (not used by condor), draws test colours for a few seconds, and
# puts the screen back. Tap the tablet's screen afterwards to redraw condor.
#   condor-linux\gl-test.cmd [ip]
param([string]$Ip = "")
$ipFile = "$HOME\.condor\tablet-ip"
if ($Ip -eq "" -and (Test-Path $ipFile)) { $Ip = (Get-Content $ipFile -Raw).Trim() }
if ($Ip -notmatch '^\d+\.\d+\.\d+\.\d+$') { Write-Host "Which IP? condor-linux\gl-test.cmd 192.168.x.y" -ForegroundColor Red; exit 1 }
$ssh = @('-o', 'ConnectTimeout=8', '-o', 'BatchMode=yes', '-o', 'StrictHostKeyChecking=accept-new')
$bin = Join-Path $PSScriptRoot 'os\condor-gl\gltest'
$ErrorActionPreference = 'Continue'
& scp @ssh $bin "root@${Ip}:/tmp/gltest"
if ($LASTEXITCODE -ne 0) { Write-Host "copy failed: is the tablet on Wi-Fi at ${Ip}?" -ForegroundColor Red; exit 1 }
# Run it in Android's own filesystem (init's root, /proc/1/root), not in Alpine: the GPU
# driver loads its libraries from /vendor, which only exists there.
& ssh @ssh "root@$Ip" "cp /tmp/gltest /proc/1/root/data/local/tmp/gltest; chmod 755 /proc/1/root/data/local/tmp/gltest; /system/bin/stop surfaceflinger; sleep 1; chroot /proc/1/root /data/local/tmp/gltest 2>&1; echo exit code `$?"
