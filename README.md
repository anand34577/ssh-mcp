# SSH MCP Server

> Give AI assistants controlled access to SSH, SFTP, and secure tunnels—without exposing your credentials.

SSH MCP Server is a production-minded [Model Context Protocol](https://modelcontextprotocol.io/) server for connecting an AI client to one or many SSH targets. It provides command execution, SFTP file operations, and optional loopback-only TCP forwarding through a simple MCP stdio process.

The recommended implementation is Go: it builds small, self-contained binaries for Windows AMD64, Linux AMD64, and Linux ARM64. A Java 11 implementation is included for environments that prefer the JVM.

## Why this project

AI assistants are useful when they can inspect systems and perform operational tasks, but SSH credentials should never become part of the model conversation. This server keeps credentials in the server process, verifies host keys, applies per-target limits and policies, and exposes only safe operational metadata to the MCP client.

## Highlights

- Multiple SSH servers in one MCP process, selected by a stable `serverId`.
- Remote command execution with timeouts, output limits, exit status, and truncation reporting.
- SFTP listing, metadata, bounded reads, writes, directory creation, and deletion.
- Optional SSH TCP forwarding restricted to loopback addresses.
- Passwords and key passphrases loaded from environment variables; no inline secrets.
- Strict `known_hosts` verification by default.
- Per-server command allow/deny policies and concurrency limits.
- Audit logging to stderr without command text or credentials.
- Portable Go binaries that require no Go, Java, JDK, JRE, C compiler, or Visual Studio at runtime.

## Choose an implementation

| Implementation | Best for | Location |
|---|---|---|
| Go | Recommended single-file deployment | [`go/`](go/) |
| Java 11 | JVM-based deployments and Java tooling | [`java/`](java/) |

The detailed Go setup, configuration, MCP client examples, tool reference, security guidance, and troubleshooting are in [`go/README.md`](go/README.md).

## Quick start with the Go binary

Download the release artifact for the target platform, then copy the configuration template:

```powershell
Copy-Item .\config\servers.example.json .\config\servers.json
```

Edit `config/servers.json` and set `SSH_MCP_CONFIG` plus the credential environment variables in the MCP client's process environment. Start the correct binary:

```powershell
& .\go\dist\ssh-mcp-server-windows-amd64.exe `
  -config (Resolve-Path .\config\servers.json)
```

The Linux artifacts are:

```text
go/dist/ssh-mcp-server-linux-amd64
go/dist/ssh-mcp-server-linux-arm64
```

The server uses MCP stdio transport: protocol messages use stdout, and operational logs use stderr. Do not mix diagnostic output into stdout.

## Build the release matrix

Go 1.26 or newer is required only to build. The build uses `CGO_ENABLED=0` and produces:

- Windows AMD64: `go/dist/ssh-mcp-server-windows-amd64.exe`
- Linux AMD64: `go/dist/ssh-mcp-server-linux-amd64`
- Linux ARM64: `go/dist/ssh-mcp-server-linux-arm64`

From Windows, build all three targets:

```powershell
cd go
powershell -ExecutionPolicy Bypass -File .\scripts\build-all.ps1
```

From a Unix shell, build one Linux target:

```bash
cd go
GOOS=linux GOARCH=amd64 ./scripts/build-unix.sh
GOOS=linux GOARCH=arm64 ./scripts/build-unix.sh
```

Run the Go checks locally:

```bash
cd go
go test ./...
go vet ./...
```

## GitHub Actions

Every push and pull request runs Go formatting, tests, race detection, `go vet`, all three Go cross-builds, and Java 11 tests. Pushing a tag such as `v1.0.0` builds the three Go release artifacts, generates SHA-256 checksums, and publishes a GitHub Release with generated notes.

To publish a release:

```bash
git add .
git commit -m "Prepare release v1.0.0"
git tag -a v1.0.0 -m "Release v1.0.0"
git push origin main
git push origin v1.0.0
```

The tag push automatically starts `.github/workflows/release.yml`. The workflow builds Windows AMD64, Linux AMD64, and Linux ARM64 binaries and publishes them with checksum files.

## Configuration and security

Each configured server should use an indirect credential reference:

```json
{
  "id": "production",
  "host": "server.example.com",
  "port": 22,
  "username": "deploy",
  "privateKeyPath": "${user.home}/.ssh/id_ed25519",
  "privateKeyPassphraseEnv": "SSH_MCP_PRODUCTION_KEY_PASSPHRASE",
  "knownHostsPath": "${user.home}/.ssh/known_hosts",
  "strictHostKeyChecking": true,
  "allowSftp": true,
  "allowPortForwarding": false,
  "allowedCommandPrefixes": ["systemctl status", "journalctl"],
  "deniedCommandRegexes": ["(?i).*\\b(shutdown|reboot|poweroff)\\b.*"]
}
```

Passwords use `passwordEnv`; private keys use `privateKeyPath`; encrypted key passphrases use `privateKeyPassphraseEnv`. The values of those environment variables are never MCP arguments, tool results, or audit-log fields. Inline `password`, `passphrase`, `privateKey`, `privateKeyContent`, and `secret` fields are rejected.

Strict host-key checking is enabled by default. Verify host fingerprints out of band before adding them to `known_hosts`. Keep `allowPortForwarding` disabled unless it is explicitly required, use least-privilege remote accounts, and treat command execution and SFTP writes/deletes as privileged operations.

## MCP tools

- `ssh_list_servers` — list configured targets and non-secret capabilities.
- `ssh_exec` — execute a bounded remote command.
- `sftp_list` — list a remote directory.
- `sftp_stat` — inspect remote file or directory metadata.
- `sftp_read_file` — read bounded UTF-8 or base64 content.
- `sftp_write_file` — write bounded UTF-8 or base64 content.
- `sftp_mkdir` — create a remote directory.
- `sftp_delete` — delete a file or empty directory.
- `ssh_tunnel_open` — open a loopback-only local TCP forward.
- `ssh_tunnel_close` — close a tunnel.
- `ssh_tunnel_list` — list active tunnels without credentials.

See [`go/README.md`](go/README.md) for request examples and client configuration.

## Java implementation

The Java implementation targets Java 11 and includes Maven, jlink, jpackage, and optional GraalVM Native Image workflows:

```powershell
cd java
mvn clean verify
```

Read [`java/README.md`](java/README.md) for Java packaging details. The bundled-runtime app image runs without Java installed on the target machine. A Windows GraalVM Native Image build requires the MSVC/Windows SDK toolchain; TDM-GCC cannot replace that linker toolchain.

## Repository layout

```text
config/                         Shared configuration template
go/                             Recommended Go implementation
  cmd/ssh-mcp-server/           Executable entry point
  internal/config/              Configuration and validation
  internal/mcpserver/           MCP tool registration
  internal/sshservice/          SSH, SFTP, and tunnel operations
  scripts/                      Cross-platform build scripts
java/                           Java 11 implementation
.github/workflows/              Continuous integration and release automation
```

## Contributing

Before opening a pull request:

```bash
cd go
go test ./...
go vet ./...
```

For Java changes, run `mvn test` from `java/`. Never commit real server configurations, private keys, passwords, host fingerprints from private infrastructure, or generated binaries.

## License

This project is licensed under the [MIT License](LICENSE).
