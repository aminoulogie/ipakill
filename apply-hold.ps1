# apply-hold.ps1
# Run from the repo root:  cd C:\Users\pro\.condor\ipakill ; .\apply-hold.ps1
# Patches glsf.cpp ("glsf hold N" = show condor's screen unmoved for N seconds, then exit),
# gl-sf.sh (runs "glsf hold 30") and gl-sf.ps1 (prints the size/date of the glsf it sends),
# then rebuilds glsf. It does NOT touch the tablet and does NOT commit anything.
$ErrorActionPreference = 'Stop'
$root = (Get-Location).Path
$gl = Join-Path $root 'condor-linux\os\condor-gl'
$cpp = Join-Path $gl 'glsf.cpp'
$sh = Join-Path $gl 'gl-sf.sh'
$ps1 = Join-Path $root 'condor-linux\gl-sf.ps1'
foreach ($f in @($cpp, $sh, $ps1)) {
    if (-not (Test-Path $f)) { Write-Host "Missing $f - run this from the ipakill folder." -ForegroundColor Red; exit 1 }
}
$utf8 = New-Object System.Text.UTF8Encoding($false)
$nl = [Environment]::NewLine

function Patch-Regex($path, $pattern, $replacement, $what) {
    $text = [IO.File]::ReadAllText($path)
    $n = [regex]::Matches($text, $pattern).Count
    if ($n -ne 1) { throw "$what : expected 1 match, found $n. Nothing written for this step." }
    [IO.File]::WriteAllText($path, [regex]::Replace($text, $pattern, $replacement), $utf8)
    Write-Host "patched: $what" -ForegroundColor Green
}

# --- glsf.cpp -------------------------------------------------------------------------
$src = [IO.File]::ReadAllText($cpp)
if ($src -match 'int hold = 0') {
    Write-Host "glsf.cpp already has the hold mode, skipping." -ForegroundColor Yellow
} else {
    $holdCode = 'int hold = 0; /* "glsf hold N": condor screen, unmoved, for N seconds, then exit */' + $nl +
        '        if (argc > 2 && argv[1][0] == ''h'')' + $nl +
        '                for (const char *s = argv[2]; *s >= ''0'' && *s <= ''9''; s++) hold = hold * 10 + (*s - ''0'');'
    Patch-Regex $cpp '\(void\)argc;\s*\(void\)argv;' $holdCode 'glsf.cpp: read "hold N"'
    Patch-Regex $cpp 'glUniform2f\(uShift,\s*2\.0f \* 300 / FW,\s*0\);' 'glUniform2f(uShift, hold ? 0.0f : 2.0f * 300 / FW, 0);' 'glsf.cpp: no shift in hold mode'
    Patch-Regex $cpp 'while \(now\(\) - t0 < 6\) \{' 'while (now() - t0 < (hold ? hold : 6)) {' 'glsf.cpp: hold duration'
    $exit = '$1' + $nl + '        if (hold) { say("done: held the screen\n"); return 0; }' + $nl + '$2'
    Patch-Regex $cpp '(say\("     %\.1f frames/s\\n", frames / \(now\(\) - t0\)\);)(\s*// 2\. Red\.)' $exit 'glsf.cpp: exit after the hold'
}

# --- gl-sf.sh -------------------------------------------------------------------------
$shText = [IO.File]::ReadAllText($sh)
if ($shText -match 'glsf hold 30') {
    Write-Host "gl-sf.sh already runs hold 30, skipping." -ForegroundColor Yellow
} else {
    Patch-Regex $sh '/data/local/tmp/glsf 2>&1;' '/data/local/tmp/glsf hold 30 2>&1;' 'gl-sf.sh: run "glsf hold 30"'
}

# --- gl-sf.ps1: show which glsf is about to be sent --------------------------------------
$psText = [IO.File]::ReadAllText($ps1)
if ($psText -match 'glsf to send') {
    Write-Host "gl-sf.ps1 already prints the glsf size, skipping." -ForegroundColor Yellow
} else {
    $marker = '& scp @ssh $bin'
    if (([regex]::Matches($psText, [regex]::Escape($marker))).Count -ne 1) { throw "gl-sf.ps1: scp line not found exactly once." }
    $line = 'Write-Host ("glsf to send: " + (Get-Item $bin).Length + " bytes, built " + (Get-Item $bin).LastWriteTime) -ForegroundColor Cyan'
    [IO.File]::WriteAllText($ps1, $psText.Replace($marker, $line + $nl + $marker), $utf8)
    Write-Host "patched: gl-sf.ps1 prints the glsf size and date" -ForegroundColor Green
}

# --- build ------------------------------------------------------------------------------
Write-Host "building glsf..." -ForegroundColor Cyan
Push-Location $gl
try {
    $haveTools = (Get-Command sh -ErrorAction SilentlyContinue) -and (Get-Command clang++ -ErrorAction SilentlyContinue) -and (Get-Command ld.lld -ErrorAction SilentlyContinue)
    if ($haveTools) {
        & sh ./build.sh
    } elseif (Get-Command wsl -ErrorAction SilentlyContinue) {
        # C:\Users\pro\... -> /mnt/c/Users/pro/... (wslpath loses the backslashes when called from here)
        $wp = '/mnt/' + $gl.Substring(0, 1).ToLower() + ($gl.Substring(2) -replace '\\', '/')
        $have = (wsl sh -c "command -v clang++ >/dev/null && command -v ld.lld >/dev/null && echo yes") 2>$null
        if ($have -ne 'yes') {
            throw "WSL has no clang++ / ld.lld. In WSL run:  sudo apt update && sudo apt install -y clang lld   then run this script again (the patches are already applied, it will skip them)."
        }
        wsl sh -c "cd '$wp' && sh ./build.sh"
    } else {
        throw "No sh + clang++ + ld.lld on this PC, and no WSL. The source is patched; glsf was NOT rebuilt."
    }
    if ($LASTEXITCODE -ne 0) { throw "build.sh failed (exit $LASTEXITCODE). Paste the output." }
} finally {
    Pop-Location
}

# build.sh also rebuilds gltest and glanim: put the committed versions back so only glsf changes.
git checkout -- condor-linux/os/condor-init/glanim.bin condor-linux/os/condor-gl/glanim condor-linux/os/condor-gl/gltest 2>$null

$bin = Join-Path $gl 'glsf'
Write-Host ""
Write-Host ("glsf is now " + (Get-Item $bin).Length + " bytes, built " + (Get-Item $bin).LastWriteTime + " (it was 14748 before).") -ForegroundColor Green
git --no-pager status --short
Write-Host ""
Write-Host "Nothing was sent to the tablet. To test: charge it, 'condor takeover arm', reboot, wait for Wi-Fi, then:" -ForegroundColor Cyan
Write-Host "  .\condor-linux\gl-sf.cmd <tablet-ip>" -ForegroundColor Cyan
