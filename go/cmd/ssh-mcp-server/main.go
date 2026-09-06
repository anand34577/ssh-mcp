package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"example.com/ssh-mcp-server/internal/config"
	"example.com/ssh-mcp-server/internal/mcpserver"
	"example.com/ssh-mcp-server/internal/sshservice"
)

func main() {
	if err := run(); err != nil {
		log.Printf("MCP server stopped: %v", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "", "path to the SSH MCP JSON configuration file")
	flag.Parse()

	catalog, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("configuration error: %w", err)
	}
	if config.AutoAcceptHostKeys() {
		log.Printf("WARNING: SSH_MCP_AUTO_ACCEPT_HOST_KEYS is enabled; host-key verification is disabled for every target")
	}
	service := sshservice.New(catalog)
	defer service.Close()
	if err := mcpserver.Run(context.Background(), service); err != nil &&
		!(errors.Is(err, io.EOF) || strings.Contains(err.Error(), "server is closing: EOF")) {
		return err
	}
	return nil
}
