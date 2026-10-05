# gl-sf.ps1: the SurfaceFlinger GPU test, over Wi-Fi. This is the GPU route that can actually
# reach this tablet's panel: glsf asks SurfaceFlinger for a full-screen layer and draws into it
# with OpenGL ES, the way Android's boot animation does. Watch the tablet: condor's screen
# moved down (6 s), then red (4 s), then a moving gradient (4 s). Restart the tablet after.
#   condor-linux\gl-sf.cmd [ip]
param([string]$Ip = "")
$ipFile = "$HOME\.condor\tablet-ip"
if ($Ip -eq "" -and (Test-Path $ipFile)) { $Ip = (Get-Content $ipFile -Raw).Trim() }
if ($Ip -notmatch '^\d+\.\d+\.\d+\.\d+$') { Write-Host "Which IP? condor-linux\gl-sf.cmd 192.168.x.y" -ForegroundColor Red; exit 1 }
$ssh = @('-o', 'ConnectTimeout=8', '-o', 'BatchMode=yes', '-o', 'StrictHostKeyChecking=accept-new')
$bin = Join-Path $PSScriptRoot 'os\condor-gl\glsf'
$ErrorActionPreference = 'Continue'
# The latest test first (GitHub is often slow to answer from this PC: a few tries).
Set-Location (Split-Path $PSScriptRoot)
for ($i = 1; $i -le 10; $i++) {
    git pull --no-edit 2>&1 | Out-Null
    if ($LASTEXITCODE -eq 0) { break }
    Write-Host "git pull failed, trying again ($i)" -ForegroundColor Yellow
    Start-Sleep 5
}
if (-not (Test-Path $bin)) { Write-Host "glsf isn't built. Run build.sh in os\condor-gl first." -ForegroundColor Red; exit 1 }
& scp @ssh $bin (Join-Path $PSScriptRoot 'os\condor-gl\gl-sf.sh') "root@${Ip}:/tmp/"
if ($LASTEXITCODE -ne 0) { Write-Host "copy failed: is the tablet on Wi-Fi at ${Ip}?" -ForegroundColor Red; exit 1 }
Write-Host "Watch the tablet now: condor's screen moved down (6 s), then red (4 s), then a moving gradient (4 s)." -ForegroundColor Cyan
Write-Host "When it's done, restart the tablet (hold power)." -ForegroundColor Cyan
& ssh @ssh "root@$Ip" "sh /tmp/gl-sf.sh"
