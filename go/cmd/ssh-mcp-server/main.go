package main

import (
	"context"
	"flag"
	"log"

	"example.com/ssh-mcp-server/internal/config"
	"example.com/ssh-mcp-server/internal/mcpserver"
	"example.com/ssh-mcp-server/internal/sshservice"
)

func main() {
	configPath := flag.String("config", "", "path to the SSH MCP JSON configuration file")
	flag.Parse()

	catalog, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("configuration error: %v", err)
	}
	service := sshservice.New(catalog)
	defer service.Close()
	if err := mcpserver.Run(context.Background(), service); err != nil {
		log.Fatalf("MCP server stopped: %v", err)
	}
}
