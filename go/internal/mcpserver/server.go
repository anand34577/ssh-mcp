package mcpserver

import (
	"context"
	"log/slog"
	"os"

	"example.com/ssh-mcp-server/internal/sshservice"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ServerIDInput struct {
	ServerID string `json:"serverId"`
}

type ExecInput struct {
	ServerID       string `json:"serverId"`
	Command        string `json:"command"`
	TimeoutMs      int64  `json:"timeoutMs,omitempty"`
	MaxOutputBytes int    `json:"maxOutputBytes,omitempty"`
}

type SFTPListInput struct {
	ServerID   string `json:"serverId"`
	Path       string `json:"path"`
	MaxEntries int    `json:"maxEntries,omitempty"`
}

type SFTPStatInput struct {
	ServerID string `json:"serverId"`
	Path     string `json:"path"`
}

type SFTPReadInput struct {
	ServerID string `json:"serverId"`
	Path     string `json:"path"`
	MaxBytes int    `json:"maxBytes,omitempty"`
	Encoding string `json:"encoding,omitempty"`
}

type SFTPWriteInput struct {
	ServerID      string  `json:"serverId"`
	Path          string  `json:"path"`
	Content       *string `json:"content,omitempty"`
	ContentBase64 *string `json:"contentBase64,omitempty"`
}

type SFTPMkdirInput struct {
	ServerID string `json:"serverId"`
	Path     string `json:"path"`
}

type SFTPDeleteInput struct {
	ServerID  string `json:"serverId"`
	Path      string `json:"path"`
	Directory bool   `json:"directory,omitempty"`
}

type TunnelOpenInput struct {
	ServerID   string `json:"serverId"`
	LocalHost  string `json:"localHost,omitempty"`
	LocalPort  int    `json:"localPort,omitempty"`
	RemoteHost string `json:"remoteHost"`
	RemotePort int    `json:"remotePort"`
}

type TunnelCloseInput struct {
	TunnelID string `json:"tunnelId"`
}

func New(service *sshservice.Service) *mcp.Server {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	server := mcp.NewServer(
		&mcp.Implementation{Name: "ssh-mcp-server-go", Version: "1.0.0"},
		&mcp.ServerOptions{
			Instructions: "Use the configured SSH server IDs. Credentials are loaded by the server process and are never returned by tools.",
			Logger:       logger,
		},
	)

	addTool(server, "ssh_list_servers", "List configured SSH targets and their enabled capabilities.", func(_ context.Context, _ struct{}) (map[string]any, error) {
		return map[string]any{"servers": service.ListServers()}, nil
	})
	addTool(server, "ssh_exec", "Execute a command on a configured SSH target with bounded output and timeout.", func(ctx context.Context, input ExecInput) (map[string]any, error) {
		return service.Exec(ctx, sshservice.ExecRequest{ServerID: input.ServerID, Command: input.Command, TimeoutMs: input.TimeoutMs, MaxOutputBytes: input.MaxOutputBytes})
	})
	addTool(server, "sftp_list", "List a remote directory through SFTP.", func(ctx context.Context, input SFTPListInput) (map[string]any, error) {
		return service.SFTPList(ctx, sshservice.SFTPListRequest{ServerID: input.ServerID, Path: input.Path, MaxEntries: input.MaxEntries})
	})
	addTool(server, "sftp_stat", "Read remote file or directory metadata through SFTP.", func(ctx context.Context, input SFTPStatInput) (map[string]any, error) {
		return service.SFTPStat(ctx, sshservice.SFTPStatRequest{ServerID: input.ServerID, Path: input.Path})
	})
	addTool(server, "sftp_read_file", "Read a remote file with an explicit byte limit as UTF-8 or base64.", func(ctx context.Context, input SFTPReadInput) (map[string]any, error) {
		return service.SFTPRead(ctx, sshservice.SFTPReadRequest{ServerID: input.ServerID, Path: input.Path, MaxBytes: input.MaxBytes, Encoding: input.Encoding})
	})
	addTool(server, "sftp_write_file", "Write bounded UTF-8 or base64 content to a remote file through SFTP.", func(ctx context.Context, input SFTPWriteInput) (map[string]any, error) {
		return service.SFTPWrite(ctx, sshservice.SFTPWriteRequest{ServerID: input.ServerID, Path: input.Path, Content: input.Content, ContentBase64: input.ContentBase64})
	})
	addTool(server, "sftp_mkdir", "Create a remote directory through SFTP.", func(ctx context.Context, input SFTPMkdirInput) (map[string]any, error) {
		return service.SFTPMkdir(ctx, sshservice.SFTPMkdirRequest{ServerID: input.ServerID, Path: input.Path})
	})
	addTool(server, "sftp_delete", "Delete a remote file or empty directory through SFTP.", func(ctx context.Context, input SFTPDeleteInput) (map[string]any, error) {
		return service.SFTPDelete(ctx, sshservice.SFTPDeleteRequest{ServerID: input.ServerID, Path: input.Path, Directory: input.Directory})
	})
	addTool(server, "ssh_tunnel_open", "Open a loopback-only local TCP forward through SSH.", func(ctx context.Context, input TunnelOpenInput) (map[string]any, error) {
		return service.OpenTunnel(ctx, sshservice.TunnelOpenRequest{ServerID: input.ServerID, LocalHost: input.LocalHost, LocalPort: input.LocalPort, RemoteHost: input.RemoteHost, RemotePort: input.RemotePort})
	})
	addTool(server, "ssh_tunnel_close", "Close an SSH local port forward.", func(ctx context.Context, input TunnelCloseInput) (map[string]any, error) {
		return service.CloseTunnel(ctx, input.TunnelID)
	})
	addTool(server, "ssh_tunnel_list", "List active SSH tunnels without credentials.", func(_ context.Context, _ struct{}) (map[string]any, error) {
		return map[string]any{"tunnels": service.ListTunnels()}, nil
	})

	return server
}

func Run(ctx context.Context, service *sshservice.Service) error {
	return New(service).Run(ctx, &mcp.StdioTransport{})
}

func addTool[In any, Out any](server *mcp.Server, name, description string, handler func(context.Context, In) (Out, error)) {
	mcp.AddTool(server, &mcp.Tool{Name: name, Description: description}, func(ctx context.Context, _ *mcp.CallToolRequest, input In) (*mcp.CallToolResult, Out, error) {
		output, err := handler(ctx, input)
		return nil, output, err
	})
}
