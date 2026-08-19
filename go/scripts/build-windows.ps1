$ErrorActionPreference = 'Stop'

$projectRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$goCandidates = @()
if ($env:GO_HOME) {
    $goCandidates += (Join-Path $env:GO_HOME 'bin\go.exe')
}
$goCommand = Get-Command go.exe -ErrorAction SilentlyContinue
if ($goCommand) {
    $goCandidates += $goCommand.Source
}
$goCandidates += 'C:\Program Files\Go\bin\go.exe'
$goExe = $goCandidates | Where-Object { Test-Path $_ } | Select-Object -First 1
if (-not $goExe) {
    throw 'Go was not found. Install Go or set GO_HOME.'
}

$outputDirectory = Join-Path $projectRoot 'dist'
New-Item -ItemType Directory -Force -Path $outputDirectory | Out-Null

Push-Location $projectRoot
$originalGoOS = $env:GOOS
$originalGoARCH = $env:GOARCH
$originalCgoEnabled = $env:CGO_ENABLED
try {
    & $goExe test ./...
    if ($LASTEXITCODE -ne 0) { throw 'Go tests failed.' }

    $env:GOOS = 'windows'
    $env:GOARCH = 'amd64'
    $env:CGO_ENABLED = '0'
    $outputPath = Join-Path $outputDirectory 'ssh-mcp-server-windows-amd64.exe'
    & $goExe build -trimpath -ldflags '-s -w' -o $outputPath ./cmd/ssh-mcp-server
    if ($LASTEXITCODE -ne 0) { throw 'Go build failed for windows/amd64.' }
    Write-Host "Built $outputPath"
} finally {
    $env:GOOS = $originalGoOS
    $env:GOARCH = $originalGoARCH
    $env:CGO_ENABLED = $originalCgoEnabled
    Pop-Location
}
