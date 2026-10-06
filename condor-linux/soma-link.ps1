# soma-link.ps1: link the tablet's Soma tab to your Soma (the same data as the PC and the
# phone). It asks for the two things Soma shows in Settings > Sync on the PC: the server
# address and the recovery code, and sends them to the tablet over Wi-Fi. They aren't kept
# on this PC.
#   condor-linux\soma-link.cmd [ip]
param([string]$Ip = "")
$ipFile = "$HOME\.condor\tablet-ip"
if ($Ip -eq "" -and (Test-Path $ipFile)) { $Ip = (Get-Content $ipFile -Raw).Trim() }
if ($Ip -notmatch '^\d+\.\d+\.\d+\.\d+$') { Write-Host "Which IP? condor-linux\soma-link.cmd 192.168.x.y" -ForegroundColor Red; exit 1 }
Write-Host "In Soma on this PC: Settings > Sync. Copy the Server address, then Show recovery code." -ForegroundColor Cyan
$url = (Read-Host "Server address").Trim()
$code = (Read-Host "Recovery code").Trim()
if ($url -eq "" -or $code -eq "") { Write-Host "Both are needed." -ForegroundColor Red; exit 1 }
if ($url -match "['\s]" -or $code -match "'") { Write-Host "That doesn't look right: paste them again." -ForegroundColor Red; exit 1 }
$ssh = @('-o', 'ConnectTimeout=8', '-o', 'BatchMode=yes', '-o', 'StrictHostKeyChecking=accept-new')
$d = '/proc/1/root/data/condor/soma'
& ssh @ssh "root@$Ip" "mkdir -p $d && chmod 700 $d && umask 077 && printf '%s\n%s\n' '$url' '$code' > $d/link.txt"
if ($LASTEXITCODE -ne 0) { Write-Host "Couldn't reach the tablet at $Ip (is it on Wi-Fi?)" -ForegroundColor Red; exit 1 }
Write-Host "Sent. Open the Soma tab on the tablet: it links and syncs within a few seconds." -ForegroundColor Green
