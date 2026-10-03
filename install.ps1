# Installs the latest vanityrig release binary on Windows.
# Usage: irm https://raw.githubusercontent.com/bytestrix/vanityrig/main/install.ps1 | iex
$ErrorActionPreference = 'Stop'

$repo = 'bytestrix/vanityrig'
$binDir = if ($env:VANITYRIG_INSTALL_DIR) { $env:VANITYRIG_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'vanityrig' }

$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
    'AMD64' { 'amd64' }
    'ARM64' { 'arm64' }
    default { throw "unsupported architecture: $env:PROCESSOR_ARCHITECTURE" }
}

$version = (Invoke-RestMethod "https://api.github.com/repos/$repo/releases/latest").tag_name
if (-not $version) { throw "could not determine the latest release of $repo" }

$archive = "vanityrig_windows_$arch.zip"
$url = "https://github.com/$repo/releases/download/$version/$archive"

$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ([System.Guid]::NewGuid())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    Write-Host "Downloading vanityrig $version for windows/$arch..."
    Invoke-WebRequest $url -OutFile (Join-Path $tmp $archive)
    Expand-Archive (Join-Path $tmp $archive) -DestinationPath $tmp

    New-Item -ItemType Directory -Force -Path $binDir | Out-Null
    Move-Item -Force (Join-Path $tmp 'vanityrig.exe') (Join-Path $binDir 'vanityrig.exe')
} finally {
    Remove-Item -Recurse -Force $tmp
}

Write-Host "Installed vanityrig $version to $binDir\vanityrig.exe"

$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (($userPath -split ';') -notcontains $binDir) {
    [Environment]::SetEnvironmentVariable('Path', "$userPath;$binDir", 'User')
    Write-Host "Added $binDir to your PATH. Open a new terminal, then run: vanityrig"
} else {
    Write-Host 'Run it: vanityrig'
}
