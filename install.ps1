<#
SSH MCP installer for Windows.

Install or update (the same command does both):
  irm https://raw.githubusercontent.com/anand34577/ssh-mcp/main/install.ps1 | iex
Uninstall (add -Purge to also delete your server list):
  & ([scriptblock]::Create((irm https://raw.githubusercontent.com/anand34577/ssh-mcp/main/install.ps1))) -Uninstall

It downloads the right program for this computer from GitHub, checks its
SHA-256 checksum, installs it to %LOCALAPPDATA%\Programs\ssh-mcp, adds that
folder to your PATH, and starts the guided setup on a first install. Updates
keep your servers and AI app connections. Nothing is sent anywhere: no
analytics, no telemetry.

Options (environment variables):
  $env:SSH_MCP_VERSION = 'v1.2.0'    install a specific release instead of the latest
  $env:SSH_MCP_DOWNLOAD_URL = 'url'  download from a mirror that has the release files
  $env:SSH_MCP_NO_SETUP = '1'        skip the guided setup
#>
param([switch]$Uninstall, [switch]$Purge)

# Everything runs in a child scope so "irm | iex" leaves the session's
# preferences and variables untouched.
& {
    param([switch]$Uninstall, [switch]$Purge)
    $ErrorActionPreference = 'Stop'
    $ProgressPreference = 'SilentlyContinue'

    $repo = 'anand34577/ssh-mcp'
    $installDir = Join-Path $env:LOCALAPPDATA 'Programs\ssh-mcp'
    $target = Join-Path $installDir 'ssh-mcp-server.exe'

    function Set-UserPath([string]$dir, [bool]$present) {
        $path = [Environment]::GetEnvironmentVariable('Path', 'User')
        $entries = @($path -split ';' | Where-Object { $_ -and ($_.TrimEnd('\') -ine $dir.TrimEnd('\')) })
        if ($present) { $entries += $dir }
        $updated = $entries -join ';'
        if ($updated -ne $path) {
            [Environment]::SetEnvironmentVariable('Path', $updated, 'User')
            return $true
        }
        return $false
    }

    function Get-Label([string]$versionLine) { $versionLine -replace '^ssh-mcp-server ', '' }

    try {
        if ($Uninstall) {
            if (Test-Path $target) {
                # Removes SSH MCP from every AI app, then deletes the program.
                if ($Purge) { & $target uninstall -yes -purge } else { & $target uninstall -yes }
            }
            if (Test-Path $installDir) { Remove-Item -Recurse -Force $installDir -ErrorAction SilentlyContinue }
            [void](Set-UserPath $installDir $false)
            Write-Host 'SSH MCP is uninstalled.'
            return
        }

        # Windows PowerShell 5.1 may default to older TLS versions.
        [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

        $cpu = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
        $arch = switch ($cpu) {
            'AMD64' { 'amd64' }
            'ARM64' { 'arm64' }
            default { throw "Unsupported processor '$cpu'. SSH MCP needs 64-bit Windows (Intel/AMD or ARM)." }
        }
        $asset = "ssh-mcp-server-windows-$arch.exe"
        $base = if ($env:SSH_MCP_DOWNLOAD_URL) {
            $env:SSH_MCP_DOWNLOAD_URL.TrimEnd('/')
        } elseif ($env:SSH_MCP_VERSION) {
            "https://github.com/$repo/releases/download/$($env:SSH_MCP_VERSION)"
        } else {
            "https://github.com/$repo/releases/latest/download"
        }

        $previous = $null
        if (Test-Path $target) { $previous = & $target version }

        $temp = Join-Path ([IO.Path]::GetTempPath()) ('ssh-mcp-' + [Guid]::NewGuid().ToString('N'))
        New-Item -ItemType Directory -Force -Path $temp | Out-Null
        try {
            Write-Host "Downloading SSH MCP for Windows ($arch)..."
            $exe = Join-Path $temp $asset
            Invoke-WebRequest -UseBasicParsing -Uri "$base/$asset" -OutFile $exe
            Invoke-WebRequest -UseBasicParsing -Uri "$base/$asset.sha256" -OutFile "$exe.sha256"

            $expected = ((Get-Content -Raw "$exe.sha256").Trim() -split '\s+')[0].ToLowerInvariant()
            $actual = (Get-FileHash -Algorithm SHA256 $exe).Hash.ToLowerInvariant()
            if (-not $expected -or $expected -ne $actual) {
                throw 'The download is damaged or was changed (checksum mismatch). Please try again.'
            }
            Write-Host 'Checksum verified.'

            New-Item -ItemType Directory -Force -Path $installDir | Out-Null
            if (Test-Path $target) {
                # A running program can't be overwritten, but it can be renamed;
                # the program deletes the old copy the next time it starts.
                Remove-Item -Force "$target.old" -ErrorAction SilentlyContinue
                Move-Item -Force $target "$target.old"
            }
            Move-Item -Force $exe $target
            Unblock-File $target
        } finally {
            Remove-Item -Recurse -Force $temp -ErrorAction SilentlyContinue
        }

        if (Set-UserPath $installDir $true) {
            $env:Path = "$env:Path;$installDir"
            Write-Host "Added $installDir to your PATH (new terminals will pick it up)."
        }

        $current = & $target version
        if ($previous) {
            if ($previous -eq $current) {
                Write-Host "You already have the latest version ($(Get-Label $current))."
            } else {
                Write-Host "Updated from $(Get-Label $previous) to $(Get-Label $current)."
                Write-Host 'Your servers and AI app connections were kept. Restart your AI apps to use the new version.'
            }
            return
        }

        Write-Host "Installed $(Get-Label $current) to $target"
        if ($env:SSH_MCP_NO_SETUP -eq '1') {
            Write-Host "Run 'ssh-mcp-server setup' whenever you're ready to add a server."
        } else {
            & $target setup
        }
    } catch {
        Write-Host "Install stopped: $($_.Exception.Message)" -ForegroundColor Red
        Write-Host "You can also download the program yourself from https://github.com/$repo/releases"
    }
} -Uninstall:$Uninstall -Purge:$Purge
