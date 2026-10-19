# SSH MCP Server

A standalone MCP server that lets an MCP client use configured SSH targets for command execution, SFTP, and optional loopback-only TCP forwarding. It supports multiple targets in one process. The JAR targets Java 11+, and the packaged Windows/Linux executables include their runtime so the target machine does not need a JDK or JRE.

The server uses MCP stdio transport: JSON-RPC messages are read from stdin and responses are written to stdout. Operational logs go to stderr, so they cannot corrupt the MCP stream.

## Why this implementation

The official Java MCP SDK currently has a Java 17 build baseline. This project keeps the MCP wire implementation small and Java 11-compatible, while using Apache MINA SSHD for the SSH client layer. The result is a smaller executable and a Java 11 runtime target without taking a Spring Boot runtime dependency.

## Build

Requirements:

- JDK 11 (the build is compiled with `--release 11`)
- Maven 3.6.3 or newer

```bash
mvn clean verify
```

The executable uber-JAR is:

```text
target/ssh-mcp-server.jar
```

Run Java build and packaging commands from the `java` directory. The shared configuration directory is one level above it; set `SSH_MCP_CONFIG` to an absolute path (recommended) or pass `-Dssh.mcp.config=..\config\servers.json` when running locally.

## Configure targets

Copy `config/servers.example.json` to `config/servers.json` from the repository root (the `$schema` reference gives editor completion). Each entry has a unique `id`; the model sends that id to every SSH tool, so one server process can safely manage multiple targets.

Credentials are deliberately indirect:

- Passwords are read from `passwordEnv`.
- Private keys are read from `privateKeyPath`.
- Encrypted private-key passphrases are read from `privateKeyPassphraseEnv`.
- Inline `password`, `passphrase`, `privateKey`, `privateKeyContent`, and `secret` fields are rejected.

Set the environment variables in the MCP client process environment, for example:

```text
SSH_MCP_STAGING_PASSWORD=provided-by-your-secret-manager
SSH_MCP_PRODUCTION_KEY_PASSPHRASE=provided-by-your-secret-manager
```

The values are never MCP tool arguments, never returned by `ssh_list_servers`, and are not written to the audit log. Protect the process environment and the private-key files with the operating system’s normal account/service permissions.

## Host-key security

Strict host-key verification is required by default. Every target must specify an existing `knownHostsPath`. Populate it using a trusted administrative channel and verify the fingerprint out of band before starting the server:

```bash
ssh-keyscan -H server.example.com >> ~/.ssh/known_hosts
```

Do not use `StrictHostKeyChecking=no` in production. An explicit insecure exception requires `SSH_MCP_ALLOW_INSECURE_HOST_KEYS=true` in the process environment and `strictHostKeyChecking: false` in the target configuration.

For a temporary compatibility/emergency mode only, `SSH_MCP_AUTO_ACCEPT_HOST_KEYS=true` accepts new and changed host keys for every configured target, even when strict checking is enabled. This disables MITM protection; keep it unset in normal or production use.

## Command line

```text
ssh-mcp-server [--config <path>] [--validate [--connect]] [--version]
```

- `--config <path>` — the configuration file. Without it the server uses `-Dssh.mcp.config`, then `SSH_MCP_CONFIG`, then the first existing `./config/servers.json`, `servers.json` or `config/servers.json` next to the jar, `../config/servers.json` from the jar (packaged layouts), or `<user config dir>/ssh-mcp/servers.json`.
- `--validate` — checks credentials, key files, and `known_hosts` entries for every server, prints a report, and exits non-zero if anything is missing. Add `--connect` to also log in. See the [Go guide](../go/README.md#4-validate-the-setup) for sample output; both implementations print the same report.
- `--version` — prints the version from the jar manifest.

## MCP client configuration

Point the client at the launcher or the JAR, using absolute paths:

```json
{
  "mcpServers": {
    "ssh": {
      "command": "C:\\path\\to\\dist\\bin\\ssh-mcp-server.cmd",
      "args": ["--config", "C:\\path\\to\\config\\servers.json"],
      "env": {
        "SSH_MCP_STAGING_PASSWORD": "injected-by-the-client-secret-store"
      }
    }
  }
}
```

On Unix, use `dist/bin/ssh-mcp-server`, or `java -jar /path/to/ssh-mcp-server.jar --config /path/to/servers.json`.

## Tools and behavior

The tool set, argument names, and result shapes are identical to the Go implementation. See [skills/ssh-mcp/references/tools.md](../skills/ssh-mcp/references/tools.md). Tool calls return the result JSON as text and as `structuredContent`. Errors carry `{"errorCode", "message"}` with `isError: true`.

- **Concurrency.** Tool calls run on a bounded worker pool (64 at most), so a long `ssh_exec` never blocks `ping` or calls to other servers. Per-server limits come from `maxConcurrentOperations` and `maxConcurrentTunnels`, which are separate pools. `notifications/cancelled` interrupts the matching call.
- **Timeouts.** A command that exceeds `timeoutMs` returns `timedOut: true` with whatever output was captured; it is not an error. Connection time is not counted against the command timeout.
- **Tunnels** send SSH heartbeats and are closed automatically when the connection drops.
- **Keyboard-interactive.** `passwordEnv` also answers a lone, hidden, password-style keyboard-interactive prompt (Apache MINA SSHD's heuristic), so it works against servers that only advertise `keyboard-interactive`.
- **stdout is reserved for MCP.** `System.out` is redirected to stderr at startup, so library output can never corrupt the protocol stream. Messages are framed with `\n` on every platform.

`ssh_exec` is intentionally powerful. The remote account's permissions remain the primary security boundary. For production, use a least-privilege account and set `allowedCommandPrefixes` or `deniedCommandRegexes` for each target.

## Executable packaging without Java installed

There are two executable formats:

1. The recommended `jpackage` app image is self-contained and includes a JDK 11 runtime. It produces an `.exe` on Windows and does not require Java on the target machine. It is a small directory containing the executable, runtime, and application files.
2. GraalVM Native Image can produce a true native single executable. Native Image builds are platform-specific: build the Windows executable on Windows and the Linux executable on Linux. The target machine still does not need a JDK or JRE.

### Self-contained Windows executable

JDK 11 does not contain `jpackage`, so the packaging script builds the application with JDK 11, creates a JDK 11 `jlink` runtime, and uses any available JDK 14+ `jpackage` tool to create the app image:

Windows PowerShell:

```powershell
$env:JAVA_HOME = 'C:\Program Files\Eclipse Adoptium\jdk-11.0.31.11-hotspot'
powershell -ExecutionPolicy Bypass -File .\scripts\package-windows.ps1
```

Linux/macOS:

```bash
export JAVA_HOME=/path/to/jdk-11
chmod +x scripts/package-unix.sh
scripts/package-unix.sh
```

The portable distribution contains `dist/runtime`, the application JAR, and an executable launcher under `dist/bin`. If a JDK 14+ `jpackage` tool is available while packaging, the scripts additionally create an OS app image under `dist/native`; on Windows that includes an `.exe` launcher. The application itself is still compiled for and runs on Java 11 because the app image uses the JDK 11 runtime image created by `jlink`.

The generated Windows executable is:

```text
dist/native/ssh-mcp-server/ssh-mcp-server.exe
```

Copy the complete `dist/native/ssh-mcp-server` directory to the target machine. No JDK or JRE installation is required.

### True single-file GraalVM executable

The supplied GraalVM installation can be used as the build tool:

```powershell
$env:GRAALVM_HOME = 'C:\Program Files\Java\graalvm-community-openjdk-25.0.2'
powershell -ExecutionPolicy Bypass -File .\scripts\build-native-windows.ps1
```

This writes:

```text
dist/native-image/ssh-mcp-server.exe
```

Windows Native Image compilation also requires Visual Studio 2022 Build Tools with MSVC and the Windows SDK. TDM-GCC/MinGW cannot replace that Windows linker toolchain for GraalVM Native Image. Run the script from an x64 Native Tools Command Prompt. GraalVM documents this as a build-time requirement; it is not a Java/JRE requirement for the resulting executable.

For Linux/macOS, use the corresponding GraalVM installation on that operating system:

```bash
export GRAALVM_HOME=/path/to/graalvm-community-openjdk
chmod +x scripts/build-native-unix.sh
scripts/build-native-unix.sh
```

Native executables are OS/architecture-specific; one Windows `.exe` cannot run on Linux, and one Linux binary cannot run on Windows. Build one artifact per deployment platform.

## Security and operations checklist

- Keep `servers.json`, private keys, and process environment permissions restricted to the service account.
- Verify every known-hosts fingerprint out of band; do not accept unknown keys automatically.
- Use dedicated least-privilege remote accounts.
- Keep `allowPortForwarding` false unless it is specifically needed.
- Keep output and timeout limits appropriate for the target.
- Route stderr to a protected service log. Audit messages contain target ids, operation names, durations, and a command hash, never command text or credentials.
- Review tool calls because the server can perform destructive remote actions when the configured remote account permits them.
