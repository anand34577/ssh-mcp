# SSH MCP Server

[![CI](https://github.com/anand34577/ssh-mcp/actions/workflows/ci.yml/badge.svg)](https://github.com/anand34577/ssh-mcp/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

> Give an AI assistant controlled, audited access to your servers over SSH — without ever putting a password or private key in front of the model.

SSH MCP Server is a [Model Context Protocol](https://modelcontextprotocol.io/) server that connects an MCP client (Claude, Claude Code, or any other MCP-compatible client) to one or more SSH targets. It exposes remote command execution, SFTP file operations, and optional loopback-only TCP tunnels as MCP tools, while keeping credentials, host keys, and command policy entirely on the server side.

Two independent, feature-equivalent implementations are provided:

| Implementation | Status | Runtime requirement | Location |
|---|---|---|---|
| **Go** | Recommended | None — self-contained static binary | [`go/`](go/) |
| **Java 11** | Fully supported | None for packaged builds — bundles its own runtime | [`java/`](java/) |

This document covers both, with the Go binary as the primary path.

## Table of contents

- [Why this project exists](#why-this-project-exists)
- [Features](#features)
- [Which SSH authentication methods work](#which-ssh-authentication-methods-work)
- [Quick start (Go)](#quick-start-go)
- [Configuration reference](#configuration-reference)
- [MCP tools](#mcp-tools)
- [Security model](#security-model)
- [Known limits and operational notes](#known-limits-and-operational-notes)
- [Building from source](#building-from-source)
- [Releases and CI/CD](#releases-and-cicd)
- [Java implementation](#java-implementation)
- [Repository layout](#repository-layout)
- [Troubleshooting](#troubleshooting)
- [Contributing](#contributing)
- [Security policy](#security-policy)
- [License](#license)

## Why this project exists

An AI assistant is genuinely useful when it can inspect and operate real systems — check a log, restart a service, ship a file — but handing it raw SSH credentials is not an acceptable way to get there. This server draws that boundary explicitly:

- Credentials (passwords, private keys, passphrases) live only in the server process's environment or filesystem. They are never sent to the model, never appear in a tool argument, never appear in a tool result, and never appear in a log line.
- Every target is configured once, ahead of time, by whoever operates the server — not invented on the fly by the model.
- Host keys are verified by default. Command policy, output size, timeouts, and concurrency are all bounded per target.
- Every operation is audited to `stderr` (target id, operation, duration, success/failure, and a command hash — never the command text or any secret).

## Features

- Multiple SSH servers in a single MCP process, selected by a stable `serverId`.
- Remote command execution (`ssh_exec`) with bounded timeout, bounded stdout/stderr, exit code, exit signal, and truncation reporting.
- Full SFTP surface: list, stat, bounded read, bounded write (UTF-8 or base64), mkdir, delete.
- Optional SSH local port forwarding (`ssh_tunnel_*`), restricted to a loopback local bind, disabled by default.
- Password, private-key, and keyboard-interactive authentication — see [below](#which-ssh-authentication-methods-work).
- Strict `known_hosts` verification by default, with an explicit, clearly-labeled escape hatch for emergencies.
- Per-server command allow-list / deny-list policy.
- Independent concurrency limits for short-lived operations vs. long-lived tunnels, so one can't starve the other.
- Structured audit logging to `stderr`, kept separate from the MCP protocol stream on `stdout`/`stdin`.
- Portable Go binaries for Windows, Linux, and macOS on both amd64 and arm64 — no runtime dependency at all.
- A Java 11 implementation with a bundled-JRE app image, so the target machine still needs no JDK/JRE installed.

## Which SSH authentication methods work

**Yes — this works with plain username/password SSH, not just keys.** Both implementations support:

| Method | Config field | Notes |
|---|---|---|
| Password | `passwordEnv` | Names an environment variable holding the password; the value is read only at connect time and never leaves the process. |
| Private key | `privateKeyPath` (+ optional `privateKeyPassphraseEnv`) | RSA, ECDSA, Ed25519, and encrypted keys are supported. |
| Both | set both fields | The client offers public-key first, then password, matching normal OpenSSH client behavior. |
| Keyboard-interactive | automatic, via `passwordEnv` | Many servers (PAM-backed `sshd`, network appliances, some cloud images) only advertise `keyboard-interactive` instead of `password`, even for a plain username/password login. Both implementations automatically answer a single, non-echoed, password-style keyboard-interactive prompt using the same `passwordEnv` value — no extra configuration needed. |

What is **not** supported, by design: SSH agent forwarding, GSSAPI/Kerberos, and certificate-based host authentication beyond `known_hosts`. These are less common for automation targets and were left out to keep the trust boundary simple; contributions are welcome if you need them (see [Contributing](#contributing)).

Compatibility note: both SSH client libraries (`golang.org/x/crypto/ssh` and Apache MINA SSHD) ship modern, secure default cipher/KEX/host-key algorithm sets. Very old network equipment that only speaks deprecated algorithms (e.g. `diffie-hellman-group1-sha1`, SSH-1) may not be reachable. This is intentional — the fix is to upgrade the remote endpoint, not to weaken the client.

## Quick start (Go)

1. **Get a binary.** Download the release asset for your platform from the [Releases page](https://github.com/anand34577/ssh-mcp/releases) (or [build it yourself](#building-from-source)):

   ```text
   ssh-mcp-server-linux-amd64
   ssh-mcp-server-linux-arm64
   ssh-mcp-server-darwin-amd64
   ssh-mcp-server-darwin-arm64
   ssh-mcp-server-windows-amd64.exe
   ssh-mcp-server-windows-arm64.exe
   ```

2. **Create your configuration** from the template:

   ```bash
   cp config/servers.example.json config/servers.json
   ```

   Edit `config/servers.json` — see the full [configuration reference](#configuration-reference) below. A minimal password-auth entry:

   ```json
   {
     "servers": [
       {
         "id": "staging",
         "host": "staging.example.com",
         "username": "ops",
         "passwordEnv": "SSH_MCP_STAGING_PASSWORD",
         "knownHostsPath": "${user.home}/.ssh/known_hosts"
       }
     ]
   }
   ```

3. **Populate `known_hosts`** for every host you configure, verifying the fingerprint out of band:

   ```bash
   ssh-keyscan -H staging.example.com >> ~/.ssh/known_hosts
   ```

4. **Point your MCP client at the binary**, supplying the config path and credential environment variables. Example client config:

   ```json
   {
     "mcpServers": {
       "ssh": {
         "command": "/opt/ssh-mcp/ssh-mcp-server-linux-amd64",
         "args": ["-config", "/opt/ssh-mcp/config/servers.json"],
         "env": {
           "SSH_MCP_STAGING_PASSWORD": "injected-by-your-secret-store"
         }
       }
     }
   }
   ```

5. **Restart the MCP client.** Ask it to run `ssh_list_servers` to confirm the target is visible, then try `ssh_exec` with a harmless command like `uname -a` or `whoami`.

Full walkthrough, every tool's request/response shape, and platform-specific launch commands: [`go/README.md`](go/README.md).

## Configuration reference

Configuration is a single JSON file (`config/servers.json` by default; override with `-config` or `SSH_MCP_CONFIG`). It must contain a `servers` array; every other top-level field is rejected. Each server entry:

| Field | Type | Default | Description |
|---|---|---|---|
| `id` | string | required | Unique identifier, `[A-Za-z0-9._-]{1,64}`. This is the `serverId` every tool call uses. |
| `host` | string | required | Hostname or IP address. |
| `port` | integer | `22` | 1–65535. |
| `username` | string | required | Remote SSH username. |
| `passwordEnv` | string | — | Name of an environment variable holding the password. Configure this, `privateKeyPath`, or both. |
| `privateKeyPath` | string | — | Path to a private key file. Supports `${user.home}` and `~/`. |
| `privateKeyPassphraseEnv` | string | — | Name of an environment variable holding the key's passphrase, if encrypted. |
| `knownHostsPath` | string | — | Path to a `known_hosts` file. Required whenever `strictHostKeyChecking` is `true` (the default). |
| `strictHostKeyChecking` | boolean | `true` | When `false`, requires the explicit `SSH_MCP_ALLOW_INSECURE_HOST_KEYS=true` environment override to load. |
| `connectTimeoutMs` | integer | `10000` | 1000–120000. TCP + SSH handshake timeout, and also the maximum time a tunnel-open request will wait for a free tunnel slot. |
| `commandTimeoutMs` | integer | `60000` | 100–3600000. Default/maximum `ssh_exec` and SFTP operation timeout. |
| `maxOutputBytes` | integer | `1048576` | 1024–104857600. Default/maximum bytes captured per stdout/stderr stream, and per SFTP read/write. |
| `maxConcurrentOperations` | integer | `4` | 1–64. Maximum concurrent `ssh_exec`/`sftp_*` calls for this server. |
| `maxConcurrentTunnels` | integer | `4` | 1–64. Maximum concurrent open port forwards for this server — a **separate** pool from `maxConcurrentOperations`, so open tunnels can never block command execution or SFTP on the same target, and vice versa. |
| `allowSftp` | boolean | `true` | Enables the `sftp_*` tools for this server. |
| `allowPortForwarding` | boolean | `false` | Enables `ssh_tunnel_open` for this server. |
| `allowedCommandPrefixes` | string[] | `[]` (no restriction) | When non-empty, `ssh_exec` commands must match one prefix exactly at a word boundary, and shell metacharacters (`; | & < > $ \` ( ) { } ' " \` and newlines) are rejected outright. |
| `deniedCommandRegexes` | string[] | `[]` | Regular expressions; any match rejects the command, checked before the allow-list. |

Rules enforced at load time, in both implementations:

- Unknown fields at the root or in any server entry are rejected — a typo cannot silently fall back to an insecure default.
- Inline secret-shaped fields (`password`, `passphrase`, `privateKey`, `privateKeyContent`, `secret`) are rejected outright; only the `*Env` indirection is accepted.
- Duplicate `id`s are rejected.
- `strictHostKeyChecking: false` requires the operator to also set `SSH_MCP_ALLOW_INSECURE_HOST_KEYS=true` in the process environment — a config file alone cannot disable host-key verification.

Environment variables recognized by the server process itself (not per-server config):

| Variable | Effect |
|---|---|
| `SSH_MCP_CONFIG` | Path to the configuration file, if `-config`/`-Dssh.mcp.config` is not passed. |
| `SSH_MCP_ALLOW_INSECURE_HOST_KEYS` | Set to `true` to permit a server entry with `strictHostKeyChecking: false`. |
| `SSH_MCP_AUTO_ACCEPT_HOST_KEYS` | Set to `true` to accept **any** host key for **every** configured server, bypassing `known_hosts` entirely. This disables MITM protection process-wide. Intended only as a temporary emergency/compatibility switch — never leave it set in production. |

## MCP tools

| Tool | Purpose | Requires |
|---|---|---|
| `ssh_list_servers` | List configured server ids and non-secret capabilities (host, port, username, enabled auth methods, policy flags, concurrency limits). Never returns a secret. | — |
| `ssh_exec` | Run a command on a target. Returns `stdout`, `stderr`, `exitCode`, `exitSignal`, `timedOut`, truncation flags, `durationMs`. | — |
| `sftp_list` | List a remote directory. | `allowSftp: true` |
| `sftp_stat` | Read file/directory metadata (size, type, permissions, modified time). | `allowSftp: true` |
| `sftp_read_file` | Read a file, bounded by byte count, as UTF-8 or base64. | `allowSftp: true` |
| `sftp_write_file` | Write a file from UTF-8 (`content`) or base64 (`contentBase64`) — exactly one of the two. | `allowSftp: true` |
| `sftp_mkdir` | Create a directory. | `allowSftp: true` |
| `sftp_delete` | Delete a file, or an empty directory when `directory: true`. | `allowSftp: true` |
| `ssh_tunnel_open` | Open a local TCP forward. The local bind must be loopback (`127.0.0.1`, `::1`, or `localhost`); `localPort: 0` picks a free port. | `allowPortForwarding: true` |
| `ssh_tunnel_close` | Close a tunnel by `tunnelId`. | — |
| `ssh_tunnel_list` | List currently open tunnels for this process, with no credentials. | — |

Full request/response JSON examples for every tool: [`go/README.md`](go/README.md#6-available-mcp-tools) (identical tool shapes on the Java side).

## Security model

- **Credentials never reach the model.** They are read from the process environment or from key files on disk, only at the moment a connection is opened, and are excluded from every tool result and log line.
- **Host keys are verified by default** against an operator-provisioned `known_hosts` file. There is no implicit trust-on-first-use.
- **Command policy is server-side and explicit.** `allowedCommandPrefixes` and `deniedCommandRegexes` are enforced by the MCP server itself, before anything reaches the remote shell — the model cannot argue its way past them.
- **Everything is bounded.** Output size, execution time, and concurrency are capped per server, so a runaway or malicious command cannot exhaust memory or hold the process hostage.
- **Tunnels are loopback-only on the local side.** A tunnel can never bind to a non-loopback local address, which prevents accidentally exposing a forwarded port to the network.
- **The remote account is still the ultimate boundary.** This server enforces policy at the MCP layer, but it cannot restrict what the configured SSH account is permitted to do on the remote system. Use least-privilege service accounts for anything automation touches.

See [SECURITY.md](SECURITY.md) for the vulnerability reporting process, and each implementation's README for a per-target hardening checklist.

## Known limits and operational notes

Documented here deliberately, rather than discovered the hard way:

- **No SSH agent support.** Only file-based private keys are read from `privateKeyPath`. If you need agent-based auth, open an issue or contribute it.
- **Legacy algorithm support is intentionally not provided.** Extremely old SSH servers (pre-2015 network gear, SSH-1 devices) that only speak deprecated ciphers/KEX will not connect. Upgrading the remote endpoint is the recommended fix.
- **`allowedCommandPrefixes` is conservative by design.** Any shell metacharacter — including a plain double quote used to wrap an argument — is rejected outright when an allow-list is configured, to prevent allow-list bypass via shell chaining/substitution. If a target needs quoted arguments, either leave the allow-list empty (and rely on `deniedCommandRegexes` plus a least-privilege remote account) or use a wrapper script on the remote end that takes simple, unquoted arguments.
- **Tunnels and exec/SFTP operations use independent concurrency pools** (`maxConcurrentTunnels` vs. `maxConcurrentOperations`, fixed in this release). Before this change, both implementations shared one pool, so a handful of open tunnels could silently starve command execution on the same server; that is no longer possible.
- **`ssh_tunnel_open` waits up to `connectTimeoutMs`** for a free tunnel slot before failing with a clear "tunnel limit reached" error, rather than hanging indefinitely.

## Building from source

### Go

Requires Go 1.26 or newer (build-time only; the resulting binary has no runtime dependency).

```bash
cd go
go build ./cmd/ssh-mcp-server
go test ./...
go vet ./...
```

Cross-compile any target with `GOOS`/`GOARCH`:

```bash
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o dist/ssh-mcp-server-linux-arm64 ./cmd/ssh-mcp-server
```

Or use the provided scripts:

```powershell
cd go
powershell -ExecutionPolicy Bypass -File .\scripts\build-all.ps1
```

```bash
cd go
GOOS=linux GOARCH=amd64 ./scripts/build-unix.sh
```

### Java

Requires JDK 11+ and Maven.

```bash
cd java
mvn clean verify
```

See [`java/README.md`](java/README.md) for the portable jlink runtime and native-image packaging options.

## Releases and CI/CD

- **CI** (`.github/workflows/ci.yml`) runs on every push and pull request: Go formatting, `go test -race`, `go vet`, a Go build matrix, and the full Java 11 Maven verification.
- **Release** (`.github/workflows/release.yml`) runs on a `v*` tag push or manual dispatch, and publishes a single GitHub Release containing:
  - Go binaries for **linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64, windows/arm64**, each with a `.sha256` checksum file.
  - A cross-platform Java shaded jar (`ssh-mcp-server.jar`), runnable anywhere with a JRE 11+.
  - Self-contained Java runtime bundles (jlink-based, no JRE required on the target machine) for Linux, macOS, and Windows, each archived with a checksum.

To cut a release:

```bash
git tag -a v1.1.0 -m "Release v1.1.0"
git push origin v1.1.0
```

## Java implementation

The Java implementation is fully supported and exposes the identical tool set and configuration schema as Go — the two are interchangeable from an MCP client's point of view. It targets Java 11 (rather than the MCP SDK's Java 17 baseline) using a small hand-rolled JSON-RPC/stdio layer and Apache MINA SSHD for the SSH client, and ships:

- A portable uber-jar (`mvn clean verify` → `target/ssh-mcp-server.jar`).
- A bundled-runtime app image (jlink + optional jpackage) that needs no JDK/JRE on the target machine.
- An optional GraalVM Native Image build for a true single-file native executable.

Full setup, packaging, and platform-specific instructions: [`java/README.md`](java/README.md).

## Repository layout

```text
config/                         Shared configuration template (config/servers.example.json)
go/                              Go implementation (recommended)
  cmd/ssh-mcp-server/            Executable entry point
  internal/config/               Configuration loading and validation
  internal/mcpserver/            MCP protocol / tool registration
  internal/sshservice/           SSH, SFTP, and tunnel operations
  scripts/                       Cross-platform build scripts
java/                            Java 11 implementation
  src/main/java/com/example/sshmcp/   Config, SshService, McpStdioServer, Main, AuditLog
  scripts/                       jlink/jpackage/native-image packaging scripts
.github/workflows/               CI and release automation
SECURITY.md                      Vulnerability reporting process
CONTRIBUTING.md                  Contribution guidelines
LICENSE                          MIT License
```

## Troubleshooting

| Symptom | Likely cause / fix |
|---|---|
| `configuration error: knownHostsPath is not a regular file` | The `knownHostsPath` file doesn't exist yet. Populate it with `ssh-keyscan` (after verifying the fingerprint) or point at an existing `known_hosts`. |
| `password credential is unavailable` | The environment variable named by `passwordEnv` isn't set in the MCP client's process environment for the server. |
| `private key could not be read` / `private key could not be parsed` | Check the key path, file permissions, key format, and (if encrypted) `privateKeyPassphraseEnv`. |
| Host-key verification failure on connect | The live host key doesn't match `known_hosts`. Verify the new fingerprint out of band before updating the file — do not blindly re-`ssh-keyscan`. |
| `command is not allowed by the server command policy` | The command doesn't match an `allowedCommandPrefixes` entry, or contains a shell metacharacter while an allow-list is configured. See [Known limits](#known-limits-and-operational-notes). |
| `tunnel limit reached for this server` / `Tunnel limit reached...` | `maxConcurrentTunnels` open tunnels already exist for that server; close one with `ssh_tunnel_close` or raise the limit. |
| `permission denied` running the Linux/macOS binary | `chmod +x` the binary after download. |
| Windows says the executable isn't compatible | Match the binary to your CPU architecture — `windows-amd64.exe` for Intel/AMD, `windows-arm64.exe` for ARM64 (e.g. Surface Pro X, Copilot+ PCs). |

More implementation-specific troubleshooting: [`go/README.md`](go/README.md#troubleshooting).

## Contributing

Contributions are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md) for the checklist (run `go test`/`go vet` or `mvn test`, never commit real credentials or infrastructure details, document behavior changes). Good candidates for a first contribution: SSH agent support, additional platform packaging, or improved audit-log structure (e.g. JSON lines for SIEM ingestion).

## Security policy

This software can execute commands and modify remote systems — treat it as privileged infrastructure. See [SECURITY.md](SECURITY.md) for how to report a vulnerability privately.

## License

This project is licensed under the [MIT License](LICENSE) — see the file for the full text. In short: you may use, copy, modify, merge, publish, distribute, sublicense, and sell copies of this software, provided the copyright notice and license text are preserved, and with no warranty of any kind.
