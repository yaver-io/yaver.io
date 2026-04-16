# Yaver CLI installer for Windows
# Usage: irm https://yaver.io/install.ps1 | iex
$ErrorActionPreference = "Stop"

$repo = "kivanccakmak/yaver.io"
$installDir = "$env:LOCALAPPDATA\yaver"

Write-Host "Installing yaver..." -ForegroundColor Cyan

# Get latest semver release
$releases = Invoke-RestMethod "https://api.github.com/repos/$repo/releases?per_page=100"
$latest = ($releases | Where-Object { $_.tag_name -match '^v\d' } | Select-Object -First 1).tag_name
if (-not $latest) {
    throw "Could not determine latest Yaver release"
}
Write-Host "Latest version: $latest"

$url = "https://github.com/$repo/releases/download/$latest/yaver-windows-amd64.exe"
Write-Host "Downloading $url..."

# Create install directory
New-Item -ItemType Directory -Force -Path $installDir | Out-Null

# Download
$dest = "$installDir\yaver.exe"
Invoke-WebRequest -Uri $url -OutFile $dest

# Add to PATH if not already there
$currentPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($currentPath -notlike "*$installDir*") {
    [Environment]::SetEnvironmentVariable("Path", "$currentPath;$installDir", "User")
    Write-Host "Added $installDir to PATH" -ForegroundColor Green
}

Write-Host ""
Write-Host "yaver installed to $dest" -ForegroundColor Green
Write-Host ""
& $dest version
Write-Host ""
Write-Host "Get started:" -ForegroundColor Cyan
Write-Host "  yaver auth    Sign in & start the agent"
Write-Host ""
Write-Host "Restart your terminal for PATH changes to take effect." -ForegroundColor Yellow
