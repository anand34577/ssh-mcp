# Go SSH MCP Server — Usage Guide

This is a standalone MCP server that lets an MCP client connect to one or more configured SSH servers. It supports remote command execution, SFTP file operations, and optional loopback-only SSH TCP tunnels.

The release binaries are self-contained Go executables. The target machine does not need Go, Java, a JDK, a JRE, Visual Studio, or TDM-GCC.

## Choose the correct binary

| Target system | Binary |
|---|---|
| Windows 64-bit Intel/AMD | `dist/ssh-mcp-server-windows-amd64.exe` |
| Linux 64-bit Intel/AMD | `dist/ssh-mcp-server-linux-amd64` |
| Linux 64-bit ARM | `dist/ssh-mcp-server-linux-arm64` |

The binaries are operating-system and CPU specific. Do not run the Linux binary directly on Windows or use the ARM64 binary on an x86_64 machine.

## 1. Create the configuration

From the repository root, copy the template:

```powershell
Copy-Item .\config\servers.example.json .\config\servers.json
```

Edit `config/servers.json`. A configuration can contain multiple SSH targets:

```json
{
  "servers": [
    {
      "id": "production",
      "host": "server.example.com",
      "port": 22,
      "username": "deploy",
      "privateKeyPath": "${user.home}/.ssh/id_ed25519",
      "privateKeyPassphraseEnv": "SSH_MCP_PRODUCTION_KEY_PASSPHRASE",
      "knownHostsPath": "${user.home}/.ssh/known_hosts",
      "strictHostKeyChecking": true,
      "connectTimeoutMs": 10000,
      "commandTimeoutMs": 60000,
      "maxOutputBytes": 1048576,
      "maxConcurrentOperations": 4,
      "maxConcurrentTunnels": 4,
      "allowSftp": true,
      "allowPortForwarding": false,
      "allowedCommandPrefixes": [],
      "deniedCommandRegexes": [
        "(?i).*\\b(shutdown|reboot|poweroff)\\b.*"
      ]
    },
    {
      "id": "staging",
      "host": "staging.example.com",
      "port": 22,
      "username": "ops",
      "passwordEnv": "SSH_MCP_STAGING_PASSWORD",
      "knownHostsPath": "${user.home}/.ssh/known_hosts",
      "strictHostKeyChecking": true
    }
  ]
}
```

Important configuration rules:

- Every server needs a unique `id`. The MCP client uses this value as `serverId`.
- Configure either `passwordEnv`, `privateKeyPath`, or both.
- `passwordEnv` and `privateKeyPassphraseEnv` contain environment-variable names, not secret values.
- Inline `password`, `passphrase`, `privateKey`, `privateKeyContent`, and `secret` fields are rejected.
- Strict host-key verification is enabled by default and requires an existing `knownHostsPath`.
- For an explicitly temporary compatibility/emergency mode, set `SSH_MCP_AUTO_ACCEPT_HOST_KEYS=true`; this accepts new and changed host keys for every configured target and disables MITM protection. Keep it unset in normal or production use.
- SFTP is enabled by default when `allowSftp` is omitted.
- Port forwarding is disabled by default.
- `maxOutputBytes`, `commandTimeoutMs`, and `maxConcurrentOperations` protect the server from unbounded work.
- `maxConcurrentOperations` bounds concurrent `ssh_exec`/`sftp_*` calls per server; `maxConcurrentTunnels` bounds concurrent open port forwards per server, independently. A long-lived tunnel never consumes an exec/SFTP slot (or vice versa), so opening several tunnels cannot starve command execution on the same target.
- Password authentication also answers single-prompt keyboard-interactive challenges (common on PAM-backed servers) with the same `passwordEnv` value, so `passwordEnv` works whether the server advertises `password` or `keyboard-interactive`.
- Unknown root/server fields are rejected. If `allowedCommandPrefixes` is non-empty, shell-control characters and prefix lookalikes are rejected; configure simple command prefixes accordingly.

Protect `servers.json`, private keys, and the process environment with the operating system account permissions.

## 2. Prepare host-key verification

Populate `known_hosts` through a trusted administrative process. Verify the server fingerprint out of band before trusting it:

```bash
ssh-keyscan -H server.example.com >> ~/.ssh/known_hosts
```

Do not disable host-key verification in production. An insecure exception requires both:

```text
strictHostKeyChecking: false
SSH_MCP_ALLOW_INSECURE_HOST_KEYS=true
```

## 3. Provide credentials securely

The LLM never receives the password or private-key contents. The MCP client starts the server with the required environment variables, and the Go process reads them only when it opens an SSH connection.

### Windows PowerShell

```powershell
$env:SSH_MCP_CONFIG = 'C:\path\to\ssh-mcp\config\servers.json'
$env:SSH_MCP_STAGING_PASSWORD = 'injected-by-your-secret-store'
$env:SSH_MCP_PRODUCTION_KEY_PASSPHRASE = 'injected-by-your-secret-store'
```

### Linux

```bash
export SSH_MCP_CONFIG=/opt/ssh-mcp/config/servers.json
export SSH_MCP_STAGING_PASSWORD='injected-by-your-secret-store'
export SSH_MCP_PRODUCTION_KEY_PASSPHRASE='injected-by-your-secret-store'
```

Prefer the MCP client's secret manager or service-account environment rather than storing credentials in shell history.

## 4. Run the server directly

The `-config` option is explicit and recommended. If omitted, the server checks `SSH_MCP_CONFIG`, then uses `config/servers.json` relative to its working directory.

### Windows AMD64

```powershell
& 'C:\path\to\ssh-mcp\go\dist\ssh-mcp-server-windows-amd64.exe' `
  -config 'C:\path\to\ssh-mcp\config\servers.json'
```

### Linux AMD64

```bash
chmod +x ./dist/ssh-mcp-server-linux-amd64
./dist/ssh-mcp-server-linux-amd64 \
  -config /opt/ssh-mcp/config/servers.json
```

### Linux ARM64

```bash
chmod +x ./dist/ssh-mcp-server-linux-arm64
./dist/ssh-mcp-server-linux-arm64 \
  -config /opt/ssh-mcp/config/servers.json
```

The server uses MCP stdio transport. JSON-RPC is read from standard input and responses are written to standard output. Logs go to standard error; do not redirect diagnostic output into the MCP protocol stream.

## 5. Configure an MCP client

Use the appropriate executable as the MCP server command. The exact configuration key can vary by MCP client, but the shape is commonly:

### Windows

```json
{
  "mcpServers": {
    "ssh": {
      "command": "C:\\path\\to\\ssh-mcp\\go\\dist\\ssh-mcp-server-windows-amd64.exe",
      "args": [
        "-config",
        "C:\\path\\to\\ssh-mcp\\config\\servers.json"
      ],
      "env": {
        "SSH_MCP_STAGING_PASSWORD": "injected-by-your-secret-store",
        "SSH_MCP_PRODUCTION_KEY_PASSPHRASE": "injected-by-your-secret-store"
      }
    }
  }
}
```

### Linux

```json
{
  "mcpServers": {
    "ssh": {
      "command": "/opt/ssh-mcp/go/dist/ssh-mcp-server-linux-amd64",
      "args": [
        "-config",
        "/opt/ssh-mcp/config/servers.json"
      ],
      "env": {
        "SSH_MCP_STAGING_PASSWORD": "injected-by-your-secret-store"
      }
    }
  }
}
```

For Linux ARM64, replace the command with `ssh-mcp-server-linux-arm64`.

## 6. Available MCP tools

All tools that operate on a target accept the configured `serverId`.

### `ssh_list_servers`

Lists configured server IDs, host/port/user metadata, and enabled capabilities. It never returns passwords, private keys, passphrases, or environment-variable values.

Arguments: none.

### `ssh_exec`

Runs a remote command.

```json
{
  "serverId": "production",
  "command": "uname -a",
  "timeoutMs": 30000,
  "maxOutputBytes": 65536
}
```

The result contains `stdout`, `stderr`, `exitCode`, `exitSignal`, `timedOut`, truncation flags, and `durationMs`. A non-zero remote exit code is returned as command result data; it is not treated as a server crash. If the remote process was killed by a signal rather than exiting normally, `exitCode` is `null` and `exitSignal` names the signal (for example `"KILL"`).

### `sftp_list`

Lists a remote directory.

```json
{
  "serverId": "production",
  "path": "/var/log",
  "maxEntries": 100
}
```

### `sftp_stat`

Reads remote file or directory metadata.

```json
{
  "serverId": "production",
  "path": "/etc/hostname"
}
```

### `sftp_read_file`

Reads a bounded remote file. Use `encoding: "utf8"` for text or `encoding: "base64"` for binary data.

```json
{
  "serverId": "production",
  "path": "/etc/hostname",
  "maxBytes": 4096,
  "encoding": "utf8"
}
```

### `sftp_write_file`

Writes bounded content. Provide exactly one of `content` or `contentBase64`.

```json
{
  "serverId": "production",
  "path": "/tmp/hello.txt",
  "content": "hello from MCP\n"
}
```

### `sftp_mkdir`

Creates a remote directory.

```json
{
  "serverId": "production",
  "path": "/tmp/mcp-example"
}
```

### `sftp_delete`

Deletes a remote file, or an empty directory when `directory` is `true`.

```json
{
  "serverId": "production",
  "path": "/tmp/hello.txt",
  "directory": false
}
```

### `ssh_tunnel_open`

Opens a local TCP forward through SSH. The local bind is loopback-only.

```json
{
  "serverId": "production",
  "localHost": "127.0.0.1",
  "localPort": 0,
  "remoteHost": "127.0.0.1",
  "remotePort": 5432
}
```

Use `localPort: 0` to select a free local port. Forwarding must be enabled for that target with `allowPortForwarding: true`.

### `ssh_tunnel_list` and `ssh_tunnel_close`

`ssh_tunnel_list` returns active tunnel IDs and local ports. Close a tunnel explicitly:

```json
{
  "tunnelId": "tunnel-1"
}
```

Tunnels are also closed when the server process exits.

## Security recommendations

- Use separate least-privilege SSH accounts for automation.
- Keep strict `known_hosts` verification enabled.
- Use private-key authentication where practical.
- Keep `allowPortForwarding` false unless forwarding is required.
- Set `allowedCommandPrefixes` for targets that should run only a limited command set.
- Add `deniedCommandRegexes` for known destructive commands.
- Keep `maxOutputBytes` and timeouts appropriate for each target.
- Protect the configuration, private keys, environment variables, and stderr logs.
- Treat `ssh_exec`, SFTP writes/deletes, and tunnels as privileged operations; the remote account permissions are the final security boundary.

## Build from source

Go 1.26 or newer is required only to build the server. The build uses the official Go MCP SDK and `CGO_ENABLED=0`.

On Windows, build the complete release matrix:

```powershell
cd go
powershell -ExecutionPolicy Bypass -File .\scripts\build-all.ps1
```

For one target from a Unix environment:

```bash
cd go
GOOS=linux GOARCH=amd64 ./scripts/build-unix.sh
GOOS=linux GOARCH=arm64 ./scripts/build-unix.sh
```

## Troubleshooting

### `configuration error: knownHostsPath is not a regular file`

The configured `knownHostsPath` does not exist or is not a file. Create/populate it through a trusted SSH administration process and use an absolute path if necessary.

### `password credential is unavailable`

The environment variable named by `passwordEnv` was not present in the MCP server process. Configure it in the MCP client's `env` section or service account environment.

### `private key could not be read` or `private key could not be parsed`

Check the private-key path, file permissions, key format, and passphrase environment variable.

### Host-key verification failure

The remote host key does not match the trusted `known_hosts` entry. Verify the fingerprint out of band before changing the file.

### `permission denied` on Linux

Make the binary executable:

```bash
chmod +x ./dist/ssh-mcp-server-linux-amd64
```

### Windows says the executable is not compatible

Use `ssh-mcp-server-windows-amd64.exe` on Intel/AMD 64-bit Windows. The Linux binaries cannot run on Windows.

## License

This project is licensed under the [MIT License](../LICENSE).
