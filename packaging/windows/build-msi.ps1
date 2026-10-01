<#
 Builds dist\gorget_<version>_windows_<arch>.msi.
 Needs: Go, Node, a C compiler for the desktop app (cgo), and the WiX CLI (dotnet tool install --global wix).
 Usage:  powershell -File packaging\windows\build-msi.ps1 -Version 0.3.0 [-Arch amd64]
 Not code-signed (no certificate yet): Windows SmartScreen will warn. Sign dist\*.msi with signtool when you have one.
#>
param(
    [Parameter(Mandatory = $true)][string]$Version,
    [ValidateSet('amd64', 'arm64')][string]$Arch = 'amd64'
)
$ErrorActionPreference = 'Stop'
$root = Resolve-Path "$PSScriptRoot\..\.."
$bin = Join-Path $root "bin\windows-$Arch"
$dist = Join-Path $root 'dist'
New-Item -ItemType Directory -Force $bin, $dist | Out-Null

$ldflags = "-s -w -X github.com/anand34577/gorget/client.Version=$Version"
$env:GOOS = 'windows'; $env:GOARCH = $Arch
Push-Location $root
try {
    $env:CGO_ENABLED = '0'
    go build -trimpath -ldflags $ldflags -o "$bin\gorget.exe" .\cmd\gorget
    Push-Location desktop\frontend; npm ci --no-audit --no-fund; npm run build; Pop-Location
    Push-Location desktop
    $env:CGO_ENABLED = '1'
    go build -trimpath -ldflags "$ldflags -H=windowsgui" -o "$bin\gorget-desktop.exe" .
    Pop-Location
} finally { Pop-Location; Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED -ErrorAction SilentlyContinue }

# Wintun driver DLL (WireGuard LLC, signed). The hash pins the exact release.
$wintunZip = Join-Path $env:TEMP 'wintun-0.14.1.zip'
$wintunSha = '07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51'
if (-not (Test-Path "$bin\wintun.dll")) {
    Invoke-WebRequest 'https://www.wintun.net/builds/wintun-0.14.1.zip' -OutFile $wintunZip
    if ((Get-FileHash $wintunZip -Algorithm SHA256).Hash.ToLower() -ne $wintunSha) { throw 'wintun download does not match the pinned SHA-256' }
    $x = Join-Path $env:TEMP 'wintun-extract'
    Expand-Archive $wintunZip $x -Force
    Copy-Item "$x\wintun\bin\$Arch\wintun.dll" "$bin\wintun.dll"
}

$msi = Join-Path $dist "gorget_${Version}_windows_$Arch.msi"
wix build -ext WixToolset.Util.wixext "$PSScriptRoot\gorget.wxs" -arch $(if ($Arch -eq 'arm64') { 'arm64' } else { 'x64' }) `
    -d Version=$Version -d "BinDir=$bin" -d "Root=$root" -o $msi
Write-Host "built $msi"
