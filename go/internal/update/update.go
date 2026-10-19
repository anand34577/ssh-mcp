// Package update implements "ssh-mcp-server update": it replaces the running
// program with the latest release after verifying its SHA-256 checksum.
//
// It only runs when the user asks for it. Nothing else in the program
// contacts the network on its own, and no information about the user or
// their servers is ever sent anywhere.
package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Repository is where releases are published.
const Repository = "anand34577/ssh-mcp"

// Environment variables shared with the install scripts.
const (
	EnvDownloadURL = "SSH_MCP_DOWNLOAD_URL" // a mirror holding the release files
	EnvVersion     = "SSH_MCP_VERSION"      // a specific release tag instead of the latest
)

// maxDownload bounds the download; release binaries are far smaller.
const maxDownload = 256 << 20

// Updater downloads and installs a release over Executable.
type Updater struct {
	BaseURL    string // directory URL holding the release files
	Asset      string // file name for this platform
	Executable string // the file to replace
	Client     *http.Client
}

// AssetName is the release file name for a platform.
func AssetName(goos, goarch string) string {
	name := fmt.Sprintf("ssh-mcp-server-%s-%s", goos, goarch)
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// New returns an updater for the running program.
func New() (*Updater, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("cannot find this program's location: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	base := "https://github.com/" + Repository + "/releases/latest/download"
	if version := strings.TrimSpace(os.Getenv(EnvVersion)); version != "" {
		base = "https://github.com/" + Repository + "/releases/download/" + version
	}
	if mirror := strings.TrimSpace(os.Getenv(EnvDownloadURL)); mirror != "" {
		base = strings.TrimRight(mirror, "/")
	}
	return &Updater{
		BaseURL:    base,
		Asset:      AssetName(runtime.GOOS, runtime.GOARCH),
		Executable: executable,
		Client:     &http.Client{Timeout: 5 * time.Minute},
	}, nil
}

// Run installs the release if it differs from the running program. It
// reports whether the program was replaced.
func (u *Updater) Run(ctx context.Context) (bool, error) {
	checksumFile, err := u.get(ctx, u.Asset+".sha256", 4096)
	if err != nil {
		return false, err
	}
	want := strings.ToLower(strings.TrimSpace(strings.Fields(string(checksumFile) + " ")[0]))
	if len(want) != sha256.Size*2 {
		return false, fmt.Errorf("the checksum file for %s is not valid", u.Asset)
	}
	current, err := os.ReadFile(u.Executable)
	if err != nil {
		return false, err
	}
	if sum := sha256.Sum256(current); hex.EncodeToString(sum[:]) == want {
		return false, nil
	}

	binary, err := u.get(ctx, u.Asset, maxDownload)
	if err != nil {
		return false, err
	}
	if sum := sha256.Sum256(binary); hex.EncodeToString(sum[:]) != want {
		return false, errors.New("the download is damaged or was changed (checksum mismatch); nothing was replaced")
	}
	if err := replace(u.Executable, binary); err != nil {
		return false, fmt.Errorf("could not replace %s: %w (if it is in a system folder, run the update with administrator rights or reinstall)", u.Executable, err)
	}
	return true, nil
}

func (u *Updater) get(ctx context.Context, name string, limit int64) ([]byte, error) {
	url := u.BaseURL + "/" + name
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "ssh-mcp-server-updater")
	response, err := u.Client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("could not download %s: %w", url, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("could not download %s: %s", url, response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("could not download %s: %w", url, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s is larger than expected", url)
	}
	return data, nil
}

// replace swaps in the new program. Windows cannot overwrite a running
// program but can rename it, so the old copy is moved aside and removed on
// the next start (see CleanupOld).
func replace(path string, data []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".ssh-mcp-server-update-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err := io.Copy(temp, bytes.NewReader(data)); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Chmod(0o755); err != nil && runtime.GOOS != "windows" {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		return os.Rename(temp.Name(), path)
	}
	old := path + ".old"
	_ = os.Remove(old)
	if err := os.Rename(path, old); err != nil {
		return err
	}
	if err := os.Rename(temp.Name(), path); err != nil {
		_ = os.Rename(old, path)
		return err
	}
	return nil
}

// CleanupOld removes the copy a previous Windows update moved aside. It is
// silent because the old copy may still be running in an AI app.
func CleanupOld() {
	if executable, err := os.Executable(); err == nil {
		_ = os.Remove(executable + ".old")
	}
}
