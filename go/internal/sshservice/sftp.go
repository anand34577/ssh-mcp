package sshservice

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"example.com/ssh-mcp-server/internal/config"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

const (
	defaultMaxEntries = 1000
	maxMaxEntries     = 10000
)

type SFTPListRequest struct {
	ServerID   string
	Path       string
	MaxEntries int
}

type SFTPStatRequest struct {
	ServerID string
	Path     string
}

type SFTPReadRequest struct {
	ServerID string
	Path     string
	Offset   int64
	MaxBytes int
	Encoding string
}

type SFTPWriteRequest struct {
	ServerID      string
	Path          string
	Content       *string
	ContentBase64 *string
}

type SFTPMkdirRequest struct {
	ServerID string
	Path     string
	Parents  bool
}

type SFTPDeleteRequest struct {
	ServerID  string
	Path      string
	Directory bool
}

type SFTPRenameRequest struct {
	ServerID  string
	From      string
	To        string
	Overwrite bool
}

func (s *Service) SFTPList(ctx context.Context, request SFTPListRequest) (map[string]any, error) {
	server, path, err := s.sftpTarget(request.ServerID, request.Path)
	if err != nil {
		return nil, err
	}
	maxEntries := request.MaxEntries
	if maxEntries == 0 {
		maxEntries = defaultMaxEntries
	}
	if maxEntries < 1 || maxEntries > maxMaxEntries {
		return nil, fmt.Errorf("maxEntries must be between 1 and %d", maxMaxEntries)
	}
	return s.withSFTP(ctx, server, "sftp_list", func(ctx context.Context, client *sftp.Client) (map[string]any, error) {
		entries, err := client.ReadDirContext(ctx, path)
		if err != nil {
			return nil, err
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		shown := entries[:min(len(entries), maxEntries)]
		items := make([]map[string]any, 0, len(shown))
		for _, entry := range shown {
			items = append(items, fileInfoMap(entry, entry.Name()))
		}
		return map[string]any{
			"serverId":     server.ID,
			"path":         path,
			"entries":      items,
			"totalEntries": len(entries),
			"truncated":    len(entries) > maxEntries,
		}, nil
	})
}

func (s *Service) SFTPStat(ctx context.Context, request SFTPStatRequest) (map[string]any, error) {
	server, path, err := s.sftpTarget(request.ServerID, request.Path)
	if err != nil {
		return nil, err
	}
	return s.withSFTP(ctx, server, "sftp_stat", func(_ context.Context, client *sftp.Client) (map[string]any, error) {
		info, err := client.Stat(path)
		if err != nil {
			return nil, err
		}
		result := fileInfoMap(info, "")
		result["serverId"] = server.ID
		result["path"] = path
		return result, nil
	})
}

func (s *Service) SFTPRead(ctx context.Context, request SFTPReadRequest) (map[string]any, error) {
	server, path, err := s.sftpTarget(request.ServerID, request.Path)
	if err != nil {
		return nil, err
	}
	if request.Offset < 0 {
		return nil, errors.New("offset must be zero or positive")
	}
	maxBytes := request.MaxBytes
	if maxBytes == 0 {
		maxBytes = server.MaxOutputBytes
	}
	if maxBytes < 1 || maxBytes > server.MaxOutputBytes {
		return nil, fmt.Errorf("maxBytes must be between 1 and %d", server.MaxOutputBytes)
	}
	encoding, err := readEncoding(request.Encoding)
	if err != nil {
		return nil, err
	}
	return s.withSFTP(ctx, server, "sftp_read_file", func(_ context.Context, client *sftp.Client) (map[string]any, error) {
		file, err := client.Open(path)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			return nil, err
		}
		if info.IsDir() {
			return nil, errors.New("path is a directory; use sftp_list")
		}
		if _, err := file.Seek(request.Offset, io.SeekStart); err != nil {
			return nil, err
		}
		// Read one extra byte to learn whether more data follows.
		data, err := io.ReadAll(io.LimitReader(file, int64(maxBytes)+1))
		if err != nil {
			return nil, err
		}
		truncated := len(data) > maxBytes
		if truncated {
			data = data[:maxBytes]
			if encoding == "utf8" {
				data = trimIncompleteRune(data)
			}
		}
		result := map[string]any{
			"serverId":   server.ID,
			"path":       path,
			"size":       info.Size(),
			"offset":     request.Offset,
			"bytes":      len(data),
			"nextOffset": request.Offset + int64(len(data)),
			"truncated":  truncated,
			"encoding":   encoding,
		}
		if encoding == "base64" {
			result["contentBase64"] = base64.StdEncoding.EncodeToString(data)
		} else {
			result["content"] = string(data)
			result["validUtf8"] = utf8.Valid(data)
		}
		return result, nil
	})
}

func (s *Service) SFTPWrite(ctx context.Context, request SFTPWriteRequest) (map[string]any, error) {
	server, path, err := s.sftpTarget(request.ServerID, request.Path)
	if err != nil {
		return nil, err
	}
	if (request.Content == nil) == (request.ContentBase64 == nil) {
		return nil, errors.New("provide exactly one of content or contentBase64")
	}
	var data []byte
	if request.Content != nil {
		data = []byte(*request.Content)
	} else if data, err = decodeBase64(*request.ContentBase64); err != nil {
		return nil, err
	}
	if len(data) > server.MaxOutputBytes {
		return nil, fmt.Errorf("file content is %d bytes, which exceeds maxOutputBytes (%d) for this server", len(data), server.MaxOutputBytes)
	}
	if err := checkPathGuard(server, "sftp_write_file", path, true); err != nil {
		return nil, err
	}
	return s.withSFTP(ctx, server, "sftp_write_file", func(_ context.Context, client *sftp.Client) (map[string]any, error) {
		file, err := client.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
		if err != nil {
			return nil, err
		}
		if _, err := file.Write(data); err != nil {
			_ = file.Close()
			return nil, err
		}
		// Close flushes outstanding writes; its error means data may be lost.
		if err := file.Close(); err != nil {
			return nil, err
		}
		return map[string]any{"serverId": server.ID, "path": path, "bytesWritten": len(data)}, nil
	})
}

func (s *Service) SFTPMkdir(ctx context.Context, request SFTPMkdirRequest) (map[string]any, error) {
	server, path, err := s.sftpTarget(request.ServerID, request.Path)
	if err != nil {
		return nil, err
	}
	return s.withSFTP(ctx, server, "sftp_mkdir", func(_ context.Context, client *sftp.Client) (map[string]any, error) {
		if request.Parents {
			err = client.MkdirAll(path)
		} else {
			err = client.Mkdir(path)
		}
		if err != nil {
			return nil, err
		}
		return map[string]any{"serverId": server.ID, "path": path, "created": true}, nil
	})
}

func (s *Service) SFTPDelete(ctx context.Context, request SFTPDeleteRequest) (map[string]any, error) {
	server, path, err := s.sftpTarget(request.ServerID, request.Path)
	if err != nil {
		return nil, err
	}
	if err := checkPathGuard(server, "sftp_delete", path, false); err != nil {
		return nil, err
	}
	return s.withSFTP(ctx, server, "sftp_delete", func(_ context.Context, client *sftp.Client) (map[string]any, error) {
		if request.Directory {
			err = client.RemoveDirectory(path)
		} else {
			err = client.Remove(path)
		}
		if err != nil {
			return nil, err
		}
		return map[string]any{"serverId": server.ID, "path": path, "deleted": true}, nil
	})
}

func (s *Service) SFTPRename(ctx context.Context, request SFTPRenameRequest) (map[string]any, error) {
	server, from, err := s.sftpTarget(request.ServerID, request.From)
	if err != nil {
		return nil, err
	}
	to, err := requiredPath("to", request.To)
	if err != nil {
		return nil, err
	}
	if err := checkPathGuard(server, "sftp_rename", from, false); err != nil {
		return nil, err
	}
	if err := checkPathGuard(server, "sftp_rename", to, true); err != nil {
		return nil, err
	}
	return s.withSFTP(ctx, server, "sftp_rename", func(_ context.Context, client *sftp.Client) (map[string]any, error) {
		if request.Overwrite {
			// posix-rename@openssh.com atomically replaces an existing target.
			err = client.PosixRename(from, to)
		} else {
			err = client.Rename(from, to)
		}
		if err != nil {
			return nil, err
		}
		return map[string]any{"serverId": server.ID, "from": from, "to": to, "renamed": true}, nil
	})
}

func (s *Service) withSFTP(ctx context.Context, server *config.ServerConfig, operation string, fn func(context.Context, *sftp.Client) (map[string]any, error)) (map[string]any, error) {
	timeout := time.Duration(server.CommandTimeoutMs) * time.Millisecond
	result, err := s.withClient(ctx, server, operation, "", timeout, func(ctx context.Context, client *ssh.Client) (map[string]any, error) {
		sftpClient, err := sftp.NewClient(client)
		if err != nil {
			return nil, fmt.Errorf("SFTP subsystem unavailable: %w", err)
		}
		defer sftpClient.Close()
		result, err := fn(ctx, sftpClient)
		return result, describeSFTPError(err)
	})
	if err != nil {
		return nil, operationError("SFTP "+strings.TrimPrefix(operation, "sftp_"), err)
	}
	return result, nil
}

// checkPathGuard applies system protection to an SFTP change.
func checkPathGuard(server *config.ServerConfig, operation, path string, write bool) error {
	if !server.ProtectSystem {
		return nil
	}
	if reason := pathViolation(path, write); reason != "" {
		audit(operation, server, time.Now(), false, "policy_rejected")
		return guardError(server, reason)
	}
	return nil
}

// sftpTarget validates the server (with SFTP enabled) and the primary path.
func (s *Service) sftpTarget(serverID, path string) (*config.ServerConfig, string, error) {
	server, err := s.server(serverID)
	if err != nil {
		return nil, "", err
	}
	if !server.AllowSFTP {
		return nil, "", fmt.Errorf("SFTP is disabled for server %q (allowSftp is false)", server.ID)
	}
	if path, err = requiredPath("path", path); err != nil {
		return nil, "", err
	}
	return server, path, nil
}

func requiredPath(name, path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	if strings.ContainsRune(path, 0) {
		return "", fmt.Errorf("%s must not contain NUL bytes", name)
	}
	return path, nil
}

func readEncoding(value string) (string, error) {
	switch encoding := strings.ToLower(strings.TrimSpace(value)); encoding {
	case "", "utf8", "utf-8":
		return "utf8", nil
	case "base64":
		return "base64", nil
	default:
		return "", errors.New("encoding must be 'utf8' or 'base64'")
	}
}

// decodeBase64 accepts standard base64 with or without padding and ignores
// whitespace such as line breaks.
func decodeBase64(value string) ([]byte, error) {
	compact := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, value)
	data, err := base64.StdEncoding.DecodeString(compact)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(compact, "="))
	}
	if err != nil {
		return nil, errors.New("contentBase64 is not valid base64")
	}
	return data, nil
}

// trimIncompleteRune drops a UTF-8 sequence cut off at the end of a chunk so
// the returned text is valid and nextOffset resumes on a rune boundary.
func trimIncompleteRune(data []byte) []byte {
	for back := 1; back <= utf8.UTFMax-1 && back <= len(data); back++ {
		b := data[len(data)-back]
		if !utf8.RuneStart(b) {
			continue
		}
		if !utf8.FullRune(data[len(data)-back:]) {
			return data[:len(data)-back]
		}
		break
	}
	return data
}

// describeSFTPError turns remote SFTP status codes into plain messages. It is
// applied only to SFTP calls, never to local errors such as a missing key.
func describeSFTPError(err error) error {
	var status *sftp.StatusError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, os.ErrNotExist):
		return errors.New("no such file or directory")
	case errors.Is(err, os.ErrPermission):
		return errors.New("permission denied")
	case errors.Is(err, os.ErrExist):
		return errors.New("target already exists")
	case errors.As(err, &status) && status.FxCode() == sftp.ErrSSHFxOpUnsupported:
		return errors.New("operation not supported by the remote SFTP server")
	case errors.As(err, &status) && status.FxCode() == sftp.ErrSSHFxFailure:
		return errors.New("the remote SFTP server reported a failure (for example: target exists, directory not empty, or parent missing)")
	default:
		return err
	}
}

func fileInfoMap(info os.FileInfo, name string) map[string]any {
	typeName := "other"
	switch mode := info.Mode(); {
	case mode.IsDir():
		typeName = "directory"
	case mode.IsRegular():
		typeName = "file"
	case mode&os.ModeSymlink != 0:
		typeName = "symlink"
	}
	return map[string]any{
		"name":        name,
		"size":        info.Size(),
		"type":        typeName,
		"permissions": strconv.FormatUint(uint64(info.Mode().Perm()), 8),
		"modified":    info.ModTime().UTC().Format(time.RFC3339),
	}
}
