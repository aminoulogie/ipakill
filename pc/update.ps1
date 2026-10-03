# Updates ipakill's PC part without needing to see the screen: it speaks what
# happens. Run from Win+R:
#   powershell -ep bypass -c "irm https://raw.githubusercontent.com/aminoulogie/ipakill/livecontainer/pc/update.ps1|iex"
$ErrorActionPreference = 'Stop'
$voice = $null
try { $voice = New-Object -ComObject SAPI.SpVoice } catch {}
function Say($t) { Write-Host $t; if ($voice) { try { [void]$voice.Speak($t) } catch {} } }

$bin = Join-Path $env:USERPROFILE '.local\bin'
$exe = Join-Path $bin 'ipakill-core.exe'
$new = Join-Path $bin 'ipakill-core.new.exe'
try {
    New-Item -ItemType Directory -Force $bin | Out-Null
    Say 'Downloading ipakill.'
    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
    Invoke-WebRequest -UseBasicParsing 'https://github.com/aminoulogie/ipakill/releases/download/pc-core/ipakill-core.exe' -OutFile $new
    # The running server holds the .exe; stop it, swap, start the new one.
    Get-Process ipakill-core -ErrorAction SilentlyContinue | Stop-Process -Force
    Start-Sleep -Seconds 1
    Remove-Item "$exe.old" -Force -ErrorAction SilentlyContinue
    if (Test-Path $exe) { Move-Item $exe "$exe.old" }
    Move-Item $new $exe
    Start-Process $exe -ArgumentList 'serve', '--shell'
    Say 'ipakill is updated and running. You can use the terminal in the ipakill app now.'
} catch {
    Say ('The update failed. ' + $_.Exception.Message)
    Read-Host 'Press Enter to close'
}
