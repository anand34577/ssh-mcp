package main

import (
	"strings"
	"testing"
)

func TestCommands(t *testing.T) {
	var out strings.Builder
	if code := run([]string{"version"}, &out); code != 0 || !strings.HasPrefix(out.String(), "ssh-mcp-server ") {
		t.Fatalf("version: code=%d out=%q", code, out.String())
	}
	out.Reset()
	if code := run([]string{"help"}, &out); code != 0 || !strings.Contains(out.String(), "no telemetry") {
		t.Fatalf("help: code=%d out=%q", code, out.String())
	}
	if code := run([]string{"frobnicate"}, &out); code != 2 {
		t.Fatalf("unknown command: code=%d", code)
	}
}
