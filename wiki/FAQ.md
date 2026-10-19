# FAQ

### Is it free?
Yes. Free and open source under the MIT license, for personal and commercial use.

### Does it collect any data?
No. No analytics, no telemetry, nothing. See [Privacy](Privacy).

### Which AI apps does it work with?
Claude Desktop, Claude Code, Codex, Gemini CLI, Cursor, VS Code (Copilot), and Windsurf are connected automatically. Any other app that supports MCP works too; see [Connect Your AI App](Connect-Your-AI-App).

### Do I need to install anything else, like Python, Node, or Java?
No. It's a single program with everything built in.

### Does it work with password logins, or only keys?
Both. Keys are recommended, but plain passwords work, including servers that ask for the password in a "keyboard-interactive" prompt.

### Can the AI see my password?
No. Passwords are used only on your computer, at the moment of signing in.

### Can I stop the AI from changing things?
Yes. Choose **look only** when adding the server, or set an allow-list in the [Settings File](Settings-File). The server account's own permissions always apply too.

### Can I use it with several servers?
Yes, as many as you like. Each has its own name and its own rules.

### Does the AI keep a session open on my server?
No. Each action signs in, does one thing, and disconnects. Nothing is left running.

### Does it work on a Raspberry Pi?
Yes, both as the computer running SSH MCP (Linux ARM build) and as a server you connect to.

### Does it support two-factor authentication?
Not one-time codes. Use a key-based account for the AI.

### Can I run it on a server for my whole team?
SSH MCP is designed to run on each person's own computer, next to their AI app, using their own keys. That keeps everyone's access separate and auditable.

### What's the Java edition?
The same tool rebuilt in Java, for organisations that prefer Java. It uses the same settings file. Most people should use the regular edition. See [For Developers](For-Developers).

### How do I update or remove it?
See [Update, Move, or Uninstall](Update-Move-or-Uninstall). One command each.
