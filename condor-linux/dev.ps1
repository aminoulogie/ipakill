# dev.ps1: build condor-init on this PC and run it on the tablet, in one step.
# Use it after every change:  .\dev.cmd   (the tablet must already be in takeover mode;
# if it's in Android, it arms the takeover and reboots instead).
$ErrorActionPreference = 'Stop'
$src = Join-Path $PSScriptRoot 'os\condor-init'
Push-Location $src
try {
    $env:GOOS = 'linux'; $env:GOARCH = '386'; $env:CGO_ENABLED = '0'
    Write-Host '==> building condor-init' -ForegroundColor Cyan
    go build -o condor-init .
    if ($LASTEXITCODE -ne 0) { throw 'build failed' }
} finally {
    Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED -ErrorAction SilentlyContinue
}
try {
    $status = condor takeover status | Out-String
    if ($status -match 'running:\s+yes') {
        Write-Host '==> installing and restarting on the tablet' -ForegroundColor Cyan
        condor takeover push condor-init
        condor takeover restart
    } else {
        Write-Host '==> tablet is in Android: arming the takeover and rebooting' -ForegroundColor Cyan
        condor takeover arm condor-init
        condor reboot
    }
} finally {
    Pop-Location
}
Write-Host '==> done. Look at the tablet, or type:  condor term' -ForegroundColor Green
