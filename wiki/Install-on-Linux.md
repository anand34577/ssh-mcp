# Install on Linux

Works on any 64-bit Linux, on regular PCs and servers (x86-64) and ARM (Raspberry Pi 4/5, ARM cloud servers). There's nothing else to install: no libraries, no runtime.

## The easy way

```bash
curl -fsSL https://raw.githubusercontent.com/anand34577/ssh-mcp/main/install.sh | sh
```

No `curl`? `wget -qO- https://raw.githubusercontent.com/anand34577/ssh-mcp/main/install.sh | sh` works too.

What the installer does:

- Picks the right version for your processor.
- Downloads it from this project's GitHub releases and **checks its fingerprint (SHA-256)**, so a damaged or tampered download is refused.
- Installs it to `~/.local/bin`. No `sudo` needed.
- Starts the setup.

### Options

Set these before the command if you need them:

| Option | What it does |
|---|---|
| `SSH_MCP_INSTALL_DIR=/opt/ssh-mcp` | Install somewhere else |
| `SSH_MCP_VERSION=v1.2.0` | Install a specific version |
| `SSH_MCP_NO_SETUP=1` | Skip the setup (for scripts) |
| `SSH_MCP_DOWNLOAD_URL=https://mirror/...` | Download from your own mirror |

Example:

```bash
curl -fsSL https://raw.githubusercontent.com/anand34577/ssh-mcp/main/install.sh | SSH_MCP_NO_SETUP=1 sh
```

## Prefer to read the script first?

Good habit. Download it, read it, then run it:

```bash
curl -fsSLO https://raw.githubusercontent.com/anand34577/ssh-mcp/main/install.sh
less install.sh
sh install.sh
```

## Without the installer

```bash
curl -fsSLO https://github.com/anand34577/ssh-mcp/releases/latest/download/ssh-mcp-server-linux-amd64
curl -fsSLO https://github.com/anand34577/ssh-mcp/releases/latest/download/ssh-mcp-server-linux-amd64.sha256
sha256sum -c ssh-mcp-server-linux-amd64.sha256
chmod +x ssh-mcp-server-linux-amd64
./ssh-mcp-server-linux-amd64 setup
```

Use `linux-arm64` instead of `linux-amd64` on ARM machines (`uname -m` says `aarch64`).

**Next:** [Setup Guide](Setup-Guide) · [Update, Move, or Uninstall](Update-Move-or-Uninstall)
