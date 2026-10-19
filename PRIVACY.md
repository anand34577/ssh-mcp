# Privacy

SSH MCP collects nothing: no analytics, no telemetry, no crash reports, no usage statistics, no accounts.

The program connects only to:

1. **Your own SSH servers**, when your AI app asks it to do something on them.
2. **GitHub releases** (or a mirror you choose with `SSH_MCP_DOWNLOAD_URL`), only when you run the installer or `ssh-mcp-server update`. This is a plain file download; nothing about you or your servers is sent.

It never checks for updates on its own.

Everything else stays on your computer:

- your server list (`servers.json`),
- trusted fingerprints (`~/.ssh/known_hosts`),
- any passwords you chose to save in your AI apps' own settings files.

Passwords and keys are never shown to the AI and are never logged.

Your AI app and its provider see the results of the actions you ask for (command output, file contents, server names). Choose an AI app whose privacy policy you're comfortable with.

This promise is part of the project's contribution rules: changes that add other network traffic are not accepted. The full code is open for anyone to check.

More detail: [Privacy in the wiki](https://github.com/anand34577/ssh-mcp/wiki/Privacy).
