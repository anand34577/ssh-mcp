package sshservice

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"example.com/ssh-mcp-server/internal/config"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type Service struct {
	catalog *config.Catalog

	mu         sync.RWMutex
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

type SFTPListRequest struct {
	ServerID   string
	Path       string
	MaxEntries int
}

type SFTPStatRequest struct {
	ServerID string
	Path     string
}

type SFTPReadRequest struct {
	ServerID string
	Path     string
	MaxBytes int
	Encoding string
}

type SFTPWriteRequest struct {
	ServerID      string
	Path          string
	Content       *string
	ContentBase64 *string
}

type SFTPMkdirRequest struct {
	ServerID string
	Path     string
}

type SFTPDeleteRequest struct {
	ServerID  string
	Path      string
	Directory bool
}

type TunnelOpenRequest struct {
	ServerID   string
	LocalHost  string
	LocalPort  int
	RemoteHost string
	RemotePort int
}

type Tunnel struct {
	id        string
	server    *config.ServerConfig
	client    *ssh.Client
	listener  net.Listener
	closeOnce sync.Once
}

func New(catalog *config.Catalog) *Service {
	return &Service{catalog: catalog, tunnels: make(map[string]*Tunnel)}
}

func (s *Service) ListServers() []map[string]any {
	items := make([]map[string]any, 0, len(s.catalog.Servers))
	for _, server := range s.catalog.Servers {
		items = append(items, map[string]any{
			"id":                    server.ID,
			"host":                  server.Host,
			"port":                  server.Port,
			"username":              server.Username,
			"strictHostKeyChecking": server.StrictHostKeyChecking,
			"allowSftp":             server.AllowSFTP,
			"allowPortForwarding":   server.AllowPortForwarding,
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
	if len(command) > 32768 {
		return nil, errors.New("command must be at most 32768 characters")
	}
	if err := checkCommandPolicy(server, command); err != nil {
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
	operationCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	started := time.Now()

	result, err := s.withClient(operationCtx, server, "ssh_exec", func(client *ssh.Client) (map[string]any, error) {
		session, err := client.NewSession()
		if err != nil {
			return nil, err
		}
		defer session.Close()

		stdout := &limitedBuffer{limit: maxOutput}
		stderr := &limitedBuffer{limit: maxOutput}
		session.Stdout = stdout
		session.Stderr = stderr
		if err := session.Start(command); err != nil {
			return nil, err
		}

		wait := make(chan error, 1)
		go func() { wait <- session.Wait() }()
		var waitErr error
		timedOut := false
		select {
		case waitErr = <-wait:
		case <-operationCtx.Done():
			timedOut = errors.Is(operationCtx.Err(), context.DeadlineExceeded)
			_ = session.Close()
			select {
			case waitErr = <-wait:
			case <-time.After(2 * time.Second):
				waitErr = operationCtx.Err()
			}
		}

		exitCode := any(nil)
		if exitErr, ok := waitErr.(*ssh.ExitError); ok {
			exitCode = exitErr.ExitStatus()
		} else if waitErr == nil {
			exitCode = 0
		} else if !timedOut {
			return nil, waitErr
		}

		return map[string]any{
			"serverId":        server.ID,
			"stdout":          stdout.String(),
			"stderr":          stderr.String(),
			"exitCode":        exitCode,
			"exitSignal":      "",
			"timedOut":        timedOut,
			"stdoutTruncated": stdout.truncated,
			"stderrTruncated": stderr.truncated,
			"durationMs":      time.Since(started).Milliseconds(),
		}, nil
	})
	if err != nil {
		return nil, safeOperationError("SSH command", err)
	}
	audit("ssh_exec", server, started, true, "commandHash="+hash(command))
	return result, nil
}

func (s *Service) SFTPList(ctx context.Context, request SFTPListRequest) (map[string]any, error) {
	server, err := s.sftpServer(request.ServerID)
	if err != nil {
		return nil, err
	}
	path, err := requiredPath(request.Path)
	if err != nil {
		return nil, err
	}
	maxEntries := request.MaxEntries
	if maxEntries == 0 {
		maxEntries = 1000
	}
	if maxEntries < 1 || maxEntries > 10000 {
		return nil, errors.New("maxEntries must be between 1 and 10000")
	}
	operationCtx, cancel := context.WithTimeout(ctx, time.Duration(server.CommandTimeoutMs)*time.Millisecond)
	defer cancel()

	return s.withSFTP(operationCtx, server, "sftp_list", func(client *sftp.Client) (map[string]any, error) {
		entries, err := client.ReadDir(path)
		if err != nil {
			return nil, err
		}
		items := make([]map[string]any, 0, min(len(entries), maxEntries))
		truncated := len(entries) > maxEntries
		for _, entry := range entries[:min(len(entries), maxEntries)] {
			items = append(items, fileInfoMap(entry, entry.Name()))
		}
		return map[string]any{"serverId": server.ID, "path": path, "entries": items, "truncated": truncated}, nil
	})
}

func (s *Service) SFTPStat(ctx context.Context, request SFTPStatRequest) (map[string]any, error) {
	server, err := s.sftpServer(request.ServerID)
	if err != nil {
		return nil, err
	}
	path, err := requiredPath(request.Path)
	if err != nil {
		return nil, err
	}
	operationCtx, cancel := context.WithTimeout(ctx, time.Duration(server.CommandTimeoutMs)*time.Millisecond)
	defer cancel()
	return s.withSFTP(operationCtx, server, "sftp_stat", func(client *sftp.Client) (map[string]any, error) {
		info, err := client.Stat(path)
		if err != nil {
			return nil, err
		}
		result := fileInfoMap(info, "")
		result["serverId"] = server.ID
		result["path"] = path
		return result, nil
	})
}

func (s *Service) SFTPRead(ctx context.Context, request SFTPReadRequest) (map[string]any, error) {
	server, err := s.sftpServer(request.ServerID)
	if err != nil {
		return nil, err
	}
	path, err := requiredPath(request.Path)
	if err != nil {
		return nil, err
	}
	maxBytes := request.MaxBytes
	if maxBytes == 0 {
		maxBytes = server.MaxOutputBytes
	}
	if maxBytes < 1 || maxBytes > server.MaxOutputBytes {
		return nil, fmt.Errorf("maxBytes must be between 1 and %d", server.MaxOutputBytes)
	}
	encoding := strings.ToLower(strings.TrimSpace(request.Encoding))
	if encoding == "" {
		encoding = "utf8"
	}
	if encoding != "utf8" && encoding != "base64" {
		return nil, errors.New("encoding must be 'utf8' or 'base64'")
	}
	operationCtx, cancel := context.WithTimeout(ctx, time.Duration(server.CommandTimeoutMs)*time.Millisecond)
	defer cancel()
	return s.withSFTP(operationCtx, server, "sftp_read_file", func(client *sftp.Client) (map[string]any, error) {
		file, err := client.Open(path)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, int64(maxBytes)+1))
		if err != nil {
			return nil, err
		}
		truncated := len(data) > maxBytes
		if truncated {
			data = data[:maxBytes]
		}
		result := map[string]any{
			"serverId":  server.ID,
			"path":      path,
			"bytes":     len(data),
			"truncated": truncated,
			"encoding":  encoding,
		}
		if encoding == "base64" {
			result["contentBase64"] = base64.StdEncoding.EncodeToString(data)
		} else {
			result["content"] = string(data)
		}
		return result, nil
	})
}

func (s *Service) SFTPWrite(ctx context.Context, request SFTPWriteRequest) (map[string]any, error) {
	server, err := s.sftpServer(request.ServerID)
	if err != nil {
		return nil, err
	}
	path, err := requiredPath(request.Path)
	if err != nil {
		return nil, err
	}
	if (request.Content == nil) == (request.ContentBase64 == nil) {
		return nil, errors.New("provide exactly one of content or contentBase64")
	}
	var data []byte
	if request.Content != nil {
		data = []byte(*request.Content)
	} else {
		data, err = base64.StdEncoding.DecodeString(*request.ContentBase64)
		if err != nil {
			return nil, errors.New("contentBase64 is not valid base64")
		}
	}
	if len(data) > server.MaxOutputBytes {
		return nil, errors.New("file content exceeds maxOutputBytes for this server")
	}
	operationCtx, cancel := context.WithTimeout(ctx, time.Duration(server.CommandTimeoutMs)*time.Millisecond)
	defer cancel()
	return s.withSFTP(operationCtx, server, "sftp_write_file", func(client *sftp.Client) (map[string]any, error) {
		file, err := client.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		if _, err := file.Write(data); err != nil {
			return nil, err
		}
		return map[string]any{"serverId": server.ID, "path": path, "bytesWritten": len(data)}, nil
	})
}

func (s *Service) SFTPMkdir(ctx context.Context, request SFTPMkdirRequest) (map[string]any, error) {
	server, err := s.sftpServer(request.ServerID)
	if err != nil {
		return nil, err
	}
	path, err := requiredPath(request.Path)
	if err != nil {
		return nil, err
	}
	operationCtx, cancel := context.WithTimeout(ctx, time.Duration(server.CommandTimeoutMs)*time.Millisecond)
	defer cancel()
	return s.withSFTP(operationCtx, server, "sftp_mkdir", func(client *sftp.Client) (map[string]any, error) {
		if err := client.Mkdir(path); err != nil {
			return nil, err
		}
		return map[string]any{"serverId": server.ID, "path": path, "created": true}, nil
	})
}

func (s *Service) SFTPDelete(ctx context.Context, request SFTPDeleteRequest) (map[string]any, error) {
	server, err := s.sftpServer(request.ServerID)
	if err != nil {
		return nil, err
	}
	path, err := requiredPath(request.Path)
	if err != nil {
		return nil, err
	}
	operationCtx, cancel := context.WithTimeout(ctx, time.Duration(server.CommandTimeoutMs)*time.Millisecond)
	defer cancel()
	return s.withSFTP(operationCtx, server, "sftp_delete", func(client *sftp.Client) (map[string]any, error) {
		if request.Directory {
			err = client.RemoveDirectory(path)
		} else {
			err = client.Remove(path)
		}
		if err != nil {
			return nil, err
		}
		return map[string]any{"serverId": server.ID, "path": path, "deleted": true}, nil
	})
}

func (s *Service) OpenTunnel(ctx context.Context, request TunnelOpenRequest) (map[string]any, error) {
	if s.closed.Load() {
		return nil, errors.New("SSH service is closed")
	}
	server, err := s.server(request.ServerID)
	if err != nil {
		return nil, err
	}
	if !server.AllowPortForwarding {
		return nil, errors.New("port forwarding is disabled for this server")
	}
	localHost := strings.TrimSpace(request.LocalHost)
	if localHost == "" {
		localHost = "127.0.0.1"
	}
	if !isLoopbackHost(localHost) {
		return nil, errors.New("for safety, localHost must be loopback")
	}
	if request.LocalPort < 0 || request.LocalPort > 65535 {
		return nil, errors.New("localPort must be between 0 and 65535")
	}
	remoteHost := strings.TrimSpace(request.RemoteHost)
	if remoteHost == "" {
		return nil, errors.New("remoteHost is required")
	}
	if request.RemotePort < 1 || request.RemotePort > 65535 {
		return nil, errors.New("remotePort must be between 1 and 65535")
	}
	if err := acquire(ctx, server); err != nil {
		return nil, err
	}
	client, err := s.connect(ctx, server)
	if err != nil {
		release(server)
		return nil, safeOperationError("SSH tunnel connection", err)
	}
	if s.closed.Load() {
		_ = client.Close()
		release(server)
		return nil, errors.New("SSH service is closed")
	}
	listener, err := client.Listen("tcp", net.JoinHostPort(localHost, strconv.Itoa(request.LocalPort)))
	if err != nil {
		_ = client.Close()
		release(server)
		return nil, safeOperationError("SSH tunnel listener", err)
	}

	id := fmt.Sprintf("tunnel-%d", s.nextTunnel.Add(1))
	tunnel := &Tunnel{id: id, server: server, client: client, listener: listener}
	s.mu.Lock()
	s.tunnels[id] = tunnel
	s.mu.Unlock()
	go tunnel.acceptLoop(remoteHost, request.RemotePort)

	address, _ := listener.Addr().(*net.TCPAddr)
	localPort := request.LocalPort
	if address != nil {
		localPort = address.Port
	}
	log.Printf("audit operation=ssh_tunnel_open server=%s tunnel=%s local=%s:%d remote=%s:%d", server.ID, id, localHost, localPort, remoteHost, request.RemotePort)
	return map[string]any{
		"tunnelId":   id,
		"serverId":   server.ID,
		"localHost":  localHost,
		"localPort":  localPort,
		"remoteHost": remoteHost,
		"remotePort": request.RemotePort,
		"warning":    "The tunnel remains active until ssh_tunnel_close or process exit",
	}, nil
}

func (s *Service) CloseTunnel(_ context.Context, id string) (map[string]any, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, errors.New("tunnelId is required")
	}
	s.mu.Lock()
	tunnel := s.tunnels[id]
	if tunnel != nil {
		delete(s.tunnels, id)
	}
	s.mu.Unlock()
	if tunnel == nil {
		return nil, errors.New("unknown tunnelId")
	}
	tunnel.Close()
	return map[string]any{"tunnelId": id, "closed": true}, nil
}

func (s *Service) ListTunnels() []map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]map[string]any, 0, len(s.tunnels))
	for _, tunnel := range s.tunnels {
		localHost := ""
		localPort := 0
		if address, ok := tunnel.listener.Addr().(*net.TCPAddr); ok {
			localHost = address.IP.String()
			localPort = address.Port
		}
		items = append(items, map[string]any{
			"tunnelId":  tunnel.id,
			"serverId":  tunnel.server.ID,
			"localHost": localHost,
			"localPort": localPort,
			"open":      true,
		})
	}
	return items
}

func (s *Service) Close() {
	if !s.closed.CompareAndSwap(false, true) {
		return
	}
	s.mu.Lock()
	tunnels := make([]*Tunnel, 0, len(s.tunnels))
	for id, tunnel := range s.tunnels {
		delete(s.tunnels, id)
		tunnels = append(tunnels, tunnel)
	}
	s.mu.Unlock()
	for _, tunnel := range tunnels {
		tunnel.Close()
	}
}

func (t *Tunnel) Close() {
	t.closeOnce.Do(func() {
		_ = t.listener.Close()
		_ = t.client.Close()
		release(t.server)
	})
}

func (t *Tunnel) acceptLoop(remoteHost string, remotePort int) {
	remoteAddress := net.JoinHostPort(remoteHost, strconv.Itoa(remotePort))
	for {
		local, err := t.listener.Accept()
		if err != nil {
			return
		}
		go t.proxy(local, remoteAddress)
	}
}

func (t *Tunnel) proxy(local net.Conn, remoteAddress string) {
	remote, err := t.client.Dial("tcp", remoteAddress)
	if err != nil {
		_ = local.Close()
		return
	}
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(remote, local); _ = remote.Close(); done <- struct{}{} }()
	go func() { _, _ = io.Copy(local, remote); _ = local.Close(); done <- struct{}{} }()
	<-done
	_ = local.Close()
	_ = remote.Close()
}

func (s *Service) withSFTP(ctx context.Context, server *config.ServerConfig, operation string, fn func(*sftp.Client) (map[string]any, error)) (map[string]any, error) {
	return s.withClient(ctx, server, operation, func(client *ssh.Client) (map[string]any, error) {
		sftpClient, err := sftp.NewClient(client)
		if err != nil {
			return nil, err
		}
		defer sftpClient.Close()
		return fn(sftpClient)
	})
}

func (s *Service) withClient(ctx context.Context, server *config.ServerConfig, operation string, fn func(*ssh.Client) (map[string]any, error)) (map[string]any, error) {
	if s.closed.Load() {
		return nil, errors.New("SSH service is closed")
	}
	if err := acquire(ctx, server); err != nil {
		return nil, err
	}
	defer release(server)
	started := time.Now()
	client, err := s.connect(ctx, server)
	if err != nil {
		audit(operation, server, started, false, "connection_failed")
		return nil, err
	}
	defer client.Close()
	result, err := fn(client)
	audit(operation, server, started, err == nil, "")
	return result, err
}

func (s *Service) connect(ctx context.Context, server *config.ServerConfig) (*ssh.Client, error) {
	var hostKeyCallback ssh.HostKeyCallback
	if server.StrictHostKeyChecking {
		callback, err := knownhosts.New(server.KnownHostsPath)
		if err != nil {
			return nil, errors.New("known_hosts could not be loaded")
		}
		hostKeyCallback = callback
	} else {
		hostKeyCallback = ssh.InsecureIgnoreHostKey()
	}

	authMethods := make([]ssh.AuthMethod, 0, 2)
	if server.PrivateKeyPath != "" {
		keyData, err := os.ReadFile(filepath.Clean(server.PrivateKeyPath))
		if err != nil {
			return nil, errors.New("private key could not be read")
		}
		var signer ssh.Signer
		if server.PrivateKeyPassphraseEnv != "" {
			passphrase := os.Getenv(server.PrivateKeyPassphraseEnv)
			if passphrase == "" {
				return nil, errors.New("private key passphrase is unavailable")
			}
			signer, err = ssh.ParsePrivateKeyWithPassphrase(keyData, []byte(passphrase))
		} else {
			signer, err = ssh.ParsePrivateKey(keyData)
		}
		if err != nil {
			return nil, errors.New("private key could not be parsed")
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	}
	if server.PasswordEnv != "" {
		password := os.Getenv(server.PasswordEnv)
		if password == "" {
			return nil, errors.New("password credential is unavailable")
		}
		authMethods = append(authMethods, ssh.Password(password))
	}
	if len(authMethods) == 0 {
		return nil, errors.New("no SSH authentication method is configured")
	}

	sshConfig := &ssh.ClientConfig{
		User:            server.Username,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCallback,
		Timeout:         time.Duration(server.ConnectTimeoutMs) * time.Millisecond,
	}
	address := net.JoinHostPort(server.Host, strconv.Itoa(server.Port))
	dialer := &net.Dialer{Timeout: time.Duration(server.ConnectTimeoutMs) * time.Millisecond}
	rawConn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	conn, channels, requests, err := ssh.NewClientConn(rawConn, address, sshConfig)
	if err != nil {
		_ = rawConn.Close()
		return nil, err
	}
	return ssh.NewClient(conn, channels, requests), nil
}

func (s *Service) server(id string) (*config.ServerConfig, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, errors.New("serverId is required")
	}
	server := s.catalog.Servers[id]
	if server == nil {
		return nil, errors.New("unknown serverId")
	}
	return server, nil
}

func (s *Service) sftpServer(id string) (*config.ServerConfig, error) {
	server, err := s.server(id)
	if err != nil {
		return nil, err
	}
	if !server.AllowSFTP {
		return nil, errors.New("SFTP is disabled for this server")
	}
	return server, nil
}

func acquire(ctx context.Context, server *config.ServerConfig) error {
	select {
	case server.Slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func release(server *config.ServerConfig) {
	<-server.Slots
}

func checkCommandPolicy(server *config.ServerConfig, command string) error {
	for _, pattern := range server.DeniedPatterns {
		if pattern.MatchString(command) {
			return errors.New("command rejected by the server command policy")
		}
	}
	if len(server.AllowedCommandPrefixes) == 0 {
		return nil
	}
	trimmed := strings.TrimSpace(command)
	for _, prefix := range server.AllowedCommandPrefixes {
		prefix = strings.TrimSpace(prefix)
		if trimmed == prefix || strings.HasPrefix(trimmed, prefix+" ") {
			return nil
		}
	}
	return errors.New("command is not allowed by the server command policy")
}

func requiredPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("path is required")
	}
	return path, nil
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

func fileInfoMap(info os.FileInfo, name string) map[string]any {
	typeName := "other"
	if info.IsDir() {
		typeName = "directory"
	} else if info.Mode().IsRegular() {
		typeName = "file"
	} else if info.Mode()&os.ModeSymlink != 0 {
		typeName = "symlink"
	}
	result := map[string]any{
		"name":        name,
		"size":        info.Size(),
		"type":        typeName,
		"permissions": strconv.FormatUint(uint64(info.Mode().Perm()), 8),
		"modified":    info.ModTime().UTC().Format(time.RFC3339Nano),
	}
	return result
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") || host == "127.0.0.1" || host == "::1" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func safeOperationError(operation string, err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%s timed out", operation)
	}
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("%s was cancelled", operation)
	}
	return fmt.Errorf("%s failed: %v", operation, err)
}

func audit(operation string, server *config.ServerConfig, started time.Time, success bool, detail string) {
	if detail != "" {
		detail = " " + detail
	}
	log.Printf("audit operation=%s server=%s success=%t durationMs=%d%s", operation, server.ID, success, time.Since(started).Milliseconds(), detail)
}

func hash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:8])
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}

type limitedBuffer struct {
	bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(data []byte) (int, error) {
	remaining := b.limit - b.Len()
	if remaining <= 0 {
		b.truncated = true
		return len(data), nil
	}
	if len(data) > remaining {
		_, _ = b.Buffer.Write(data[:remaining])
		b.truncated = true
		return len(data), nil
	}
	return b.Buffer.Write(data)
}

func (b *limitedBuffer) String() string {
	data := b.Bytes()
	if utf8.Valid(data) {
		return string(data)
	}
	return string(bytes.ToValidUTF8(data, []byte("�")))
}
