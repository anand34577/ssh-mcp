package sshservice

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"example.com/ssh-mcp-server/internal/config"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// connect dials the server and completes the SSH handshake and
// authentication within connectTimeoutMs, honoring ctx cancellation.
func (s *Service) connect(ctx context.Context, server *config.ServerConfig) (*ssh.Client, error) {
	hostKeyCallback, hostKeyAlgorithms, err := hostKeyPolicy(server)
	if err != nil {
		return nil, err
	}
	auth, err := authMethods(server)
	if err != nil {
		return nil, err
	}
	timeout := time.Duration(server.ConnectTimeoutMs) * time.Millisecond
	sshConfig := &ssh.ClientConfig{
		User:              server.Username,
		Auth:              auth,
		HostKeyCallback:   hostKeyCallback,
		HostKeyAlgorithms: hostKeyAlgorithms,
		Timeout:           timeout,
	}

	address := serverAddress(server)
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	rawConn, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", address)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("cannot reach %s: %w", address, err)
	}
	// ssh.NewClientConn ignores ClientConfig.Timeout and contexts, so bound
	// the handshake with a socket deadline and close the socket on cancel.
	_ = rawConn.SetDeadline(time.Now().Add(timeout))
	stop := context.AfterFunc(dialCtx, func() { _ = rawConn.Close() })
	conn, channels, requests, err := ssh.NewClientConn(rawConn, address, sshConfig)
	if !stop() && err == nil {
		_ = conn.Close()
		err = dialCtx.Err()
	}
	if err != nil {
		_ = rawConn.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if dialCtx.Err() != nil {
			return nil, fmt.Errorf("SSH handshake with %s timed out after %dms", address, server.ConnectTimeoutMs)
		}
		return nil, describeHandshakeError(server, address, err)
	}
	_ = rawConn.SetDeadline(time.Time{})
	return ssh.NewClient(conn, channels, requests), nil
}

func serverAddress(server *config.ServerConfig) string {
	return net.JoinHostPort(server.Host, strconv.Itoa(server.Port))
}

// hostKeyPolicy returns the host-key callback for a server and, when
// verifying against known_hosts, the host-key algorithms recorded there.
// Restricting negotiation to recorded algorithms avoids spurious failures
// when the server prefers a key type that is not in known_hosts.
func hostKeyPolicy(server *config.ServerConfig) (ssh.HostKeyCallback, []string, error) {
	if !server.StrictHostKeyChecking || config.AutoAcceptHostKeys() {
		return ssh.InsecureIgnoreHostKey(), nil, nil
	}
	callback, err := knownhosts.New(server.KnownHostsPath)
	if err != nil {
		return nil, nil, fmt.Errorf("known_hosts file %s could not be loaded: %w", server.KnownHostsPath, err)
	}
	return callback, knownHostAlgorithms(callback, serverAddress(server), server.Port), nil
}

// probeKey is a throwaway key that never matches a known_hosts entry; checking
// it makes knownhosts report every key it does have for an address.
var probeKey = sync.OnceValue(func() ssh.PublicKey {
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil
	}
	key, _ := ssh.NewPublicKey(public)
	return key
})

// knownHostKeys returns the keys recorded for address. A nil slice means the
// host is not present in known_hosts.
func knownHostKeys(callback ssh.HostKeyCallback, address string, port int) []knownhosts.KnownKey {
	key := probeKey()
	if key == nil {
		return nil
	}
	var keyErr *knownhosts.KeyError
	if err := callback(address, &net.TCPAddr{IP: net.IPv4zero, Port: port}, key); errors.As(err, &keyErr) {
		return keyErr.Want
	}
	return nil
}

func knownHostAlgorithms(callback ssh.HostKeyCallback, address string, port int) []string {
	var algorithms []string
	seen := make(map[string]bool)
	add := func(names ...string) {
		for _, name := range names {
			if !seen[name] {
				seen[name] = true
				algorithms = append(algorithms, name)
			}
		}
	}
	for _, known := range knownHostKeys(callback, address, port) {
		if known.Key.Type() == ssh.KeyAlgoRSA {
			add(ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA)
		} else {
			add(known.Key.Type())
		}
	}
	return algorithms
}

func authMethods(server *config.ServerConfig) ([]ssh.AuthMethod, error) {
	methods := make([]ssh.AuthMethod, 0, 3)
	if server.PrivateKeyPath != "" {
		signer, err := loadSigner(server)
		if err != nil {
			return nil, err
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}
	if server.PasswordEnv != "" {
		password := os.Getenv(server.PasswordEnv)
		if password == "" {
			return nil, fmt.Errorf("environment variable %s (passwordEnv for server %q) is not set or empty in the MCP server process", server.PasswordEnv, server.ID)
		}
		// Many servers (PAM-backed sshd, network appliances) only advertise
		// keyboard-interactive, even for a plain password login.
		methods = append(methods, ssh.Password(password), ssh.KeyboardInteractive(passwordChallenge(password)))
	}
	if len(methods) == 0 {
		return nil, errors.New("no SSH authentication method is configured")
	}
	return methods, nil
}

// passwordChallenge answers a keyboard-interactive round only when it is a
// single hidden (password-style) prompt, so the password is never sent in
// reply to an echoed prompt such as a username or one-time code.
func passwordChallenge(password string) ssh.KeyboardInteractiveChallenge {
	return func(_, _ string, questions []string, echos []bool) ([]string, error) {
		switch {
		case len(questions) == 0:
			return nil, nil
		case len(questions) == 1 && !echos[0]:
			return []string{password}, nil
		default:
			return nil, errors.New("keyboard-interactive challenge is not a single password prompt (multi-factor prompts are not supported)")
		}
	}
}

func loadSigner(server *config.ServerConfig) (ssh.Signer, error) {
	keyData, err := os.ReadFile(filepath.Clean(server.PrivateKeyPath))
	if err != nil {
		return nil, fmt.Errorf("private key for server %q could not be read: %w", server.ID, err)
	}
	if server.PrivateKeyPassphraseEnv != "" {
		passphrase := os.Getenv(server.PrivateKeyPassphraseEnv)
		if passphrase == "" {
			return nil, fmt.Errorf("environment variable %s (privateKeyPassphraseEnv for server %q) is not set or empty in the MCP server process", server.PrivateKeyPassphraseEnv, server.ID)
		}
		signer, err := ssh.ParsePrivateKeyWithPassphrase(keyData, []byte(passphrase))
		if err != nil {
			return nil, fmt.Errorf("private key %s could not be decrypted or parsed: %w", server.PrivateKeyPath, err)
		}
		return signer, nil
	}
	signer, err := ssh.ParsePrivateKey(keyData)
	var missing *ssh.PassphraseMissingError
	if errors.As(err, &missing) {
		return nil, fmt.Errorf("private key %s is encrypted; set privateKeyPassphraseEnv for server %q", server.PrivateKeyPath, server.ID)
	}
	if err != nil {
		return nil, fmt.Errorf("private key %s could not be parsed: %w", server.PrivateKeyPath, err)
	}
	return signer, nil
}

func describeHandshakeError(server *config.ServerConfig, address string, err error) error {
	var keyErr *knownhosts.KeyError
	if errors.As(err, &keyErr) {
		if len(keyErr.Want) == 0 {
			return fmt.Errorf("host key for %s is not in %s; verify the server fingerprint out of band, then add it (for example: ssh-keyscan -p %d %s >> %s)",
				address, server.KnownHostsPath, server.Port, server.Host, server.KnownHostsPath)
		}
		return fmt.Errorf("HOST KEY MISMATCH for %s: the server presented a key that differs from %s. This can indicate a man-in-the-middle attack; verify the new fingerprint out of band before updating known_hosts",
			address, server.KnownHostsPath)
	}
	var revoked *knownhosts.RevokedError
	if errors.As(err, &revoked) {
		return fmt.Errorf("host key for %s is marked @revoked in %s", address, server.KnownHostsPath)
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return fmt.Errorf("SSH handshake with %s timed out after %dms", address, server.ConnectTimeoutMs)
	}
	return fmt.Errorf("SSH handshake with %s failed: %w", address, err)
}
