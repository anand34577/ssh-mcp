# Install on Windows

Works on Windows 10 and 11, on regular PCs and ARM devices (like Surface Pro X and Copilot+ PCs).

## The easy way

1. Open the **Start menu**, type **PowerShell**, and open it.
2. Paste this line and press Enter:

   ```powershell
   irm https://raw.githubusercontent.com/anand34577/ssh-mcp/main/install.ps1 | iex
   ```

3. Follow the setup that starts automatically.

What the installer does:

- Picks the right version for your PC.
- Downloads it from this project's GitHub releases and **checks its fingerprint (SHA-256)**, so a damaged or tampered download is refused.
- Installs it to `%LOCALAPPDATA%\Programs\ssh-mcp`, which is just for your account, so no admin rights are needed.
- Adds that folder to your PATH, so `ssh-mcp-server` works in new terminals.
- Starts the setup.

## Without the terminal

1. Go to the [latest release](https://github.com/anand34577/ssh-mcp/releases/latest).
2. Download `ssh-mcp-server-windows-amd64.exe`, or `ssh-mcp-server-windows-arm64.exe` for ARM devices.
3. Double-click it. The setup opens and offers to install the program properly.

**Windows SmartScreen** may say *"Windows protected your PC"* because the program is new and not signed by a big company. Click **More info**, then **Run anyway**. You can always check the file against `SHA256SUMS.txt` on the release page:

```powershell
Get-FileHash .\ssh-mcp-server-windows-amd64.exe
```

## Check that it worked

```powershell
ssh-mcp-server version
```

If PowerShell says the command isn't found, open a new PowerShell window (the PATH change needs a fresh one).

**Next:** [Setup Guide](Setup-Guide) · [Update, Move, or Uninstall](Update-Move-or-Uninstall)
