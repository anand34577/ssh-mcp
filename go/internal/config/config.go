// Package config loads and validates the SSH MCP server configuration file.
//
// Validation is deliberately strict: unknown fields, inline secrets, and
// out-of-range limits are rejected at startup so a typo can never silently
// fall back to an insecure default.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Environment variables recognized by the server process.
const (
	EnvConfigPath            = "SSH_MCP_CONFIG"
	EnvAllowInsecureHostKeys = "SSH_MCP_ALLOW_INSECURE_HOST_KEYS"
	EnvAutoAcceptHostKeys    = "SSH_MCP_AUTO_ACCEPT_HOST_KEYS"
)

// ShellMetacharacters are rejected in commands (and allow-list prefixes) when
// an allow-list is configured, to prevent chaining or substitution past it.
const ShellMetacharacters = ";|&<>$`(){}'\"\\"

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
	MaxConcurrentTunnels    int      `json:"maxConcurrentTunnels"`
	AllowSFTP               bool     `json:"allowSftp"`
	AllowPortForwarding     bool     `json:"allowPortForwarding"`
	AllowedCommandPrefixes  []string `json:"allowedCommandPrefixes"`
	DeniedCommandRegexes    []string `json:"deniedCommandRegexes"`
	// ProtectSystem (default true) refuses commands and file changes that
	// could wipe, break, or switch off the server; see sshservice/guard.go.
	ProtectSystem bool `json:"protectSystem"`

	DeniedPatterns []*regexp.Regexp `json:"-"`
	// Slots bounds concurrent short-lived operations (ssh_exec, sftp_*).
	Slots chan struct{} `json:"-"`
	// TunnelSlots bounds concurrent long-lived port forwards separately from
	// Slots, so open tunnels cannot starve exec/SFTP operations on the same
	// server (and vice versa).
	TunnelSlots chan struct{} `json:"-"`
}

var allowedServerFields = map[string]struct{}{
	"id": {}, "host": {}, "port": {}, "username": {}, "passwordEnv": {},
	"privateKeyPath": {}, "privateKeyPassphraseEnv": {}, "knownHostsPath": {},
	"strictHostKeyChecking": {}, "connectTimeoutMs": {}, "commandTimeoutMs": {},
	"maxOutputBytes": {}, "maxConcurrentOperations": {}, "maxConcurrentTunnels": {}, "allowSftp": {},
	"allowPortForwarding": {}, "allowedCommandPrefixes": {}, "deniedCommandRegexes": {},
	"protectSystem": {},
}

// "$schema" lets editors offer completion and validation from
// config/servers.schema.json; it has no effect on the server.
var allowedRootFields = map[string]struct{}{
	"$schema": {},
	"servers": {},
}

var forbiddenSecretFields = []string{"password", "passphrase", "privateKey", "privateKeyContent", "secret"}

var (
	serverIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	envNamePattern  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
)

// Resolve picks the configuration file to load: an explicit path wins, then
// SSH_MCP_CONFIG, then the first existing default location. It also returns
// the default locations that were searched, for error reporting.
func Resolve(explicit string) (string, []string) {
	if path := strings.TrimSpace(explicit); path != "" {
		return filepath.Clean(Expand(path)), nil
	}
	if path := strings.TrimSpace(os.Getenv(EnvConfigPath)); path != "" {
		return filepath.Clean(Expand(path)), nil
	}
	candidates := DefaultLocations()
	for _, candidate := range candidates {
		if isRegularFile(candidate) {
			return candidate, candidates
		}
	}
	return candidates[0], candidates
}

// DefaultLocations lists where the server looks for servers.json when neither
// -config nor SSH_MCP_CONFIG is given, in priority order.
func DefaultLocations() []string {
	var locations []string
	if abs, err := filepath.Abs(filepath.Join("config", "servers.json")); err == nil {
		locations = append(locations, abs)
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		locations = append(locations, filepath.Join(dir, "servers.json"), filepath.Join(dir, "config", "servers.json"))
	}
	if path, err := UserConfigPath(); err == nil {
		locations = append(locations, path)
	}
	unique := locations[:0]
	for _, location := range locations {
		if !slices.Contains(unique, location) {
			unique = append(unique, location)
		}
	}
	return unique
}

// UserConfigPath is the per-user configuration file that "ssh-mcp-server
// setup" writes: %APPDATA%\ssh-mcp\servers.json on Windows,
// ~/Library/Application Support/ssh-mcp/servers.json on macOS, and
// $XDG_CONFIG_HOME/ssh-mcp/servers.json (or ~/.config/...) elsewhere.
func UserConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "ssh-mcp", "servers.json"), nil
}

func Load(explicit string) (*Catalog, error) {
	path, searched := Resolve(explicit)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && len(searched) > 0 {
			return nil, fmt.Errorf("no configuration file found; searched %s. Run \"ssh-mcp-server setup\" to create one, pass -config <path>, or set %s",
				strings.Join(searched, ", "), EnvConfigPath)
		}
		return nil, fmt.Errorf("configuration file %s: %w", path, err)
	}
	catalog, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	catalog.Source = path
	return catalog, nil
}

// Parse validates configuration file contents.
func Parse(data []byte) (*Catalog, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	if fields == nil {
		return nil, errors.New("configuration must be a JSON object")
	}
	for field := range fields {
		if _, ok := allowedRootFields[field]; !ok {
			return nil, fmt.Errorf("configuration contains unknown field %q", field)
		}
	}
	var servers []json.RawMessage
	if raw, ok := fields["servers"]; ok {
		if err := json.Unmarshal(raw, &servers); err != nil {
			return nil, fmt.Errorf("configuration servers must be an array: %w", err)
		}
	}
	if len(servers) == 0 {
		return nil, errors.New("configuration must contain at least one server in \"servers\"")
	}

	catalog := &Catalog{Servers: make(map[string]*ServerConfig, len(servers))}
	for index, raw := range servers {
		server, err := parseServer(raw)
		if err != nil {
			return nil, fmt.Errorf("servers[%d]: %w", index, err)
		}
		if _, exists := catalog.Servers[server.ID]; exists {
			return nil, fmt.Errorf("servers[%d]: duplicate server id %q", index, server.ID)
		}
		catalog.Servers[server.ID] = server
	}
	return catalog, nil
}

func parseServer(raw json.RawMessage) (*ServerConfig, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, errors.New("each server entry must be a JSON object")
	}
	for field := range fields {
		for _, forbidden := range forbiddenSecretFields {
			if strings.EqualFold(field, forbidden) {
				return nil, fmt.Errorf("secrets must not be stored directly in configuration; use an environment variable reference instead of %q", field)
			}
		}
		if _, ok := allowedServerFields[field]; !ok {
			return nil, fmt.Errorf("unknown field %q", field)
		}
	}

	var server ServerConfig
	if err := json.Unmarshal(raw, &server); err != nil {
		return nil, fmt.Errorf("invalid server entry: %w", err)
	}
	server.ID = strings.TrimSpace(server.ID)
	server.Host = strings.TrimSpace(server.Host)
	server.Username = strings.TrimSpace(server.Username)
	server.PasswordEnv = strings.TrimSpace(server.PasswordEnv)
	server.PrivateKeyPath = strings.TrimSpace(server.PrivateKeyPath)
	server.PrivateKeyPassphraseEnv = strings.TrimSpace(server.PrivateKeyPassphraseEnv)
	server.KnownHostsPath = strings.TrimSpace(server.KnownHostsPath)

	if server.ID == "" || server.Host == "" || server.Username == "" {
		return nil, errors.New("id, host, and username are required")
	}
	if !serverIDPattern.MatchString(server.ID) {
		return nil, fmt.Errorf("server id %q must match [A-Za-z0-9._-]{1,64}", server.ID)
	}
	// Accept "[::1]" as well as "::1"; the brackets are re-added when dialing.
	server.Host = strings.TrimSuffix(strings.TrimPrefix(server.Host, "["), "]")
	if strings.ContainsFunc(server.Host+server.Username, isSpaceOrControl) {
		return nil, fmt.Errorf("server %q host and username must not contain whitespace or control characters", server.ID)
	}
	if server.PasswordEnv == "" && server.PrivateKeyPath == "" {
		return nil, fmt.Errorf("server %q must configure passwordEnv, privateKeyPath, or both", server.ID)
	}
	if server.PasswordEnv != "" && !envNamePattern.MatchString(server.PasswordEnv) {
		return nil, fmt.Errorf("server %q passwordEnv must be an environment variable name, not a value", server.ID)
	}
	if server.PrivateKeyPassphraseEnv != "" && !envNamePattern.MatchString(server.PrivateKeyPassphraseEnv) {
		return nil, fmt.Errorf("server %q privateKeyPassphraseEnv must be an environment variable name, not a value", server.ID)
	}
	if server.PrivateKeyPassphraseEnv != "" && server.PrivateKeyPath == "" {
		return nil, fmt.Errorf("server %q sets privateKeyPassphraseEnv without privateKeyPath", server.ID)
	}

	if !hasNonNullField(fields, "strictHostKeyChecking") {
		server.StrictHostKeyChecking = true
	}
	autoAccept := AutoAcceptHostKeys()
	if server.StrictHostKeyChecking && server.KnownHostsPath == "" && !autoAccept {
		return nil, fmt.Errorf("server %q must configure knownHostsPath when strictHostKeyChecking is true", server.ID)
	}
	if !server.StrictHostKeyChecking && !AllowInsecureHostKeys() && !autoAccept {
		return nil, fmt.Errorf("server %q disables host-key checking; set %s=true only for an explicit exception", server.ID, EnvAllowInsecureHostKeys)
	}
	server.PrivateKeyPath = Expand(server.PrivateKeyPath)
	server.KnownHostsPath = Expand(server.KnownHostsPath)
	if server.StrictHostKeyChecking && !autoAccept && !isRegularFile(server.KnownHostsPath) {
		return nil, fmt.Errorf("server %q knownHostsPath is not a regular file: %s (create it with ssh-keyscan after verifying the host fingerprint)", server.ID, server.KnownHostsPath)
	}

	if !hasNonNullField(fields, "allowSftp") {
		server.AllowSFTP = true
	}
	if !hasNonNullField(fields, "protectSystem") {
		server.ProtectSystem = true
	}
	limits := []error{
		bounded(fields, server.ID, "port", &server.Port, 22, 1, 65535),
		bounded(fields, server.ID, "connectTimeoutMs", &server.ConnectTimeoutMs, 10000, 1000, 120000),
		bounded(fields, server.ID, "commandTimeoutMs", &server.CommandTimeoutMs, 60000, 100, 3600000),
		bounded(fields, server.ID, "maxOutputBytes", &server.MaxOutputBytes, 1048576, 1024, 104857600),
		bounded(fields, server.ID, "maxConcurrentOperations", &server.MaxConcurrentOperations, 4, 1, 64),
		bounded(fields, server.ID, "maxConcurrentTunnels", &server.MaxConcurrentTunnels, 4, 1, 64),
	}
	if err := errors.Join(limits...); err != nil {
		return nil, err
	}

	for i, expression := range server.DeniedCommandRegexes {
		if strings.TrimSpace(expression) == "" {
			return nil, fmt.Errorf("server %q deniedCommandRegexes[%d] must be non-empty", server.ID, i)
		}
		compiled, err := regexp.Compile(expression)
		if err != nil {
			return nil, fmt.Errorf("server %q deniedCommandRegexes[%d] is invalid: %w", server.ID, i, err)
		}
		server.DeniedPatterns = append(server.DeniedPatterns, compiled)
	}
	for i, prefix := range server.AllowedCommandPrefixes {
		prefix = strings.TrimSpace(prefix)
		if prefix == "" {
			return nil, fmt.Errorf("server %q allowedCommandPrefixes[%d] must be non-empty", server.ID, i)
		}
		if strings.ContainsAny(prefix, ShellMetacharacters) {
			return nil, fmt.Errorf("server %q allowedCommandPrefixes[%d] contains shell metacharacters and could never match", server.ID, i)
		}
		server.AllowedCommandPrefixes[i] = prefix
	}
	server.Slots = make(chan struct{}, server.MaxConcurrentOperations)
	server.TunnelSlots = make(chan struct{}, server.MaxConcurrentTunnels)
	return &server, nil
}

// bounded applies a default when the field is absent or null, and otherwise
// requires the value to be within [low, high]. An explicit 0 is rejected
// rather than silently treated as "use the default".
func bounded[T int | int64](fields map[string]json.RawMessage, id, name string, value *T, def, low, high T) error {
	if !hasNonNullField(fields, name) {
		*value = def
		return nil
	}
	if *value < low || *value > high {
		return fmt.Errorf("server %q %s must be between %d and %d", id, name, low, high)
	}
	return nil
}

// AutoAcceptHostKeys reports whether host-key verification is disabled for
// every target by the process-wide emergency switch.
func AutoAcceptHostKeys() bool {
	return envTrue(EnvAutoAcceptHostKeys)
}

// AllowInsecureHostKeys reports whether per-server strictHostKeyChecking:false
// entries are permitted.
func AllowInsecureHostKeys() bool {
	return envTrue(EnvAllowInsecureHostKeys)
}

func envTrue(name string) bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(name)), "true")
}

func hasNonNullField(fields map[string]json.RawMessage, name string) bool {
	raw, present := fields[name]
	return present && strings.TrimSpace(string(raw)) != "null"
}

func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func isSpaceOrControl(r rune) bool {
	return r <= ' ' || r == 0x7f
}

// Expand resolves ${user.home}, ${ENV_VAR}, and a leading ~/ in a path.
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
	if value == "~" {
		return home
	}
	if strings.HasPrefix(value, "~/") || strings.HasPrefix(value, `~\`) {
		value = filepath.Join(home, value[2:])
	}
	return value
}
