# Privacy

**Short version: SSH MCP collects nothing.** No analytics, no telemetry, no crash reports, no usage statistics, no accounts, no ads.

## What it connects to

SSH MCP only ever connects to:

1. **Your own servers**, when your AI asks it to do something on them.
2. **GitHub**, only when *you* run the installer or `ssh-mcp-server update`, to download the program and its checksum. Nothing about you is sent; it's an ordinary file download. You can even point it at your own mirror with `SSH_MCP_DOWNLOAD_URL`.

It never checks for updates on its own and never talks to any other service.

## What stays on your computer

| What | Where |
|---|---|
| Your server list | your settings file ([location](Setup-Guide#where-the-settings-are-kept)) |
| Trusted server fingerprints | `~/.ssh/known_hosts` |
| Passwords you chose to save | your AI app's own settings file |
| Activity log | printed by the program for your AI app to show or discard; nothing is saved by SSH MCP itself |

## What your AI app sees

Your AI app (and the AI company behind it) sees what any tool result contains: command output, file contents you ask it to read, and your server names and addresses. It never sees your passwords or keys.

Choose an AI app whose privacy policy you're comfortable with, because that's where your conversation goes. SSH MCP itself adds nothing.

## Check for yourself

SSH MCP is open source under the MIT license. Every line is on [GitHub](https://github.com/anand34577/ssh-mcp), and every release is built in public by GitHub Actions from that code.
