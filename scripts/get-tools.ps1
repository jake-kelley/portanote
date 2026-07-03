# Downloads portable pandoc + tectonic into .\tools\ (no installation).
# These enable the true Eisvogel PDF export. Run from the portanote folder:
#   powershell -ExecutionPolicy Bypass -File scripts\get-tools.ps1

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
$tools = Join-Path $root "tools"
New-Item -ItemType Directory -Force $tools | Out-Null
$tmp = Join-Path $env:TEMP "portanote-tools"
New-Item -ItemType Directory -Force $tmp | Out-Null

function Get-Asset($repo, $pattern) {
    $rel = Invoke-RestMethod "https://api.github.com/repos/$repo/releases/latest"
    $asset = $rel.assets | Where-Object { $_.name -match $pattern } | Select-Object -First 1
    if (-not $asset) { throw "no asset matching $pattern in $repo" }
    Write-Host "downloading $($asset.name) ..."
    $out = Join-Path $tmp $asset.name
    Invoke-WebRequest $asset.browser_download_url -OutFile $out
    return $out
}

# --- pandoc ---
$zip = Get-Asset "jgm/pandoc" "windows-x86_64\.zip$"
Expand-Archive -Force $zip $tmp
$pandoc = Get-ChildItem $tmp -Recurse -Filter pandoc.exe | Select-Object -First 1
Copy-Item $pandoc.FullName (Join-Path $tools "pandoc.exe") -Force
Write-Host "tools\pandoc.exe ready"

# --- tectonic (self-contained LaTeX engine) ---
$zip = Get-Asset "tectonic-typesetting/tectonic" "x86_64-pc-windows-msvc\.zip$"
Expand-Archive -Force $zip $tmp
$tect = Get-ChildItem $tmp -Recurse -Filter tectonic.exe | Select-Object -First 1
Copy-Item $tect.FullName (Join-Path $tools "tectonic.exe") -Force
Write-Host "tools\tectonic.exe ready"

Remove-Item -Recurse -Force $tmp
Write-Host "`nDone. First PDF export downloads LaTeX packages (~100 MB) into tools\tectonic-cache - allow a few minutes."
