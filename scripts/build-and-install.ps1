<#
.SYNOPSIS
    Build and install the omnicode CLI binary.
.DESCRIPTION
    Builds the omnicode binary into build/ and optionally installs it to
    $HOME/.local/bin.
.PARAMETER Install
    If set, copies the binary to $HOME/.local/bin.
.PARAMETER Version
    Version string to embed via ldflags. Auto-detected from git tag if not provided.
.PARAMETER Clean
    If set, runs go clean before building.
.EXAMPLE
    .\scripts\build-and-install.ps1
    .\scripts\build-and-install.ps1 -Install
    .\scripts\build-and-install.ps1 -Install -Clean
#>

param(
    [switch]$Install,
    [string]$Version = "",
    [switch]$Clean
)

$ErrorActionPreference = "Stop"
$RepoRoot = Resolve-Path "$PSScriptRoot\.."
$BuildDir = Join-Path $RepoRoot "build"

# --- Defaults ---
if (-not $Version) {
    $gitTag = & git -C $RepoRoot tag --points-at HEAD 2>$null
    if ($gitTag) {
        $Version = $gitTag -join ","
    } else {
        $gitCommit = & git -C $RepoRoot rev-parse --short HEAD 2>$null
        $Version = if ($gitCommit) { "dev-$gitCommit" } else { "dev" }
    }
}

Write-Host "=== omnicode build script ===" -ForegroundColor Cyan
Write-Host "Repo root : $RepoRoot"
Write-Host "Build dir : $BuildDir"
Write-Host "Version   : $Version"
Write-Host ""

# --- Clean ---
if ($Clean) {
    Write-Host ">> Cleaning..." -ForegroundColor Yellow
    & go clean -cache
    & go clean -testcache
}

# --- Ensure build directory ---
if (-not (Test-Path $BuildDir)) {
    New-Item -ItemType Directory -Path $BuildDir -Force | Out-Null
}

# --- Build ---
Write-Host ">> Building omnicode..." -ForegroundColor Green
$ldflags = "-X 'main.version=$Version' -X 'main.date=$(Get-Date -Format 'yyyy-MM-dd')'"
$binaryName = "omnicode.exe"
$binaryPath = Join-Path $BuildDir $binaryName

Push-Location $RepoRoot
try {
    & go build -ldflags $ldflags -o $binaryPath ./cmd/omnicode
    if ($LASTEXITCODE -ne 0) {
        throw "go build failed with exit code $LASTEXITCODE"
    }
} finally {
    Pop-Location
}

Write-Host ">> Binary created: $binaryPath" -ForegroundColor Green

# --- Verify ---
Write-Host ">> Verifying binary..." -ForegroundColor Yellow
$fileInfo = Get-Item $binaryPath
Write-Host "   Size: $($fileInfo.Length.ToString('N0')) bytes"

# --- Install ---
if ($Install) {
    $installDir = "$HOME\.local\bin"
    Write-Host ">> Installing to $installDir ..." -ForegroundColor Green

    if (-not (Test-Path $installDir)) {
        New-Item -ItemType Directory -Path $installDir -Force | Out-Null
    }

    $installPath = Join-Path $installDir $binaryName
    Copy-Item -Path $binaryPath -Destination $installPath -Force
    Write-Host "   Installed: $installPath"

    # Add to user PATH if not already there
    $userPath = [Environment]::GetEnvironmentVariable("PATH", "User")
    if ($userPath -notlike "*$installDir*") {
        $newPath = "$installDir;$userPath"
        [Environment]::SetEnvironmentVariable("PATH", $newPath, "User")
        Write-Host "   Added $installDir to user PATH (restart terminal to apply)" -ForegroundColor Yellow
    } else {
        Write-Host "   $installDir already in PATH" -ForegroundColor Gray
    }
}

Write-Host ""
Write-Host "=== Build complete ===" -ForegroundColor Cyan
return $binaryPath