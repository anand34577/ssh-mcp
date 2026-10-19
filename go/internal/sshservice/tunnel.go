package sshservice

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"example.com/ssh-mcp-server/internal/config"
	"golang.org/x/crypto/ssh"
)

const (
	maxTunnelConnections = 64
	keepaliveInterval    = 30 * time.Second
	keepaliveTimeout     = 15 * time.Second
)

type TunnelOpenRequest struct {
	ServerID   string
	LocalHost  string
	LocalPort  int
	RemoteHost string
	RemotePort int
}

// Tunnel is an SSH local port forward: connections accepted on a loopback
// listener on this machine are relayed to remoteHost:remotePort as seen from
// the SSH server.
type Tunnel struct {
	id            string
	server        *config.ServerConfig
	client        *ssh.Client
	listener      net.Listener
	service       *Service
	remoteAddress string
	openedAt      time.Time
	connections   chan struct{}
	done          chan struct{}
	closeOnce     sync.Once
}

func (s *Service) OpenTunnel(ctx context.Context, request TunnelOpenRequest) (map[string]any, error) {
	if s.closed.Load() {
		return nil, errServiceClosed
	}
	server, err := s.server(request.ServerID)
	if err != nil {
		return nil, err
	}
	if !server.AllowPortForwarding {
		return nil, fmt.Errorf("port forwarding is disabled for server %q (allowPortForwarding is false)", server.ID)
	}
	localHost := strings.Trim(strings.TrimSpace(request.LocalHost), "[]")
	if localHost == "" {
		localHost = "127.0.0.1"
	}
	if !isLoopbackHost(localHost) {
		return nil, errors.New("for safety, localHost must be a loopback address (127.0.0.1, ::1, or localhost)")
	}
	if request.LocalPort < 0 || request.LocalPort > 65535 {
		return nil, errors.New("localPort must be between 0 and 65535 (0 picks a free port)")
	}
	remoteHost := strings.TrimSpace(request.RemoteHost)
	if remoteHost == "" {
		return nil, errors.New("remoteHost is required")
	}
	if request.RemotePort < 1 || request.RemotePort > 65535 {
		return nil, errors.New("remotePort must be between 1 and 65535")
	}

	if err := acquireTunnelSlot(ctx, server); err != nil {
		return nil, err
	}
	started := time.Now()
	client, err := s.connect(ctx, server)
	if err != nil {
		<-server.TunnelSlots
		audit("ssh_tunnel_open", server, started, false, "connection_failed")
		return nil, operationError("SSH tunnel connection", err)
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(localHost, strconv.Itoa(request.LocalPort)))
	if err != nil {
		_ = client.Close()
		<-server.TunnelSlots
		audit("ssh_tunnel_open", server, started, false, "listen_failed")
		return nil, fmt.Errorf("cannot listen on %s:%d: %w", localHost, request.LocalPort, err)
	}

	tunnel := &Tunnel{
		id:            fmt.Sprintf("tunnel-%d", s.nextTunnel.Add(1)),
		server:        server,
		client:        client,
		listener:      listener,
		service:       s,
		remoteAddress: net.JoinHostPort(remoteHost, strconv.Itoa(request.RemotePort)),
		openedAt:      time.Now().UTC(),
		connections:   make(chan struct{}, maxTunnelConnections),
		done:          make(chan struct{}),
	}
	s.mu.Lock()
	if s.closed.Load() {
		s.mu.Unlock()
		_ = listener.Close()
		_ = client.Close()
		<-server.TunnelSlots
		return nil, errServiceClosed
	}
	s.tunnels[tunnel.id] = tunnel
	s.mu.Unlock()
	go tunnel.acceptLoop()
	go tunnel.monitor()

	info := tunnel.info()
	audit("ssh_tunnel_open", server, started, true, fmt.Sprintf("tunnel=%s local=%s remote=%s", tunnel.id, listener.Addr(), tunnel.remoteAddress))
	info["warning"] = "The tunnel stays open until ssh_tunnel_close, the SSH connection drops, or the MCP server exits"
	return info, nil
}

func (s *Service) CloseTunnel(_ context.Context, id string) (map[string]any, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, errors.New("tunnelId is required; call ssh_tunnel_list to see open tunnels")
	}
	s.mu.Lock()
	tunnel := s.tunnels[id]
	s.mu.Unlock()
	if tunnel == nil {
		return nil, fmt.Errorf("unknown tunnelId %q (it may already be closed); call ssh_tunnel_list to see open tunnels", id)
	}
	tunnel.Close("closed by request")
	return map[string]any{"tunnelId": id, "closed": true}, nil
}

func (s *Service) ListTunnels() []map[string]any {
	s.mu.Lock()
	tunnels := make([]*Tunnel, 0, len(s.tunnels))
	for _, tunnel := range s.tunnels {
		tunnels = append(tunnels, tunnel)
	}
	s.mu.Unlock()
	sort.Slice(tunnels, func(i, j int) bool { return tunnels[i].openedAt.Before(tunnels[j].openedAt) })
	items := make([]map[string]any, 0, len(tunnels))
	for _, tunnel := range tunnels {
		items = append(items, tunnel.info())
	}
	return items
}

func (t *Tunnel) info() map[string]any {
	localHost, localPort := "", 0
	if address, ok := t.listener.Addr().(*net.TCPAddr); ok {
		localHost, localPort = address.IP.String(), address.Port
	}
	remoteHost, remotePort, _ := net.SplitHostPort(t.remoteAddress)
	port, _ := strconv.Atoi(remotePort)
	return map[string]any{
		"tunnelId":   t.id,
		"serverId":   t.server.ID,
		"localHost":  localHost,
		"localPort":  localPort,
		"remoteHost": remoteHost,
		"remotePort": port,
		"openedAt":   t.openedAt.Format(time.RFC3339),
		"open":       true,
	}
}

// Close stops the listener, closes the SSH connection (which ends every
// relayed connection), releases the tunnel slot, and unregisters the tunnel.
func (t *Tunnel) Close(reason string) {
	t.closeOnce.Do(func() {
		close(t.done)
		_ = t.listener.Close()
		_ = t.client.Close()
		<-t.server.TunnelSlots
		t.service.mu.Lock()
		if t.service.tunnels[t.id] == t {
			delete(t.service.tunnels, t.id)
		}
		t.service.mu.Unlock()
		log.Printf("audit operation=ssh_tunnel_close server=%s tunnel=%s reason=%q", t.server.ID, t.id, reason)
	})
}

func (t *Tunnel) acceptLoop() {
	defer t.Close("listener stopped")
	for {
		local, err := t.listener.Accept()
		if err != nil {
			return
		}
		select {
		case t.connections <- struct{}{}:
			go func() {
				defer func() { <-t.connections }()
				t.relay(local)
			}()
		default:
			_ = local.Close()
		}
	}
}

// monitor closes the tunnel when the SSH connection ends or stops answering
// keepalives, so a dead tunnel never lingers in ssh_tunnel_list.
func (t *Tunnel) monitor() {
	lost := make(chan struct{})
	go func() {
		_ = t.client.Wait()
		close(lost)
	}()
	ticker := time.NewTicker(keepaliveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-t.done:
			return
		case <-lost:
			t.Close("SSH connection lost")
			return
		case <-ticker.C:
			if err := keepalive(t.client); err != nil {
				t.Close("keepalive failed")
				return
			}
		}
	}
}

func keepalive(client *ssh.Client) error {
	result := make(chan error, 1)
	go func() {
		_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
		result <- err
	}()
	select {
	case err := <-result:
		return err
	case <-time.After(keepaliveTimeout):
		return errors.New("keepalive timed out")
	}
}

func (t *Tunnel) relay(local net.Conn) {
	remote, err := t.client.Dial("tcp", t.remoteAddress)
	if err != nil {
		_ = local.Close()
		return
	}
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		// Unblock both copies if the tunnel closes mid-transfer.
		select {
		case <-t.done:
			_ = local.Close()
			_ = remote.Close()
		case <-finished:
		}
	}()
	copied := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(remote, local); closeWrite(remote); copied <- struct{}{} }()
	go func() { _, _ = io.Copy(local, remote); closeWrite(local); copied <- struct{}{} }()
	<-copied
	<-copied
	_ = local.Close()
	_ = remote.Close()
}

func closeWrite(conn net.Conn) {
	if writer, ok := conn.(interface{ CloseWrite() error }); ok {
		_ = writer.CloseWrite()
	}
}

func acquireTunnelSlot(ctx context.Context, server *config.ServerConfig) error {
	timer := time.NewTimer(time.Duration(server.ConnectTimeoutMs) * time.Millisecond)
	defer timer.Stop()
	select {
	case server.TunnelSlots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return fmt.Errorf("tunnel limit reached for server %q: %d tunnels already open; close one with ssh_tunnel_close or raise maxConcurrentTunnels", server.ID, server.MaxConcurrentTunnels)
	}
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}
