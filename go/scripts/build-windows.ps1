<#
.SYNOPSIS
  Runs the Go tests and builds the Windows binary for this machine's CPU into go/dist.
.PARAMETER Arch
  amd64 or arm64 (default: the current machine's architecture).
.PARAMETER Version
  Version stamped into the binary (default: git describe, or "dev").
#>
param([ValidateSet('amd64', 'arm64')][string]$Arch, [string]$Version)
$ErrorActionPreference = 'Stop'

$projectRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$goCandidates = @()
if ($env:GO_HOME) { $goCandidates += (Join-Path $env:GO_HOME 'bin\go.exe') }
$goCommand = Get-Command go.exe -ErrorAction SilentlyContinue
if ($goCommand) { $goCandidates += $goCommand.Source }
$goCandidates += 'C:\Program Files\Go\bin\go.exe'
$goExe = $goCandidates | Where-Object { Test-Path $_ } | Select-Object -First 1
if (-not $goExe) { throw 'Go was not found. Install Go 1.26+ or set GO_HOME.' }

if (-not $Arch) { $Arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' } }
if (-not $Version) {
    $Version = (& git -C $projectRoot describe --tags --always --dirty 2>$null)
    if ($LASTEXITCODE -ne 0 -or -not $Version) { $Version = 'dev' }
}

$outputDirectory = Join-Path $projectRoot 'dist'
New-Item -ItemType Directory -Force -Path $outputDirectory | Out-Null

Push-Location $projectRoot
$saved = @{ GOOS = $env:GOOS; GOARCH = $env:GOARCH; CGO_ENABLED = $env:CGO_ENABLED }
try {
    & $goExe test ./...
    if ($LASTEXITCODE -ne 0) { throw 'Go tests failed.' }

    $env:GOOS = 'windows'
    $env:GOARCH = $Arch
    $env:CGO_ENABLED = '0'
    $outputPath = Join-Path $outputDirectory "ssh-mcp-server-windows-$Arch.exe"
    & $goExe build -trimpath -ldflags "-s -w -X main.version=$Version" -o $outputPath ./cmd/ssh-mcp-server
    if ($LASTEXITCODE -ne 0) { throw "Go build failed for windows/$Arch." }
    Write-Host "Built $outputPath ($Version)"
} finally {
    $env:GOOS = $saved.GOOS
    $env:GOARCH = $saved.GOARCH
    $env:CGO_ENABLED = $saved.CGO_ENABLED
    Pop-Location
}
