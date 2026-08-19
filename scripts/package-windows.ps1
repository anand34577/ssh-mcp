$ErrorActionPreference = "Stop"

$repo = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$jdkHome = if ($env:JAVA_HOME) { (Resolve-Path $env:JAVA_HOME).Path } else { throw "Set JAVA_HOME to a JDK 11 installation" }
$javaExe = Join-Path $jdkHome "bin\java.exe"
$jlinkExe = Join-Path $jdkHome "bin\jlink.exe"
if (!(Test-Path -LiteralPath $javaExe) -or !(Test-Path -LiteralPath $jlinkExe)) {
    throw "JAVA_HOME must point to a JDK, not a JRE"
}
$previousErrorAction = $ErrorActionPreference
$ErrorActionPreference = "Continue"
$javaVersion = (& $javaExe -version 2>&1 | Out-String)
$ErrorActionPreference = $previousErrorAction
if ($javaVersion -notmatch 'version "11\.') {
    throw "Packaging must use JDK 11. Detected: $javaVersion"
}

$env:JAVA_HOME = $jdkHome
$env:Path = (Join-Path $jdkHome "bin") + ";" + $env:Path
Push-Location $repo
try {
    & mvn.cmd -q clean package
    if ($LASTEXITCODE -ne 0) { throw "Maven build failed" }

    $dist = Join-Path $repo "dist"
    if (!(Test-Path -LiteralPath $dist)) { New-Item -ItemType Directory -Path $dist | Out-Null }
    $runtime = Join-Path $dist "runtime"
    $app = Join-Path $dist "app"
    if (Test-Path -LiteralPath $runtime) { Remove-Item -LiteralPath $runtime -Recurse -Force }
    if (Test-Path -LiteralPath $app) { Remove-Item -LiteralPath $app -Recurse -Force }
    New-Item -ItemType Directory -Force -Path $app | Out-Null

    & $jlinkExe --add-modules java.base,java.logging,java.naming,java.management,jdk.crypto.ec,jdk.crypto.cryptoki,java.security.jgss --strip-debug --no-man-pages --no-header-files --compress=2 --output $runtime
    if ($LASTEXITCODE -ne 0) { throw "jlink failed" }
    Copy-Item -LiteralPath (Join-Path $repo "target\ssh-mcp-server.jar") -Destination (Join-Path $app "ssh-mcp-server.jar")

    $bin = Join-Path $dist "bin"
    if (!(Test-Path -LiteralPath $bin)) { New-Item -ItemType Directory -Path $bin | Out-Null }
    @('@echo off', 'set "APP_HOME=%~dp0.."', '"%APP_HOME%\runtime\bin\java.exe" -jar "%APP_HOME%\app\ssh-mcp-server.jar" %*') | Set-Content -Encoding ASCII (Join-Path $bin "ssh-mcp-server.cmd")

    $jpackage = Get-Command jpackage.exe -ErrorAction SilentlyContinue
    if ($jpackage) {
        $native = Join-Path $dist "native"
        if (Test-Path -LiteralPath $native) { Remove-Item -LiteralPath $native -Recurse -Force }
        New-Item -ItemType Directory -Force -Path $native | Out-Null
        & $jpackage.Source --type app-image --name ssh-mcp-server --app-version 1.0.0 --input $app --main-jar ssh-mcp-server.jar --main-class com.example.sshmcp.Main --runtime-image $runtime --dest $native --vendor "SSH MCP Server" --win-console
        if ($LASTEXITCODE -ne 0) { throw "jpackage failed" }
        Write-Host "Native Windows app image: $native\ssh-mcp-server"
    }
    else {
        Write-Host "Portable Windows distribution created. Install JDK 14+ to additionally create a native app image with jpackage."
    }
    Write-Host "Portable distribution: $dist"
}
finally {
    Pop-Location
}
