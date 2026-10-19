package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func releaseServer(t *testing.T, binary []byte, checksum string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ssh-mcp-server-test.sha256":
			_, _ = w.Write([]byte(checksum + "  ssh-mcp-server-test\n"))
		case "/ssh-mcp-server-test":
			_, _ = w.Write(binary)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func updater(t *testing.T, baseURL, current string) *Updater {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ssh-mcp-server")
	if err := os.WriteFile(path, []byte(current), 0o755); err != nil {
		t.Fatal(err)
	}
	return &Updater{BaseURL: baseURL, Asset: "ssh-mcp-server-test", Executable: path, Client: http.DefaultClient}
}

func sum(data string) string {
	digest := sha256.Sum256([]byte(data))
	return hex.EncodeToString(digest[:])
}

func TestUpdateReplacesThenReportsUpToDate(t *testing.T) {
	server := releaseServer(t, []byte("new program"), sum("new program"))
	u := updater(t, server.URL, "old program")

	updated, err := u.Run(context.Background())
	if err != nil || !updated {
		t.Fatalf("first run: updated=%v err=%v", updated, err)
	}
	if data, _ := os.ReadFile(u.Executable); string(data) != "new program" {
		t.Fatalf("program = %q", data)
	}
	updated, err = u.Run(context.Background())
	if err != nil || updated {
		t.Fatalf("second run: updated=%v err=%v", updated, err)
	}
}

func TestUpdateRefusesTamperedDownload(t *testing.T) {
	server := releaseServer(t, []byte("evil program"), sum("new program"))
	u := updater(t, server.URL, "old program")
	if _, err := u.Run(context.Background()); err == nil {
		t.Fatal("expected a checksum error")
	}
	if data, _ := os.ReadFile(u.Executable); string(data) != "old program" {
		t.Fatalf("program was replaced: %q", data)
	}
}

func TestUpdateReportsMissingRelease(t *testing.T) {
	server := releaseServer(t, nil, "not-a-checksum")
	u := updater(t, server.URL, "old program")
	if _, err := u.Run(context.Background()); err == nil {
		t.Fatal("expected an error for an invalid checksum file")
	}
	u.Asset = "missing"
	if _, err := u.Run(context.Background()); err == nil {
		t.Fatal("expected an error for a missing file")
	}
}

func TestAssetName(t *testing.T) {
	if got := AssetName("windows", "arm64"); got != "ssh-mcp-server-windows-arm64.exe" {
		t.Errorf("AssetName = %q", got)
	}
	if got := AssetName("darwin", "amd64"); got != "ssh-mcp-server-darwin-amd64" {
		t.Errorf("AssetName = %q", got)
	}
}
