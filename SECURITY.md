# Security Policy

SSH MCP Server can execute commands and modify remote systems. Treat it as privileged infrastructure software.

## Reporting a vulnerability

Please do not open a public issue for a suspected vulnerability. Use the repository's private GitHub Security Advisory flow when available, or contact the maintainers through the private security contact configured for the repository.

Include a clear description, affected implementation and version, reproduction steps that do not contain real credentials, and a suggested mitigation if known.

Never send passwords, private keys, production hostnames, or private `known_hosts` contents in a report.

## Operational security

- Use least-privilege SSH accounts.
- Keep strict `known_hosts` verification enabled.
- Supply credentials through protected process environment or a secret manager.
- Keep port forwarding disabled unless required.
- Review command policies, output limits, and timeout limits per target.
- Protect stderr audit logs and configuration files.
