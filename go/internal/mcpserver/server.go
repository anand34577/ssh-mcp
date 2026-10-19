// Package mcpserver registers the SSH tools with the MCP Go SDK and serves
// them over stdio.
package mcpserver

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"

	"example.com/ssh-mcp-server/internal/sshservice"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// maxMessageBytes bounds a single JSON-RPC line read from stdin.
const maxMessageBytes = 128 << 20

// Instructions are sent to the client at initialization and shown to the
// model; keep them short, concrete, and in sync with the Java implementation.
const Instructions = `SSH access to pre-configured servers. Credentials, host keys, and command policy are managed by the server operator and are never visible to you.
Workflow: call ssh_list_servers first and use its "id" as serverId. Check each server's sftpEnabled, portForwardingEnabled, allowedCommandPrefixes, commandTimeoutMs, and maxOutputBytes before acting.
Prefer sftp_* tools over shell commands for reading and writing files. ssh_exec runs a non-interactive command with no TTY: avoid commands that prompt (use flags like -y), page output, or run forever. A non-zero exitCode is a normal result, not a tool failure; timedOut=true means the command was killed.
When stdoutTruncated/stderrTruncated or sftp_read_file truncated is true, narrow the command or page with offset=nextOffset instead of retrying unchanged.
Close every tunnel you open with ssh_tunnel_close. Confirm with the user before destructive actions (deleting files, restarting services, overwriting configuration). If a call fails with "blocked by system protection" or "not allowed by the server command policy", stop and tell the user; never rephrase, encode, or split the command to get around it.`

type ExecInput struct {
	ServerID       string `json:"serverId" jsonschema:"Server id from ssh_list_servers"`
	Command        string `json:"command" jsonschema:"Command line run by the remote user's login shell without a TTY (max 32768 bytes)"`
	TimeoutMs      int64  `json:"timeoutMs,omitempty" jsonschema:"Optional timeout in milliseconds, 100 up to the server's commandTimeoutMs (the default). Connection time is not counted"`
	MaxOutputBytes int    `json:"maxOutputBytes,omitempty" jsonschema:"Optional cap on captured bytes per stream, 1024 up to the server's maxOutputBytes (the default)"`
}

type SFTPListInput struct {
	ServerID   string `json:"serverId" jsonschema:"Server id from ssh_list_servers"`
	Path       string `json:"path" jsonschema:"Remote directory path; relative paths start in the login directory"`
	MaxEntries int    `json:"maxEntries,omitempty" jsonschema:"Maximum entries to return, 1-10000 (default 1000); entries are sorted by name"`
}

type SFTPStatInput struct {
	ServerID string `json:"serverId" jsonschema:"Server id from ssh_list_servers"`
	Path     string `json:"path" jsonschema:"Remote file or directory path"`
}

type SFTPReadInput struct {
	ServerID string `json:"serverId" jsonschema:"Server id from ssh_list_servers"`
	Path     string `json:"path" jsonschema:"Remote file path"`
	Offset   int64  `json:"offset,omitempty" jsonschema:"Byte offset to start reading from (default 0); pass the previous nextOffset to page through a large file"`
	MaxBytes int    `json:"maxBytes,omitempty" jsonschema:"Maximum bytes to return, 1 up to the server's maxOutputBytes (the default)"`
	Encoding string `json:"encoding,omitempty" jsonschema:"utf8 (default, for text) or base64 (for binary files)"`
}

type SFTPWriteInput struct {
	ServerID      string  `json:"serverId" jsonschema:"Server id from ssh_list_servers"`
	Path          string  `json:"path" jsonschema:"Remote file path; the file is created or fully replaced and the parent directory must exist"`
	Content       *string `json:"content,omitempty" jsonschema:"Text content written as UTF-8; provide exactly one of content or contentBase64"`
	ContentBase64 *string `json:"contentBase64,omitempty" jsonschema:"Binary content as standard base64; provide exactly one of content or contentBase64"`
}

type SFTPMkdirInput struct {
	ServerID string `json:"serverId" jsonschema:"Server id from ssh_list_servers"`
	Path     string `json:"path" jsonschema:"Remote directory path to create"`
	Parents  bool   `json:"parents,omitempty" jsonschema:"Create missing parent directories and succeed if the directory already exists (like mkdir -p)"`
}

type SFTPDeleteInput struct {
	ServerID  string `json:"serverId" jsonschema:"Server id from ssh_list_servers"`
	Path      string `json:"path" jsonschema:"Remote path to delete"`
	Directory bool   `json:"directory,omitempty" jsonschema:"Set true to remove an empty directory; non-empty directories are never removed"`
}

type SFTPRenameInput struct {
	ServerID  string `json:"serverId" jsonschema:"Server id from ssh_list_servers"`
	From      string `json:"from" jsonschema:"Existing remote path"`
	To        string `json:"to" jsonschema:"New remote path"`
	Overwrite bool   `json:"overwrite,omitempty" jsonschema:"Atomically replace an existing target (requires the OpenSSH posix-rename extension); default false fails if the target exists"`
}

type TunnelOpenInput struct {
	ServerID   string `json:"serverId" jsonschema:"Server id from ssh_list_servers"`
	LocalHost  string `json:"localHost,omitempty" jsonschema:"Loopback address to listen on: 127.0.0.1 (default), ::1, or localhost"`
	LocalPort  int    `json:"localPort,omitempty" jsonschema:"Local port, or 0 (default) to pick a free port"`
	RemoteHost string `json:"remoteHost" jsonschema:"Destination host as seen from the SSH server, for example 127.0.0.1 for a service on the server itself"`
	RemotePort int    `json:"remotePort" jsonschema:"Destination TCP port, 1-65535"`
}

type TunnelCloseInput struct {
	TunnelID string `json:"tunnelId" jsonschema:"Tunnel id returned by ssh_tunnel_open or ssh_tunnel_list"`
}

// New builds the MCP server with every SSH tool registered.
func New(service *sshservice.Service, version string) *mcp.Server {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	server := mcp.NewServer(
		&mcp.Implementation{Name: "ssh-mcp-server", Title: "SSH MCP Server", Version: version},
		&mcp.ServerOptions{Instructions: Instructions, Logger: logger},
	)

	addTool(server, "ssh_list_servers", "List configured SSH servers with their ids, capabilities, limits, and command policy. Call this first; it never returns secrets.",
		readOnly("List SSH servers", false),
		func(_ context.Context, _ struct{}) (map[string]any, error) {
			return map[string]any{"servers": service.ListServers(), "secretsExposedToModel": false}, nil
		})
	addTool(server, "ssh_exec", "Run a non-interactive command on a server and return stdout, stderr, exitCode, exitSignal, timedOut, and truncation flags. Output is capped; a non-zero exit code is returned as a normal result.",
		mutating("Run SSH command", true, false),
		func(ctx context.Context, input ExecInput) (map[string]any, error) {
			return service.Exec(ctx, sshservice.ExecRequest{ServerID: input.ServerID, Command: input.Command, TimeoutMs: input.TimeoutMs, MaxOutputBytes: input.MaxOutputBytes})
		})
	addTool(server, "sftp_list", "List a remote directory (name, type, size, permissions, modified time), sorted by name.",
		readOnly("List remote directory", true),
		func(ctx context.Context, input SFTPListInput) (map[string]any, error) {
			return service.SFTPList(ctx, sshservice.SFTPListRequest{ServerID: input.ServerID, Path: input.Path, MaxEntries: input.MaxEntries})
		})
	addTool(server, "sftp_stat", "Get metadata for a remote file or directory (type, size, permissions, modified time); follows symlinks.",
		readOnly("Stat remote path", true),
		func(ctx context.Context, input SFTPStatInput) (map[string]any, error) {
			return service.SFTPStat(ctx, sshservice.SFTPStatRequest{ServerID: input.ServerID, Path: input.Path})
		})
	addTool(server, "sftp_read_file", "Read a remote file as UTF-8 text or base64, up to maxBytes from offset. Returns size, nextOffset, and truncated so large files can be paged.",
		readOnly("Read remote file", true),
		func(ctx context.Context, input SFTPReadInput) (map[string]any, error) {
			return service.SFTPRead(ctx, sshservice.SFTPReadRequest{ServerID: input.ServerID, Path: input.Path, Offset: input.Offset, MaxBytes: input.MaxBytes, Encoding: input.Encoding})
		})
	addTool(server, "sftp_write_file", "Create or overwrite a remote file with UTF-8 content or base64 contentBase64 (exactly one). Size is capped at the server's maxOutputBytes.",
		mutating("Write remote file", true, true),
		func(ctx context.Context, input SFTPWriteInput) (map[string]any, error) {
			return service.SFTPWrite(ctx, sshservice.SFTPWriteRequest{ServerID: input.ServerID, Path: input.Path, Content: input.Content, ContentBase64: input.ContentBase64})
		})
	addTool(server, "sftp_mkdir", "Create a remote directory; set parents=true to create missing parents (like mkdir -p).",
		mutating("Create remote directory", false, false),
		func(ctx context.Context, input SFTPMkdirInput) (map[string]any, error) {
			return service.SFTPMkdir(ctx, sshservice.SFTPMkdirRequest{ServerID: input.ServerID, Path: input.Path, Parents: input.Parents})
		})
	addTool(server, "sftp_delete", "Delete one remote file, or an empty directory when directory=true. Not recursive.",
		mutating("Delete remote path", true, true),
		func(ctx context.Context, input SFTPDeleteInput) (map[string]any, error) {
			return service.SFTPDelete(ctx, sshservice.SFTPDeleteRequest{ServerID: input.ServerID, Path: input.Path, Directory: input.Directory})
		})
	addTool(server, "sftp_rename", "Rename or move a remote file or directory. Fails if the target exists unless overwrite=true.",
		mutating("Rename remote path", true, false),
		func(ctx context.Context, input SFTPRenameInput) (map[string]any, error) {
			return service.SFTPRename(ctx, sshservice.SFTPRenameRequest{ServerID: input.ServerID, From: input.From, To: input.To, Overwrite: input.Overwrite})
		})
	addTool(server, "ssh_tunnel_open", "Open a local port forward: a loopback port on the MCP host relays to remoteHost:remotePort as reached from the SSH server. Requires portForwardingEnabled. Close it with ssh_tunnel_close when done.",
		mutating("Open SSH tunnel", false, false),
		func(ctx context.Context, input TunnelOpenInput) (map[string]any, error) {
			return service.OpenTunnel(ctx, sshservice.TunnelOpenRequest{ServerID: input.ServerID, LocalHost: input.LocalHost, LocalPort: input.LocalPort, RemoteHost: input.RemoteHost, RemotePort: input.RemotePort})
		})
	addTool(server, "ssh_tunnel_close", "Close an open tunnel by tunnelId.",
		mutating("Close SSH tunnel", false, true),
		func(ctx context.Context, input TunnelCloseInput) (map[string]any, error) {
			return service.CloseTunnel(ctx, input.TunnelID)
		})
	addTool(server, "ssh_tunnel_list", "List open tunnels with their local and remote endpoints.",
		readOnly("List SSH tunnels", false),
		func(_ context.Context, _ struct{}) (map[string]any, error) {
			return map[string]any{"tunnels": service.ListTunnels()}, nil
		})

	return server
}

// Run serves MCP over stdin/stdout until the client disconnects or ctx ends.
func Run(ctx context.Context, service *sshservice.Service, version string) error {
	reader := &boundedLineReader{reader: bufio.NewReaderSize(os.Stdin, 64*1024), maxBytes: maxMessageBytes}
	transport := &mcp.IOTransport{Reader: reader, Writer: nopCloserWriter{Writer: os.Stdout}}
	return New(service, version).Run(ctx, transport)
}

func readOnly(title string, openWorld bool) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{Title: title, ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &openWorld}
}

func mutating(title string, destructive, idempotent bool) *mcp.ToolAnnotations {
	openWorld := true
	return &mcp.ToolAnnotations{Title: title, DestructiveHint: &destructive, IdempotentHint: idempotent, OpenWorldHint: &openWorld}
}

func addTool[In any](server *mcp.Server, name, description string, annotations *mcp.ToolAnnotations, handler func(context.Context, In) (map[string]any, error)) {
	tool := &mcp.Tool{Name: name, Title: annotations.Title, Description: description, Annotations: annotations}
	mcp.AddTool(server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, input In) (*mcp.CallToolResult, map[string]any, error) {
		output, err := handler(ctx, input)
		return nil, output, err
	})
}

type nopCloserWriter struct {
	io.Writer
}

func (nopCloserWriter) Close() error { return nil }

// boundedLineReader feeds newline-delimited JSON-RPC messages to the SDK and
// fails instead of buffering a single message larger than maxBytes.
type boundedLineReader struct {
	reader   *bufio.Reader
	maxBytes int
	pending  []byte
	readErr  error
}

func (r *boundedLineReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(r.pending) == 0 && r.readErr == nil {
		r.pending, r.readErr = r.readLine()
	}
	if len(r.pending) > 0 {
		n := copy(p, r.pending)
		r.pending = r.pending[n:]
		if len(r.pending) == 0 && r.readErr != nil {
			return n, r.takeErr()
		}
		return n, nil
	}
	if r.readErr != nil {
		return 0, r.takeErr()
	}
	return 0, io.EOF
}

func (r *boundedLineReader) takeErr() error {
	err := r.readErr
	r.readErr = nil
	return err
}

func (r *boundedLineReader) readLine() ([]byte, error) {
	var line []byte
	for {
		fragment, err := r.reader.ReadSlice('\n')
		if len(line)+len(fragment) > r.maxBytes {
			return nil, errors.New("MCP message exceeds the maximum allowed size")
		}
		line = append(line, fragment...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return line, err
	}
}

func (r *boundedLineReader) Close() error { return nil }
