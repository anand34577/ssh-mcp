package setup

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// mcpName is the name the server is registered under in every AI app.
const mcpName = "ssh"

type appFormat int

const (
	formatJSON appFormat = iota // a JSON settings file with an MCP servers object
	formatTOML                  // Codex's config.toml
	formatCLI                   // a command-line tool that manages its own settings
)

// App is an AI app (or coding agent) the server can be connected to.
type App struct {
	ID     string // short name for -app, e.g. "codex"
	Name   string // display name
	format appFormat
	// Path is the settings file; Key is the JSON object holding MCP servers.
	Path string
	Key  string
	// VSCode adds the "type": "stdio" field VS Code requires.
	VSCode bool
	// CLI is the path of the command-line tool (Claude Code's "claude").
	CLI string
}

// detectApps lists the AI apps installed for this user.
func (w *Wizard) detectApps() []App {
	var apps []App
	isDir := func(dir string) bool {
		info, err := os.Stat(dir)
		return dir != "" && err == nil && info.IsDir()
	}
	onPath := func(name string) string {
		if w.LookPath == nil {
			return ""
		}
		path, err := w.LookPath(name)
		if err != nil {
			return ""
		}
		return path
	}
	addJSON := func(id, name, dir, file, key string, vscode bool) {
		if isDir(dir) {
			apps = append(apps, App{ID: id, Name: name, format: formatJSON, Path: filepath.Join(dir, file), Key: key, VSCode: vscode})
		}
	}

	if w.AppDir != "" {
		addJSON("claude-desktop", "Claude Desktop", filepath.Join(w.AppDir, "Claude"), "claude_desktop_config.json", "mcpServers", false)
	}
	if path := onPath("claude"); path != "" {
		apps = append(apps, App{ID: "claude-code", Name: "Claude Code", format: formatCLI, CLI: path})
	}
	if w.Home != "" {
		codexHome := os.Getenv("CODEX_HOME")
		if codexHome == "" {
			codexHome = filepath.Join(w.Home, ".codex")
		}
		if isDir(codexHome) || onPath("codex") != "" {
			apps = append(apps, App{ID: "codex", Name: "Codex", format: formatTOML, Path: filepath.Join(codexHome, "config.toml")})
		}
		addJSON("gemini", "Gemini CLI", filepath.Join(w.Home, ".gemini"), "settings.json", "mcpServers", false)
		addJSON("cursor", "Cursor", filepath.Join(w.Home, ".cursor"), "mcp.json", "mcpServers", false)
		addJSON("windsurf", "Windsurf", filepath.Join(w.Home, ".codeium", "windsurf"), "mcp_config.json", "mcpServers", false)
	}
	if w.AppDir != "" {
		addJSON("vscode", "VS Code", filepath.Join(w.AppDir, "Code", "User"), "mcp.json", "servers", true)
	}
	return apps
}

// existingEnv returns the environment already stored for this server in an
// app's settings, so reconnecting never drops saved passwords.
func existingEnv(app App) map[string]string {
	result := map[string]string{}
	switch app.format {
	case formatJSON:
		settings, err := readJSONObject(app.Path)
		if err != nil {
			return result
		}
		servers, _ := settings[app.Key].(map[string]any)
		entry, _ := servers[mcpName].(map[string]any)
		env, _ := entry["env"].(map[string]any)
		for name, value := range env {
			if text, ok := value.(string); ok {
				result[name] = text
			}
		}
	case formatTOML:
		data, err := os.ReadFile(app.Path)
		if err != nil {
			return result
		}
		inEnv := false
		for _, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "[") {
				inEnv = tomlTableName(trimmed) == "mcp_servers."+mcpName+".env"
				continue
			}
			if name, raw, ok := strings.Cut(trimmed, "="); inEnv && ok {
				if value, ok := tomlUnquote(strings.TrimSpace(raw)); ok {
					result[strings.Trim(strings.TrimSpace(name), `"`)] = value
				}
			}
		}
	}
	return result
}

// connectApp registers the server with one app. env holds the secret
// environment variables the app must pass to the server.
func (w *Wizard) connectApp(app App, env map[string]string) error {
	args := []string{"-config", w.ConfigPath}
	switch app.format {
	case formatCLI:
		// Re-adding replaces an older registration, if any.
		_ = w.Exec(app.CLI, "mcp", "remove", "--scope", "user", mcpName)
		command := []string{"mcp", "add", "--scope", "user", mcpName}
		for _, name := range sortedKeys(env) {
			command = append(command, "-e", name+"="+env[name])
		}
		command = append(append(command, "--", w.Executable), args...)
		// Windows runs .cmd shims through cmd.exe, which would interpret
		// characters such as & or % inside a password.
		if ext := strings.ToLower(filepath.Ext(app.CLI)); (ext == ".cmd" || ext == ".bat") &&
			strings.ContainsAny(strings.Join(command, " "), "\"&|<>^%!") {
			return errors.New("a password or path contains characters that can't be passed safely to " + filepath.Base(app.CLI))
		}
		return w.Exec(app.CLI, command...)

	case formatTOML:
		text, err := readText(app.Path)
		if err != nil {
			return err
		}
		text, _ = removeTOMLServer(text)
		var b strings.Builder
		b.WriteString(text)
		if text != "" {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "[mcp_servers.%s]\ncommand = %s\nargs = [%s, %s]\n", mcpName, tomlString(w.Executable), tomlString(args[0]), tomlString(args[1]))
		if len(env) > 0 {
			fmt.Fprintf(&b, "\n[mcp_servers.%s.env]\n", mcpName)
			for _, name := range sortedKeys(env) {
				fmt.Fprintf(&b, "%s = %s\n", name, tomlString(env[name]))
			}
		}
		return replaceWithBackup(app.Path, []byte(b.String()))

	default:
		settings, err := readJSONObject(app.Path)
		if err != nil {
			return err
		}
		servers, err := serversObject(settings, app)
		if err != nil {
			return err
		}
		entry := map[string]any{"command": w.Executable, "args": args}
		if app.VSCode {
			entry["type"] = "stdio"
		}
		if len(env) > 0 {
			entry["env"] = env
		}
		servers[mcpName] = entry
		settings[app.Key] = servers
		return writeJSONWithBackup(app.Path, settings)
	}
}

// disconnectApp removes the server from one app. It reports whether there
// was anything to remove.
func (w *Wizard) disconnectApp(app App) (bool, error) {
	switch app.format {
	case formatCLI:
		if err := w.Exec(app.CLI, "mcp", "remove", "--scope", "user", mcpName); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "no mcp server") {
				return false, nil // it wasn't connected
			}
			return false, err
		}
		return true, nil
	case formatTOML:
		text, err := readText(app.Path)
		if err != nil {
			return false, err
		}
		text, removed := removeTOMLServer(text)
		if !removed {
			return false, nil
		}
		if text != "" {
			text += "\n"
		}
		return true, replaceWithBackup(app.Path, []byte(text))
	default:
		if _, err := os.Stat(app.Path); errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		settings, err := readJSONObject(app.Path)
		if err != nil {
			return false, err
		}
		servers, _ := settings[app.Key].(map[string]any)
		if _, ok := servers[mcpName]; !ok {
			return false, nil
		}
		delete(servers, mcpName)
		return true, writeJSONWithBackup(app.Path, settings)
	}
}

func serversObject(settings map[string]any, app App) (map[string]any, error) {
	switch servers := settings[app.Key].(type) {
	case map[string]any:
		return servers, nil
	case nil:
		return map[string]any{}, nil
	default:
		return nil, fmt.Errorf("%s has an unexpected %q section, so I left it alone", app.Path, app.Key)
	}
}

// readJSONObject reads a JSON settings file; a missing or empty file is an
// empty object. Files with comments are refused rather than rewritten.
func readJSONObject(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) || (err == nil && len(bytes.TrimSpace(data)) == 0) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil || settings == nil {
		return nil, fmt.Errorf("%s isn't plain JSON (it may contain comments), so I left it alone", path)
	}
	return settings, nil
}

func writeJSONWithBackup(path string, settings map[string]any) error {
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return replaceWithBackup(path, append(data, '\n'))
}

// replaceWithBackup saves the previous contents as <file>.bak, then writes
// the new contents atomically.
func replaceWithBackup(path string, data []byte) error {
	if original, err := os.ReadFile(path); err == nil {
		if err := os.WriteFile(path+".bak", original, 0o600); err != nil {
			return fmt.Errorf("could not back up %s: %w", path, err)
		}
	}
	return writeFileAtomic(path, data)
}

func readText(path string) (string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return strings.TrimRight(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n"), err
}

// removeTOMLServer drops the [mcp_servers.ssh] table and its sub-tables.
func removeTOMLServer(text string) (string, bool) {
	var kept []string
	skipping, removed := false, false
	for _, line := range strings.Split(text, "\n") {
		if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "[") {
			name := tomlTableName(trimmed)
			skipping = name == "mcp_servers."+mcpName || strings.HasPrefix(name, "mcp_servers."+mcpName+".")
			removed = removed || skipping
		}
		if !skipping {
			kept = append(kept, line)
		}
	}
	return strings.TrimRight(strings.Join(kept, "\n"), "\n"), removed
}

// tomlTableName normalizes a table header such as [mcp_servers."ssh"] # x.
func tomlTableName(header string) string {
	if end := strings.Index(header, "]"); end > 0 {
		header = header[:end]
	}
	header = strings.Trim(header, "[]")
	return strings.NewReplacer(`"`, "", "'", "", " ", "", "\t", "").Replace(header)
}

// tomlString encodes a TOML basic string.
func tomlString(value string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range value {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// tomlUnquote decodes a basic ("...") or literal ('...') TOML string.
func tomlUnquote(value string) (string, bool) {
	if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
		return value[1 : len(value)-1], true
	}
	text, err := strconv.Unquote(value)
	return text, err == nil && strings.HasPrefix(value, `"`)
}

// manualSnippet is the settings block to paste into an app by hand.
func (w *Wizard) manualSnippet(envNames []string) string {
	entry := map[string]any{"command": w.Executable, "args": []string{"-config", w.ConfigPath}}
	if len(envNames) > 0 {
		env := map[string]string{}
		for _, name := range envNames {
			env[name] = "<your password>"
		}
		entry["env"] = env
	}
	data, _ := json.MarshalIndent(map[string]any{"mcpServers": map[string]any{mcpName: entry}}, "", "  ")
	return string(data)
}

func sortedKeys(values map[string]string) []string {
	return slices.Sorted(maps.Keys(values))
}
