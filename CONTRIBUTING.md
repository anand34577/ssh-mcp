# Contributing

Thanks for helping improve SSH MCP Server.

## Before opening a pull request

Run the relevant checks locally:

```bash
cd go
go test ./...
go vet ./...
```

For Java changes:

```bash
cd java
mvn test
```

Keep changes focused, document user-visible behavior, and update the usage guide when configuration or tool behavior changes.

## Security-sensitive changes

Do not add credentials, private keys, private infrastructure hostnames, or real `known_hosts` data to commits, tests, logs, or examples. New SSH functionality must preserve strict host-key verification, bounded work, and the rule that secret values never enter MCP tool arguments or results.

## Pull requests

Include:

- What changed and why.
- Tests run and their results.
- Any security or compatibility implications.
- Documentation updates for new tools, flags, or configuration fields.
