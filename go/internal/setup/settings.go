package setup

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"example.com/ssh-mcp-server/internal/config"
)

// settingsFile is servers.json as the wizard edits it. Existing server
// entries are kept as raw JSON so fields the wizard does not know about
// (limits, policies) survive untouched.
type settingsFile struct {
	Schema  string            `json:"$schema,omitempty"`
	Servers []json.RawMessage `json:"servers"`
}

// serverEntry is a new server written by the wizard. Optional fields are
// omitted so the file stays short and every default applies.
type serverEntry struct {
	ID                      string   `json:"id"`
	Host                    string   `json:"host"`
	Port                    int      `json:"port,omitempty"`
	Username                string   `json:"username"`
	PasswordEnv             string   `json:"passwordEnv,omitempty"`
	PrivateKeyPath          string   `json:"privateKeyPath,omitempty"`
	PrivateKeyPassphraseEnv string   `json:"privateKeyPassphraseEnv,omitempty"`
	KnownHostsPath          string   `json:"knownHostsPath"`
	AllowSFTP               *bool    `json:"allowSftp,omitempty"`
	AllowedCommandPrefixes  []string `json:"allowedCommandPrefixes,omitempty"`
	DeniedCommandRegexes    []string `json:"deniedCommandRegexes,omitempty"`
}

// serverSummary is the part of any entry the wizard needs for menus and for
// knowing which secrets an AI app must be given.
type serverSummary struct {
	ID                      string `json:"id"`
	Host                    string `json:"host"`
	Port                    int    `json:"port"`
	Username                string `json:"username"`
	PasswordEnv             string `json:"passwordEnv"`
	PrivateKeyPassphraseEnv string `json:"privateKeyPassphraseEnv"`
}

// lookOnlyPrefixes are the commands allowed in "look only" mode: status and
// log readers. The allow-list also blocks pipes, redirects, and chaining.
var lookOnlyPrefixes = []string{
	"uptime", "whoami", "uname", "df", "du", "free", "ps", "top -b -n 1",
	"ls", "cat", "head", "tail", "grep", "wc", "stat", "lsblk", "who", "w",
	"journalctl", "systemctl status", "systemctl is-active", "systemctl list-units",
	"docker ps", "docker logs", "docker stats --no-stream",
	"ip addr", "ip route", "ss",
}

// lookOnlyDenied blocks the few options of allowed commands that change state.
var lookOnlyDenied = []string{`(?i)--(vacuum|rotate|flush|delete)`}

func readSettings(path string) (*settingsFile, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &settingsFile{}, nil
	}
	if err != nil {
		return nil, err
	}
	var settings settingsFile
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&settings); err != nil {
		return nil, fmt.Errorf("%s could not be read: %w", path, err)
	}
	return &settings, nil
}

func (s *settingsFile) summaries() []serverSummary {
	summaries := make([]serverSummary, 0, len(s.Servers))
	for _, raw := range s.Servers {
		var summary serverSummary
		if json.Unmarshal(raw, &summary) == nil {
			if summary.Port == 0 {
				summary.Port = 22
			}
			summaries = append(summaries, summary)
		}
	}
	return summaries
}

func (s *settingsFile) ids() []string {
	var ids []string
	for _, summary := range s.summaries() {
		ids = append(ids, summary.ID)
	}
	return ids
}

// write validates the settings with the same rules the server uses, then
// replaces the file atomically with owner-only permissions.
func (s *settingsFile) write(path string) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if len(s.Servers) > 0 {
		if _, err := config.Parse(data); err != nil {
			return fmt.Errorf("the settings would not be valid: %w", err)
		}
	}
	return writeFileAtomic(path, append(data, '\n'))
}

func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	// Owner-only on Unix; Windows applies the user profile's ACLs instead.
	_ = temp.Chmod(0o600)
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}

// parseAddress accepts "host", "user@host", "host:port", "user@host:port",
// "[ipv6]:port", and an optional ssh:// prefix.
func parseAddress(input string) (user, host string, port int, err error) {
	input = strings.TrimPrefix(strings.TrimSpace(input), "ssh://")
	if i := strings.LastIndex(input, "@"); i >= 0 {
		user, input = input[:i], input[i+1:]
	}
	host = input
	if h, p, splitErr := net.SplitHostPort(input); splitErr == nil {
		port, err = strconv.Atoi(p)
		if err != nil || port < 1 || port > 65535 {
			return "", "", 0, errors.New("the port must be a number between 1 and 65535")
		}
		host = h
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if host == "" {
		return "", "", 0, errors.New("please type a server name or IP address")
	}
	if strings.ContainsFunc(host+user, unicode.IsSpace) {
		return "", "", 0, errors.New("the address can't contain spaces")
	}
	return user, host, port, nil
}

var nonIDChars = regexp.MustCompile(`[^a-z0-9._-]+`)

// suggestID turns a host into a short, unique server name.
func suggestID(host string, taken []string) string {
	base := host
	if net.ParseIP(host) != nil {
		base = "server-" + strings.NewReplacer(".", "-", ":", "-").Replace(host)
	} else if i := strings.Index(host, "."); i > 0 {
		base = host[:i]
	}
	base = strings.Trim(nonIDChars.ReplaceAllString(strings.ToLower(base), "-"), "-.")
	if base == "" {
		base = "server"
	}
	if len(base) > 60 {
		base = base[:60]
	}
	id := base
	for n := 2; contains(taken, id); n++ {
		id = fmt.Sprintf("%s-%d", base, n)
	}
	return id
}

var validID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// envName builds the environment variable that holds a server's secret, for
// example SSH_MCP_STAGING_PASSWORD.
func envName(id, suffix string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(id) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return "SSH_MCP_" + b.String() + "_" + suffix
}

// portablePath writes paths under the home directory as ~/..., so the
// settings file stays readable and survives a renamed user profile.
func portablePath(path, home string) string {
	if home == "" {
		return path
	}
	if rel, err := filepath.Rel(home, path); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
		return "~/" + filepath.ToSlash(rel)
	}
	return path
}

func contains(values []string, value string) bool {
	for _, v := range values {
		if strings.EqualFold(v, value) {
			return true
		}
	}
	return false
}
