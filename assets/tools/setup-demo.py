"""Generates assets/setup-demo.svg: a terminal window replaying the wizard."""
import sys
from xml.sax.saxutils import escape

# (kind, text, typed answer)
LINES = [
    ("prompt", "$ ssh-mcp-server setup", None),
    ("blank", "", None),
    ("head", "SSH MCP setup", None),
    ("text", "This connects your AI assistant (Claude, Cursor, VS Code, and others) to your", None),
    ("text", "servers over SSH. Your passwords and keys stay on this computer.", None),
    ("blank", "", None),
    ("text", "Let's add your first server.", None),
    ("blank", "", None),
    ("ask", "Server address (for example me@example.com or 203.0.113.10): ", "deploy@web.example.com"),
    ("ask", "SSH port [22]: ", ""),
    ("ask", "A short name your AI will use for it [web]: ", ""),
    ("blank", "", None),
    ("text", "How do you sign in to this server?", None),
    ("text", "  1) With my key file /home/you/.ssh/id_ed25519 (recommended)", None),
    ("text", "  2) With a password", None),
    ("text", "  3) With a different key file", None),
    ("ask", "Choose [1]: ", "1"),
    ("blank", "", None),
    ("text", "What should your AI be allowed to do on this server?", None),
    ("text", "  1) Anything my account can do", None),
    ("text", "  2) Look only: check status and read logs, no changes", None),
    ("ask", "Choose [1]: ", "2"),
    ("blank", "", None),
    ("text", "  The server's fingerprint is:", None),
    ("finger", "    ED25519  SHA256:Oqx37yay52SWroGnz6dYWAE40eSJoHPAtf94BpHZA6g", None),
    ("ask", "Trust this server? [y/N]: ", "y"),
    ("ok", "  [ok] Signed in to web.", None),
    ("ok", "  [ok] Saved \"web\".", None),
    ("blank", "", None),
    ("ask", "Connect to Claude Desktop? [Y/n]: ", "y"),
    ("ok", "  [ok] Connected to Claude Desktop.", None),
    ("blank", "", None),
    ("head", "All set! Restart your AI app, then try asking it:", None),
    ("quote", "  \"How much disk space is left on web?\"", None),
]

WIDTH, LINE, TOP, LEFT, FONT = 1400, 29, 112, 56, 19
COLORS = {"prompt": "#64748B", "text": "#CBD5E1", "head": "#FFFFFF", "ask": "#CBD5E1",
          "ok": "#34D399", "finger": "#FBBF24", "quote": "#C4B5FD"}

height = TOP + LINE * len(LINES) + 24
out = [f'''<svg xmlns="http://www.w3.org/2000/svg" width="{WIDTH}" height="{height}" viewBox="0 0 {WIDTH} {height}" role="img" aria-label="The SSH MCP setup wizard adding a server in a terminal">
  <defs>
    <radialGradient id="glow" cx="0.85" cy="0" r="0.9">
      <stop offset="0" stop-color="#6366F1" stop-opacity="0.18"/>
      <stop offset="1" stop-color="#6366F1" stop-opacity="0"/>
    </radialGradient>
    <style>
      .mono {{ font-family: 'JetBrains Mono', 'Cascadia Code', Consolas, 'SF Mono', Menlo, monospace; font-size: {FONT}px; white-space: pre; }}
      .sans {{ font-family: Inter, 'Segoe UI', -apple-system, BlinkMacSystemFont, Roboto, Helvetica, Arial, sans-serif; }}
    </style>
  </defs>
  <rect width="{WIDTH}" height="{height}" rx="22" fill="#0D1526"/>
  <rect width="{WIDTH}" height="{height}" rx="22" fill="url(#glow)"/>
  <rect x="0.5" y="0.5" width="{WIDTH - 1}" height="{height - 1}" rx="22" fill="none" stroke="#FFFFFF" stroke-opacity="0.10"/>
  <path d="M0 22 A22 22 0 0 1 22 0 H{WIDTH - 22} A22 22 0 0 1 {WIDTH} 22 V60 H0 Z" fill="#FFFFFF" fill-opacity="0.04"/>
  <circle cx="34" cy="30" r="8" fill="#F87171"/>
  <circle cx="60" cy="30" r="8" fill="#FBBF24"/>
  <circle cx="86" cy="30" r="8" fill="#34D399"/>
  <text x="{WIDTH // 2}" y="37" text-anchor="middle" class="sans" font-size="18" font-weight="600" fill="#94A3B8">Terminal: ssh-mcp-server setup</text>
  <g class="mono" xml:space="preserve">''']
for i, (kind, text, typed) in enumerate(LINES):
    if kind == "blank":
        continue
    y = TOP + i * LINE
    weight = ' font-weight="700"' if kind == "head" else ""
    stripped = text.lstrip(" ")
    body = " " * (len(text) - len(stripped)) + escape(stripped)
    if typed is not None:
        answer = escape(typed) if typed else "↵"
        color = "#67E8F9" if typed else "#475569"
        body += f'<tspan fill="{color}" font-weight="700">{answer}</tspan>'
    out.append(f'    <text x="{LEFT}" y="{y}" fill="{COLORS[kind]}"{weight}>{body}</text>')
out.append("  </g>\n</svg>\n")
with open(sys.argv[1], "w", encoding="utf-8", newline="\n") as f:
    f.write("\n".join(out))
print(height)
