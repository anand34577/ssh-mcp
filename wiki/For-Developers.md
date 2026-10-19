# For Developers

Everything you need to build, test, release, or contribute.

## Project layout

| Folder | What's inside |
|---|---|
| `go/` | The main edition (Go): one self-contained program for every platform |
| `java/` | The Java 11 edition, with the same tools and settings |
| `config/` | Example settings file and its JSON Schema |
| `skills/ssh-mcp/` | Agent skill that teaches AI models to use the tools well |
| `wiki/` | These pages, published to the GitHub wiki automatically |
| `assets/` | Logo, banners, social images, and the scripts that render them |
| `install.sh`, `install.ps1` | One-line installers |

## Build and test

Go 1.26 or newer:

```bash
cd go
go test ./...          # includes end-to-end tests against a built-in SSH server
go build ./cmd/ssh-mcp-server
```

Java 11 or newer, with Maven:

```bash
cd java
mvn verify
```

No real server is needed for the tests.

## Commands

```text
ssh-mcp-server setup [-config file]        guided setup
ssh-mcp-server connect [-app a,b]          connect AI apps, no questions
ssh-mcp-server update                      self-update (checksum verified)
ssh-mcp-server uninstall [-purge] [-yes]   remove from apps and delete the program
ssh-mcp-server -validate [-connect]        check settings (and sign in)
ssh-mcp-server -config file                run as an MCP server over stdin/stdout
ssh-mcp-server version | help
```

Running the program with no arguments in a terminal (or double-clicking it) opens the setup. When an AI app starts it, stdin is a pipe, so it runs as an MCP server.

## Releases

Releases are fully automatic and built in public by GitHub Actions:

1. Set the version in `java/pom.xml` (for example `1.2.0`) and merge to `main`.
2. Either push a tag (`git tag v1.2.0 && git push origin v1.2.0`), or open **Actions > Release > Run workflow** and type `v1.2.0`.

The workflow tests everything, builds the programs for Windows, macOS, and Linux (Intel/AMD and ARM), adds the Windows icon and version details, builds the Java editions, attaches the installers and a `SHA256SUMS.txt`, and publishes the release with friendly install notes. Running it without a version makes a test build you can download from the workflow run.

Every push and pull request runs the tests on Linux and Windows, builds all platforms, checks the installer scripts, and runs the Java tests on Java 11 and 21.

## The wiki

Edit the Markdown files in `wiki/` and merge to `main`. The **Publish wiki** workflow copies them to the GitHub wiki. (The very first time, create any page in the Wiki tab so the wiki exists.) Link between pages with `[Text](Page-Name)`.

## Artwork

The images in `assets/` are hand-written SVG. To regenerate the PNG versions (GitHub social preview, square post image, icons) after editing:

```bash
assets/tools/render.sh
```

It uses headless Chrome or Chromium. Upload `assets/social-preview.png` in **Settings > General > Social preview**.

## Contributing

See [CONTRIBUTING.md](https://github.com/anand34577/ssh-mcp/blob/main/CONTRIBUTING.md). The most important rules:

- Keep the Go and Java editions in step when you change a tool or setting.
- Never commit real passwords, keys, server names, or `known_hosts` data.
- No telemetry, analytics, or automatic network calls. The program only goes online for the user's own servers or an update they asked for.
