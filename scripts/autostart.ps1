# Deploys Portanote to your Documents folder and sets it to start hidden at
# every login (no console window). Detects everything it needs: the binary in
# the current folder, your Documents path, and the notes folder. Run with
# -Uninstall to remove the login launcher again.
#
# Run it FROM THE FOLDER THAT HOLDS THE DOWNLOADED PORTANOTE BINARY:
#   powershell -ExecutionPolicy Bypass -File autostart.ps1
# or with no downloads at all beyond the binary:
#   iwr -useb https://raw.githubusercontent.com/jake-kelley/portanote/main/scripts/autostart.ps1 | iex
#
# What it does:
#   1. copies the binary into Documents\portanote\ (plus notes\ and tools\ if
#      they sit next to the binary and aren't already there) - use -InPlace
#      to skip this and run the binary from where it is
#   2. installs a hidden launcher in your Startup folder pointing there

[CmdletBinding()]
param(
    # Path to the portanote exe. Default: auto-detect in the current folder.
    [string]$Binary = '',
    # Where to deploy. Default: Documents\portanote.
    [string]$Dest = '',
    # Notes folder to pass as -dir. Default: notes\ inside the deploy folder.
    [string]$NotesDir = '',
    # Don't copy anything; run the binary from where it is now.
    [switch]$InPlace,
    # Don't start Portanote (or open the browser) after installing.
    [switch]$NoStart,
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
        Write-Host "Your deployed folder (binary + notes) is untouched."
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

if ($Dest -eq '') { $Dest = Join-Path ([Environment]::GetFolderPath('MyDocuments')) 'portanote' }
$srcDir = Split-Path $Binary -Parent

# deploy unless asked not to (or the binary already lives in the destination)
$copied = @()
if (-not $InPlace -and $srcDir.TrimEnd('\') -ine $Dest.TrimEnd('\')) {
    New-Item -ItemType Directory -Force $Dest | Out-Null
    $target = Join-Path $Dest (Split-Path $Binary -Leaf)
    try {
        Copy-Item $Binary $target -Force
    } catch {
        throw "Could not copy the binary to $target - if Portanote is running from there, stop it first (Task Manager). ($_)"
    }
    $copied += (Split-Path $Binary -Leaf)
    # clear mark-of-the-web so SmartScreen doesn't block the background launch
    Unblock-File $target -ErrorAction SilentlyContinue
    # bring along notes/tools sitting next to the downloaded binary, but never
    # overwrite ones already deployed
    foreach ($d in @('notes', 'tools')) {
        $srcSub = Join-Path $srcDir $d
        $dstSub = Join-Path $Dest $d
        if ((Test-Path $srcSub) -and -not (Test-Path $dstSub)) {
            Copy-Item $srcSub $dstSub -Recurse
            $copied += "$d\"
        }
    }
    $Binary = $target
} else {
    $Dest = $srcDir
}

if ($NotesDir -eq '') { $NotesDir = Join-Path $Dest 'notes' }

# 0 = hidden window; -no-browser so login doesn't pop a browser tab
$line = 'CreateObject("WScript.Shell").Run """' + $Binary + '"" -no-browser -dir ""' + $NotesDir + '""", 0, False'
# UTF-16 LE: the encoding Windows Script Host reads reliably for any username
[System.IO.File]::WriteAllText($launcher, $line + "`r`n", [System.Text.Encoding]::Unicode)

if ($copied.Count -gt 0) {
    Write-Host "Deployed to $Dest  ($($copied -join ', '))"
    Write-Host "The copy in $srcDir is no longer used - delete it when convenient."
}
Write-Host "Installed $launcher"
Write-Host "  binary: $Binary"
Write-Host "  notes:  $NotesDir"
Write-Host ""

if ($NoStart) {
    Write-Host "Portanote will start hidden at your next login - bookmark http://127.0.0.1:8737"
} else {
    $name = [System.IO.Path]::GetFileNameWithoutExtension($Binary)
    if (-not (Get-Process $name -ErrorAction SilentlyContinue)) {
        Start-Process wscript.exe -ArgumentList ('"' + $launcher + '"')
        Start-Sleep -Seconds 2
    }
    Start-Process "http://127.0.0.1:8737"
    Write-Host "Portanote is running now and will start at every login - bookmark http://127.0.0.1:8737"
}
Write-Host "Undo autostart anytime:  powershell -ExecutionPolicy Bypass -File autostart.ps1 -Uninstall"
