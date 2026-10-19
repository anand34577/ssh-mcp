<#
.SYNOPSIS
  Runs the Go tests and cross-compiles every release target into go/dist.
.PARAMETER Version
  Version stamped into the binary (default: git describe, or "dev").
#>
param([string]$Version)
$ErrorActionPreference = 'Stop'

$projectRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$goCandidates = @()
if ($env:GO_HOME) { $goCandidates += (Join-Path $env:GO_HOME 'bin\go.exe') }
$goCommand = Get-Command go.exe -ErrorAction SilentlyContinue
if ($goCommand) { $goCandidates += $goCommand.Source }
$goCandidates += 'C:\Program Files\Go\bin\go.exe'
$goExe = $goCandidates | Where-Object { Test-Path $_ } | Select-Object -First 1
if (-not $goExe) { throw 'Go was not found. Install Go 1.26+ or set GO_HOME.' }

if (-not $Version) {
    $Version = (& git -C $projectRoot describe --tags --always --dirty 2>$null)
    if ($LASTEXITCODE -ne 0 -or -not $Version) { $Version = 'dev' }
}

$outputDirectory = Join-Path $projectRoot 'dist'
New-Item -ItemType Directory -Force -Path $outputDirectory | Out-Null

$targets = @(
    @{ GOOS = 'windows'; GOARCH = 'amd64'; Ext = '.exe' },
    @{ GOOS = 'windows'; GOARCH = 'arm64'; Ext = '.exe' },
    @{ GOOS = 'linux';   GOARCH = 'amd64'; Ext = '' },
    @{ GOOS = 'linux';   GOARCH = 'arm64'; Ext = '' },
    @{ GOOS = 'darwin';  GOARCH = 'amd64'; Ext = '' },
    @{ GOOS = 'darwin';  GOARCH = 'arm64'; Ext = '' }
)

Push-Location $projectRoot
$saved = @{ GOOS = $env:GOOS; GOARCH = $env:GOARCH; CGO_ENABLED = $env:CGO_ENABLED }
try {
    & $goExe test ./...
    if ($LASTEXITCODE -ne 0) { throw 'Go tests failed.' }

    $env:CGO_ENABLED = '0'
    foreach ($target in $targets) {
        $env:GOOS = $target.GOOS
        $env:GOARCH = $target.GOARCH
        $outputPath = Join-Path $outputDirectory "ssh-mcp-server-$($target.GOOS)-$($target.GOARCH)$($target.Ext)"
        & $goExe build -trimpath -ldflags "-s -w -X main.version=$Version" -o $outputPath ./cmd/ssh-mcp-server
        if ($LASTEXITCODE -ne 0) { throw "Go build failed for $($target.GOOS)/$($target.GOARCH)." }
        $hash = (Get-FileHash $outputPath -Algorithm SHA256).Hash.ToLower()
        "$hash  $(Split-Path $outputPath -Leaf)" | Set-Content -Encoding ascii "$outputPath.sha256"
        Write-Host "Built $outputPath ($Version)"
    }
} finally {
    $env:GOOS = $saved.GOOS
    $env:GOARCH = $saved.GOARCH
    $env:CGO_ENABLED = $saved.CGO_ENABLED
    Pop-Location
}
