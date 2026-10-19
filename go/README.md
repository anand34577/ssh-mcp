# Go SSH MCP Server — Usage Guide

A single self-contained binary that lets an MCP client run commands, transfer files over SFTP, and open loopback-only TCP tunnels on SSH servers you configure in advance. The target machine needs no Go, Java, or other runtime.

## The quick way

The one-line installers in the repository root download the right binary, verify its checksum, install it per user, and start the guided setup:

```bash
curl -fsSL https://raw.githubusercontent.com/anand34577/ssh-mcp/main/install.sh | sh          # macOS / Linux
irm https://raw.githubusercontent.com/anand34577/ssh-mcp/main/install.ps1 | iex               # Windows PowerShell
```

The binary manages its own lifecycle:

| Command | What it does |
|---|---|
| `setup [-config file]` | Interactive: add a server (host-key fingerprint confirmation, sign-in test, optional look-only policy), test, remove, connect apps. Also runs when the binary is started from a terminal with no arguments, or double-clicked. |
| `connect [-app ids]` | Non-interactive: registers the server with Claude Desktop, Claude Code (`claude mcp add --scope user`), Codex (`~/.codex/config.toml`), Gemini CLI, Cursor, Windsurf, and VS Code. It reuses secrets already stored in any app's `env` block, backs up each file to `.bak`, and leaves files with comments untouched. |
| `update` | Downloads the release asset for this platform, verifies it against its `.sha256`, and atomically replaces the binary (on Windows, by renaming the running binary aside). Honors `SSH_MCP_VERSION` and `SSH_MCP_DOWNLOAD_URL`. |
| `uninstall [-purge] [-yes]` | Removes the registration from every app, optionally deletes `servers.json`, and deletes the binary. `known_hosts` and keys are never touched. |

Nothing contacts the network except SSH connections and an explicit `update`: there is no telemetry and no background update check.

The manual steps below are for people who want full control.

## 1. Choose the binary

| Target system | Binary |
|---|---|
| Windows x86-64 / ARM64 | `ssh-mcp-server-windows-amd64.exe` / `ssh-mcp-server-windows-arm64.exe` |
| Linux x86-64 / ARM64 | `ssh-mcp-server-linux-amd64` / `ssh-mcp-server-linux-arm64` |
| macOS Intel / Apple silicon | `ssh-mcp-server-darwin-amd64` / `ssh-mcp-server-darwin-arm64` |

Download from the [Releases page](https://github.com/anand34577/ssh-mcp/releases) and verify it with `sha256sum -c <binary>.sha256`. On Linux and macOS run `chmod +x <binary>`; on macOS you may also need `xattr -d com.apple.quarantine <binary>`. Check it runs with `<binary> -version`.

## 2. Create the configuration

```bash
cp config/servers.example.json config/servers.json      # PowerShell: Copy-Item
```

`servers.example.json` references `servers.schema.json`, so VS Code and other editors offer completion and validation as you edit. A minimal password-auth target:

```json
{
  "$schema": "./servers.schema.json",
  "servers": [
    {
      "id": "staging",
      "host": "staging.example.com",
      "username": "ops",
      "passwordEnv": "SSH_MCP_STAGING_PASSWORD",
      "knownHostsPath": "~/.ssh/known_hosts"
    }
  ]
}
```

Every field, default, and limit is documented in the [configuration reference](../README.md#configuration-reference). The important rules:

- Each `id` is unique; tools address targets by it as `serverId`.
- `passwordEnv` and `privateKeyPassphraseEnv` contain environment-variable **names**. Inline `password`, `passphrase`, `privateKey`, `privateKeyContent`, and `secret` fields are rejected.
- Configure `passwordEnv`, `privateKeyPath`, or both. The password also answers single hidden-prompt keyboard-interactive challenges, which PAM-backed servers often use.
- Unknown fields and out-of-range values are errors, so a typo never silently weakens security.

### Where the configuration is found

1. `-config <path>`
2. `SSH_MCP_CONFIG`
3. The first existing file among `./config/servers.json`, `servers.json` or `config/servers.json` next to the binary, and `<user config dir>/ssh-mcp/servers.json` (`%APPDATA%` on Windows, `~/Library/Application Support` on macOS, `$XDG_CONFIG_HOME` or `~/.config` on Linux).

MCP clients start servers from unpredictable working directories, so pass `-config` with an absolute path, or put `servers.json` next to the binary.

## 3. Trust the host keys

Strict `known_hosts` verification is on by default. Verify each server's fingerprint out of band, then record it:

```bash
ssh-keyscan -p 22 staging.example.com >> ~/.ssh/known_hosts
ssh-keygen -lf ~/.ssh/known_hosts     # compare with the fingerprint you were given
```

The client negotiates only the host-key types recorded for that host, so a `known_hosts` file holding just one key type (for example only ECDSA) works even when the server prefers another.

Disabling verification for one target requires both `"strictHostKeyChecking": false` and `SSH_MCP_ALLOW_INSECURE_HOST_KEYS=true`. `SSH_MCP_AUTO_ACCEPT_HOST_KEYS=true` disables it for every target. Both are emergency switches only.

## 4. Validate the setup

Run the built-in check before wiring up a client. It touches no remote state:

```bash
export SSH_MCP_STAGING_PASSWORD='…'
./ssh-mcp-server-linux-amd64 -config /opt/ssh-mcp/servers.json -validate            # offline checks
./ssh-mcp-server-linux-amd64 -config /opt/ssh-mcp/servers.json -validate -connect   # also log in
```

```text
Configuration: /opt/ssh-mcp/servers.json

[OK (connected)] staging (staging.example.com:22)

[FAIL] production (server.example.com:22)
  error:   environment variable SSH_MCP_PRODUCTION_KEY_PASSPHRASE (privateKeyPassphraseEnv for server "production") is not set or empty in the MCP server process
  error:   host server.example.com:22 is not in /home/me/.ssh/known_hosts; verify its fingerprint, then run: ssh-keyscan -p 22 server.example.com >> /home/me/.ssh/known_hosts

1 of 2 server(s) ready.
```

The exit code is 0 only when every server is ready.

## 5. Configure the MCP client

Supply the credentials through the client's `env` block (ideally from a secret store), not your shell profile.

Claude Code:

```bash
claude mcp add ssh --env SSH_MCP_STAGING_PASSWORD=… -- /opt/ssh-mcp/ssh-mcp-server-linux-amd64 -config /opt/ssh-mcp/servers.json
```

Claude Desktop and other clients using the `mcpServers` format:

```json
{
  "mcpServers": {
    "ssh": {
      "command": "C:\\ssh-mcp\\ssh-mcp-server-windows-amd64.exe",
      "args": ["-config", "C:\\ssh-mcp\\servers.json"],
      "env": {
        "SSH_MCP_STAGING_PASSWORD": "injected-by-your-secret-store"
      }
    }
  }
}
```

Restart the client and ask it to call `ssh_list_servers`.

The server speaks MCP over stdio. Logs and audit lines go to stderr, so never redirect stderr into stdout. It shuts down cleanly on SIGINT/SIGTERM or when the client closes stdin, and open tunnels are closed on exit.

## 6. Tools

| Tool | Purpose |
|---|---|
| `ssh_list_servers` | Ids, capabilities, limits, and allowed command prefixes. Never secrets. |
| `ssh_exec` | Run a non-interactive command; returns `stdout`, `stderr`, `exitCode`, `exitSignal`, `timedOut`, truncation flags |
| `sftp_list`, `sftp_stat` | Inspect remote paths |
| `sftp_read_file` | Read text or base64; page large files with `offset` / `nextOffset` |
| `sftp_write_file` | Create or replace a file from `content` or `contentBase64` |
| `sftp_mkdir` | Create a directory (`parents: true` for `mkdir -p`) |
| `sftp_delete` | Delete a file or an empty directory |
| `sftp_rename` | Rename or move (`overwrite: true` for atomic replace) |
| `ssh_tunnel_open` / `_list` / `_close` | Local port forwards from a loopback port on this machine |

Arguments and result shapes for every tool: [skills/ssh-mcp/references/tools.md](../skills/ssh-mcp/references/tools.md). Tool descriptions, per-argument schema descriptions, and MCP annotations (`readOnlyHint`, `destructiveHint`, …) are also published to the client through `tools/list`.

## Build from source

Go 1.26 or newer is needed only to build.

```bash
cd go
go test ./...                       # includes end-to-end tests against an in-process SSH server
go build -ldflags "-X main.version=v1.2.0" ./cmd/ssh-mcp-server
```

Release builds for every platform, with checksums:

```powershell
cd go
powershell -ExecutionPolicy Bypass -File .\scripts\build-all.ps1   # all six targets
powershell -ExecutionPolicy Bypass -File .\scripts\build-windows.ps1  # this machine only
```

```bash
cd go
./scripts/build-unix.sh                         # this machine
GOOS=linux GOARCH=arm64 ./scripts/build-unix.sh # cross-compile
```

## Troubleshooting

| Message | Fix |
|---|---|
| `no configuration file found; searched …` | Run `ssh-mcp-server setup`, pass `-config` with an absolute path, or set `SSH_MCP_CONFIG`. |
| `knownHostsPath is not a regular file` | Create the file with `ssh-keyscan` (after verifying the fingerprint) or fix the path. |
| `host key for … is not in …` | Add the verified key; non-default ports are recorded as `[host]:port`. |
| `HOST KEY MISMATCH` | The key changed. Verify out of band before replacing the entry (`ssh-keygen -R host`). |
| `environment variable X (passwordEnv …) is not set` | Set it in the MCP client's `env` block for this server. |
| `private key … is encrypted` | Configure `privateKeyPassphraseEnv` and provide the variable. |
| `unable to authenticate` | Wrong username or password, or the key isn't in `authorized_keys`. |
| `keyboard-interactive challenge is not a single password prompt` | The server requires MFA; use a key-based service account. |
| `cannot reach …` / `handshake … timed out` | Network, firewall, or port problem; raise `connectTimeoutMs` on slow links. |
| `server "x" is busy` | All `maxConcurrentOperations` slots are in use; retry or raise the limit. |

## License

[MIT](../LICENSE)
