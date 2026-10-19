# Setting up the SSH MCP server for a user

Use this when a user wants to install, update, move, remove, or fix the server. Credentials are always the **user's** to enter. Never ask them to paste a password or private key into the conversation, and never type one into a command for them.

## The short path (recommend this first)

1. **Install.** Ask the user to run the one-liner for their system in their own terminal:
   - Windows PowerShell: `irm https://raw.githubusercontent.com/anand34577/ssh-mcp/main/install.ps1 | iex`
   - macOS / Linux: `curl -fsSL https://raw.githubusercontent.com/anand34577/ssh-mcp/main/install.sh | sh`

   It downloads the right binary, verifies its SHA-256 checksum, installs per user (no admin rights), and starts the setup.
2. **Setup.** The interactive `ssh-mcp-server setup` asks for the address (`user@host[:port]`), sign-in method (key file or password), access level (full or look-only), confirms the host-key fingerprint with the user, tests the sign-in, and connects detected AI apps. The user must answer it themselves: it reads hidden password input and fingerprint confirmations from their terminal. Don't try to drive it through a non-interactive tool.
3. **Restart the AI app**, then call `ssh_list_servers`.

## Lifecycle commands

These are safe for an agent to suggest, and `connect` and `-validate` are safe for an agent to run on the user's machine because they need no input:

| Command | Purpose |
|---|---|
| `ssh-mcp-server connect [-app codex,cursor]` | Register with Claude Desktop, Claude Code, Codex, Gemini CLI, Cursor, Windsurf, and VS Code without prompts. Reuses passwords stored in any app. Fixes registrations after the binary moved. |
| `ssh-mcp-server -validate [-connect]` | Diagnose every server. Exit 0 means all ready. |
| `ssh-mcp-server update` | Self-update with checksum verification; keeps servers and app connections. Re-running the installer does the same. |
| `ssh-mcp-server uninstall [-purge] [-yes]` | Remove from every app and delete the binary; `-purge` also deletes `servers.json`. Ask the user first, and never add `-purge` unless they asked to delete their settings. |

The server has no telemetry and never contacts the network except for SSH connections and an explicit update.

## Where things live

| | Windows | macOS | Linux |
|---|---|---|---|
| Program (installer) | `%LOCALAPPDATA%\Programs\ssh-mcp\ssh-mcp-server.exe` | `~/.local/bin/ssh-mcp-server` | `~/.local/bin/ssh-mcp-server` |
| Settings | `%APPDATA%\ssh-mcp\servers.json` | `~/Library/Application Support/ssh-mcp/servers.json` | `~/.config/ssh-mcp/servers.json` |

Trusted host keys are in `~/.ssh/known_hosts`. Passwords live only in AI apps' `env` blocks (as `SSH_MCP_<ID>_PASSWORD`) or the user's environment, never in `servers.json`.

## Moving or migrating

- **New computer:** install, copy `servers.json`, `~/.ssh/known_hosts`, and key files, run `setup` > *Test my servers* (re-enter passwords), then `connect`.
- **Old hand-written setup** (for example `config/servers.json` in a checkout): `ssh-mcp-server setup -config /path/to/servers.json`, or copy the file to the settings location and run `connect`.
- **Java edition:** same settings file and tools; switching needs no changes.

## Writing servers.json by hand

Start from `config/servers.example.json`. Minimal password entry:

```json
{
  "servers": [{
    "id": "staging",
    "host": "staging.example.com",
    "username": "ops",
    "passwordEnv": "SSH_MCP_STAGING_PASSWORD",
    "knownHostsPath": "~/.ssh/known_hosts"
  }]
}
```

Key rules:

- `passwordEnv` / `privateKeyPassphraseEnv` hold environment variable **names**. Inline `password`, `passphrase`, `privateKey`, and `secret` fields are rejected.
- Configure `passwordEnv`, `privateKeyPath`, or both (public key is tried first).
- Paths accept `~/`, `${user.home}`, and `${ENV_VAR}`.
- Unknown fields are errors, so typos never silently weaken security.
- Least privilege: `allowPortForwarding` stays `false` unless needed. `allowedCommandPrefixes` restricts commands (shell metacharacters are then rejected). `deniedCommandRegexes` and `protectSystem` are guard rails, not a sandbox.

Hand-adding a host key, after the user verifies the fingerprint out of band:

```bash
ssh-keyscan -p 22 staging.example.com >> ~/.ssh/known_hosts
ssh-keygen -lf ~/.ssh/known_hosts
```

`strictHostKeyChecking: false` only loads with `SSH_MCP_ALLOW_INSECURE_HOST_KEYS=true`, and `SSH_MCP_AUTO_ACCEPT_HOST_KEYS=true` disables verification everywhere. Both are emergency switches, never a fix for a mismatch.

## The safety net (`protectSystem`)

Every server has `protectSystem` on unless its entry says `"protectSystem": false`. While it is on, the server refuses:

- recursive deletes of `/`, system folders (`/etc`, `/usr`, `/var`, `/boot`, ...), or home folders (`rm -rf /`, `rm -rf ~`, `find / -delete`)
- recursive `chmod`, `chown`, or `chgrp` on those folders, and `mv` of them
- formatting or overwriting disks (`mkfs`, `wipefs`, `dd of=/dev/sda`, `> /dev/sda`, `shred /dev/...`)
- `shutdown`, `reboot`, `poweroff`, `halt`, `init 0`/`6`, `systemctl reboot` and similar, and fork bombs
- SFTP changes to `/etc/passwd`, `/etc/shadow`, `/etc/group`, `/etc/gshadow`, `/etc/sudoers`, `/etc/fstab`, writes inside `/boot`, `/dev`, `/proc`, `/sys`, and deleting or renaming system or home folders

It looks through `sudo`, `env`, `sh -c`, quotes, and `;`, `&&`, `|` chains. Everyday commands such as `rm -rf /tmp/build` or `systemctl restart nginx` are not affected.

**Turning it off is the user's decision, never yours.** Only help when the user explicitly asks to turn it off. Then:

1. Explain what it protects against, and suggest turning it off only for the one server and only for as long as needed.
2. Tell the user to add `"protectSystem": false` to that server's entry in `servers.json` (see [Where things live](#where-things-live)), save, and fully restart the AI app.
3. Remind them to remove the line afterwards to turn it back on.

Never edit `servers.json` to turn it off on your own, and never suggest it as a way past a blocked command unless the user asked.

## Manual client registration

Generic `mcpServers` JSON (Claude Desktop, Cursor, Windsurf, Gemini CLI):

```json
{ "mcpServers": { "ssh": {
  "command": "/home/me/.local/bin/ssh-mcp-server",
  "args": ["-config", "/home/me/.config/ssh-mcp/servers.json"],
  "env": { "SSH_MCP_STAGING_PASSWORD": "entered by the user" }
} } }
```

- **Codex** (`~/.codex/config.toml`): a `[mcp_servers.ssh]` table with `command` and `args`, plus a `[mcp_servers.ssh.env]` table.
- **VS Code** (`mcp.json`): the same entry under `"servers"`, with `"type": "stdio"`.
- **Claude Code:** `claude mcp add --scope user ssh -- <program> -config <settings>`.

Always use absolute paths.

## Troubleshooting

| Message | Fix |
|---|---|
| `no configuration file found; searched …` | Run `ssh-mcp-server setup`, or pass `-config` with an absolute path. |
| `knownHostsPath is not a regular file` | Run setup to add the server (it creates the file), or create it with `ssh-keyscan` after verifying. |
| `host key for … is not in …` | Re-add the server with setup and confirm the fingerprint. Non-22 ports are stored as `[host]:port`. |
| `HOST KEY MISMATCH` | The key changed: a rebuild, or an attack. Verify out of band, then `ssh-keygen -R host` and re-add with setup. |
| `environment variable X … is not set` | The AI app isn't passing the password: setup > *Connect to my AI apps* saves it, or use a key. |
| `private key … is encrypted` | Set `privateKeyPassphraseEnv` and provide that variable (setup does this). |
| `unable to authenticate` | Wrong user/password, or the public key isn't in `authorized_keys` (`ssh-copy-id`). |
| `keyboard-interactive challenge is not a single password prompt` | The server requires MFA/OTP; use a key-based account. |
| `cannot reach host:port` / `timed out` | DNS, firewall, VPN, or wrong port; check with `ssh` directly. Raise `connectTimeoutMs` for slow links. |
| `command is not allowed by the server command policy` | Expected in look-only or allow-list mode. |
| `blocked by system protection: …` | The safety net refused a risky action. Expected. See [The safety net](#the-safety-net-protectsystem). |
| AI app doesn't show the tools | Fully restart the app; run `ssh-mcp-server connect`. |

Logs and audit lines (`audit operation=… server=… success=…`) go to stderr, which the AI app usually shows in its MCP log view. Command text is never logged, only a short hash.
