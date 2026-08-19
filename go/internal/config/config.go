package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Catalog struct {
	Source  string
	Servers map[string]*ServerConfig
}

type ServerConfig struct {
	ID                      string   `json:"id"`
	Host                    string   `json:"host"`
	Port                    int      `json:"port"`
	Username                string   `json:"username"`
	PasswordEnv             string   `json:"passwordEnv"`
	PrivateKeyPath          string   `json:"privateKeyPath"`
	PrivateKeyPassphraseEnv string   `json:"privateKeyPassphraseEnv"`
	KnownHostsPath          string   `json:"knownHostsPath"`
	StrictHostKeyChecking   bool     `json:"strictHostKeyChecking"`
	ConnectTimeoutMs        int64    `json:"connectTimeoutMs"`
	CommandTimeoutMs        int64    `json:"commandTimeoutMs"`
	MaxOutputBytes          int      `json:"maxOutputBytes"`
	MaxConcurrentOperations int      `json:"maxConcurrentOperations"`
	AllowSFTP               bool     `json:"allowSftp"`
	AllowPortForwarding     bool     `json:"allowPortForwarding"`
	AllowedCommandPrefixes  []string `json:"allowedCommandPrefixes"`
	DeniedCommandRegexes    []string `json:"deniedCommandRegexes"`

	DeniedPatterns []*regexp.Regexp `json:"-"`
	Slots          chan struct{}    `json:"-"`
}

func Load(path string) (*Catalog, error) {
	if strings.TrimSpace(path) == "" {
		path = os.Getenv("SSH_MCP_CONFIG")
	}
	if strings.TrimSpace(path) == "" {
		path = "config/servers.json"
	}
	path = filepath.Clean(Expand(path))
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("configuration file %s: %w", path, err)
	}

	var root struct {
		Servers []json.RawMessage `json:"servers"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse configuration: %w", err)
	}
	if root.Servers == nil || len(root.Servers) == 0 {
		return nil, fmt.Errorf("configuration must contain at least one server")
	}

	catalog := &Catalog{Source: path, Servers: make(map[string]*ServerConfig, len(root.Servers))}
	for _, raw := range root.Servers {
		server, err := parseServer(raw)
		if err != nil {
			return nil, err
		}
		if _, exists := catalog.Servers[server.ID]; exists {
			return nil, fmt.Errorf("duplicate server id: %s", server.ID)
		}
		catalog.Servers[server.ID] = server
	}
	return catalog, nil
}

func parseServer(raw json.RawMessage) (*ServerConfig, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("each server entry must be an object: %w", err)
	}
	for field := range fields {
		for _, forbidden := range []string{"password", "passphrase", "privateKey", "privateKeyContent", "secret"} {
			if strings.EqualFold(field, forbidden) {
				return nil, fmt.Errorf("secrets must not be stored directly in configuration; use an environment variable reference for %q", field)
			}
		}
	}

	var server ServerConfig
	if err := json.Unmarshal(raw, &server); err != nil {
		return nil, fmt.Errorf("parse server entry: %w", err)
	}
	server.ID = strings.TrimSpace(server.ID)
	server.Host = strings.TrimSpace(server.Host)
	server.Username = strings.TrimSpace(server.Username)
	server.PasswordEnv = strings.TrimSpace(server.PasswordEnv)
	server.PrivateKeyPath = strings.TrimSpace(server.PrivateKeyPath)
	server.PrivateKeyPassphraseEnv = strings.TrimSpace(server.PrivateKeyPassphraseEnv)
	server.KnownHostsPath = strings.TrimSpace(server.KnownHostsPath)

	if server.ID == "" || server.Host == "" || server.Username == "" {
		return nil, fmt.Errorf("id, host, and username are required")
	}
	if !regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`).MatchString(server.ID) {
		return nil, fmt.Errorf("server id contains unsupported characters: %s", server.ID)
	}
	if server.Port == 0 {
		server.Port = 22
	}
	if server.Port < 1 || server.Port > 65535 {
		return nil, fmt.Errorf("server %q port must be between 1 and 65535", server.ID)
	}
	if server.PasswordEnv == "" && server.PrivateKeyPath == "" {
		return nil, fmt.Errorf("server %q must configure passwordEnv or privateKeyPath", server.ID)
	}
	if server.PasswordEnv != "" && !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`).MatchString(server.PasswordEnv) {
		return nil, fmt.Errorf("server %q has an invalid passwordEnv name", server.ID)
	}
	if server.PrivateKeyPassphraseEnv != "" && !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`).MatchString(server.PrivateKeyPassphraseEnv) {
		return nil, fmt.Errorf("server %q has an invalid privateKeyPassphraseEnv name", server.ID)
	}

	if _, present := fields["strictHostKeyChecking"]; !present {
		server.StrictHostKeyChecking = true
	}
	if server.StrictHostKeyChecking && server.KnownHostsPath == "" {
		return nil, fmt.Errorf("server %q must configure knownHostsPath when strictHostKeyChecking is true", server.ID)
	}
	if !server.StrictHostKeyChecking && !strings.EqualFold(os.Getenv("SSH_MCP_ALLOW_INSECURE_HOST_KEYS"), "true") {
		return nil, fmt.Errorf("server %q disables host-key checking; set SSH_MCP_ALLOW_INSECURE_HOST_KEYS=true only for an explicit exception", server.ID)
	}
	server.PrivateKeyPath = Expand(server.PrivateKeyPath)
	server.KnownHostsPath = Expand(server.KnownHostsPath)
	if server.StrictHostKeyChecking {
		info, err := os.Stat(server.KnownHostsPath)
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("server %q knownHostsPath is not a regular file: %s", server.ID, server.KnownHostsPath)
		}
	}

	if server.ConnectTimeoutMs == 0 {
		server.ConnectTimeoutMs = 10000
	}
	if server.ConnectTimeoutMs < 1000 || server.ConnectTimeoutMs > 120000 {
		return nil, fmt.Errorf("server %q connectTimeoutMs must be between 1000 and 120000", server.ID)
	}
	if server.CommandTimeoutMs == 0 {
		server.CommandTimeoutMs = 60000
	}
	if server.CommandTimeoutMs < 100 || server.CommandTimeoutMs > 3600000 {
		return nil, fmt.Errorf("server %q commandTimeoutMs must be between 100 and 3600000", server.ID)
	}
	if server.MaxOutputBytes == 0 {
		server.MaxOutputBytes = 1048576
	}
	if server.MaxOutputBytes < 1024 || server.MaxOutputBytes > 104857600 {
		return nil, fmt.Errorf("server %q maxOutputBytes must be between 1024 and 104857600", server.ID)
	}
	if server.MaxConcurrentOperations == 0 {
		server.MaxConcurrentOperations = 4
	}
	if server.MaxConcurrentOperations < 1 || server.MaxConcurrentOperations > 64 {
		return nil, fmt.Errorf("server %q maxConcurrentOperations must be between 1 and 64", server.ID)
	}
	if _, present := fields["allowSftp"]; !present {
		server.AllowSFTP = true
	}

	for _, expression := range server.DeniedCommandRegexes {
		compiled, err := regexp.Compile(expression)
		if err != nil {
			return nil, fmt.Errorf("server %q has invalid denied command regex: %w", server.ID, err)
		}
		server.DeniedPatterns = append(server.DeniedPatterns, compiled)
	}
	server.Slots = make(chan struct{}, server.MaxConcurrentOperations)
	return &server, nil
}

func Expand(value string) string {
	if value == "" {
		return value
	}
	home, _ := os.UserHomeDir()
	value = os.Expand(value, func(key string) string {
		if key == "user.home" {
			return home
		}
		return os.Getenv(key)
	})
	if strings.HasPrefix(value, "~/") || strings.HasPrefix(value, `~\`) {
		value = filepath.Join(home, value[2:])
	}
	return value
}
