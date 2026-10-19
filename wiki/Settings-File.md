# Settings File

Most people never need to open this file; the setup writes it for you. It's plain JSON, so you can edit it in any text editor if you like. Find it [here](Setup-Guide#where-the-settings-are-kept).

## A simple example

```json
{
  "servers": [
    {
      "id": "web",
      "host": "web.example.com",
      "username": "deploy",
      "privateKeyPath": "~/.ssh/id_ed25519",
      "knownHostsPath": "~/.ssh/known_hosts"
    },
    {
      "id": "staging",
      "host": "203.0.113.10",
      "port": 2222,
      "username": "ops",
      "passwordEnv": "SSH_MCP_STAGING_PASSWORD",
      "knownHostsPath": "~/.ssh/known_hosts"
    }
  ]
}
```

After editing, check your work:

```bash
ssh-mcp-server -validate            # checks the file
ssh-mcp-server -validate -connect   # also signs in to each server
```

It tells you exactly what's wrong, in plain words, and how to fix it.

## The basics

| Setting | What it means | Needed? |
|---|---|---|
| `id` | Short name for the server (letters, numbers, `.` `_` `-`) | Yes |
| `host` | Server address or IP | Yes |
| `username` | Account to sign in as | Yes |
| `port` | SSH port | No, default `22` |
| `privateKeyPath` | Your key file | One of these two |
| `passwordEnv` | **Name** of the environment variable holding the password (not the password itself) | One of these two |
| `privateKeyPassphraseEnv` | Name of the variable holding your key's passphrase, if it has one | Only for protected keys |
| `knownHostsPath` | File of trusted fingerprints, usually `~/.ssh/known_hosts` | Yes |

Paths can start with `~/` for your home folder.

## Safety and limits

| Setting | What it does | Default |
|---|---|---|
| `allowSftp` | Allow reading and changing files | `true` |
| `allowPortForwarding` | Allow tunnels | `false` |
| `allowedCommandPrefixes` | If set, **only** commands starting with these are allowed, e.g. `["df", "uptime", "systemctl status"]` | no limit |
| `deniedCommandRegexes` | Patterns that are always refused, e.g. `["(?i)\\b(shutdown|reboot)\\b"]` | none |
| `commandTimeoutMs` | Longest a command may run, in milliseconds | `60000` (1 minute) |
| `maxOutputBytes` | Most output kept per command or file read | `1048576` (1 MB) |
| `connectTimeoutMs` | How long to wait when connecting | `10000` (10 s) |
| `maxConcurrentOperations` | How many actions at once on this server | `4` |
| `maxConcurrentTunnels` | How many tunnels at once | `4` |
| `strictHostKeyChecking` | Check the server's fingerprint. Leave this on. | `true` |

When `allowedCommandPrefixes` is set, symbols that chain commands (`;`, `|`, `&`, `$`, quotes, and so on) are refused too, so the list can't be sneaked around.

## Things SSH MCP refuses, on purpose

- Passwords or keys written directly in the file (`password`, `privateKey`, ...). Use `passwordEnv` / `privateKeyPath` instead.
- Misspelled settings, so a typo can never quietly switch off a safety feature.
- Two servers with the same `id`.

## For power users

- A full description for editors (autocomplete in VS Code and others): add `"$schema": "https://raw.githubusercontent.com/anand34577/ssh-mcp/main/config/servers.schema.json"` at the top.
- Full technical reference: [README](https://github.com/anand34577/ssh-mcp#configuration-reference).
- Emergency switches (avoid): `SSH_MCP_ALLOW_INSECURE_HOST_KEYS` and `SSH_MCP_AUTO_ACCEPT_HOST_KEYS` turn off fingerprint checks. Only for temporary troubleshooting.
