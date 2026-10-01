# Gorget client installer for Windows.
#
#   irm https://vpn.example.com/install.ps1 | iex        (from your server: it knows its own address)
#   irm https://raw.githubusercontent.com/anand34577/gorget/main/install.ps1 | iex
#
# Options (environment variables, set before running):
#   $env:GORGET_SERVER     connect to this server after installing
#   $env:GORGET_SETUP_KEY  sign in with a setup key instead of the browser
#   $env:GORGET_VERSION    install a specific release such as v0.4.0 (default: the latest)
#
# Downloads only from the project's GitHub releases and checks the installer against the
# release's SHA256SUMS. Windows asks for administrator approval once, for the installer.

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'   # Invoke-WebRequest is much faster without the progress bar
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

$Repo = 'anand34577/gorget'
# A Gorget server serving this file fills in its own address here.
$DefaultServer = ''

$Server = if ($env:GORGET_SERVER) { $env:GORGET_SERVER } else { $DefaultServer }
$SetupKey = $env:GORGET_SETUP_KEY
$Version = $env:GORGET_VERSION

function Step($msg) { Write-Host "==> $msg" -ForegroundColor Cyan }
function Fail($msg) { Write-Host "Error: $msg" -ForegroundColor Red; throw $msg }

$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
    'AMD64' { 'amd64' }
    'ARM64' { 'arm64' }
    default { Fail "unsupported processor $($env:PROCESSOR_ARCHITECTURE) (64-bit Intel/AMD and ARM are available)" }
}

if ($Version) {
    if (-not $Version.StartsWith('v')) { $Version = "v$Version" }
    $base = "https://github.com/$Repo/releases/download/$Version"
} else {
    $base = "https://github.com/$Repo/releases/latest/download"
}

$tmp = Join-Path ([IO.Path]::GetTempPath()) ("gorget-" + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    Step 'Checking the latest Gorget release'
    try { $sums = (Invoke-WebRequest -UseBasicParsing "$base/SHA256SUMS").Content }
    catch { Fail "couldn't download the release list from $base (no internet, or no release yet?)" }
    if ($sums -is [byte[]]) { $sums = [Text.Encoding]::UTF8.GetString($sums) }

    $entries = foreach ($line in ($sums -split "`n")) {
        $parts = $line.Trim() -split '\s+', 2
        if ($parts.Count -eq 2) { [pscustomobject]@{ Hash = $parts[0].ToLower(); Name = $parts[1].TrimStart('*') } }
    }
    $msi = $entries | Where-Object { $_.Name -match "^gorget_[0-9][^_]*_windows_$arch\.msi$" } | Select-Object -First 1
    if (-not $msi -and $arch -eq 'arm64') {
        # x64 programs run on Windows on ARM; the tunnel driver needs the native build, so say so.
        Fail 'this release has no Windows ARM installer yet'
    }
    if (-not $msi) { Fail "this release has no Windows installer for $arch" }

    $file = Join-Path $tmp $msi.Name
    Step "Downloading $($msi.Name)"
    Invoke-WebRequest -UseBasicParsing "$base/$($msi.Name)" -OutFile $file
    $got = (Get-FileHash $file -Algorithm SHA256).Hash.ToLower()
    if ($got -ne $msi.Hash) { Fail "checksum mismatch for $($msi.Name) (expected $($msi.Hash), got $got). Not installing." }

    Step 'Installing (Windows asks for administrator approval)'
    $p = Start-Process msiexec.exe -ArgumentList @('/i', "`"$file`"", '/qb', '/norestart') -Verb RunAs -Wait -PassThru
    if ($p.ExitCode -ne 0 -and $p.ExitCode -ne 3010) { Fail "the installer stopped with code $($p.ExitCode)" }
} finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}

$gorget = Join-Path $env:ProgramFiles 'Gorget\gorget.exe'
if (-not (Test-Path $gorget)) { Fail "gorget.exe wasn't found in $env:ProgramFiles\Gorget" }

# Wait until the service answers.
for ($i = 0; $i -lt 30; $i++) {
    & $gorget status *> $null
    if ($LASTEXITCODE -eq 0) { break }
    Start-Sleep -Milliseconds 500
}

Write-Host ''
Write-Host "Gorget is installed. The tray app is in the Start menu; open a new terminal to use the gorget command." -ForegroundColor Green

if (-not $Server) {
    Write-Host ''
    Write-Host 'Connect this computer:'
    Write-Host '  gorget up -server vpn.example.com'
    return
}

Write-Host ''
Step "Connecting to $Server"
if ($SetupKey) {
    # Through standard input, so the key doesn't appear in the process list.
    $SetupKey | & $gorget up -server $Server -setup-key -
} else {
    & $gorget up -server $Server
}
