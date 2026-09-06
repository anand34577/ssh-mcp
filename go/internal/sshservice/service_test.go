package sshservice

import (
	"context"
	"testing"

	"example.com/ssh-mcp-server/internal/config"
)

func testService() *Service {
	server := &config.ServerConfig{
		ID:                      "test",
		Host:                    "127.0.0.1",
		Username:                "test",
		PasswordEnv:             "SSH_MCP_TEST_PASSWORD",
		StrictHostKeyChecking:   false,
		CommandTimeoutMs:        1000,
		ConnectTimeoutMs:        1000,
		MaxOutputBytes:          1024,
		MaxConcurrentOperations: 1,
		Slots:                   make(chan struct{}, 1),
		DeniedPatterns:          nil,
	}
	return New(&config.Catalog{Servers: map[string]*config.ServerConfig{"test": server}})
}

func TestExecRejectsDeniedCommandBeforeConnecting(t *testing.T) {
	service := testService()
	defer service.Close()
	server := service.catalog.Servers["test"]
	server.AllowedCommandPrefixes = []string{"printf"}

	if _, err := service.Exec(context.Background(), ExecRequest{ServerID: "test", Command: "rm -rf /"}); err == nil {
		t.Fatal("disallowed command was accepted")
	}
}

func TestTunnelRequiresLoopback(t *testing.T) {
	service := testService()
	defer service.Close()
	server := service.catalog.Servers["test"]
	server.AllowPortForwarding = true

	if _, err := service.OpenTunnel(context.Background(), TunnelOpenRequest{ServerID: "test", LocalHost: "0.0.0.0", RemoteHost: "127.0.0.1", RemotePort: 22}); err == nil {
		t.Fatal("non-loopback tunnel bind was accepted")
	}
}

func TestCommandPolicyRejectsShellOperators(t *testing.T) {
	service := testService()
	defer service.Close()
	server := service.catalog.Servers["test"]
	server.AllowedCommandPrefixes = []string{"systemctl status"}
	if err := checkCommandPolicy(server, "systemctl status ; rm -rf /"); err == nil {
		t.Fatal("shell operator was accepted by an allowlisted command")
	}
}

func TestCommandPolicyRequiresPrefixBoundary(t *testing.T) {
	service := testService()
	defer service.Close()
	server := service.catalog.Servers["test"]
	server.AllowedCommandPrefixes = []string{"systemctl status"}
	if err := checkCommandPolicy(server, "systemctl status-malicious"); err == nil {
		t.Fatal("prefix without a command boundary was accepted")
	}
	if err := checkCommandPolicy(server, "systemctl status\tunit"); err != nil {
		t.Fatalf("whitespace-separated command was rejected: %v", err)
	}
}
