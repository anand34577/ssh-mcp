// Package sshservice implements the SSH, SFTP, and tunnel operations exposed
// as MCP tools. Every operation opens its own SSH connection, is bounded by a
// per-server concurrency slot and timeout, and is audited to stderr without
// secrets or command text.
package sshservice

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"example.com/ssh-mcp-server/internal/config"
	"golang.org/x/crypto/ssh"
)

const (
	maxCommandBytes = 32768
	// execKillGrace is how long a timed-out command gets to report its exit
	// after the session is closed before its result is returned anyway.
	execKillGrace = 2 * time.Second
)

var errServiceClosed = errors.New("SSH service is shutting down")

type Service struct {
	catalog *config.Catalog

	mu         sync.Mutex
	tunnels    map[string]*Tunnel
	nextTunnel atomic.Uint64
	closed     atomic.Bool
}

type ExecRequest struct {
	ServerID       string
	Command        string
	TimeoutMs      int64
	MaxOutputBytes int
}

func New(catalog *config.Catalog) *Service {
	return &Service{catalog: catalog, tunnels: make(map[string]*Tunnel)}
}

// ListServers returns non-secret metadata for every configured target.
func (s *Service) ListServers() []map[string]any {
	items := make([]map[string]any, 0, len(s.catalog.Servers))
	for _, server := range s.sortedServers() {
		authMethods := make([]string, 0, 2)
		if server.PrivateKeyPath != "" {
			authMethods = append(authMethods, "publicKey")
		}
		if server.PasswordEnv != "" {
			authMethods = append(authMethods, "password")
		}
		allowedPrefixes := server.AllowedCommandPrefixes
		if allowedPrefixes == nil {
			allowedPrefixes = []string{}
		}
		items = append(items, map[string]any{
			"id":                      server.ID,
			"host":                    server.Host,
			"port":                    server.Port,
			"username":                server.Username,
			"configuredAuthMethods":   authMethods,
			"strictHostKeyChecking":   server.StrictHostKeyChecking,
			"effectiveHostKeyPolicy":  effectiveHostKeyPolicy(server),
			"allowSftp":               server.AllowSFTP,
			"allowPortForwarding":     server.AllowPortForwarding,
			"sftpEnabled":             server.AllowSFTP,
			"portForwardingEnabled":   server.AllowPortForwarding,
			"commandTimeoutMs":        server.CommandTimeoutMs,
			"maxOutputBytes":          server.MaxOutputBytes,
			"maxConcurrentOperations": server.MaxConcurrentOperations,
			"maxConcurrentTunnels":    server.MaxConcurrentTunnels,
			"allowedCommandPrefixes":  allowedPrefixes,
			"deniedCommandRuleCount":  len(server.DeniedPatterns),
			"protectSystem":           server.ProtectSystem,
		})
	}
	return items
}

func (s *Service) Exec(ctx context.Context, request ExecRequest) (map[string]any, error) {
	server, err := s.server(request.ServerID)
	if err != nil {
		return nil, err
	}
	command := strings.TrimSpace(request.Command)
	if command == "" {
		return nil, errors.New("command is required")
	}
	if len(command) > maxCommandBytes {
		return nil, fmt.Errorf("command must be at most %d bytes", maxCommandBytes)
	}
	commandHash := "commandHash=" + hash(command)
	if err := checkCommandPolicy(server, command); err != nil {
		audit("ssh_exec", server, time.Now(), false, "policy_rejected "+commandHash)
		return nil, err
	}
	timeout, err := validateTimeout(request.TimeoutMs, server.CommandTimeoutMs)
	if err != nil {
		return nil, err
	}
	maxOutput, err := validateExecOutput(request.MaxOutputBytes, server.MaxOutputBytes)
	if err != nil {
		return nil, err
	}

	result, err := s.withClient(ctx, server, "ssh_exec", commandHash, timeout, func(ctx context.Context, client *ssh.Client) (map[string]any, error) {
		session, err := client.NewSession()
		if err != nil {
			return nil, err
		}
		defer session.Close()

		stdout := &limitedBuffer{limit: maxOutput}
		stderr := &limitedBuffer{limit: maxOutput}
		session.Stdout = stdout
		session.Stderr = stderr
		started := time.Now()
		if err := session.Start(command); err != nil {
			return nil, err
		}

		wait := make(chan error, 1)
		go func() { wait <- session.Wait() }()
		var waitErr error
		contextDone := false
		select {
		case waitErr = <-wait:
		case <-ctx.Done():
			contextDone = true
			_ = session.Signal(ssh.SIGKILL)
			_ = session.Close()
			select {
			case waitErr = <-wait:
			case <-time.After(execKillGrace):
				waitErr = ctx.Err()
			}
		}

		// The client is closed when the deadline passes, so Wait can also
		// return a transport error first; treat that as a timeout too.
		var exitErr *ssh.ExitError
		exited := waitErr == nil || errors.As(waitErr, &exitErr)
		timedOut := false
		if contextDone || !exited {
			switch {
			case ctx.Err() == nil:
				return nil, waitErr
			case !errors.Is(ctx.Err(), context.DeadlineExceeded):
				return nil, ctx.Err()
			}
			timedOut = true
		}

		var exitCode any
		exitSignal := ""
		switch {
		case exitErr != nil && exitErr.Signal() != "":
			exitSignal = exitErr.Signal()
		case exitErr != nil:
			exitCode = exitErr.ExitStatus()
		case waitErr == nil:
			exitCode = 0
		}

		return map[string]any{
			"serverId":        server.ID,
			"stdout":          stdout.String(),
			"stderr":          stderr.String(),
			"exitCode":        exitCode,
			"exitSignal":      exitSignal,
			"timedOut":        timedOut,
			"stdoutTruncated": stdout.Truncated(),
			"stderrTruncated": stderr.Truncated(),
			"durationMs":      time.Since(started).Milliseconds(),
		}, nil
	})
	if err != nil {
		return nil, operationError("SSH command", err)
	}
	return result, nil
}

// Close shuts the service down and closes every open tunnel. In-flight
// operations are interrupted by their own contexts.
func (s *Service) Close() {
	if !s.closed.CompareAndSwap(false, true) {
		return
	}
	s.mu.Lock()
	tunnels := make([]*Tunnel, 0, len(s.tunnels))
	for _, tunnel := range s.tunnels {
		tunnels = append(tunnels, tunnel)
	}
	s.mu.Unlock()
	for _, tunnel := range tunnels {
		tunnel.Close("service shutdown")
	}
}

// withClient acquires an operation slot, connects, and runs fn with a context
// bounded by timeout. Connection time does not count against timeout.
func (s *Service) withClient(ctx context.Context, server *config.ServerConfig, operation, detail string, timeout time.Duration, fn func(context.Context, *ssh.Client) (map[string]any, error)) (map[string]any, error) {
	if s.closed.Load() {
		return nil, errServiceClosed
	}
	started := time.Now()
	if err := acquireSlot(ctx, server); err != nil {
		audit(operation, server, started, false, "busy "+detail)
		return nil, err
	}
	defer func() { <-server.Slots }()

	client, err := s.connect(ctx, server)
	if err != nil {
		audit(operation, server, started, false, "connection_failed "+detail)
		return nil, err
	}
	defer client.Close()

	operationCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// Closing the client unblocks any SSH/SFTP call still waiting on the
	// network once the operation is cancelled or times out.
	stop := context.AfterFunc(operationCtx, func() { _ = client.Close() })
	defer stop()

	result, err := fn(operationCtx, client)
	if err != nil && operationCtx.Err() != nil {
		// The call most likely failed because the client was closed
		// underneath it; report the timeout/cancellation instead.
		err = operationCtx.Err()
	}
	audit(operation, server, started, err == nil, detail)
	return result, err
}

// acquireSlot waits for an operation slot for at most the time another
// operation could legitimately hold one.
func acquireSlot(ctx context.Context, server *config.ServerConfig) error {
	wait := time.Duration(server.ConnectTimeoutMs+server.CommandTimeoutMs) * time.Millisecond
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case server.Slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return fmt.Errorf("server %q is busy: all %d operation slots are in use; retry later or raise maxConcurrentOperations", server.ID, server.MaxConcurrentOperations)
	}
}

func (s *Service) server(id string) (*config.ServerConfig, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, errors.New("serverId is required; call ssh_list_servers to see configured ids")
	}
	server := s.catalog.Servers[id]
	if server == nil {
		return nil, fmt.Errorf("unknown serverId %q; configured ids: %s", id, strings.Join(s.serverIDs(), ", "))
	}
	return server, nil
}

func (s *Service) sortedServers() []*config.ServerConfig {
	servers := make([]*config.ServerConfig, 0, len(s.catalog.Servers))
	for _, server := range s.catalog.Servers {
		servers = append(servers, server)
	}
	sort.Slice(servers, func(i, j int) bool { return servers[i].ID < servers[j].ID })
	return servers
}

func (s *Service) serverIDs() []string {
	servers := s.sortedServers()
	ids := make([]string, len(servers))
	for i, server := range servers {
		ids[i] = server.ID
	}
	return ids
}

func effectiveHostKeyPolicy(server *config.ServerConfig) string {
	switch {
	case config.AutoAcceptHostKeys():
		return "auto-accept"
	case !server.StrictHostKeyChecking:
		return "disabled"
	default:
		return "strict"
	}
}

func validateTimeout(requested, configured int64) (time.Duration, error) {
	if requested == 0 {
		requested = configured
	}
	if requested < 100 || requested > configured {
		return 0, fmt.Errorf("timeoutMs must be between 100 and %d", configured)
	}
	return time.Duration(requested) * time.Millisecond, nil
}

func validateExecOutput(requested, configured int) (int, error) {
	if requested == 0 {
		requested = configured
	}
	if requested < 1024 || requested > configured {
		return 0, fmt.Errorf("maxOutputBytes must be between 1024 and %d", configured)
	}
	return requested, nil
}

// operationError converts an operation failure into a message that is safe
// and useful to return to the model.
func operationError(operation string, err error) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%s timed out", operation)
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("%s was cancelled", operation)
	default:
		return fmt.Errorf("%s failed: %w", operation, err)
	}
}

func audit(operation string, server *config.ServerConfig, started time.Time, success bool, detail string) {
	if detail = strings.TrimSpace(detail); detail != "" {
		detail = " " + detail
	}
	log.Printf("audit operation=%s server=%s success=%t durationMs=%d%s", operation, server.ID, success, time.Since(started).Milliseconds(), detail)
}

func hash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:8])
}

// limitedBuffer keeps at most limit bytes and records whether more arrived.
// It is safe for concurrent use because a timed-out session may still be
// delivering output while the result is being built.
type limitedBuffer struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	// Always report the full length so the session keeps draining the
	// remote stream; excess bytes are counted as truncation, not errors.
	kept := data
	if remaining := b.limit - b.buffer.Len(); len(kept) > remaining {
		b.truncated = true
		kept = kept[:max(remaining, 0)]
	}
	b.buffer.Write(kept)
	return len(data), nil
}

func (b *limitedBuffer) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncated
}

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	data := b.buffer.Bytes()
	if utf8.Valid(data) {
		return string(data)
	}
	return string(bytes.ToValidUTF8(data, []byte("�")))
}
