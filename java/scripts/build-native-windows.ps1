$ErrorActionPreference = "Stop"

$repo = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$graalHome = if ($env:GRAALVM_HOME) { (Resolve-Path $env:GRAALVM_HOME).Path } else { "C:\Program Files\Java\graalvm-community-openjdk-25.0.2" }
$nativeImage = Join-Path $graalHome "bin\native-image.cmd"
if (!(Test-Path -LiteralPath $nativeImage)) {
    throw "GraalVM native-image was not found at $nativeImage. Set GRAALVM_HOME to a GraalVM installation with Native Image."
}

$vsWhereCandidates = @(
    "C:\Program Files (x86)\Microsoft Visual Studio\Installer\vswhere.exe",
    "C:\Program Files\Microsoft Visual Studio\Installer\vswhere.exe"
)
$hasVsWhere = ($vsWhereCandidates | Where-Object { Test-Path -LiteralPath $_ }).Count -gt 0
$hasCompiler = $null -ne (Get-Command cl.exe -ErrorAction SilentlyContinue)
if (!$hasVsWhere -and !$hasCompiler) {
    $gcc = Get-Command gcc.exe -ErrorAction SilentlyContinue
    if ($gcc) {
        throw "TDM-GCC/MinGW was found at $($gcc.Source), but GraalVM Native Image on Windows requires MSVC 14.x+ and the Windows SDK; TDM-GCC cannot replace that linker toolchain. Use the bundled-runtime executable from scripts/package-windows.ps1, or install the Microsoft Build Tools required for the single-file GraalVM build."
    }
    throw "Windows Native Image builds require Visual Studio 2022 Build Tools with MSVC and the Windows SDK. Run this from an x64 Native Tools Command Prompt or install the required toolchain."
}

$env:JAVA_HOME = $graalHome
$env:Path = (Join-Path $graalHome "bin") + ";" + $env:Path
Push-Location $repo
try {
    & mvn.cmd -q clean package
    if ($LASTEXITCODE -ne 0) { throw "Maven build failed" }

    $outputDir = Join-Path $repo "dist\native-image"
    if (!(Test-Path -LiteralPath $outputDir)) { New-Item -ItemType Directory -Force -Path $outputDir | Out-Null }
    $output = Join-Path $outputDir "ssh-mcp-server.exe"
    if (Test-Path -LiteralPath $output) { Remove-Item -LiteralPath $output -Force }

    & $nativeImage --no-fallback -H:+ReportExceptionStackTraces -H:Name=$output -jar (Join-Path $repo "target\ssh-mcp-server.jar")
    if ($LASTEXITCODE -ne 0) { throw "native-image failed" }

    Write-Host "Self-contained native executable: $output"
    Write-Host "This file does not require a JDK or JRE. Configure SSH_MCP_CONFIG and run it as an MCP stdio server."
}
finally {
    Pop-Location
}
