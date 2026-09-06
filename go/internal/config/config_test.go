package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaultsAndRejectsInlineSecrets(t *testing.T) {
	dir := t.TempDir()
	knownHosts := filepath.Join(dir, "known_hosts")
	knownHostsJSON := filepath.ToSlash(knownHosts)
	if err := os.WriteFile(knownHosts, []byte(""), 0600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "servers.json")
	contents := `{"servers":[{"id":"one","host":"example.test","username":"ops","privateKeyPath":"~/.ssh/id_ed25519","knownHostsPath":"` + knownHostsJSON + `","strictHostKeyChecking":null,"allowSftp":null}]}`
	if err := os.WriteFile(configPath, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}

	catalog, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	server := catalog.Servers["one"]
	if server == nil {
		t.Fatal("server was not loaded")
	}
	if !server.StrictHostKeyChecking || !server.AllowSFTP || server.Port != 22 {
		t.Fatalf("unexpected defaults: %+v", server)
	}
	if server.MaxOutputBytes != 1048576 || server.MaxConcurrentOperations != 4 || server.MaxConcurrentTunnels != 4 {
		t.Fatalf("unexpected limit defaults: %+v", server)
	}

	secretConfig := filepath.Join(dir, "secret.json")
	secretContents := `{"servers":[{"id":"one","host":"example.test","username":"ops","password":"do-not-store"}]}`
	if err := os.WriteFile(secretConfig, []byte(secretContents), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(secretConfig); err == nil {
		t.Fatal("inline secret was accepted")
	}
}

func TestLoadRejectsDuplicateIDs(t *testing.T) {
	dir := t.TempDir()
	knownHosts := filepath.Join(dir, "known_hosts")
	knownHostsJSON := filepath.ToSlash(knownHosts)
	if err := os.WriteFile(knownHosts, []byte(""), 0600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "servers.json")
	contents := `{"servers":[{"id":"one","host":"a","username":"u","passwordEnv":"A","knownHostsPath":"` + knownHostsJSON + `"},{"id":"one","host":"b","username":"u","passwordEnv":"B","knownHostsPath":"` + knownHostsJSON + `"}]}`
	if err := os.WriteFile(configPath, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(configPath); err == nil {
		t.Fatal("duplicate server id was accepted")
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	dir := t.TempDir()
	knownHosts := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(knownHosts, []byte(""), 0600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "servers.json")
	contents := `{"servers":[{"id":"one","host":"example.test","username":"ops","passwordEnv":"SSH_PASSWORD","knownHostsPath":"` + filepath.ToSlash(knownHosts) + `","allowSFTP":false}]}`
	if err := os.WriteFile(configPath, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(configPath); err == nil {
		t.Fatal("unknown configuration field was accepted")
	}
}

func TestLoadRejectsUnknownRootFields(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "servers.json")
	contents := `{"servers":[],"unexpected":true}`
	if err := os.WriteFile(configPath, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(configPath); err == nil {
		t.Fatal("unknown root configuration field was accepted")
	}
}

func TestLoadAllowsExplicitAutoAcceptModeWithoutKnownHosts(t *testing.T) {
	t.Setenv("SSH_MCP_AUTO_ACCEPT_HOST_KEYS", "true")
	dir := t.TempDir()
	configPath := filepath.Join(dir, "servers.json")
	contents := `{"servers":[{"id":"one","host":"example.test","username":"ops","passwordEnv":"SSH_PASSWORD"}]}`
	if err := os.WriteFile(configPath, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	catalog, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !catalog.Servers["one"].StrictHostKeyChecking {
		t.Fatal("auto-accept mode unexpectedly changed the configured strict setting")
	}
}
