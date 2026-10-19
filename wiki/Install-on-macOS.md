# Install on macOS

Works on Macs with Apple chips (M1 and newer) and Intel chips.

## The easy way

1. Open **Terminal** (press `Cmd + Space`, type *Terminal*, press Enter).
2. Paste this line and press Enter:

   ```bash
   curl -fsSL https://raw.githubusercontent.com/anand34577/ssh-mcp/main/install.sh | sh
   ```

3. Follow the setup that starts automatically.

What the installer does:

- Picks the right version for your Mac.
- Downloads it from this project's GitHub releases and **checks its fingerprint (SHA-256)**, so a damaged or tampered download is refused.
- Installs it to `~/.local/bin`, which is just for your account, so no password or `sudo` is needed.
- Tells macOS it's okay to run, so you don't get the "can't be opened" warning.
- Starts the setup.

If it mentions that `~/.local/bin` isn't on your PATH, copy the line it shows into `~/.zshrc`. That's only needed if you want to type `ssh-mcp-server` by name; your AI apps use the full path and work either way.

## Without the installer

1. Download from the [latest release](https://github.com/anand34577/ssh-mcp/releases/latest):
   - Apple chip: `ssh-mcp-server-darwin-arm64`
   - Intel chip: `ssh-mcp-server-darwin-amd64`

   Not sure which you have? Apple menu > **About This Mac** > look for *Chip*.
2. In Terminal, allow it to run and start the setup:

   ```bash
   cd ~/Downloads
   chmod +x ssh-mcp-server-darwin-arm64
   xattr -d com.apple.quarantine ssh-mcp-server-darwin-arm64
   ./ssh-mcp-server-darwin-arm64 setup
   ```

   The setup offers to move it to a permanent place.

## Check that it worked

```bash
~/.local/bin/ssh-mcp-server version
```

**Next:** [Setup Guide](Setup-Guide) · [Update, Move, or Uninstall](Update-Move-or-Uninstall)
