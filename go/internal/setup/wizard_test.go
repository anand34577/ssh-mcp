package setup

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"example.com/ssh-mcp-server/internal/config"
	"example.com/ssh-mcp-server/internal/sshservice"
	"golang.org/x/crypto/ssh"
)

type fixture struct {
	wizard     *Wizard
	out        *strings.Builder
	home       string
	appDir     string
	configPath string
	execCalls  [][]string
	testedWith map[string]string
}

// newFixture builds a wizard over temporary directories. The test server
// always reports success unless failSignIn is set.
func newFixture(t *testing.T, input string, failSignIn bool) *fixture {
	t.Helper()
	root := t.TempDir()
	f := &fixture{
		out:        &strings.Builder{},
		home:       filepath.Join(root, "home"),
		appDir:     filepath.Join(root, "appdata"),
		configPath: filepath.Join(root, "config", "servers.json"),
		testedWith: map[string]string{},
	}
	for _, dir := range []string{f.home, f.appDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// config.Expand resolves "~/" with the real home directory lookup.
	t.Setenv("HOME", f.home)
	t.Setenv("USERPROFILE", f.home)
	t.Setenv("CODEX_HOME", "")

	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostKey, err := ssh.NewPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	f.wizard = &Wizard{
		In:         bufio.NewReader(strings.NewReader(input)),
		Out:        f.out,
		ConfigPath: f.configPath,
		Executable: filepath.Join(root, "bin", "ssh-mcp-server"),
		Home:       f.home,
		AppDir:     f.appDir,
		Username:   "me",
		LookPath:   func(string) (string, error) { return "", exec.ErrNotFound },
		Exec: func(name string, args ...string) error {
			f.execCalls = append(f.execCalls, append([]string{name}, args...))
			return nil
		},
		FetchHostKey: func(context.Context, string, int) (ssh.PublicKey, error) { return hostKey, nil },
		TestServer: func(_ context.Context, server *config.ServerConfig) sshservice.CheckResult {
			for _, name := range []string{server.PasswordEnv, server.PrivateKeyPassphraseEnv} {
				if name != "" {
					f.testedWith[name] = os.Getenv(name)
				}
			}
			result := sshservice.CheckResult{ServerID: server.ID}
			if failSignIn {
				result.Problems = []string{"ssh: handshake failed: ssh: unable to authenticate"}
			}
			return result
		},
	}
	return f
}

func (f *fixture) run(t *testing.T) {
	t.Helper()
	if err := f.wizard.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v\noutput:\n%s", err, f.out)
	}
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return value
}

func TestFirstRunAddsPasswordServerAndConnectsApps(t *testing.T) {
	answers := strings.Join([]string{
		"deploy@web.example.com:2222", // address with user and port
		"",                            // keep the suggested name "web"
		"1",                           // sign in with a password (no key files exist)
		"",                            // full access
		"y",                           // trust the fingerprint
		"s3cret",                      // password for the sign-in test
		"y", "y", "",                  // Claude Desktop, Claude Code, Cursor
	}, "\n") + "\n"
	f := newFixture(t, answers, false)
	if err := os.MkdirAll(filepath.Join(f.appDir, "Claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	cursorDir := filepath.Join(f.home, ".cursor")
	if err := os.MkdirAll(cursorDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cursorConfig := filepath.Join(cursorDir, "mcp.json")
	if err := os.WriteFile(cursorConfig, []byte(`{"mcpServers":{"other":{"command":"other"}},"theme":"dark"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	f.wizard.LookPath = func(name string) (string, error) {
		if name == "claude" {
			return "/usr/local/bin/claude", nil
		}
		return "", exec.ErrNotFound
	}
	f.run(t)

	catalog, err := config.Load(f.configPath)
	if err != nil {
		t.Fatalf("saved settings do not load: %v\n%s", err, f.out)
	}
	server := catalog.Servers["web"]
	if server == nil || server.Host != "web.example.com" || server.Port != 2222 || server.Username != "deploy" ||
		server.PasswordEnv != "SSH_MCP_WEB_PASSWORD" || !server.AllowSFTP {
		t.Fatalf("unexpected server: %+v", server)
	}
	if raw, _ := os.ReadFile(f.configPath); strings.Contains(string(raw), "s3cret") {
		t.Fatal("the password was written to servers.json")
	}
	knownHosts, err := os.ReadFile(filepath.Join(f.home, ".ssh", "known_hosts"))
	if err != nil || !strings.HasPrefix(string(knownHosts), "[web.example.com]:2222 ssh-ed25519 ") {
		t.Fatalf("known_hosts = %q, %v", knownHosts, err)
	}
	if f.testedWith["SSH_MCP_WEB_PASSWORD"] != "s3cret" {
		t.Fatalf("sign-in test did not receive the password: %v", f.testedWith)
	}
	if _, set := os.LookupEnv("SSH_MCP_WEB_PASSWORD"); set {
		t.Fatal("the password was left in the process environment")
	}

	claude := readJSON(t, filepath.Join(f.appDir, "Claude", "claude_desktop_config.json"))
	entry := claude["mcpServers"].(map[string]any)["ssh"].(map[string]any)
	if entry["command"] != f.wizard.Executable || entry["env"].(map[string]any)["SSH_MCP_WEB_PASSWORD"] != "s3cret" {
		t.Fatalf("Claude Desktop entry = %v", entry)
	}
	if args := entry["args"].([]any); len(args) != 2 || args[0] != "-config" || args[1] != f.configPath {
		t.Fatalf("Claude Desktop args = %v", args)
	}

	cursor := readJSON(t, cursorConfig)
	servers := cursor["mcpServers"].(map[string]any)
	if servers["other"] == nil || servers["ssh"] == nil || cursor["theme"] != "dark" {
		t.Fatalf("Cursor settings not merged: %v", cursor)
	}
	if _, err := os.Stat(cursorConfig + ".bak"); err != nil {
		t.Fatalf("no backup of the Cursor settings: %v", err)
	}

	if len(f.execCalls) != 2 {
		t.Fatalf("claude calls = %v", f.execCalls)
	}
	add := strings.Join(f.execCalls[1], " ")
	for _, want := range []string{"mcp add --scope user ssh", "-e SSH_MCP_WEB_PASSWORD=s3cret", "-- " + f.wizard.Executable + " -config " + f.configPath} {
		if !strings.Contains(add, want) {
			t.Fatalf("claude mcp add = %q, missing %q", add, want)
		}
	}
	if !strings.Contains(f.out.String(), "All set!") {
		t.Fatalf("missing closing message:\n%s", f.out)
	}
}

func TestDecliningTheFingerprintSavesNothing(t *testing.T) {
	f := newFixture(t, "example.com\n\n\n\n1\n\nn\n", false)
	f.run(t)
	if _, err := os.Stat(f.configPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("settings were written: %v", err)
	}
	if !strings.Contains(f.out.String(), "No server was added") {
		t.Fatalf("unexpected output:\n%s", f.out)
	}
}

func TestLookOnlyServerWithEncryptedKey(t *testing.T) {
	answers := strings.Join([]string{
		"admin@10.0.0.5", "", // address, default port
		"",           // suggested name server-10-0-0-5
		"1",          // the detected key file
		"2",          // look only
		"y",          // trust
		"passphrase", // key passphrase for the test
		"",           // save anyway after the failed sign-in
	}, "\n") + "\n"
	f := newFixture(t, answers, true)
	_, private, _ := ed25519.GenerateKey(rand.Reader)
	block, err := ssh.MarshalPrivateKeyWithPassphrase(private, "", []byte("passphrase"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(f.home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.home, ".ssh", "id_ed25519"), pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	f.run(t)

	catalog, err := config.Load(f.configPath)
	if err != nil {
		t.Fatalf("saved settings do not load: %v\n%s", err, f.out)
	}
	server := catalog.Servers["server-10-0-0-5"]
	if server == nil || server.AllowSFTP || server.PrivateKeyPassphraseEnv != "SSH_MCP_SERVER_10_0_0_5_KEY_PASSPHRASE" ||
		!slices.Contains(server.AllowedCommandPrefixes, "df") || len(server.DeniedPatterns) == 0 {
		t.Fatalf("unexpected server: %+v", server)
	}
	output := f.out.String()
	if !strings.Contains(output, "ssh-copy-id") || !strings.Contains(output, "Paste this into your AI app") {
		t.Fatalf("missing sign-in tip or manual instructions:\n%s", output)
	}
	if strings.Contains(output, `"passphrase"`) {
		t.Fatal("a passphrase that failed to sign in was offered to AI apps")
	}
}

func TestMenuRemovesServer(t *testing.T) {
	f := newFixture(t, "4\n1\ny\n5\n", false)
	knownHosts := filepath.Join(f.home, ".ssh", "known_hosts")
	if err := os.MkdirAll(filepath.Dir(knownHosts), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(knownHosts, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	settings := `{"servers":[
		{"id":"alpha","host":"a.example.com","username":"u","passwordEnv":"A","knownHostsPath":"~/.ssh/known_hosts"},
		{"id":"beta","host":"b.example.com","username":"u","passwordEnv":"B","knownHostsPath":"~/.ssh/known_hosts","commandTimeoutMs":5000}
	]}`
	if err := writeFileAtomic(f.configPath, []byte(settings)); err != nil {
		t.Fatal(err)
	}
	f.run(t)

	catalog, err := config.Load(f.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Servers) != 1 || catalog.Servers["beta"] == nil || catalog.Servers["beta"].CommandTimeoutMs != 5000 {
		t.Fatalf("unexpected servers after removal: %v", catalog.Servers)
	}
}

func TestEndOfInputCancels(t *testing.T) {
	f := newFixture(t, "", false)
	if err := f.wizard.Run(context.Background()); !errors.Is(err, ErrCancelled) {
		t.Fatalf("Run = %v, want ErrCancelled", err)
	}
}

func TestOfferInstallCopiesProgramFromDownloads(t *testing.T) {
	f := newFixture(t, "y\n", false)
	downloads := filepath.Join(f.home, "Downloads")
	if err := os.MkdirAll(downloads, 0o700); err != nil {
		t.Fatal(err)
	}
	f.wizard.Executable = filepath.Join(downloads, "ssh-mcp-server-linux-amd64")
	if err := os.WriteFile(f.wizard.Executable, []byte("program"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.wizard.InstallDir = filepath.Join(f.home, ".local", "bin")
	if err := f.wizard.offerInstall(); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(f.wizard.InstallDir, "ssh-mcp-server")
	if f.wizard.Executable != want {
		t.Fatalf("Executable = %q, want %q", f.wizard.Executable, want)
	}
	if data, err := os.ReadFile(want); err != nil || string(data) != "program" {
		t.Fatalf("installed copy = %q, %v", data, err)
	}
}

func TestParseAddress(t *testing.T) {
	tests := []struct {
		input, user, host string
		port              int
		bad               bool
	}{
		{input: "example.com", host: "example.com"},
		{input: " me@example.com ", user: "me", host: "example.com"},
		{input: "ssh://me@example.com:2222", user: "me", host: "example.com", port: 2222},
		{input: "10.0.0.1:22", host: "10.0.0.1", port: 22},
		{input: "[::1]:2200", host: "::1", port: 2200},
		{input: "::1", host: "::1"},
		{input: "a@b@example.com", user: "a@b", host: "example.com"},
		{input: "example.com:0", bad: true},
		{input: "example.com:ssh", bad: true},
		{input: "me@", bad: true},
		{input: "my server", bad: true},
	}
	for _, tt := range tests {
		user, host, port, err := parseAddress(tt.input)
		if tt.bad {
			if err == nil {
				t.Errorf("parseAddress(%q) = %q %q %d, want error", tt.input, user, host, port)
			}
			continue
		}
		if err != nil || user != tt.user || host != tt.host || port != tt.port {
			t.Errorf("parseAddress(%q) = %q %q %d %v", tt.input, user, host, port, err)
		}
	}
}

func TestSuggestIDAndEnvName(t *testing.T) {
	if got := suggestID("web.example.com", []string{"web"}); got != "web-2" {
		t.Errorf("suggestID = %q", got)
	}
	if got := suggestID("2001:db8::1", nil); got != "server-2001-db8--1" {
		t.Errorf("suggestID(ipv6) = %q", got)
	}
	if got := suggestID("!!!", nil); got != "server" {
		t.Errorf("suggestID(symbols) = %q", got)
	}
	if got := envName("my-box.1", "PASSWORD"); got != "SSH_MCP_MY_BOX_1_PASSWORD" {
		t.Errorf("envName = %q", got)
	}
}

func TestConnectAppLeavesFilesWithCommentsAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	original := "// my settings\n{\"servers\": {}}\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	w := &Wizard{Executable: "/bin/ssh-mcp-server", ConfigPath: "/etc/servers.json"}
	if err := w.connectApp(App{Name: "VS Code", Path: path, Key: "servers", VSCode: true}, nil); err == nil {
		t.Fatal("expected an error for a file with comments")
	}
	if data, _ := os.ReadFile(path); string(data) != original {
		t.Fatalf("file was modified: %q", data)
	}

	fresh := filepath.Join(dir, "new", "mcp.json")
	if err := w.connectApp(App{Name: "VS Code", Path: fresh, Key: "servers", VSCode: true}, nil); err != nil {
		t.Fatal(err)
	}
	entry := readJSON(t, fresh)["servers"].(map[string]any)["ssh"].(map[string]any)
	if entry["type"] != "stdio" || entry["env"] != nil {
		t.Fatalf("VS Code entry = %v", entry)
	}
}

func TestConnectReusesSavedPasswordsAndSupportsCodex(t *testing.T) {
	f := newFixture(t, "", false)
	knownHosts := filepath.Join(f.home, ".ssh", "known_hosts")
	if err := os.MkdirAll(filepath.Dir(knownHosts), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(knownHosts, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	settings := `{"servers":[{"id":"web","host":"web.example.com","username":"u","passwordEnv":"SSH_MCP_WEB_PASSWORD","knownHostsPath":"~/.ssh/known_hosts"}]}`
	if err := writeFileAtomic(f.configPath, []byte(settings)); err != nil {
		t.Fatal(err)
	}
	// Claude Desktop already has the password; Codex is new and has other settings.
	claudePath := filepath.Join(f.appDir, "Claude", "claude_desktop_config.json")
	if err := writeFileAtomic(claudePath, []byte(`{"mcpServers":{"ssh":{"command":"/old/place/ssh-mcp-server","env":{"SSH_MCP_WEB_PASSWORD":"pa\"ss"}}}}`)); err != nil {
		t.Fatal(err)
	}
	codexPath := filepath.Join(f.home, ".codex", "config.toml")
	codexBefore := "model = \"o4\"\n\n[mcp_servers.ssh]\ncommand = \"old\"\n\n[mcp_servers.other]\ncommand = \"x\"\n"
	if err := writeFileAtomic(codexPath, []byte(codexBefore)); err != nil {
		t.Fatal(err)
	}

	if err := f.wizard.Connect(nil); err != nil {
		t.Fatal(err)
	}
	claude := readJSON(t, claudePath)["mcpServers"].(map[string]any)["ssh"].(map[string]any)
	if claude["command"] != f.wizard.Executable {
		t.Fatalf("Claude Desktop still points at %v", claude["command"])
	}
	codex, err := os.ReadFile(codexPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(codex)
	for _, want := range []string{`model = "o4"`, "[mcp_servers.other]", "[mcp_servers.ssh]\ncommand = ", `SSH_MCP_WEB_PASSWORD = "pa\"ss"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("config.toml missing %q:\n%s", want, text)
		}
	}
	if strings.Count(text, "[mcp_servers.ssh]") != 1 || strings.Contains(text, `command = "old"`) {
		t.Fatalf("old Codex entry not replaced:\n%s", text)
	}
	if env := existingEnv(App{format: formatTOML, Path: codexPath}); env["SSH_MCP_WEB_PASSWORD"] != `pa"ss` {
		t.Fatalf("password did not round-trip through TOML: %v", env)
	}

	f.wizard.Disconnect()
	codex, _ = os.ReadFile(codexPath)
	if strings.Contains(string(codex), "mcp_servers.ssh") || !strings.Contains(string(codex), "[mcp_servers.other]") {
		t.Fatalf("Codex entry not removed cleanly:\n%s", codex)
	}
	if servers := readJSON(t, claudePath)["mcpServers"].(map[string]any); servers["ssh"] != nil {
		t.Fatalf("Claude Desktop entry not removed: %v", servers)
	}
	if err := f.wizard.RemoveSettings(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.configPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("settings file still exists")
	}
	if _, err := os.Stat(knownHosts); err != nil {
		t.Fatal("known_hosts must be kept")
	}
}

func TestConnectUnknownAppIsAnError(t *testing.T) {
	f := newFixture(t, "", false)
	if err := f.wizard.Connect([]string{"notepad"}); err == nil {
		t.Fatal("expected an error for an app that isn't installed")
	}
}
