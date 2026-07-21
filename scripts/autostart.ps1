# Sets Portanote to start hidden at every login (no console window).
# Detects the binary and notes folder automatically and drops a launcher
# into your Startup folder. Nothing else is touched; run with -Uninstall
# to remove it again.
#
# Run it FROM THE FOLDER THAT HOLDS THE PORTANOTE BINARY:
#   powershell -ExecutionPolicy Bypass -File scripts\autostart.ps1
# or with no repo checkout at all:
#   iwr -useb https://raw.githubusercontent.com/jake-kelley/portanote/main/scripts/autostart.ps1 | iex

[CmdletBinding()]
param(
    # Path to the portanote exe. Default: auto-detect in the current folder.
    [string]$Binary = '',
    # Notes folder to pass as -dir. Default: notes\ next to the binary.
    [string]$NotesDir = '',
    # Remove the launcher from the Startup folder instead of installing it.
    [switch]$Uninstall,
    # Override the Startup folder (mainly for testing).
    [string]$StartupDir = ''
)

$ErrorActionPreference = 'Stop'

if ($StartupDir -eq '') { $StartupDir = [Environment]::GetFolderPath('Startup') }
$launcher = Join-Path $StartupDir 'portanote.vbs'

if ($Uninstall) {
    if (Test-Path $launcher) {
        Remove-Item $launcher -Force
        Write-Host "Removed $launcher - Portanote no longer starts at login."
        Write-Host "If it is running right now, stop it via Task Manager."
    } else {
        Write-Host "Nothing to remove - no launcher at $launcher."
    }
    exit 0
}

$root = (Get-Location).Path

if ($Binary -eq '') {
    foreach ($name in @('portanote-windows-amd64.exe', 'portanote.exe')) {
        $c = Join-Path $root $name
        if (Test-Path $c) { $Binary = $c; break }
    }
    if ($Binary -eq '') {
        $any = Get-ChildItem $root -Filter 'portanote*.exe' -File -ErrorAction SilentlyContinue | Select-Object -First 1
        if ($any) { $Binary = $any.FullName }
    }
    if ($Binary -eq '') {
        throw "No portanote exe found in $root. Run this from the folder that holds the binary, or pass -Binary <path>."
    }
} else {
    $Binary = (Resolve-Path $Binary).Path
}

if ($NotesDir -eq '') { $NotesDir = Join-Path (Split-Path $Binary -Parent) 'notes' }

# 0 = hidden window; -no-browser so login doesn't pop a browser tab
$line = 'CreateObject("WScript.Shell").Run """' + $Binary + '"" -no-browser -dir ""' + $NotesDir + '""", 0, False'
# UTF-16 LE: the encoding Windows Script Host reads reliably for any username
[System.IO.File]::WriteAllText($launcher, $line + "`r`n", [System.Text.Encoding]::Unicode)

Write-Host "Installed $launcher"
Write-Host "  binary: $Binary"
Write-Host "  notes:  $NotesDir"
Write-Host ""
Write-Host "Portanote will start hidden at your next login - bookmark http://127.0.0.1:8737"
Write-Host "Undo anytime:  powershell -ExecutionPolicy Bypass -File scripts\autostart.ps1 -Uninstall"
