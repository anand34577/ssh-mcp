package mcpserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"example.com/ssh-mcp-server/internal/config"
	"example.com/ssh-mcp-server/internal/sshservice"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestBoundedLineReaderRejectsOversizedMessage(t *testing.T) {
	reader := &boundedLineReader{reader: bufio.NewReader(strings.NewReader("12345\n")), maxBytes: 4}
	if _, err := io.ReadAll(reader); err == nil {
		t.Fatal("oversized message was accepted")
	}
}

func TestBoundedLineReaderPreservesMessageBoundaries(t *testing.T) {
	reader := &boundedLineReader{reader: bufio.NewReader(bytes.NewBufferString("one\ntwo\n")), maxBytes: 8}
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "one\ntwo\n" {
		t.Fatalf("unexpected data: %q", data)
	}
}

func connectClient(t *testing.T) *mcp.ClientSession {
	t.Helper()
	t.Setenv(config.EnvAutoAcceptHostKeys, "true")
	catalog, err := config.Parse([]byte(`{"servers":[{"id":"demo","host":"example.test","username":"ops","passwordEnv":"SSH_MCP_DEMO_PASSWORD","allowedCommandPrefixes":["uptime"]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	service := sshservice.New(catalog)
	t.Cleanup(service.Close)

	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := New(service, "test").Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestToolsAreDescribedForModels(t *testing.T) {
	session := connectClient(t)
	if !strings.Contains(session.InitializeResult().Instructions, "ssh_list_servers") {
		t.Fatal("server instructions do not explain the workflow")
	}
	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ssh_list_servers", "ssh_exec", "sftp_list", "sftp_stat", "sftp_read_file", "sftp_write_file",
		"sftp_mkdir", "sftp_delete", "sftp_rename", "ssh_tunnel_open", "ssh_tunnel_close", "ssh_tunnel_list"}
	tools := make(map[string]*mcp.Tool)
	for _, tool := range result.Tools {
		tools[tool.Name] = tool
	}
	for _, name := range want {
		tool := tools[name]
		if tool == nil {
			t.Errorf("tool %s is missing", name)
			continue
		}
		if tool.Description == "" || tool.Annotations == nil || tool.Annotations.Title == "" {
			t.Errorf("tool %s lacks a description or annotations", name)
		}
		schema, _ := json.Marshal(tool.InputSchema)
		var parsed struct {
			Properties map[string]struct {
				Description string `json:"description"`
			} `json:"properties"`
		}
		_ = json.Unmarshal(schema, &parsed)
		for property, definition := range parsed.Properties {
			if definition.Description == "" {
				t.Errorf("%s.%s has no description", name, property)
			}
		}
	}
	if len(result.Tools) != len(want) {
		t.Errorf("got %d tools, want %d", len(result.Tools), len(want))
	}
	if tools["sftp_read_file"] != nil && (!tools["sftp_read_file"].Annotations.ReadOnlyHint || tools["sftp_delete"].Annotations.ReadOnlyHint) {
		t.Error("read-only hints are wrong")
	}
}

func TestToolCallsReturnStructuredResultsAndErrors(t *testing.T) {
	session := connectClient(t)
	ctx := context.Background()

	listed, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "ssh_list_servers", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(listed.StructuredContent)
	if listed.IsError || !strings.Contains(string(encoded), `"demo"`) || strings.Contains(string(encoded), "SSH_MCP_DEMO_PASSWORD") {
		t.Fatalf("unexpected listing: %s", encoded)
	}

	rejected, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "ssh_exec", Arguments: map[string]any{"serverId": "demo", "command": "cat /etc/hostname"}})
	if err != nil {
		t.Fatal(err)
	}
	text := rejected.Content[0].(*mcp.TextContent).Text
	if !rejected.IsError || !strings.Contains(text, "uptime") {
		t.Fatalf("policy rejection should be a tool error naming allowed prefixes, got %v %q", rejected.IsError, text)
	}
}
