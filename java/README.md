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

Copy `config/servers.example.json` to `config/servers.json` from the repository root. Each entry has a unique `id`; the model sends that id to every SSH tool, so one server process can safely manage multiple targets.

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

## MCP client configuration

Point the client at the launcher or the JAR. Example shape for a client that supports MCP server configuration:

```json
{
  "mcpServers": {
    "ssh": {
      "command": "C:\\path\\to\\dist\\bin\\ssh-mcp-server.cmd",
      "env": {
        "SSH_MCP_CONFIG": "C:\\path\\to\\config\\servers.json",
        "SSH_MCP_STAGING_PASSWORD": "injected-by-the-client-secret-store"
      }
    }
  }
}
```

On Unix, use `dist/bin/ssh-mcp-server`. You can also use `java -jar target/ssh-mcp-server.jar`; the configuration path defaults to `config/servers.json` and can be overridden with `SSH_MCP_CONFIG` or `-Dssh.mcp.config=...`.

## Tools

- `ssh_list_servers` — non-secret target metadata and capabilities.
- `ssh_exec` — bounded remote command execution with timeout, output cap, audit hash, and optional allow/deny command policy.
- `sftp_list`, `sftp_stat`, `sftp_read_file`, `sftp_write_file`, `sftp_mkdir`, `sftp_delete` — remote file operations. Reads have explicit byte limits; writes accept UTF-8 or base64 data.
- `ssh_tunnel_open`, `ssh_tunnel_close`, `ssh_tunnel_list` — optional local TCP forwarding. Forwarding is disabled by default, and binds are loopback-only.

`ssh_exec` is intentionally powerful. The remote account’s Unix/Windows permissions remain the primary security boundary. For production, use a least-privilege account and set `allowedCommandPrefixes` or `deniedCommandRegexes` for each target. SFTP and forwarding can be disabled independently.

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
