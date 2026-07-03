# Builds portanote for Windows and Apple Silicon macOS.
# Uses go from PATH, or the portable toolchain at ~\.toolchains\go.
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot

$go = "go"
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    $go = Join-Path $env:USERPROFILE ".toolchains\go\bin\go.exe"
    if (-not (Test-Path $go)) { throw "go not found on PATH or in ~\.toolchains\go" }
}

Push-Location $root
try {
    $env:GOOS = "windows"; $env:GOARCH = "amd64"
    & $go build -ldflags="-s -w" -o dist\portanote-windows-amd64.exe .
    $env:GOOS = "darwin"; $env:GOARCH = "arm64"
    & $go build -ldflags="-s -w" -o dist\portanote-macos-arm64 .
    Copy-Item dist\portanote-windows-amd64.exe portanote.exe -Force
    Write-Host "built: dist\portanote-windows-amd64.exe, dist\portanote-macos-arm64 (+ .\portanote.exe copy)"
} finally {
    Remove-Item env:GOOS, env:GOARCH -ErrorAction SilentlyContinue
    Pop-Location
}
