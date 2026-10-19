package sshservice

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"example.com/ssh-mcp-server/internal/config"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func offlineService(t *testing.T, mutate func(*config.ServerConfig)) (*Service, *config.ServerConfig) {
	t.Helper()
	server := &config.ServerConfig{
		ID:                      "test",
		Host:                    "127.0.0.1",
		Port:                    22,
		Username:                "test",
		PasswordEnv:             testPasswordEnv,
		CommandTimeoutMs:        1000,
		ConnectTimeoutMs:        1000,
		MaxOutputBytes:          1024,
		MaxConcurrentOperations: 1,
		MaxConcurrentTunnels:    1,
		Slots:                   make(chan struct{}, 1),
		TunnelSlots:             make(chan struct{}, 1),
	}
	if mutate != nil {
		mutate(server)
	}
	service := New(&config.Catalog{Servers: map[string]*config.ServerConfig{"test": server}})
	t.Cleanup(service.Close)
	return service, server
}

func TestExecRejectsDisallowedCommandBeforeConnecting(t *testing.T) {
	service, _ := offlineService(t, func(s *config.ServerConfig) { s.AllowedCommandPrefixes = []string{"printf"} })
	if _, err := service.Exec(context.Background(), ExecRequest{ServerID: "test", Command: "rm -rf /"}); err == nil {
		t.Fatal("disallowed command was accepted")
	}
}

func TestUnknownServerListsConfiguredIDs(t *testing.T) {
	service, _ := offlineService(t, nil)
	_, err := service.Exec(context.Background(), ExecRequest{ServerID: "nope", Command: "true"})
	if err == nil || !strings.Contains(err.Error(), "test") {
		t.Fatalf("expected unknown server error listing ids, got %v", err)
	}
}

func TestTunnelRequiresLoopback(t *testing.T) {
	service, _ := offlineService(t, func(s *config.ServerConfig) { s.AllowPortForwarding = true })
	if _, err := service.OpenTunnel(context.Background(), TunnelOpenRequest{ServerID: "test", LocalHost: "0.0.0.0", RemoteHost: "127.0.0.1", RemotePort: 22}); err == nil {
		t.Fatal("non-loopback tunnel bind was accepted")
	}
}

func TestCommandPolicy(t *testing.T) {
	_, server := offlineService(t, func(s *config.ServerConfig) { s.AllowedCommandPrefixes = []string{"systemctl status"} })
	for _, command := range []string{"systemctl status ; rm -rf /", "systemctl status-malicious", "systemctl status $(id)", "systemctl status\nreboot"} {
		if err := checkCommandPolicy(server, command); err == nil {
			t.Errorf("command %q was accepted", command)
		}
	}
	for _, command := range []string{"systemctl status", "systemctl status\tnginx", "systemctl status nginx"} {
		if err := checkCommandPolicy(server, command); err != nil {
			t.Errorf("command %q was rejected: %v", command, err)
		}
	}
}

func TestPasswordChallengeOnlyAnswersHiddenSinglePrompt(t *testing.T) {
	challenge := passwordChallenge("pw")
	if answers, err := challenge("", "", []string{"Password: "}, []bool{false}); err != nil || len(answers) != 1 || answers[0] != "pw" {
		t.Fatalf("password prompt not answered: %v %v", answers, err)
	}
	if answers, err := challenge("", "", nil, nil); err != nil || len(answers) != 0 {
		t.Fatalf("informational round mishandled: %v %v", answers, err)
	}
	if _, err := challenge("", "", []string{"Verification code: "}, []bool{true}); err == nil {
		t.Fatal("password was sent to an echoed prompt")
	}
	if _, err := challenge("", "", []string{"Password: ", "OTP: "}, []bool{false, false}); err == nil {
		t.Fatal("password was sent to a multi-prompt challenge")
	}
}

func TestTrimIncompleteRune(t *testing.T) {
	text := []byte("ab€") // € is 3 bytes
	for cut := len(text) - 2; cut < len(text); cut++ {
		if got := string(trimIncompleteRune(text[:cut])); got != "ab" {
			t.Errorf("cut %d: got %q", cut, got)
		}
	}
	if got := string(trimIncompleteRune(text)); got != "ab€" {
		t.Errorf("complete text changed: %q", got)
	}
}

func TestDecodeBase64AcceptsWhitespaceAndMissingPadding(t *testing.T) {
	for _, input := range []string{"aGVsbG8=", "aGVs\nbG8=", "aGVsbG8"} {
		data, err := decodeBase64(input)
		if err != nil || string(data) != "hello" {
			t.Errorf("%q: got %q, %v", input, data, err)
		}
	}
	if _, err := decodeBase64("not base64!"); err == nil {
		t.Error("invalid base64 accepted")
	}
}

func TestLimitedBufferReportsFullWrites(t *testing.T) {
	buffer := &limitedBuffer{limit: 4}
	if n, err := buffer.Write([]byte("abcdef")); n != 6 || err != nil {
		t.Fatalf("Write returned %d, %v; want 6, nil", n, err)
	}
	if buffer.String() != "abcd" || !buffer.Truncated() {
		t.Fatalf("got %q truncated=%t", buffer.String(), buffer.Truncated())
	}
}

func TestExecEndToEnd(t *testing.T) {
	server := startTestSSHServer(t)
	service := server.service(t, "")
	ctx := context.Background()

	result, err := service.Exec(ctx, ExecRequest{ServerID: "test", Command: "echo hello world"})
	if err != nil {
		t.Fatal(err)
	}
	if result["stdout"] != "hello world\n" || result["exitCode"] != 0 || result["timedOut"] != false {
		t.Fatalf("unexpected result: %v", result)
	}

	result, err = service.Exec(ctx, ExecRequest{ServerID: "test", Command: "fail"})
	if err != nil {
		t.Fatal(err)
	}
	if result["exitCode"] != 3 || result["stderr"] != "boom\n" {
		t.Fatalf("non-zero exit not reported as a result: %v", result)
	}

	result, err = service.Exec(ctx, ExecRequest{ServerID: "test", Command: "kill"})
	if err != nil {
		t.Fatal(err)
	}
	if result["exitSignal"] != "KILL" || result["exitCode"] != nil {
		t.Fatalf("exit signal not reported: %v", result)
	}

	result, err = service.Exec(ctx, ExecRequest{ServerID: "test", Command: "flood 5000", MaxOutputBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if len(result["stdout"].(string)) != 1024 || result["stdoutTruncated"] != true {
		t.Fatalf("output was not truncated: %d bytes, %v", len(result["stdout"].(string)), result["stdoutTruncated"])
	}
}

func TestExecTimeoutReturnsResult(t *testing.T) {
	server := startTestSSHServer(t)
	service := server.service(t, "")
	started := time.Now()
	result, err := service.Exec(context.Background(), ExecRequest{ServerID: "test", Command: "sleep", TimeoutMs: 300})
	if err != nil {
		t.Fatalf("timeout should be a result, got error: %v", err)
	}
	if result["timedOut"] != true || result["exitCode"] != nil {
		t.Fatalf("unexpected result: %v", result)
	}
	if elapsed := time.Since(started); elapsed > 4*time.Second {
		t.Fatalf("timeout took too long: %v", elapsed)
	}
}

func TestAuthenticationAndCredentialErrors(t *testing.T) {
	server := startTestSSHServer(t)
	service := server.service(t, "")
	t.Setenv(testPasswordEnv, "wrong")
	if _, err := service.Exec(context.Background(), ExecRequest{ServerID: "test", Command: "echo hi"}); err == nil || !strings.Contains(err.Error(), "unable to authenticate") {
		t.Fatalf("expected authentication failure, got %v", err)
	}
	t.Setenv(testPasswordEnv, "")
	if _, err := service.Exec(context.Background(), ExecRequest{ServerID: "test", Command: "echo hi"}); err == nil || !strings.Contains(err.Error(), testPasswordEnv) {
		t.Fatalf("expected missing env var error naming %s, got %v", testPasswordEnv, err)
	}
}

func TestHostKeyVerification(t *testing.T) {
	server := startTestSSHServer(t)
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	otherKey, _ := ssh.NewSignerFromKey(other)

	server.knownHosts = writeKnownHosts(t, server.address, otherKey.PublicKey())
	service := server.service(t, "")
	if _, err := service.Exec(context.Background(), ExecRequest{ServerID: "test", Command: "echo hi"}); err == nil || !strings.Contains(err.Error(), "MISMATCH") {
		t.Fatalf("expected host key mismatch, got %v", err)
	}

	server.knownHosts = writeKnownHosts(t, "unrelated.example:22", otherKey.PublicKey())
	service = server.service(t, "")
	if _, err := service.Exec(context.Background(), ExecRequest{ServerID: "test", Command: "echo hi"}); err == nil || !strings.Contains(err.Error(), "not in") {
		t.Fatalf("expected unknown host error, got %v", err)
	}
}

func TestKnownHostAlgorithmsFollowKnownHosts(t *testing.T) {
	server := startTestSSHServer(t)
	callback, err := knownhosts.New(server.knownHosts)
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(server.address)
	portNumber, _ := strconv.Atoi(port)
	algorithms := knownHostAlgorithms(callback, server.address, portNumber)
	if len(algorithms) != 1 || algorithms[0] != ssh.KeyAlgoED25519 {
		t.Fatalf("got %v, want [%s]", algorithms, ssh.KeyAlgoED25519)
	}
	if got := knownHostAlgorithms(callback, "elsewhere.example:22", 22); got != nil {
		t.Fatalf("unknown host should yield nil, got %v", got)
	}
}

func TestSFTPEndToEnd(t *testing.T) {
	server := startTestSSHServer(t)
	service := server.service(t, "")
	ctx := context.Background()
	text := "line one\nline two — with unicode\n"

	if _, err := service.SFTPMkdir(ctx, SFTPMkdirRequest{ServerID: "test", Path: "/work/nested", Parents: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SFTPWrite(ctx, SFTPWriteRequest{ServerID: "test", Path: "/work/nested/a.txt", Content: &text}); err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString([]byte{0, 1, 2, 255})
	if _, err := service.SFTPWrite(ctx, SFTPWriteRequest{ServerID: "test", Path: "/work/bin.dat", ContentBase64: &encoded}); err != nil {
		t.Fatal(err)
	}

	read, err := service.SFTPRead(ctx, SFTPReadRequest{ServerID: "test", Path: "/work/nested/a.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if read["content"] != text || read["truncated"] != false || read["size"] != int64(len(text)) {
		t.Fatalf("unexpected read: %v", read)
	}

	// Page through the file in small chunks and reassemble it.
	var assembled strings.Builder
	offset := int64(0)
	for {
		chunk, err := service.SFTPRead(ctx, SFTPReadRequest{ServerID: "test", Path: "/work/nested/a.txt", Offset: offset, MaxBytes: 7})
		if err != nil {
			t.Fatal(err)
		}
		assembled.WriteString(chunk["content"].(string))
		offset = chunk["nextOffset"].(int64)
		if chunk["truncated"] == false {
			break
		}
	}
	if assembled.String() != text {
		t.Fatalf("paged read mismatch: %q", assembled.String())
	}

	binary, err := service.SFTPRead(ctx, SFTPReadRequest{ServerID: "test", Path: "/work/bin.dat", Encoding: "base64"})
	if err != nil || binary["contentBase64"] != encoded {
		t.Fatalf("binary round trip failed: %v %v", binary, err)
	}

	listing, err := service.SFTPList(ctx, SFTPListRequest{ServerID: "test", Path: "/work"})
	if err != nil {
		t.Fatal(err)
	}
	entries := listing["entries"].([]map[string]any)
	if len(entries) != 2 || entries[0]["name"] != "bin.dat" || entries[1]["type"] != "directory" {
		t.Fatalf("unexpected listing: %v", entries)
	}

	if _, err := service.SFTPRename(ctx, SFTPRenameRequest{ServerID: "test", From: "/work/bin.dat", To: "/work/moved.dat"}); err != nil {
		t.Fatal(err)
	}
	stat, err := service.SFTPStat(ctx, SFTPStatRequest{ServerID: "test", Path: "/work/moved.dat"})
	if err != nil || stat["size"] != int64(4) || stat["type"] != "file" {
		t.Fatalf("unexpected stat: %v %v", stat, err)
	}
	if _, err := service.SFTPDelete(ctx, SFTPDeleteRequest{ServerID: "test", Path: "/work/moved.dat"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SFTPStat(ctx, SFTPStatRequest{ServerID: "test", Path: "/work/moved.dat"}); err == nil || !strings.Contains(err.Error(), "no such file") {
		t.Fatalf("expected missing file error, got %v", err)
	}
}

func TestSFTPDisabled(t *testing.T) {
	server := startTestSSHServer(t)
	service := server.service(t, `"allowSftp":false`)
	if _, err := service.SFTPList(context.Background(), SFTPListRequest{ServerID: "test", Path: "/"}); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("expected SFTP disabled error, got %v", err)
	}
}

func TestTunnelForwardsLocalConnections(t *testing.T) {
	server := startTestSSHServer(t)
	service := server.service(t, `"allowPortForwarding":true,"maxConcurrentTunnels":1`)

	// A line-echo service reachable from the SSH server.
	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	go func() {
		for {
			conn, err := target.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				line, _ := bufio.NewReader(conn).ReadString('\n')
				_, _ = io.WriteString(conn, "echo:"+line)
			}()
		}
	}()
	_, targetPort, _ := net.SplitHostPort(target.Addr().String())
	port, _ := strconv.Atoi(targetPort)

	ctx := context.Background()
	opened, err := service.OpenTunnel(ctx, TunnelOpenRequest{ServerID: "test", RemoteHost: "127.0.0.1", RemotePort: port})
	if err != nil {
		t.Fatal(err)
	}
	localAddress := net.JoinHostPort(opened["localHost"].(string), strconv.Itoa(opened["localPort"].(int)))

	conn, err := net.DialTimeout("tcp", localAddress, 2*time.Second)
	if err != nil {
		t.Fatalf("tunnel is not listening locally: %v", err)
	}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = io.WriteString(conn, "ping\n")
	reply, err := bufio.NewReader(conn).ReadString('\n')
	_ = conn.Close()
	if err != nil || reply != "echo:ping\n" {
		t.Fatalf("unexpected reply %q, %v", reply, err)
	}

	if tunnels := service.ListTunnels(); len(tunnels) != 1 || tunnels[0]["remotePort"] != port {
		t.Fatalf("unexpected tunnel list: %v", tunnels)
	}
	// The single tunnel slot is in use.
	if _, err := service.OpenTunnel(ctx, TunnelOpenRequest{ServerID: "test", RemoteHost: "127.0.0.1", RemotePort: port}); err == nil || !strings.Contains(err.Error(), "tunnel limit") {
		t.Fatalf("expected tunnel limit error, got %v", err)
	}

	if _, err := service.CloseTunnel(ctx, opened["tunnelId"].(string)); err != nil {
		t.Fatal(err)
	}
	if tunnels := service.ListTunnels(); len(tunnels) != 0 {
		t.Fatalf("tunnel still listed after close: %v", tunnels)
	}
	if conn, err := net.DialTimeout("tcp", localAddress, time.Second); err == nil {
		_ = conn.Close()
		t.Fatal("tunnel still accepting connections after close")
	}
	// The slot was released, so a new tunnel can be opened.
	reopened, err := service.OpenTunnel(ctx, TunnelOpenRequest{ServerID: "test", RemoteHost: "127.0.0.1", RemotePort: port})
	if err != nil {
		t.Fatalf("tunnel slot was not released: %v", err)
	}
	if _, err := service.CloseTunnel(ctx, reopened["tunnelId"].(string)); err != nil {
		t.Fatal(err)
	}
}

func TestCheckReportsReadinessAndProblems(t *testing.T) {
	server := startTestSSHServer(t)
	service := server.service(t, "")
	results := service.Check(context.Background(), true)
	if len(results) != 1 || !results[0].OK() || !results[0].Connected {
		t.Fatalf("expected a connected OK result, got %+v", results)
	}

	t.Setenv(testPasswordEnv, "")
	results = service.Check(context.Background(), false)
	if results[0].OK() || !strings.Contains(strings.Join(results[0].Problems, " "), testPasswordEnv) {
		t.Fatalf("expected a missing credential problem, got %+v", results)
	}
}
