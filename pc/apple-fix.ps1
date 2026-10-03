# Some networks can't reach every address Apple's servers answer on (the
# connection to port 443 just times out), which makes plumesign fail at
# "Restoring session". For each Apple server plumesign uses, this finds an
# address that does connect, by asking several public DNS servers, and pins it
# in the hosts file. Run in an Administrator PowerShell:
#   irm https://raw.githubusercontent.com/aminoulogie/ipakill/livecontainer/pc/apple-fix.ps1 | iex
# Undo: run it again with $undo = $true set first, or delete the lines ending
# in "# ipakill-apple" from C:\Windows\System32\drivers\etc\hosts.

$hostsFile = "$env:SystemRoot\System32\drivers\etc\hosts"
$tag = '# ipakill-apple'
$servers = 'gsa.apple.com', 'developerservices2.apple.com', 'idmsa.apple.com', 'appleid.apple.com'
$dnsServers = '1.1.1.1', '8.8.8.8', '9.9.9.9', '208.67.222.222', '1.0.0.1', '8.8.4.4'
# Apple answers differently by region; Google's DNS-over-HTTPS can ask "as if
# from" other networks, which turns up more addresses to try.
$regions = '41.140.0.0/16', '81.192.0.0/16', '89.0.0.0/16', '2.0.0.0/16', '8.8.8.0/24', '1.1.1.0/24', '103.0.0.0/16', '200.0.0.0/16'
# Addresses already seen working from a network with this problem.
$known = @{ 'gsa.apple.com' = @('17.157.64.66') }

$admin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole(
    [Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $admin) {
    Write-Host 'Run this in an Administrator PowerShell (Windows key + X > Terminal (Admin)).' -ForegroundColor Red
    return
}

function Test-Port443($ip) {
    $c = New-Object Net.Sockets.TcpClient
    try { return $c.ConnectAsync($ip, 443).Wait(3000) -and $c.Connected } catch { return $false } finally { $c.Close() }
}

# Start from a hosts file without our old pins, so we test what the network gives.
$kept = Get-Content $hostsFile | Where-Object { $_ -notmatch [regex]::Escape($tag) }
Set-Content -Path $hostsFile -Value $kept -Encoding ASCII
ipconfig /flushdns | Out-Null
if ($undo) { Write-Host 'Removed the ipakill Apple pins.' -ForegroundColor Green; return }

$pins = @()
foreach ($name in $servers) {
    $current = @((Resolve-DnsName $name -Type A -ErrorAction SilentlyContinue | Where-Object IP4Address).IP4Address)
    $first = if ($current.Count -gt 0) { $current[0] } else { $null }
    if ($first -and (Test-Port443 $first)) {
        Write-Host "${name} OK ($first)" -ForegroundColor Green
        continue
    }
    $candidates = @()
    if ($known[$name]) { $candidates += $known[$name] }
    foreach ($dns in $dnsServers) {
        $candidates += (Resolve-DnsName $name -Type A -Server $dns -ErrorAction SilentlyContinue | Where-Object IP4Address).IP4Address
    }
    foreach ($subnet in $regions) {
        try {
            $r = Invoke-RestMethod "https://dns.google/resolve?name=$name&type=A&edns_client_subnet=$subnet" -TimeoutSec 5
            $candidates += ($r.Answer | Where-Object type -eq 1).data
        } catch {}
    }
    $candidates = $candidates | Where-Object { $_ -and $_ -ne $first }
    $found = $null
    foreach ($ip in ($candidates | Select-Object -Unique)) {
        if (Test-Port443 $ip) { $found = $ip; break }
    }
    if ($found) {
        Write-Host "${name} blocked at $($current -join ', ') - pinned to $found" -ForegroundColor Yellow
        $pins += "$found`t$name`t$tag"
    } else {
        Write-Host "${name}: no reachable address found (tried $($candidates -join ', '))" -ForegroundColor Red
    }
}

if ($pins) {
    Add-Content -Path $hostsFile -Value $pins -Encoding ASCII
    ipconfig /flushdns | Out-Null
}
Write-Host 'Done. Try the install again.' -ForegroundColor Green
