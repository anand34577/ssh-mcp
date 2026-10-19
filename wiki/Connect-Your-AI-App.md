# Connect Your AI App

SSH MCP works with any app or coding agent that supports **MCP** (Model Context Protocol), the standard way AI apps plug into tools.

## The automatic way (recommended)

```bash
ssh-mcp-server connect
```

This finds every supported app on your computer and connects SSH MCP to it, without asking anything. It keeps passwords you've already saved and is safe to run as often as you like. Then **restart your AI app**.

To connect only some apps:

```bash
ssh-mcp-server connect -app codex,cursor
```

| App | Name for `-app` | Where its settings live |
|---|---|---|
| Claude Desktop | `claude-desktop` | `claude_desktop_config.json` in Claude's settings folder |
| Claude Code | `claude-code` | managed with the `claude mcp` command (user scope) |
| Codex (OpenAI) | `codex` | `~/.codex/config.toml` (or `$CODEX_HOME`) |
| Gemini CLI | `gemini` | `~/.gemini/settings.json` |
| Cursor | `cursor` | `~/.cursor/mcp.json` |
| Windsurf | `windsurf` | `~/.codeium/windsurf/mcp_config.json` |
| VS Code (Copilot) | `vscode` | `mcp.json` in your VS Code user folder |

Before changing any settings file, SSH MCP saves a copy next to it as `.bak`. If a file contains comments (some apps allow that), it's left untouched and you're shown what to add by hand.

## Adding it by hand

You need two things, both shown by the setup:

- **Program:** where `ssh-mcp-server` is installed, for example
  - Windows: `C:\Users\you\AppData\Local\Programs\ssh-mcp\ssh-mcp-server.exe`
  - macOS/Linux: `/Users/you/.local/bin/ssh-mcp-server` or `/home/you/.local/bin/ssh-mcp-server`
- **Settings file:** see [Setup Guide](Setup-Guide#where-the-settings-are-kept)

Use full paths. AI apps don't start programs from your home folder.

### Claude Desktop, Cursor, Windsurf, Gemini CLI, and most others

```json
{
  "mcpServers": {
    "ssh": {
      "command": "/home/you/.local/bin/ssh-mcp-server",
      "args": ["-config", "/home/you/.config/ssh-mcp/servers.json"],
      "env": { "SSH_MCP_WEB_PASSWORD": "only if a server uses a password" }
    }
  }
}
```

### Claude Code

```bash
claude mcp add --scope user ssh -- /home/you/.local/bin/ssh-mcp-server -config /home/you/.config/ssh-mcp/servers.json
```

Add `-e SSH_MCP_WEB_PASSWORD=...` before `--` if a server uses a password.

### Codex

In `~/.codex/config.toml`:

```toml
[mcp_servers.ssh]
command = "/home/you/.local/bin/ssh-mcp-server"
args = ["-config", "/home/you/.config/ssh-mcp/servers.json"]

[mcp_servers.ssh.env]
SSH_MCP_WEB_PASSWORD = "only if a server uses a password"
```

### VS Code

In your user `mcp.json` (Command Palette > *MCP: Open User Configuration*):

```json
{
  "servers": {
    "ssh": {
      "type": "stdio",
      "command": "/home/you/.local/bin/ssh-mcp-server",
      "args": ["-config", "/home/you/.config/ssh-mcp/servers.json"]
    }
  }
}
```

### Any other app (Zed, Cline, Continue, JetBrains, ...)

Add a new MCP server of type **stdio** (sometimes called *command*), with the program and arguments above. Check that app's documentation for where its MCP settings are.

## Passwords and AI apps

Servers that sign in with a **key file** need nothing extra.

For servers that use a **password**, the AI app has to hand the password to SSH MCP when it starts it. The setup can save it in the app's own settings file on your computer (an `env` entry such as `SSH_MCP_WEB_PASSWORD`). It is never put in `servers.json` and never shown to the AI. If you'd rather not store it there, set that environment variable yourself before starting the app.

## Check it works

Restart the app and ask: **"List my SSH servers."** You should see your servers. If not, see [Troubleshooting](Troubleshooting).

## Teach your AI to use it well (optional)

The repository includes a ready-made *skill* (instructions for the AI) in [`skills/ssh-mcp`](https://github.com/anand34577/ssh-mcp/tree/main/skills/ssh-mcp). It teaches the AI to check before acting, keep commands safe, and ask before changing anything. Copy the folder into your agent's skills folder, for example `~/.claude/skills/` for Claude Code or `~/.codex/skills/` for Codex.
