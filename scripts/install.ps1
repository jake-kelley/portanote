# Installs Portanote: downloads the latest release binary if none is present
# (verifying its sha256 checksum), deploys it to your Documents folder, starts
# it, and sets it to start hidden at every login. Detects everything it needs.
# Run with -Uninstall to remove the login launcher again (the deployed folder
# with your notes is left alone).
#
# One-liner, from anywhere:
#   iwr -useb https://raw.githubusercontent.com/jake-kelley/portanote/main/scripts/install.ps1 | iex
# Already downloaded a binary? Run it from that folder and it is used instead.
#
# What it does:
#   1. finds the portanote exe in the current folder, or downloads the latest
#      release and verifies it against the release's sha256sums.txt
#   2. deploys to Documents\portanote\ (plus notes\ and tools\ if they sit
#      next to a local binary and aren't already there) - use -InPlace to
#      run from the current folder instead
#   3. installs a hidden launcher in your Startup folder, starts Portanote,
#      and opens http://127.0.0.1:8737

[CmdletBinding()]
param(
    # Path to the portanote exe. Default: auto-detect, then download.
    [string]$Binary = '',
    # Where to deploy. Default: Documents\portanote.
    [string]$Dest = '',
    # Notes folder to pass as -dir. Default: notes\ inside the deploy folder.
    [string]$NotesDir = '',
    # GitHub repo to download from.
    [string]$Repo = 'jake-kelley/portanote',
    # Don't copy anything; use/download the binary in the current folder.
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
if ($Dest -eq '') { $Dest = Join-Path ([Environment]::GetFolderPath('MyDocuments')) 'portanote' }

# --- find or download the binary -------------------------------------------

if ($Binary -eq '') {
    foreach ($name in @('portanote-windows-amd64.exe', 'portanote.exe')) {
        $c = Join-Path $root $name
        if (Test-Path $c) { $Binary = $c; break }
    }
    if ($Binary -eq '') {
        $any = Get-ChildItem $root -Filter 'portanote*.exe' -File -ErrorAction SilentlyContinue | Select-Object -First 1
        if ($any) { $Binary = $any.FullName }
    }
} else {
    $Binary = (Resolve-Path $Binary).Path
}

if ($Binary -eq '') {
    $name = 'portanote-windows-amd64.exe'
    if ($InPlace) { $dlDir = $root } else { $dlDir = $Dest }
    New-Item -ItemType Directory -Force $dlDir | Out-Null
    $target = Join-Path $dlDir $name
    $base = "https://github.com/$Repo/releases/latest/download"
    Write-Host "Downloading the latest release from $Repo ..."
    Invoke-WebRequest -UseBasicParsing "$base/$name" -OutFile $target
    # same integrity check the in-app updater performs
    $sums = (Invoke-WebRequest -UseBasicParsing "$base/sha256sums.txt").Content
    # GitHub serves the file as octet-stream, so Content may arrive as bytes
    if ($sums -is [byte[]]) { $sums = [System.Text.Encoding]::UTF8.GetString($sums) }
    $expected = ''
    foreach ($sumLine in ($sums -split "`n")) {
        if ($sumLine -match "^([0-9a-f]{64})\s+$([regex]::Escape($name))\s*$") { $expected = $Matches[1]; break }
    }
    $actual = (Get-FileHash $target -Algorithm SHA256).Hash.ToLower()
    if ($expected -eq '' -or $actual -ne $expected) {
        Remove-Item $target -Force -ErrorAction SilentlyContinue
        throw "Checksum verification failed for $name (expected '$expected', got '$actual') - download discarded."
    }
    Write-Host "Verified sha256 checksum."
    Unblock-File $target -ErrorAction SilentlyContinue
    $Binary = $target
}

# --- deploy -----------------------------------------------------------------

$srcDir = Split-Path $Binary -Parent
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

# --- launcher ---------------------------------------------------------------

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
    $procName = [System.IO.Path]::GetFileNameWithoutExtension($Binary)
    if (-not (Get-Process $procName -ErrorAction SilentlyContinue)) {
        Start-Process wscript.exe -ArgumentList ('"' + $launcher + '"')
        Start-Sleep -Seconds 2
    }
    Start-Process "http://127.0.0.1:8737"
    Write-Host "Portanote is running now and will start at every login - bookmark http://127.0.0.1:8737"
}
Write-Host "Undo autostart anytime:  powershell -ExecutionPolicy Bypass -File install.ps1 -Uninstall"
