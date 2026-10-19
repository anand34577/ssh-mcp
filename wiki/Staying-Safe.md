# Staying Safe

Giving an AI access to a server is a big deal. SSH MCP is built so you stay in control.

## What protects you

**Your passwords and keys never reach the AI.** SSH MCP reads them on your computer only at the moment it signs in. They're never part of what the AI sees, never in its answers, and never written to logs. The settings file itself refuses to hold passwords at all.

**It checks every server's identity.** The first time you add a server you confirm its fingerprint. After that, if anything ever answers in your server's place with a different ID, SSH MCP refuses to connect and tells you. This blocks a classic attack where someone pretends to be your server.

**You set the limits, per server.**

- *Look-only mode* allows only status and log commands, and no file changes.
- You can list exactly which commands are allowed, or block patterns you never want run.
- Every command has a time limit and an output limit, so nothing can run away.
- Tunnels are off unless you turn them on, and they only ever open on your own computer, never to your network.

**The AI can't argue its way past your rules.** The rules are enforced by SSH MCP on your computer, before anything reaches the server. The AI only sees the result.

**Every action is logged** on your computer: which server, what kind of action, whether it worked, and how long it took. Command text and passwords are not logged; a short fingerprint of each command is recorded instead.

**Settings files are protected.** On macOS and Linux, files SSH MCP writes can only be read by your user account. Before it changes an AI app's settings, it saves a backup copy.

**Downloads are verified.** The installers and the `update` command check each download against its published SHA-256 fingerprint and refuse anything that doesn't match.

## Good habits

- **Use a separate server account for the AI**, with only the permissions it needs. SSH MCP can't give the AI more power than that account has, so this is your strongest safety net.
- **Use look-only mode for important servers** until you're comfortable.
- **Prefer key files to passwords.** Then there's no secret to store in your AI apps at all.
- **Read before you approve.** Good AI apps show you each action before it runs. The included [skill](Connect-Your-AI-App#teach-your-ai-to-use-it-well-optional) teaches the AI to ask before changing anything.
- **Check fingerprints** when you add a server, especially over public Wi-Fi.

## About saved passwords

If a server uses a password, your AI app needs it to start SSH MCP, so the setup can save it in that app's settings file on your computer. That file is as private as your user account. If that's not good enough for a server, switch it to a key file, or set the password as an environment variable yourself before starting the app.

## Reporting a security problem

Please don't open a public issue. See [SECURITY.md](https://github.com/anand34577/ssh-mcp/blob/main/SECURITY.md) for how to report privately.

**See also:** [Privacy](Privacy)
