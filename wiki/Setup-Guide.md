# Setup Guide

The setup is a short question-and-answer helper. It starts by itself after installing, and you can open it again any time:

```bash
ssh-mcp-server setup
```

(On Windows you can also just double-click `ssh-mcp-server.exe`.)

## Adding a server, step by step

### 1. Server address

Type it the way you would for SSH, for example:

- `me@myserver.com`
- `deploy@203.0.113.10`
- `admin@myserver.com:2222` (if your server uses a different port)

If you leave out the username or port, the setup asks for them.

### 2. A short name

This is what you and your AI call the server, like `web` or `database`. The setup suggests one.

### 3. How you sign in

- **Key file (recommended).** If you already sign in with SSH keys, the setup finds them automatically. If your key has a passphrase, that works too.
- **Password.** Works fine. The password is kept out of the settings file (see [Staying Safe](Staying-Safe)).

### 4. What the AI may do

- **Anything my account can do.** Full help: fix problems, edit files, restart services.
- **Look only.** The AI can check status and read logs (`uptime`, `df`, `ps`, `journalctl`, `docker ps`, `cat`, `tail`, and similar) but can't change files or run other commands. A great choice for important servers.

Either way, the AI can never do more than the server account itself is allowed to do.

### 5. The server's fingerprint

The first time you add a server, it shows a fingerprint like:

```
ED25519  SHA256:Oqx37yay52SWroGnz6dYWAE40eSJoHPAtf94BpHZA6g
```

Think of it as the server's ID card. If your hosting provider or admin shows fingerprints (many do, in the server's dashboard), check that they match, then say **y**. From then on SSH MCP refuses to connect if the server's ID ever changes unexpectedly, which protects you from impostors.

If you already use `ssh` with this server, it's already trusted and this step is skipped.

### 6. Sign-in test

The setup signs in once to make sure everything works. If you use a password, it asks for it (typing is hidden). Press Enter to skip the test.

If the test fails, the setup tells you why and how to fix it. You can save the server anyway and test again later.

## Connecting your AI apps

After adding a server, the setup lists the AI apps it found and asks which to connect. For servers that use a password, it can save the password in those apps' own settings on your computer so they can sign in. See [Connect Your AI App](Connect-Your-AI-App) for details on each app.

## The menu

When you already have servers, the setup opens a menu:

```
What would you like to do?
  1) Add a server
  2) Test my servers
  3) Connect to my AI apps
  4) Remove a server
  5) Exit
```

## Where the settings are kept

| System | File |
|---|---|
| Windows | `%APPDATA%\ssh-mcp\servers.json` |
| macOS | `~/Library/Application Support/ssh-mcp/servers.json` |
| Linux | `~/.config/ssh-mcp/servers.json` |

The file holds server addresses and choices. It never contains passwords. You can edit it by hand if you like: see [Settings File](Settings-File).
