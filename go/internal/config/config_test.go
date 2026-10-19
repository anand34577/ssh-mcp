package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeConfig writes a known_hosts file and a configuration whose single
// server entry is a valid base plus extra JSON fields, returning the path.
func writeConfig(t *testing.T, extra string) string {
	t.Helper()
	dir := t.TempDir()
	knownHosts := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(knownHosts, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if extra != "" {
		extra = "," + extra
	}
	contents := `{"servers":[{"id":"one","host":"example.test","username":"ops","passwordEnv":"SSH_PASSWORD","knownHostsPath":"` + filepath.ToSlash(knownHosts) + `"` + extra + `}]}`
	path := filepath.Join(dir, "servers.json")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadAppliesDefaults(t *testing.T) {
	catalog, err := Load(writeConfig(t, `"privateKeyPath":"~/.ssh/id_ed25519","strictHostKeyChecking":null,"allowSftp":null`))
	if err != nil {
		t.Fatal(err)
	}
	server := catalog.Servers["one"]
	if !server.StrictHostKeyChecking || !server.AllowSFTP || server.AllowPortForwarding || server.Port != 22 {
		t.Fatalf("unexpected defaults: %+v", server)
	}
	if server.ConnectTimeoutMs != 10000 || server.CommandTimeoutMs != 60000 || server.MaxOutputBytes != 1048576 ||
		server.MaxConcurrentOperations != 4 || server.MaxConcurrentTunnels != 4 {
		t.Fatalf("unexpected limit defaults: %+v", server)
	}
	if strings.HasPrefix(server.PrivateKeyPath, "~") {
		t.Fatalf("home directory was not expanded: %s", server.PrivateKeyPath)
	}
	if cap(server.Slots) != 4 || cap(server.TunnelSlots) != 4 {
		t.Fatal("concurrency slots were not sized from the limits")
	}
}

func TestLoadAcceptsSchemaReference(t *testing.T) {
	data, err := os.ReadFile(writeConfig(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	withSchema := strings.Replace(string(data), "{", `{"$schema":"./servers.schema.json",`, 1)
	if _, err := Parse([]byte(withSchema)); err != nil {
		t.Fatalf("$schema was rejected: %v", err)
	}
}

func TestLoadRejectsInvalidServerEntries(t *testing.T) {
	cases := map[string]string{
		"inline secret":          `"password":"do-not-store"`,
		"case-variant secret":    `"PassPhrase":"x"`,
		"unknown field":          `"allowSFTP":false`,
		"explicit zero port":     `"port":0`,
		"port out of range":      `"port":70000`,
		"timeout too small":      `"commandTimeoutMs":50`,
		"bad regex":              `"deniedCommandRegexes":["("]`,
		"empty prefix":           `"allowedCommandPrefixes":[" "]`,
		"metacharacter prefix":   `"allowedCommandPrefixes":["ls;"]`,
		"env value not name":     `"passwordEnv":"hunter2 secret"`,
		"passphrase without key": `"privateKeyPassphraseEnv":"KEY_PASS"`,
		"wrong type":             `"maxOutputBytes":"big"`,
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, extra)); err == nil {
				t.Fatalf("configuration with %s was accepted", extra)
			}
		})
	}
}

func TestParseRejectsStructuralErrors(t *testing.T) {
	t.Setenv(EnvAllowInsecureHostKeys, "true")
	cases := map[string]string{
		"unknown root field": `{"servers":[],"unexpected":true}`,
		"no servers":         `{"servers":[]}`,
		"not an object":      `[1,2]`,
		"server not object":  `{"servers":[42]}`,
		"duplicate ids":      `{"servers":[{"id":"a","host":"h","username":"u","passwordEnv":"A","strictHostKeyChecking":false},{"id":"a","host":"h","username":"u","passwordEnv":"A","strictHostKeyChecking":false}]}`,
		"bad id":             `{"servers":[{"id":"has space","host":"h","username":"u","passwordEnv":"A","strictHostKeyChecking":false}]}`,
	}
	for name, contents := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(contents)); err == nil {
				t.Fatalf("%s was accepted", contents)
			}
		})
	}
}

func TestInsecureHostKeysRequireEnvironmentOverride(t *testing.T) {
	contents := []byte(`{"servers":[{"id":"a","host":"h","username":"u","passwordEnv":"A","strictHostKeyChecking":false}]}`)
	t.Setenv(EnvAllowInsecureHostKeys, "")
	if _, err := Parse(contents); err == nil {
		t.Fatal("strictHostKeyChecking=false was accepted without the override")
	}
	t.Setenv(EnvAllowInsecureHostKeys, "true")
	if _, err := Parse(contents); err != nil {
		t.Fatalf("override was not honored: %v", err)
	}
}

func TestLoadAllowsExplicitAutoAcceptModeWithoutKnownHosts(t *testing.T) {
	t.Setenv(EnvAutoAcceptHostKeys, "true")
	catalog, err := Parse([]byte(`{"servers":[{"id":"one","host":"example.test","username":"ops","passwordEnv":"SSH_PASSWORD"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !catalog.Servers["one"].StrictHostKeyChecking {
		t.Fatal("auto-accept mode unexpectedly changed the configured strict setting")
	}
}

func TestResolvePrecedenceAndMissingFileMessage(t *testing.T) {
	t.Setenv(EnvConfigPath, filepath.Join(t.TempDir(), "from-env.json"))
	if path, _ := Resolve("explicit.json"); path != "explicit.json" {
		t.Fatalf("explicit path not preferred: %s", path)
	}
	if path, searched := Resolve(""); !strings.HasSuffix(path, "from-env.json") || searched != nil {
		t.Fatalf("environment path not used: %s", path)
	}

	t.Setenv(EnvConfigPath, "")
	t.Chdir(t.TempDir())
	_, err := Load("")
	if err == nil || !strings.Contains(err.Error(), "searched") || !strings.Contains(err.Error(), EnvConfigPath) {
		t.Fatalf("missing-config error should list searched locations, got %v", err)
	}
}

func TestShippedExampleConfigurationIsValid(t *testing.T) {
	// The example references the operator's own known_hosts, which may not exist here.
	t.Setenv(EnvAutoAcceptHostKeys, "true")
	catalog, err := Load(filepath.Join("..", "..", "..", "config", "servers.example.json"))
	if err != nil {
		t.Fatalf("config/servers.example.json is invalid: %v", err)
	}
	if len(catalog.Servers) != 2 {
		t.Fatalf("expected 2 example servers, got %d", len(catalog.Servers))
	}
}

func TestExpand(t *testing.T) {
	home, _ := os.UserHomeDir()
	t.Setenv("SSH_MCP_TEST_DIR", "/opt/keys")
	cases := map[string]string{
		"${user.home}/k":        filepath.ToSlash(home) + "/k",
		"${SSH_MCP_TEST_DIR}/k": "/opt/keys/k",
		"~/k":                   filepath.ToSlash(filepath.Join(home, "k")),
		"/plain/path":           "/plain/path",
	}
	for input, want := range cases {
		if got := filepath.ToSlash(Expand(input)); got != want {
			t.Errorf("Expand(%q) = %q, want %q", input, got, want)
		}
	}
}
