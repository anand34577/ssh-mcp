package sshservice

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// FetchHostKey connects to host:port and returns the host key the server
// presents, without authenticating. Callers must show the fingerprint to the
// user and only record the key after the user confirms it.
func FetchHostKey(ctx context.Context, host string, port int, timeout time.Duration) (ssh.PublicKey, error) {
	address := net.JoinHostPort(host, strconv.Itoa(port))
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("cannot reach %s: %w", address, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	stop := context.AfterFunc(dialCtx, func() { _ = conn.Close() })
	defer stop()

	var hostKey ssh.PublicKey
	errReceived := errors.New("host key received")
	_, _, _, err = ssh.NewClientConn(conn, address, &ssh.ClientConfig{
		User: "ssh-mcp-host-key-probe",
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			hostKey = key
			return errReceived
		},
	})
	switch {
	case hostKey != nil:
		return hostKey, nil
	case dialCtx.Err() != nil:
		return nil, fmt.Errorf("%s did not answer as an SSH server within %s", address, timeout)
	default:
		return nil, fmt.Errorf("%s did not complete an SSH handshake: %w", address, err)
	}
}

// KnownHostKeyTypes returns the key types recorded for host:port in a
// known_hosts file. It returns nil when the file or the host entry is absent.
func KnownHostKeyTypes(knownHostsPath, host string, port int) ([]string, error) {
	if _, err := os.Stat(knownHostsPath); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	callback, err := knownhosts.New(knownHostsPath)
	if err != nil {
		return nil, fmt.Errorf("known_hosts file %s could not be loaded: %w", knownHostsPath, err)
	}
	var types []string
	for _, known := range knownHostKeys(callback, net.JoinHostPort(host, strconv.Itoa(port)), port) {
		types = append(types, known.Key.Type())
	}
	return types, nil
}

// TrustHostKey appends a known_hosts line for host:port, creating the file
// (and its directory) with owner-only permissions if needed.
func TrustHostKey(knownHostsPath, host string, port int, key ssh.PublicKey) error {
	if err := os.MkdirAll(filepath.Dir(knownHostsPath), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(knownHostsPath, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	// Start on a fresh line if the file does not end with a newline.
	prefix := ""
	if info, err := file.Stat(); err == nil && info.Size() > 0 {
		last := make([]byte, 1)
		if _, err := file.ReadAt(last, info.Size()-1); err == nil && last[0] != '\n' {
			prefix = "\n"
		}
	}
	line := knownhosts.Line([]string{knownhosts.Normalize(net.JoinHostPort(host, strconv.Itoa(port)))}, key)
	if _, err := file.WriteString(prefix + line + "\n"); err != nil {
		return err
	}
	return file.Close()
}
