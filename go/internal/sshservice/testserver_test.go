package sshservice

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"example.com/ssh-mcp-server/internal/config"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

const (
	testUser        = "tester"
	testPassword    = "correct horse"
	testPasswordEnv = "SSH_MCP_TEST_PASSWORD"
)

// testSSHServer is a minimal in-process SSH server supporting password auth,
// a few fake exec commands, an in-memory SFTP subsystem, and direct-tcpip
// forwarding. It lets the service be tested end to end without a real sshd.
type testSSHServer struct {
	address    string
	hostKey    ssh.Signer
	knownHosts string
}

func startTestSSHServer(t *testing.T) *testSSHServer {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostKey, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	serverConfig := &ssh.ServerConfig{
		PasswordCallback: func(meta ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			if meta.User() == testUser && string(password) == testPassword {
				return nil, nil
			}
			return nil, errors.New("access denied")
		},
	}
	serverConfig.AddHostKey(hostKey)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	// One filesystem shared by every connection, like a real server's disk.
	fs := sftp.InMemHandler()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go serveTestConn(conn, serverConfig, fs)
		}
	}()

	server := &testSSHServer{address: listener.Addr().String(), hostKey: hostKey}
	server.knownHosts = writeKnownHosts(t, server.address, hostKey.PublicKey())
	return server
}

func writeKnownHosts(t *testing.T, address string, key ssh.PublicKey) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "known_hosts")
	line := knownhosts.Line([]string{knownhosts.Normalize(address)}, key) + "\n"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// config returns a validated ServerConfig pointing at this server. extra is
// spliced into the JSON object, e.g. `"allowPortForwarding":true`.
func (s *testSSHServer) config(t *testing.T, extra string) *config.ServerConfig {
	t.Helper()
	t.Setenv(testPasswordEnv, testPassword)
	host, port, _ := net.SplitHostPort(s.address)
	if extra != "" {
		extra = "," + extra
	}
	body := fmt.Sprintf(`{"servers":[{"id":"test","host":%q,"port":%s,"username":%q,"passwordEnv":%q,"knownHostsPath":%q,"connectTimeoutMs":5000,"commandTimeoutMs":5000,"maxOutputBytes":4096%s}]}`,
		host, port, testUser, testPasswordEnv, filepath.ToSlash(s.knownHosts), extra)
	catalog, err := config.Parse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	return catalog.Servers["test"]
}

func (s *testSSHServer) service(t *testing.T, extra string) *Service {
	t.Helper()
	server := s.config(t, extra)
	service := New(&config.Catalog{Servers: map[string]*config.ServerConfig{server.ID: server}})
	t.Cleanup(service.Close)
	return service
}

func serveTestConn(conn net.Conn, serverConfig *ssh.ServerConfig, fs sftp.Handlers) {
	_, channels, requests, err := ssh.NewServerConn(conn, serverConfig)
	if err != nil {
		_ = conn.Close()
		return
	}
	go ssh.DiscardRequests(requests)
	for newChannel := range channels {
		switch newChannel.ChannelType() {
		case "session":
			go serveTestSession(newChannel, fs)
		case "direct-tcpip":
			go serveTestDirect(newChannel)
		default:
			_ = newChannel.Reject(ssh.UnknownChannelType, "unsupported")
		}
	}
}

func serveTestSession(newChannel ssh.NewChannel, fs sftp.Handlers) {
	channel, requests, err := newChannel.Accept()
	if err != nil {
		return
	}
	for request := range requests {
		switch request.Type {
		case "exec":
			var payload struct{ Command string }
			_ = ssh.Unmarshal(request.Payload, &payload)
			_ = request.Reply(true, nil)
			go runTestCommand(channel, payload.Command)
		case "subsystem":
			var payload struct{ Name string }
			_ = ssh.Unmarshal(request.Payload, &payload)
			_ = request.Reply(payload.Name == "sftp", nil)
			if payload.Name == "sftp" {
				go func() {
					_ = sftp.NewRequestServer(channel, fs).Serve()
					_ = channel.Close()
				}()
			}
		default:
			if request.WantReply {
				_ = request.Reply(false, nil)
			}
		}
	}
}

// runTestCommand implements: "echo ARGS", "fail" (stderr + exit 3),
// "flood N" (N bytes of stdout), "kill" (exit signal), and "sleep" (runs
// until the channel is closed).
func runTestCommand(channel ssh.Channel, command string) {
	name, args, _ := strings.Cut(command, " ")
	exit := func(status uint32) {
		_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
		_ = channel.Close()
	}
	switch name {
	case "echo":
		_, _ = io.WriteString(channel, args+"\n")
		exit(0)
	case "fail":
		_, _ = io.WriteString(channel.Stderr(), "boom\n")
		exit(3)
	case "flood":
		size, _ := strconv.Atoi(args)
		_, _ = channel.Write([]byte(strings.Repeat("x", size)))
		exit(0)
	case "kill":
		_, _ = channel.SendRequest("exit-signal", false, ssh.Marshal(struct {
			Signal     string
			CoreDumped bool
			Error      string
			Lang       string
		}{Signal: "KILL"}))
		_ = channel.Close()
	case "sleep":
		_, _ = io.Copy(io.Discard, channel)
	default:
		_, _ = io.WriteString(channel.Stderr(), "command not found\n")
		exit(127)
	}
}

func serveTestDirect(newChannel ssh.NewChannel) {
	var target struct {
		Host       string
		Port       uint32
		OriginHost string
		OriginPort uint32
	}
	if err := ssh.Unmarshal(newChannel.ExtraData(), &target); err != nil {
		_ = newChannel.Reject(ssh.ConnectionFailed, "bad payload")
		return
	}
	conn, err := net.Dial("tcp", net.JoinHostPort(target.Host, strconv.Itoa(int(target.Port))))
	if err != nil {
		_ = newChannel.Reject(ssh.ConnectionFailed, err.Error())
		return
	}
	channel, requests, err := newChannel.Accept()
	if err != nil {
		_ = conn.Close()
		return
	}
	go ssh.DiscardRequests(requests)
	go func() { _, _ = io.Copy(channel, conn); _ = channel.CloseWrite() }()
	_, _ = io.Copy(conn, channel)
	_ = conn.Close()
	_ = channel.Close()
}
