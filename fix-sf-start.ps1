# fix-sf-start.ps1
# Run from the repo root:  cd C:\Users\pro\.condor\ipakill ; .\fix-sf-start.ps1
# gl-sf.sh started SurfaceFlinger from the Alpine shell, which has no Android property
# environment, so "start surfaceflinger" did nothing and glsf waited for it forever.
# This starts it through condor-init's root shell (port 2324), the same place glsf runs, and
# only launches glsf if SurfaceFlinger really shows up in the process list.
# It does not touch the tablet and does not commit anything.
$ErrorActionPreference = 'Stop'
$sh = Join-Path (Get-Location).Path 'condor-linux\os\condor-gl\gl-sf.sh'
if (-not (Test-Path $sh)) { Write-Host "Run this from the ipakill folder." -ForegroundColor Red; exit 1 }
$utf8 = New-Object System.Text.UTF8Encoding($false)
$text = [IO.File]::ReadAllText($sh)
if ($text -match 'SurfaceFlinger did not start') { Write-Host "Already patched." -ForegroundColor Yellow; exit 0 }
$pattern = '/system/bin/start surfaceflinger\r?\nsleep 3'
if ([regex]::Matches($text, $pattern).Count -ne 1) { throw "Could not find the 'start surfaceflinger / sleep 3' lines exactly once. Nothing written." }
$new = @'
printf '/system/bin/start surfaceflinger; exit\n' | nc 127.0.0.1 2324 >/dev/null 2>&1
sleep 4
if ! printf 'ps; exit\n' | nc 127.0.0.1 2324 | grep -q surfaceflinger; then
        echo "SurfaceFlinger did not start (not in the process list): not running glsf"
        exit 1
fi
echo "SurfaceFlinger is running"
'@
$new = $new -replace "`r?`n", "`n"
[IO.File]::WriteAllText($sh, [regex]::Replace($text, $pattern, { param($m) $new.TrimEnd("`n") }), $utf8)
Write-Host "patched: gl-sf.sh starts SurfaceFlinger through the root shell" -ForegroundColor Green
git --no-pager diff --stat
