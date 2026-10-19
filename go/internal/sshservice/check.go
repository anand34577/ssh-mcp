package sshservice

import (
	"context"
	"fmt"

	"example.com/ssh-mcp-server/internal/config"
)

// CheckResult is the setup diagnosis for one configured server.
type CheckResult struct {
	ServerID string
	Address  string
	Problems []string
	Warnings []string
	// Connected is true when connect was requested and authentication succeeded.
	Connected bool
}

// OK reports whether the server has no blocking problems.
func (r CheckResult) OK() bool { return len(r.Problems) == 0 }

// Check diagnoses every configured server without running any remote
// command: credentials are resolvable, key files parse, and the host is
// present in known_hosts. When connect is true it also performs the SSH
// handshake and authentication.
func (s *Service) Check(ctx context.Context, connect bool) []CheckResult {
	results := make([]CheckResult, 0, len(s.catalog.Servers))
	for _, server := range s.sortedServers() {
		results = append(results, s.checkServer(ctx, server, connect))
	}
	return results
}

func (s *Service) checkServer(ctx context.Context, server *config.ServerConfig, connect bool) CheckResult {
	result := CheckResult{ServerID: server.ID, Address: serverAddress(server)}
	if _, err := authMethods(server); err != nil {
		result.Problems = append(result.Problems, err.Error())
	}

	switch policy := effectiveHostKeyPolicy(server); policy {
	case "strict":
		callback, _, err := hostKeyPolicy(server)
		if err != nil {
			result.Problems = append(result.Problems, err.Error())
		} else if len(knownHostKeys(callback, result.Address, server.Port)) == 0 {
			result.Problems = append(result.Problems, fmt.Sprintf(
				"host %s is not in %s; verify its fingerprint, then run: ssh-keyscan -p %d %s >> %s",
				result.Address, server.KnownHostsPath, server.Port, server.Host, server.KnownHostsPath))
		}
	default:
		result.Warnings = append(result.Warnings, fmt.Sprintf("host-key verification is %s; connections are not protected against man-in-the-middle attacks", policy))
	}
	if server.AllowPortForwarding {
		result.Warnings = append(result.Warnings, "port forwarding is enabled")
	}

	if connect && result.OK() {
		client, err := s.connect(ctx, server)
		if err != nil {
			result.Problems = append(result.Problems, err.Error())
		} else {
			result.Connected = true
			_ = client.Close()
		}
	}
	return result
}
