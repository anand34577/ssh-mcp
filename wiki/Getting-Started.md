# Getting Started

Three steps, about two minutes.

## 1. Install

Open a terminal and paste one line.

**Windows** (open *PowerShell* from the Start menu):

```powershell
irm https://raw.githubusercontent.com/anand34577/ssh-mcp/main/install.ps1 | iex
```

**macOS or Linux** (open *Terminal*):

```bash
curl -fsSL https://raw.githubusercontent.com/anand34577/ssh-mcp/main/install.sh | sh
```

This downloads SSH MCP, checks that the download is genuine, and installs it just for your user account. You don't need administrator rights, and there's nothing else to install.

> Rather not paste commands? Download the file for your computer from the [Releases page](https://github.com/anand34577/ssh-mcp/releases/latest) and double-click it (Windows) or run it (macOS/Linux). It offers to install itself.

## 2. Add your server

The setup starts on its own right after installing. It asks a few simple questions:

1. **The server's address**, like `me@myserver.com`
2. **How you sign in**: with your key file (recommended) or a password
3. **What the AI may do**: anything, or *look only*
4. **Whether you trust the server's fingerprint**: say yes if it matches what your provider shows

Then it signs in once to make sure everything works.

<p align="center"><img src="https://raw.githubusercontent.com/anand34577/ssh-mcp/main/assets/setup-demo.svg" alt="The setup in a terminal" width="85%"></p>

## 3. Connect your AI app

The setup finds the AI apps on your computer (Claude, Codex, Cursor, VS Code, Gemini CLI, Windsurf) and asks which ones to connect. Say yes, **restart the app**, and ask:

> List my SSH servers

That's it. Try something real next:

> How much disk space is left on my server?

## Need to change something later?

Run the setup again at any time:

```bash
ssh-mcp-server setup
```

From there you can add, test, or remove servers, and connect more apps.

**Next:** [What You Can Ask](What-You-Can-Ask) · [Staying Safe](Staying-Safe)
