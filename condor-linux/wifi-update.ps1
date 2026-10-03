# wifi-update.ps1: put the latest condor-init on the tablet over Wi-Fi (no USB cable).
#   .\condor-linux\wifi-update.cmd 192.168.1.23     the first time (the tablet's IP: Settings > Wi-Fi)
#   .\condor-linux\wifi-update.cmd                  after that (the IP is remembered)
# It gets the latest code, builds condor-init, copies it to the tablet over the SSH set up by
# "condor ssh setup", checks it, keeps the old one as condor-init.bak, restarts condor-init,
# and puts the old one back by itself if the new one doesn't start.
#
# How it reaches the tablet's files: SSH lands in the Alpine chroot, but as root it can see
# the real filesystem through /proc/1/root (init's root), so /data/condor is
# /proc/1/root/data/condor; and Android's own /system/bin/start (bind-mounted into Alpine)
# restarts the flash_recovery service that runs condor-init, as "condor takeover restart" does.
param([string]$Ip = "")
# Not 'Stop': git and ssh write progress to stderr, which Windows PowerShell 5.1 would turn
# into a terminating error. Every step checks $LASTEXITCODE instead.
$ErrorActionPreference = 'Continue'
$repo = Split-Path $PSScriptRoot -Parent
$ipFile = "$HOME\.condor\tablet-ip"

if ($Ip -eq "" -and (Test-Path $ipFile)) { $Ip = (Get-Content $ipFile -Raw).Trim() }
if ($Ip -notmatch '^\d+\.\d+\.\d+\.\d+$') {
    Write-Host "Which IP? On the tablet: Settings > Wi-Fi shows it. Then:  condor-linux\wifi-update.cmd 192.168.x.y" -ForegroundColor Red
    exit 1
}
New-Item -ItemType Directory -Force "$HOME\.condor" | Out-Null
Set-Content -Path $ipFile -Value $Ip
$ssh = @('-o', 'ConnectTimeout=8', '-o', 'BatchMode=yes', '-o', 'StrictHostKeyChecking=accept-new')

Write-Host "==> is the tablet there ($Ip)?" -ForegroundColor Cyan
$hello = & ssh @ssh "root@$Ip" "echo ok" 2>&1
if ("$hello" -notmatch 'ok') {
    Write-Host "Can't reach root@$Ip over SSH: $hello" -ForegroundColor Red
    Write-Host "Is the tablet on Wi-Fi (Settings > Wi-Fi), and is this its IP? Pass the right one: condor-linux\wifi-update.cmd <ip>" -ForegroundColor Yellow
    exit 1
}

Write-Host "==> getting the latest code" -ForegroundColor Cyan
Set-Location $repo
$pulled = $false
for ($i = 1; $i -le 3; $i++) {
    git pull --no-edit 2>&1 | Out-Null
    if ($LASTEXITCODE -eq 0) { $pulled = $true; break }
    Start-Sleep 5
}
if (-not $pulled) { Write-Host "GitHub unreachable: installing the code already on this PC" -ForegroundColor Yellow }

Write-Host "==> building condor-init" -ForegroundColor Cyan
$bin = Join-Path $env:TEMP 'condor-init'
Push-Location "$repo\condor-linux\os\condor-init"
try {
    $env:GOOS = 'linux'; $env:GOARCH = '386'; $env:CGO_ENABLED = '0'
    go build -o $bin .
    if ($LASTEXITCODE -ne 0) { Write-Host "build failed" -ForegroundColor Red; exit 1 }
} finally {
    Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED -ErrorAction SilentlyContinue
    Pop-Location
}
$md5 = (Get-FileHash $bin -Algorithm MD5).Hash.ToLower()

# The installer, run on the tablet (busybox sh in Alpine), detached so that it finishes even
# if the Wi-Fi connection drops while condor-init restarts.
$script = @'
#!/bin/sh
want=$1
D=${CONDOR_DIR:-/proc/1/root/data/condor}      # overridable for tests
START=${CONDOR_START:-"/system/bin/start flash_recovery"}
log() { echo "$(date +%T) $*"; }
restart() {
  touch $D/takeover
  for p in $(pidof condor-init); do kill $p; done
  i=0
  while pidof condor-init >/dev/null && [ $i -lt 20 ]; do sleep 0.5; i=$((i+1)); done
  $START
}
[ -f $D/condor-init ] || { log "FAILED: can't see $D (not root?)"; exit 1; }
got=$(md5sum /tmp/condor-init.new | cut -d' ' -f1)
[ "$got" = "$want" ] || { log "FAILED: the copy arrived damaged ($got, want $want)"; exit 1; }
cp $D/condor-init $D/condor-init.bak || { log "FAILED: couldn't keep a backup"; exit 1; }
cp /tmp/condor-init.new $D/condor-init.new && chmod 755 $D/condor-init.new && mv -f $D/condor-init.new $D/condor-init || {
  log "FAILED: couldn't install"; exit 1; }
[ "$(md5sum $D/condor-init | cut -d' ' -f1)" = "$want" ] || {
  cp $D/condor-init.bak $D/condor-init; log "FAILED: wrong md5 after install, old one kept"; exit 1; }
log "installed, restarting condor-init"
restart
sleep ${CONDOR_WAIT:-15}
if pidof condor-init >/dev/null; then
  rm -f /tmp/condor-init.new
  log "DONE: the new condor-init is running (pid $(pidof condor-init))"
  exit 0
fi
log "the new condor-init didn't start: putting the old one back"
cp $D/condor-init.bak $D/condor-init
restart
sleep ${CONDOR_WAIT:-10}
if pidof condor-init >/dev/null; then log "ROLLED BACK: the old version is running again"; else
  log "FAILED: nothing running. Plug in USB and run update.cmd"; fi
'@ -replace "`r", ""
$sh = Join-Path $env:TEMP 'condor-update.sh'
[IO.File]::WriteAllText($sh, $script, (New-Object Text.UTF8Encoding $false))

Write-Host "==> copying to the tablet ($([math]::Round((Get-Item $bin).Length / 1MB, 1)) MB)" -ForegroundColor Cyan
& scp @ssh $bin "root@${Ip}:/tmp/condor-init.new"
if ($LASTEXITCODE -ne 0) { Write-Host "copy failed" -ForegroundColor Red; exit 1 }
& scp @ssh $sh "root@${Ip}:/tmp/condor-update.sh"
if ($LASTEXITCODE -ne 0) { Write-Host "copy failed" -ForegroundColor Red; exit 1 }

Write-Host "==> installing and restarting (the screen goes black for a moment)" -ForegroundColor Cyan
& ssh @ssh "root@$Ip" "rm -f /tmp/condor-update.log; setsid sh /tmp/condor-update.sh $md5 >/tmp/condor-update.log 2>&1 </dev/null &"
for ($i = 0; $i -lt 30; $i++) {
    Start-Sleep 4
    $log = (& ssh @ssh "root@$Ip" "cat /tmp/condor-update.log" 2>$null) -join "`n"
    if ($log -match 'DONE|ROLLED BACK|FAILED') {
        Write-Host $log
        if ($log -match 'DONE') {
            Write-Host "==> done. Next time:  condor-linux\wifi-update.cmd" -ForegroundColor Green
            exit 0
        }
        exit 1
    }
}
Write-Host "No answer from the tablet for 2 minutes. It may have a new IP after the restart: check Settings > Wi-Fi, then run: condor-linux\wifi-update.cmd <ip>" -ForegroundColor Yellow
exit 1
