## Install in one step

**Windows** (PowerShell):

```powershell
irm https://raw.githubusercontent.com/anand34577/ssh-mcp/main/install.ps1 | iex
```

**macOS and Linux** (Terminal):

```bash
curl -fsSL https://raw.githubusercontent.com/anand34577/ssh-mcp/main/install.sh | sh
```

The installer downloads the right file for your computer, checks it, and starts a short setup that adds your first server and connects your AI app.

Prefer to download by hand? Pick your file below, then double-click it (Windows) or run it in a terminal. The setup starts on its own.

| Your computer | File |
|---|---|
| Windows (most PCs) | `ssh-mcp-server-windows-amd64.exe` |
| Windows on ARM (Surface Pro X, Copilot+ PCs) | `ssh-mcp-server-windows-arm64.exe` |
| Mac with Apple chip (M1 or newer) | `ssh-mcp-server-darwin-arm64` |
| Mac with Intel chip | `ssh-mcp-server-darwin-amd64` |
| Linux (most PCs and servers) | `ssh-mcp-server-linux-amd64` |
| Linux on ARM (Raspberry Pi 4/5, ARM servers) | `ssh-mcp-server-linux-arm64` |

Each file is a single program with nothing else to install. `SHA256SUMS.txt` lists the checksum of every file.

New here? The [wiki](https://github.com/anand34577/ssh-mcp/wiki) walks you through everything.

<details>
<summary>Java edition</summary>

`ssh-mcp-server.jar` runs anywhere Java 11 or newer is installed. The `ssh-mcp-server-*-x64` / `-arm64` archives include their own Java runtime, so nothing else is needed.
</details>
