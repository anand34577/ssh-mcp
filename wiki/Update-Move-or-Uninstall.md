# Update, Move, or Uninstall

## Update to the latest version

Either of these works. Your servers and AI app connections are kept.

```bash
ssh-mcp-server update
```

or simply run the installer again (it notices you already have SSH MCP and updates it):

```powershell
irm https://raw.githubusercontent.com/anand34577/ssh-mcp/main/install.ps1 | iex        # Windows
```

```bash
curl -fsSL https://raw.githubusercontent.com/anand34577/ssh-mcp/main/install.sh | sh     # macOS / Linux
```

Every update is checked against its published SHA-256 fingerprint before anything is replaced. It's safe to update while an AI app is using SSH MCP. Restart the app afterwards to use the new version.

SSH MCP never checks for updates on its own. It only goes online for updates when you ask (see [Privacy](Privacy)). To see what's new, watch the [Releases page](https://github.com/anand34577/ssh-mcp/releases) on GitHub.

Need a specific version? Set `SSH_MCP_VERSION=v1.2.0` before running the installer or `update`.

## Started using a new AI app?

```bash
ssh-mcp-server connect
```

It connects every supported app it finds and copies over any passwords you saved for your other apps.

## Moving to a new computer

1. Install SSH MCP on the new computer (see [Getting Started](Getting-Started)).
2. Copy these from the old computer:
   - your settings file (`servers.json`, see [where it lives](Setup-Guide#where-the-settings-are-kept))
   - `~/.ssh/known_hosts` (the trusted server fingerprints)
   - your SSH key files, if you use keys (`~/.ssh/id_ed25519` and so on)
3. Run `ssh-mcp-server setup` and choose **Test my servers**. It asks for any passwords.
4. Choose **Connect to my AI apps**.

## Coming from an older, hand-made setup

If you set up SSH MCP before the setup helper existed (for example with `config/servers.json` inside the project folder), point the helper at your existing file:

```bash
ssh-mcp-server setup -config /path/to/your/servers.json
```

Or copy that file to the [standard location](Setup-Guide#where-the-settings-are-kept) and run `ssh-mcp-server connect`. Your apps will then use the installed program and the standard file, and the old folder is no longer needed.

The Java edition uses the same settings file, so switching between editions needs no changes.

## Moved the program?

AI apps remember where the program is. If you move it, run `ssh-mcp-server connect` from the new location and every app is updated.

## Uninstall

```bash
ssh-mcp-server uninstall
```

This removes SSH MCP from every AI app (each settings file is backed up first), deletes the program, and asks whether to also delete your server list. Your SSH keys and `known_hosts` are never touched, because other tools use them too.

Or use the installer:

```powershell
# Windows (also removes the PATH entry). Add -Purge to delete your server list too.
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/anand34577/ssh-mcp/main/install.ps1))) -Uninstall
```

```bash
# macOS / Linux. Add --purge to delete your server list too.
curl -fsSL https://raw.githubusercontent.com/anand34577/ssh-mcp/main/install.sh | sh -s -- --uninstall
```

For scripts: `ssh-mcp-server uninstall -yes` (keeps the server list) or `-yes -purge` (deletes it).
