# Contributing

Thanks for helping improve SSH MCP Server.

## Before opening a pull request

Run the relevant checks locally:

```bash
cd go
go test ./...
go vet ./...
```

If you change the installers, also run `shellcheck install.sh`.

For Java changes:

```bash
cd java
mvn test
```

Both suites include end-to-end tests against an in-process SSH server (exec, SFTP, tunnels, host-key checks), so no external host is needed.

Keep changes focused, document user-visible behavior, and update the usage guide when configuration or tool behavior changes.

## Keep the two implementations in parity

The Go and Java servers must stay interchangeable for MCP clients. When you change a tool, argument, result field, error behavior, or configuration field, change both implementations in the same pull request:

- Tool names, descriptions, and argument schemas: `go/internal/mcpserver/server.go` and `java/.../ToolSpecs.java`.
- Configuration validation: `go/internal/config/config.go` and `java/.../Config.java`, plus `config/servers.schema.json`.
- System protection rules (`protectSystem`): `go/internal/sshservice/guard.go` and `java/.../SystemGuard.java`. Their tests share the same cases (`guard_test.go`, `SystemGuardTest.java`); add new cases to both.
- Documentation: the root README's configuration and tool tables, and `skills/ssh-mcp/references/tools.md`.

## Privacy promise

SSH MCP has no analytics, telemetry, crash reporting, or background network calls, and it must stay that way. The program may only connect to the user's own SSH servers, and to the release download location when the user explicitly runs the installer or `update`. Pull requests that add any other network traffic will not be accepted.

## Docs, wiki, and artwork

- User-facing guides live in `wiki/` and are published to the GitHub wiki automatically when merged to `main`. Write them in plain, friendly language for non-experts.
- Images live in `assets/`. They're hand-written SVG; run `assets/tools/render.sh` after editing to refresh the PNG versions.

## Security-sensitive changes

Do not add credentials, private keys, private infrastructure hostnames, or real `known_hosts` data to commits, tests, logs, or examples. New SSH functionality must preserve strict host-key verification, bounded work, and the rule that secret values never enter MCP tool arguments or results.

## Pull requests

Include:

- What changed and why.
- Tests run and their results.
- Any security or compatibility implications.
- Documentation updates for new tools, flags, or configuration fields.
