package mcpserver

import (
	"bufio"
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestBoundedLineReaderRejectsOversizedMessage(t *testing.T) {
	reader := &boundedLineReader{
		reader:   bufio.NewReader(strings.NewReader("12345\n")),
		maxBytes: 4,
	}
	if _, err := io.ReadAll(reader); err == nil {
		t.Fatal("oversized message was accepted")
	}
}

func TestBoundedLineReaderPreservesMessageBoundaries(t *testing.T) {
	reader := &boundedLineReader{
		reader:   bufio.NewReader(bytes.NewBufferString("one\ntwo\n")),
		maxBytes: 8,
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "one\ntwo\n" {
		t.Fatalf("unexpected data: %q", data)
	}
}
