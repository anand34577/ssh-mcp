# Tool reference

All tools take JSON arguments; unknown arguments are rejected. Optional numeric limits accept `0` or omission to mean "use the server default". Results below are the structured payloads (the Go server also returns them as `structuredContent`; the Java server returns the same JSON as text and `structuredContent`).

Errors come back as tool errors (`isError: true`) with a human-readable message. The Java server's error payload is `{"errorCode": "...", "message": "..."}`, where `errorCode` is one of `invalid_arguments`, `operation_rejected`, `ssh_operation_failed`, `cancelled`, or `unknown_tool`.

## ssh_list_servers

Arguments: none.

```json
{
  "servers": [{
    "id": "staging", "host": "staging.example.com", "port": 22, "username": "ops",
    "configuredAuthMethods": ["password"],
    "strictHostKeyChecking": true, "effectiveHostKeyPolicy": "strict",
    "sftpEnabled": true, "portForwardingEnabled": false,
    "allowSftp": true, "allowPortForwarding": false,
    "commandTimeoutMs": 60000, "maxOutputBytes": 1048576,
    "maxConcurrentOperations": 4, "maxConcurrentTunnels": 4,
    "allowedCommandPrefixes": [], "deniedCommandRuleCount": 1
  }],
  "secretsExposedToModel": false
}
```

`effectiveHostKeyPolicy` is `strict`, `disabled`, or `auto-accept`.

## ssh_exec

| Argument | Required | Notes |
|---|---|---|
| `serverId` | yes | |
| `command` | yes | ≤ 32768 bytes; run by the login shell without a TTY |
| `timeoutMs` | no | 100 … `commandTimeoutMs` (default). Connection time is not counted |
| `maxOutputBytes` | no | 1024 … server `maxOutputBytes` (default), per stream |

```json
{
  "serverId": "staging", "stdout": "…", "stderr": "",
  "exitCode": 0, "exitSignal": "", "timedOut": false,
  "stdoutTruncated": false, "stderrTruncated": false, "durationMs": 42
}
```

`exitCode` is `null` when the command was killed by a signal (`exitSignal` set, e.g. `"KILL"`) or timed out.

## sftp_list

Arguments: `serverId`, `path`, optional `maxEntries` (1–10000, default 1000).

```json
{
  "serverId": "staging", "path": "/var/log", "totalEntries": 2, "truncated": false,
  "entries": [{"name": "syslog", "type": "file", "size": 1234, "permissions": "640", "modified": "2026-10-19T02:00:00Z"}]
}
```

`type` is `file`, `directory`, `symlink`, or `other`. Entries exclude `.` and `..` and are sorted by name.

## sftp_stat

Arguments: `serverId`, `path`. Returns the same fields as a list entry plus `serverId` and `path`. Follows symlinks.

## sftp_read_file

| Argument | Required | Notes |
|---|---|---|
| `serverId`, `path` | yes | |
| `offset` | no | Byte offset (default 0) |
| `maxBytes` | no | 1 … server `maxOutputBytes` (default) |
| `encoding` | no | `utf8` (default) or `base64` |

```json
{
  "serverId": "staging", "path": "/etc/hosts", "size": 220, "offset": 0,
  "bytes": 220, "nextOffset": 220, "truncated": false, "encoding": "utf8",
  "content": "127.0.0.1 localhost\n…", "validUtf8": true
}
```

Paging: while `truncated` is true, call again with `offset = nextOffset`. In `utf8` mode a chunk never ends in the middle of a multi-byte character. For binary files `contentBase64` replaces `content`/`validUtf8`. Reading a directory is an error.

## sftp_write_file

Arguments: `serverId`, `path`, and exactly one of `content` (UTF-8 text) or `contentBase64` (standard base64; whitespace and missing padding are tolerated). Creates or truncates the file; the parent directory must exist. Size ≤ server `maxOutputBytes`.

```json
{"serverId": "staging", "path": "/tmp/app.conf", "bytesWritten": 512}
```

## sftp_mkdir

Arguments: `serverId`, `path`, optional `parents` (create missing parents; succeed if it already exists). Returns `{"serverId", "path", "created": true}`.

## sftp_delete

Arguments: `serverId`, `path`, optional `directory` (true to remove an empty directory). Returns `{"serverId", "path", "deleted": true}`.

## sftp_rename

Arguments: `serverId`, `from`, `to`, optional `overwrite`. Without `overwrite`, an existing target makes the call fail. With `overwrite: true`, the OpenSSH `posix-rename@openssh.com` extension replaces the target atomically (error if unsupported).

```json
{"serverId": "staging", "from": "/tmp/a", "to": "/tmp/b", "renamed": true}
```

## ssh_tunnel_open

| Argument | Required | Notes |
|---|---|---|
| `serverId` | yes | Server must have `portForwardingEnabled` |
| `remoteHost`, `remotePort` | yes | Destination as reached **from the SSH server** |
| `localHost` | no | `127.0.0.1` (default), `::1`, or `localhost` only |
| `localPort` | no | 0 (default) picks a free port |

```json
{
  "tunnelId": "tunnel-1", "serverId": "staging",
  "localHost": "127.0.0.1", "localPort": 54012,
  "remoteHost": "127.0.0.1", "remotePort": 5432,
  "openedAt": "2026-10-19T02:00:00Z", "open": true,
  "warning": "The tunnel stays open until ssh_tunnel_close, the SSH connection drops, or the MCP server exits"
}
```

## ssh_tunnel_list

Arguments: none. Returns `{"tunnels": [ …same fields as ssh_tunnel_open minus warning… ]}`.

## ssh_tunnel_close

Arguments: `tunnelId`. Returns `{"tunnelId": "tunnel-1", "closed": true}`.
