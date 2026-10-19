// Package setup implements "ssh-mcp-server setup": a guided, plain-language
// wizard that adds servers to servers.json, confirms their identity, tests the
// sign-in, and connects the server to the AI apps installed on this computer.
package setup

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"example.com/ssh-mcp-server/internal/config"
	"example.com/ssh-mcp-server/internal/sshservice"
	"golang.org/x/crypto/ssh"
)

// ErrCancelled is returned when the user closes the input (Ctrl+D / Ctrl+Z).
var ErrCancelled = errors.New("setup cancelled")

// Wizard holds the wizard's inputs and the operations it depends on, so tests
// can drive it without a terminal, a network, or real AI apps.
type Wizard struct {
	In  *bufio.Reader
	Out io.Writer
	// ReadSecret reads one line without echoing it. When nil, secrets are read
	// from In like any other answer.
	ReadSecret func() (string, error)

	ConfigPath string // servers.json to edit
	Executable string // this program, as AI apps should launch it
	Home       string // the user's home directory
	AppDir     string // the per-user settings directory (os.UserConfigDir)
	Username   string // suggested remote username
	// InstallDir is where a program started from Downloads is offered to be
	// copied. Empty disables the offer.
	InstallDir string

	LookPath     func(file string) (string, error)
	Exec         func(name string, args ...string) error
	FetchHostKey func(ctx context.Context, host string, port int) (ssh.PublicKey, error)
	TestServer   func(ctx context.Context, server *config.ServerConfig) sshservice.CheckResult

	// secrets typed during this run, by environment variable name. They are
	// only ever passed to AI app settings, never written to servers.json.
	secrets map[string]string
}

// New returns a wizard wired to the real terminal, network, and apps.
func New(configPath string, readSecret func() (string, error)) (*Wizard, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("cannot find this program's location: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	home, _ := os.UserHomeDir()
	appDir, _ := os.UserConfigDir()
	username := os.Getenv("USER")
	if username == "" {
		username = os.Getenv("USERNAME")
	}
	return &Wizard{
		In:         bufio.NewReader(os.Stdin),
		Out:        os.Stdout,
		ReadSecret: readSecret,
		ConfigPath: configPath,
		Executable: executable,
		Home:       home,
		AppDir:     appDir,
		Username:   username,
		InstallDir: defaultInstallDir(home),
		LookPath:   exec.LookPath,
		Exec: func(name string, args ...string) error {
			output, err := exec.Command(name, args...).CombinedOutput()
			if err != nil {
				return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
			}
			return nil
		},
		FetchHostKey: func(ctx context.Context, host string, port int) (ssh.PublicKey, error) {
			return sshservice.FetchHostKey(ctx, host, port, 10*time.Second)
		},
		TestServer: func(ctx context.Context, server *config.ServerConfig) sshservice.CheckResult {
			service := sshservice.New(&config.Catalog{Servers: map[string]*config.ServerConfig{server.ID: server}})
			defer service.Close()
			return service.Check(ctx, true)[0]
		},
	}, nil
}

// defaultInstallDir matches where the install scripts put the program.
func defaultInstallDir(home string) string {
	if runtime.GOOS == "windows" {
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			return filepath.Join(local, "Programs", "ssh-mcp")
		}
		return ""
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".local", "bin")
}

// Run shows the main menu, or goes straight to adding a server when none is
// set up yet.
func (w *Wizard) Run(ctx context.Context) error {
	if w.secrets == nil {
		w.secrets = map[string]string{}
	}
	w.say("")
	w.say("SSH MCP setup")
	w.say("=============")
	w.say("This connects your AI assistant (Claude, Cursor, VS Code, and others) to your")
	w.say("servers over SSH. Your passwords and keys stay on this computer.")
	w.say("")
	w.say("Settings file: %s", w.ConfigPath)
	if err := w.offerInstall(); err != nil {
		return err
	}

	settings, err := readSettings(w.ConfigPath)
	if err != nil {
		return err
	}
	if len(settings.Servers) == 0 {
		w.say("")
		w.say("Let's add your first server.")
		added, err := w.addServer(ctx)
		if err != nil {
			return err
		}
		if !added {
			w.say("")
			w.say("No server was added. Run setup again whenever you're ready.")
			return nil
		}
		if err := w.connectApps(); err != nil {
			return err
		}
		w.finish()
		return nil
	}

	for {
		settings, err := readSettings(w.ConfigPath)
		if err != nil {
			return err
		}
		w.say("")
		w.say("Your servers: %s", strings.Join(settings.ids(), ", "))
		choice, err := w.choose("What would you like to do?", []string{
			"Add a server",
			"Test my servers",
			"Connect to my AI apps",
			"Remove a server",
			"Exit",
		}, 1)
		if err != nil {
			return err
		}
		switch choice {
		case 1:
			added, err := w.addServer(ctx)
			if err != nil {
				return err
			}
			if added {
				if err := w.connectApps(); err != nil {
					return err
				}
			}
		case 2:
			if err := w.testServers(ctx); err != nil {
				return err
			}
		case 3:
			if err := w.connectApps(); err != nil {
				return err
			}
		case 4:
			if err := w.removeServer(); err != nil {
				return err
			}
		default:
			w.finish()
			return nil
		}
	}
}

// addServer asks for one server, confirms its identity, tests the sign-in,
// and saves it. It reports whether a server was saved.
func (w *Wizard) addServer(ctx context.Context) (bool, error) {
	settings, err := readSettings(w.ConfigPath)
	if err != nil {
		return false, err
	}
	w.heading("Add a server")

	var username, host string
	var port int
	for {
		answer, err := w.ask("Server address (for example me@example.com or 203.0.113.10)", "")
		if err != nil {
			return false, err
		}
		if username, host, port, err = parseAddress(answer); err == nil {
			break
		}
		w.problem("%s", capitalize(err.Error()))
	}
	if port == 0 {
		if port, err = w.askNumber("SSH port", 22, 1, 65535); err != nil {
			return false, err
		}
	}
	if username == "" {
		if username, err = w.askRequired("Your username on that server", w.Username); err != nil {
			return false, err
		}
	}

	id := suggestID(host, settings.ids())
	for {
		answer, err := w.ask("A short name your AI will use for it", id)
		if err != nil {
			return false, err
		}
		if !validID.MatchString(answer) {
			w.problem("Use letters, numbers, dots, dashes, or underscores (no spaces).")
			continue
		}
		if contains(settings.ids(), answer) {
			w.problem("You already have a server called %q.", answer)
			continue
		}
		id = answer
		break
	}

	entry := serverEntry{ID: id, Host: host, Username: username}
	if port != 22 {
		entry.Port = port
	}
	if err := w.chooseSignIn(&entry); err != nil {
		return false, err
	}

	access, err := w.choose("What should your AI be allowed to do on this server?", []string{
		"Anything my account can do",
		"Look only: check status and read logs, no changes",
	}, 1)
	if err != nil {
		return false, err
	}
	if access == 2 {
		off := false
		entry.AllowSFTP = &off
		entry.AllowedCommandPrefixes = lookOnlyPrefixes
		entry.DeniedCommandRegexes = lookOnlyDenied
	}

	knownHosts := filepath.Join(w.Home, ".ssh", "known_hosts")
	entry.KnownHostsPath = portablePath(knownHosts, w.Home)
	trusted, err := w.trustServer(ctx, knownHosts, host, port)
	if err != nil || !trusted {
		return false, err
	}

	if ok, err := w.testNewServer(ctx, entry); err != nil || !ok {
		return false, err
	}

	raw, err := json.Marshal(entry)
	if err != nil {
		return false, err
	}
	settings.Servers = append(settings.Servers, raw)
	if err := settings.write(w.ConfigPath); err != nil {
		return false, err
	}
	w.success("Saved %q.", id)
	return true, nil
}

func (w *Wizard) chooseSignIn(entry *serverEntry) error {
	var keys []string
	for _, name := range []string{"id_ed25519", "id_ecdsa", "id_rsa"} {
		path := filepath.Join(w.Home, ".ssh", name)
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			keys = append(keys, path)
		}
	}
	options := make([]string, 0, len(keys)+2)
	for i, key := range keys {
		label := "With my key file " + key
		if i == 0 {
			label += " (recommended)"
		}
		options = append(options, label)
	}
	options = append(options, "With a password", "With a different key file")

	choice, err := w.choose("How do you sign in to this server?", options, 1)
	if err != nil {
		return err
	}
	switch {
	case choice <= len(keys):
		return w.useKey(entry, keys[choice-1])
	case choice == len(keys)+1:
		entry.PasswordEnv = envName(entry.ID, "PASSWORD")
		return nil
	}
	for {
		path, err := w.askRequired("Path to your private key file", "")
		if err != nil {
			return err
		}
		path = config.Expand(strings.Trim(path, `"'`))
		if err := w.useKey(entry, path); err == nil {
			return nil
		} else {
			w.problem("%s", err)
		}
	}
}

// useKey checks that path is a private key and notes whether it needs a
// passphrase.
func (w *Wizard) useKey(entry *serverEntry, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("I can't open %s", path)
	}
	_, err = ssh.ParseRawPrivateKey(data)
	var missing *ssh.PassphraseMissingError
	switch {
	case errors.As(err, &missing):
		w.say("  This key is protected by a passphrase. It will be kept out of the settings file.")
		entry.PrivateKeyPassphraseEnv = envName(entry.ID, "KEY_PASSPHRASE")
	case err != nil:
		return fmt.Errorf("%s isn't a private key file (pick the one without .pub)", path)
	}
	entry.PrivateKeyPath = portablePath(path, w.Home)
	return nil
}

// trustServer makes sure the server's identity (host key) is recorded,
// asking the user to confirm a new fingerprint.
func (w *Wizard) trustServer(ctx context.Context, knownHosts, host string, port int) (bool, error) {
	known, err := sshservice.KnownHostKeyTypes(knownHosts, host, port)
	if err != nil {
		return false, err
	}
	if len(known) > 0 {
		w.success("This computer already knows this server.")
		return true, nil
	}

	w.say("")
	w.say("Checking the server's identity...")
	key, err := w.FetchHostKey(ctx, host, port)
	if err != nil {
		w.problem("I couldn't reach the server: %s", err)
		w.say("  Check the address and port, and that this computer can reach it (VPN, firewall).")
		return false, nil
	}
	w.say("")
	w.say("  The server's fingerprint is:")
	w.say("    %s  %s", strings.ToUpper(strings.TrimPrefix(key.Type(), "ssh-")), ssh.FingerprintSHA256(key))
	w.say("")
	w.say("  A fingerprint is like an ID card for the server. If your hosting provider")
	w.say("  or admin gave you one, make sure it matches before you say yes.")
	trust, err := w.confirm("Trust this server?", false)
	if err != nil {
		return false, err
	}
	if !trust {
		w.say("  Okay, nothing was saved for this server.")
		return false, nil
	}
	if err := sshservice.TrustHostKey(knownHosts, host, port, key); err != nil {
		return false, fmt.Errorf("could not save the fingerprint to %s: %w", knownHosts, err)
	}
	w.success("Saved the fingerprint to %s.", knownHosts)
	return true, nil
}

// testNewServer signs in to a server before it is saved. It reports whether
// the server should be saved.
func (w *Wizard) testNewServer(ctx context.Context, entry serverEntry) (bool, error) {
	data, err := json.Marshal(settingsFile{Servers: []json.RawMessage{mustJSON(entry)}})
	if err != nil {
		return false, err
	}
	catalog, err := config.Parse(data)
	if err != nil {
		return false, fmt.Errorf("the server settings are not valid: %w", err)
	}
	server := catalog.Servers[entry.ID]
	result, tested, err := w.test(ctx, server, entry.ID)
	if err != nil {
		return false, err
	}
	if !tested {
		w.say("  Skipped the sign-in test. You can run it later from the menu.")
		return true, nil
	}
	if result.OK() {
		return true, nil
	}
	return w.confirm("Save this server anyway? You can fix it and test again later.", true)
}

// test asks for any secret the server needs, then signs in. tested is false
// when the user skipped a secret.
func (w *Wizard) test(ctx context.Context, server *config.ServerConfig, label string) (sshservice.CheckResult, bool, error) {
	needs := []struct{ env, prompt string }{
		{server.PasswordEnv, fmt.Sprintf("Password for %s@%s", server.Username, server.Host)},
		{server.PrivateKeyPassphraseEnv, "Passphrase for " + filepath.Base(server.PrivateKeyPath)},
	}
	restore := map[string]*string{}
	defer func() {
		for name, value := range restore {
			if value == nil {
				os.Unsetenv(name)
			} else {
				os.Setenv(name, *value)
			}
		}
	}()
	for _, need := range needs {
		if need.env == "" || os.Getenv(need.env) != "" {
			continue
		}
		value, ok := w.secrets[need.env]
		if !ok {
			var err error
			value, err = w.secret(need.prompt + " (hidden; press Enter to skip the test)")
			if err != nil {
				return sshservice.CheckResult{}, false, err
			}
			if value == "" {
				return sshservice.CheckResult{}, false, nil
			}
		}
		if previous, set := os.LookupEnv(need.env); set {
			restore[need.env] = &previous
		} else {
			restore[need.env] = nil
		}
		os.Setenv(need.env, value)
		w.secrets[need.env] = value
	}

	w.say("  Signing in to %s...", label)
	result := w.TestServer(ctx, server)
	if result.OK() {
		w.success("Signed in to %s.", label)
	} else {
		for _, problem := range result.Problems {
			w.problem("%s", problem)
		}
		w.signInHint(server, result)
		// Never hand a secret that failed to sign in to an AI app.
		delete(w.secrets, server.PasswordEnv)
		delete(w.secrets, server.PrivateKeyPassphraseEnv)
	}
	return result, true, nil
}

func (w *Wizard) signInHint(server *config.ServerConfig, result sshservice.CheckResult) {
	text := strings.ToLower(strings.Join(result.Problems, " "))
	switch {
	case strings.Contains(text, "unable to authenticate") && server.PrivateKeyPath != "":
		w.say("  Tip: the server may not have your public key yet. Add it with:")
		w.say("    ssh-copy-id -i %s.pub %s@%s", config.Expand(server.PrivateKeyPath), server.Username, server.Host)
	case strings.Contains(text, "unable to authenticate"):
		w.say("  Tip: check the username and password.")
	}
}

func (w *Wizard) testServers(ctx context.Context) error {
	w.heading("Test my servers")
	catalog, err := config.Load(w.ConfigPath)
	if err != nil {
		w.problem("%s", err)
		return nil
	}
	for _, id := range slices.Sorted(maps.Keys(catalog.Servers)) {
		if _, tested, err := w.test(ctx, catalog.Servers[id], id); err != nil {
			return err
		} else if !tested {
			w.say("  Skipped %s.", id)
		}
	}
	return nil
}

func (w *Wizard) removeServer() error {
	settings, err := readSettings(w.ConfigPath)
	if err != nil {
		return err
	}
	ids := settings.ids()
	choice, err := w.choose("Which server should be removed?", append(ids, "Never mind"), len(ids)+1)
	if err != nil || choice > len(ids) {
		return err
	}
	id := ids[choice-1]
	sure, err := w.confirm(fmt.Sprintf("Remove %q? Your AI won't be able to reach it anymore.", id), false)
	if err != nil || !sure {
		return err
	}
	kept := settings.Servers[:0]
	for _, raw := range settings.Servers {
		var summary serverSummary
		if json.Unmarshal(raw, &summary) == nil && summary.ID == id {
			continue
		}
		kept = append(kept, raw)
	}
	settings.Servers = kept
	if err := settings.write(w.ConfigPath); err != nil {
		return err
	}
	w.success("Removed %q.", id)
	return nil
}

// connectApps registers the server with the AI apps the user picks, and
// stores any passwords those apps need to pass along.
func (w *Wizard) connectApps() error {
	w.heading("Connect to your AI apps")
	settings, err := readSettings(w.ConfigPath)
	if err != nil {
		return err
	}
	needs := secretNeeds(settings)

	apps := w.detectApps()
	if len(apps) == 0 {
		var names []string
		for _, n := range needs {
			names = append(names, n.env)
		}
		w.say("I didn't find a supported AI app (Claude, Codex, Cursor, VS Code, Gemini CLI,")
		w.say("Windsurf) on this computer.")
		w.say("Paste this into your AI app's MCP settings:")
		w.say("")
		w.say("%s", w.manualSnippet(names))
		return nil
	}

	var chosen []App
	for _, app := range apps {
		yes, err := w.confirm("Connect to "+app.Name+"?", true)
		if err != nil {
			return err
		}
		if yes {
			chosen = append(chosen, app)
		}
	}
	if len(chosen) == 0 {
		return nil
	}

	// Reuse passwords any app already has, then ask for the rest.
	w.adoptSavedSecrets(apps)
	var missing []need
	for _, n := range needs {
		if _, known := w.secrets[n.env]; !known && os.Getenv(n.env) == "" {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		w.say("")
		w.say("Your AI apps need a password to sign in to some servers. It can be saved in")
		w.say("the apps' own settings on this computer (not in servers.json).")
		save, err := w.confirm("Save passwords in your AI apps' settings?", true)
		if err != nil {
			return err
		}
		for _, n := range missing {
			if !save {
				w.say("  Before starting your AI app, set %s to the %s for %s.", n.env, n.what, n.server)
				continue
			}
			value, err := w.secret(fmt.Sprintf("The %s for %s (hidden; press Enter to skip)", n.what, n.server))
			if err != nil {
				return err
			}
			if value == "" {
				w.say("  Skipped. Set %s yourself before starting your AI app.", n.env)
				continue
			}
			w.secrets[n.env] = value
		}
	}

	for _, app := range chosen {
		env := existingEnv(app)
		if env == nil {
			env = map[string]string{}
		}
		for _, n := range needs {
			if value, ok := w.secrets[n.env]; ok {
				env[n.env] = value
			}
		}
		if err := w.connectApp(app, env); err != nil {
			w.problem("Couldn't connect to %s: %s", app.Name, err)
			w.say("  Add this to its MCP settings yourself:")
			w.say("%s", w.manualSnippet(sortedKeys(env)))
			continue
		}
		w.success("Connected to %s.", app.Name)
	}
	return nil
}

// need is a secret an AI app must pass to the server for one server entry.
type need struct{ env, server, what string }

func secretNeeds(settings *settingsFile) []need {
	var needs []need
	for _, server := range settings.summaries() {
		if server.PasswordEnv != "" {
			needs = append(needs, need{server.PasswordEnv, server.ID, "password"})
		}
		if server.PrivateKeyPassphraseEnv != "" {
			needs = append(needs, need{server.PrivateKeyPassphraseEnv, server.ID, "key passphrase"})
		}
	}
	return needs
}

// adoptSavedSecrets picks up passwords already saved in any app's settings,
// so connecting another app (or moving the program) never asks again.
func (w *Wizard) adoptSavedSecrets(apps []App) {
	if w.secrets == nil {
		w.secrets = map[string]string{}
	}
	for _, app := range apps {
		for name, value := range existingEnv(app) {
			if _, known := w.secrets[name]; !known && value != "" {
				w.secrets[name] = value
			}
		}
	}
}

// Connect registers the server with every detected AI app, or only those
// whose IDs are listed, without asking anything. It is safe to run again at
// any time, for example after moving the program or installing a new app.
func (w *Wizard) Connect(only []string) error {
	settings, err := readSettings(w.ConfigPath)
	if err != nil {
		return err
	}
	apps := w.detectApps()
	w.adoptSavedSecrets(apps)
	connected := 0
	for _, app := range apps {
		if len(only) > 0 && !contains(only, app.ID) {
			continue
		}
		env := existingEnv(app)
		var missing []string
		for _, n := range secretNeeds(settings) {
			if value, ok := w.secrets[n.env]; ok {
				env[n.env] = value
			} else if os.Getenv(n.env) == "" {
				missing = append(missing, n.server)
			}
		}
		if err := w.connectApp(app, env); err != nil {
			w.problem("Couldn't connect to %s: %s", app.Name, err)
			continue
		}
		connected++
		w.success("Connected to %s.", app.Name)
		if len(missing) > 0 {
			w.say("       It still needs the password for %s. Run \"ssh-mcp-server setup\" to add it.", strings.Join(slices.Compact(missing), ", "))
		}
	}
	if connected == 0 {
		if len(only) > 0 {
			return fmt.Errorf("none of these apps were found: %s (known: claude-desktop, claude-code, codex, gemini, cursor, windsurf, vscode)", strings.Join(only, ", "))
		}
		w.say("No supported AI app was found. Add this to your app's MCP settings:")
		w.say("%s", w.manualSnippet(nil))
	}
	return nil
}

// Disconnect removes the server from every detected AI app.
func (w *Wizard) Disconnect() {
	for _, app := range w.detectApps() {
		removed, err := w.disconnectApp(app)
		switch {
		case err != nil:
			w.problem("Couldn't remove it from %s: %s", app.Name, err)
		case removed:
			w.success("Removed from %s.", app.Name)
		}
	}
}

// RemoveSettings deletes the server list. known_hosts is left alone because
// other SSH tools use it too.
func (w *Wizard) RemoveSettings() error {
	for _, path := range []string{w.ConfigPath, w.ConfigPath + ".bak"} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	_ = os.Remove(filepath.Dir(w.ConfigPath)) // only succeeds when empty
	w.success("Deleted your server list (%s).", w.ConfigPath)
	return nil
}

func (w *Wizard) finish() {
	w.say("")
	w.say("All set! Restart your AI app, then try asking it:")
	w.say(`  "List my SSH servers"`)
	w.say(`  "How much disk space is left on my server?"`)
	w.say("")
	w.say("Run this setup again any time to add, test, or remove servers.")
}

// offerInstall copies a program started from Downloads or a temporary folder
// to InstallDir, because AI apps keep launching it from the recorded path.
func (w *Wizard) offerInstall() error {
	dir := strings.ToLower(filepath.Dir(w.Executable))
	temp := strings.ToLower(os.TempDir())
	if !strings.Contains(dir, "downloads") && !strings.HasPrefix(dir, temp) {
		return nil
	}
	w.say("")
	w.say("This program is running from %s.", filepath.Dir(w.Executable))
	w.say("Your AI apps will start it from wherever it is now, so it should live")
	w.say("somewhere permanent.")
	if w.InstallDir == "" {
		w.say("Move it somewhere permanent (or use the installer), then run setup again.")
		return nil
	}
	target := filepath.Join(w.InstallDir, "ssh-mcp-server"+filepath.Ext(w.Executable))
	install, err := w.confirm("Install it to "+target+" now?", true)
	if err != nil || !install {
		return err
	}
	if err := copyExecutable(w.Executable, target); err != nil {
		w.problem("Couldn't install it: %s", err)
		return nil
	}
	w.Executable = target
	w.success("Installed. You can delete the downloaded file.")
	return nil
}

func copyExecutable(source, target string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	temp := target + ".new"
	if err := os.WriteFile(temp, data, 0o755); err != nil {
		return err
	}
	return os.Rename(temp, target)
}

// --- prompts ---------------------------------------------------------------

func (w *Wizard) say(format string, args ...any) {
	fmt.Fprintf(w.Out, format+"\n", args...)
}

func (w *Wizard) heading(title string) {
	w.say("")
	w.say("%s", title)
	w.say("%s", strings.Repeat("-", len(title)))
}

func (w *Wizard) success(format string, args ...any) {
	w.say("  [ok] "+format, args...)
}

func (w *Wizard) problem(format string, args ...any) {
	w.say("  [!]  "+format, args...)
}

func (w *Wizard) readLine() (string, error) {
	line, err := w.In.ReadString('\n')
	if err != nil && (line == "" || !errors.Is(err, io.EOF)) {
		if errors.Is(err, io.EOF) {
			return "", ErrCancelled
		}
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func (w *Wizard) ask(question, def string) (string, error) {
	if def != "" {
		fmt.Fprintf(w.Out, "%s [%s]: ", question, def)
	} else {
		fmt.Fprintf(w.Out, "%s: ", question)
	}
	answer, err := w.readLine()
	if err != nil {
		return "", err
	}
	if answer == "" {
		return def, nil
	}
	return answer, nil
}

func (w *Wizard) askRequired(question, def string) (string, error) {
	for {
		answer, err := w.ask(question, def)
		if err != nil || answer != "" {
			return answer, err
		}
		w.problem("This one is needed.")
	}
}

func (w *Wizard) askNumber(question string, def, low, high int) (int, error) {
	for {
		answer, err := w.ask(question, strconv.Itoa(def))
		if err != nil {
			return 0, err
		}
		if n, err := strconv.Atoi(answer); err == nil && n >= low && n <= high {
			return n, nil
		}
		w.problem("Please type a number between %d and %d.", low, high)
	}
}

func (w *Wizard) confirm(question string, def bool) (bool, error) {
	hint := "y/N"
	if def {
		hint = "Y/n"
	}
	for {
		fmt.Fprintf(w.Out, "%s [%s]: ", question, hint)
		answer, err := w.readLine()
		if err != nil {
			return false, err
		}
		switch strings.ToLower(answer) {
		case "":
			return def, nil
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
		w.problem("Please answer y or n.")
	}
}

// choose shows numbered options and returns the 1-based choice.
func (w *Wizard) choose(question string, options []string, def int) (int, error) {
	w.say("")
	w.say("%s", question)
	for i, option := range options {
		w.say("  %d) %s", i+1, option)
	}
	return w.askNumber("Choose", def, 1, len(options))
}

func (w *Wizard) secret(prompt string) (string, error) {
	fmt.Fprintf(w.Out, "%s: ", prompt)
	if w.ReadSecret == nil {
		return w.readLine()
	}
	value, err := w.ReadSecret()
	fmt.Fprintln(w.Out)
	if errors.Is(err, io.EOF) {
		return "", ErrCancelled
	}
	return strings.TrimRight(value, "\r\n"), err
}

func capitalize(text string) string {
	if text == "" {
		return text
	}
	return strings.ToUpper(text[:1]) + text[1:]
}

func mustJSON(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}

// Confirm asks a yes/no question.
func (w *Wizard) Confirm(question string, def bool) (bool, error) {
	return w.confirm(question, def)
}
