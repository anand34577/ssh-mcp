package sshservice

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestFetchTrustAndRecognizeHostKey(t *testing.T) {
	server := startTestSSHServer(t)
	host, portText, _ := net.SplitHostPort(server.address)
	port, _ := strconv.Atoi(portText)

	key, err := FetchHostKey(context.Background(), host, port, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(key.Marshal(), server.hostKey.PublicKey().Marshal()) {
		t.Fatal("fetched key does not match the server's host key")
	}

	knownHosts := filepath.Join(t.TempDir(), "ssh", "known_hosts")
	if types, err := KnownHostKeyTypes(knownHosts, host, port); err != nil || types != nil {
		t.Fatalf("missing file: types=%v err=%v", types, err)
	}
	// A file without a trailing newline must not get two entries on one line.
	if err := os.MkdirAll(filepath.Dir(knownHosts), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(knownHosts, []byte("# existing comment"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := TrustHostKey(knownHosts, host, port, key); err != nil {
		t.Fatal(err)
	}
	types, err := KnownHostKeyTypes(knownHosts, host, port)
	if err != nil || len(types) != 1 || types[0] != key.Type() {
		t.Fatalf("types=%v err=%v", types, err)
	}

	// The recorded key is accepted by a normal, strictly verified connection.
	service := server.service(t, `"knownHostsPath": `+strconv.Quote(knownHosts))
	results := service.Check(context.Background(), true)
	if !results[0].OK() || !results[0].Connected {
		t.Fatalf("check after trusting: %+v", results[0])
	}
}

func TestFetchHostKeyReportsUnreachableAndNonSSH(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_, _ = conn.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"))
			conn.Close()
		}
	}()
	if _, err := FetchHostKey(context.Background(), "127.0.0.1", port, 2*time.Second); err == nil {
		t.Fatal("expected an error from a non-SSH service")
	}
	listener.Close()
	if _, err := FetchHostKey(context.Background(), "127.0.0.1", port, 2*time.Second); err == nil {
		t.Fatal("expected an error from a closed port")
	}
}
