// Command ssh-mcp-server is an MCP stdio server that gives an MCP client
// controlled, audited access to pre-configured SSH servers.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"example.com/ssh-mcp-server/internal/config"
	"example.com/ssh-mcp-server/internal/mcpserver"
	"example.com/ssh-mcp-server/internal/sshservice"
	"example.com/ssh-mcp-server/internal/update"
	"golang.org/x/term"
)

// version is set at build time with -ldflags "-X main.version=v1.2.3".
var version = "dev"

func main() {
	log.SetOutput(os.Stderr)
	log.SetPrefix("ssh-mcp ")
	log.SetFlags(log.LstdFlags | log.LUTC)
	update.CleanupOld()
	os.Exit(run(os.Args[1:], os.Stdout))
}

func run(args []string, stdout io.Writer) int {
	// Words such as "setup" are commands for people; flags run the server.
	// A double-click or bare terminal launch (stdin is a terminal, not an AI
	// app's pipe) opens the guided setup.
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return runCommand(args[0], args[1:], stdout)
	}
	if len(args) == 0 && term.IsTerminal(int(os.Stdin.Fd())) {
		return runSetup(nil, ownsConsole())
	}

	flags := flag.NewFlagSet("ssh-mcp-server", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	configPath := flags.String("config", "", "path to servers.json (default: $"+config.EnvConfigPath+", then ./config/servers.json, next to the binary, or the user config dir)")
	validate := flags.Bool("validate", false, "check the configuration, credentials, key files, and known_hosts entries, then exit")
	connect := flags.Bool("connect", false, "with -validate, also connect and authenticate to every server")
	showVersion := flags.Bool("version", false, "print the version and exit")
	flags.Usage = func() {
		fmt.Fprint(flags.Output(), commandHelp+"\nFlags:\n")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, "ssh-mcp-server", version)
		return 0
	}

	catalog, err := config.Load(*configPath)
	if err != nil {
		log.Printf("configuration error: %v", err)
		return 2
	}
	if config.AutoAcceptHostKeys() {
		log.Printf("WARNING: %s is enabled; host-key verification is disabled for every target", config.EnvAutoAcceptHostKeys)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	service := sshservice.New(catalog)
	defer service.Close()

	if *validate {
		return report(stdout, catalog.Source, service.Check(ctx, *connect))
	}
	log.Printf("ssh-mcp-server %s serving %d server(s) from %s", version, len(catalog.Servers), catalog.Source)
	if err := mcpserver.Run(ctx, service, version); err != nil && !isNormalShutdown(err) {
		log.Printf("MCP server stopped: %v", err)
		return 1
	}
	return 0
}

func report(out io.Writer, source string, results []sshservice.CheckResult) int {
	fmt.Fprintf(out, "Configuration: %s\n", source)
	failed := 0
	for _, result := range results {
		status := "OK"
		switch {
		case !result.OK():
			status = "FAIL"
			failed++
		case result.Connected:
			status = "OK (connected)"
		}
		fmt.Fprintf(out, "\n[%s] %s (%s)\n", status, result.ServerID, result.Address)
		for _, problem := range result.Problems {
			fmt.Fprintf(out, "  error:   %s\n", problem)
		}
		for _, warning := range result.Warnings {
			fmt.Fprintf(out, "  warning: %s\n", warning)
		}
	}
	fmt.Fprintf(out, "\n%d of %d server(s) ready.\n", len(results)-failed, len(results))
	if failed > 0 {
		return 1
	}
	return 0
}

func isNormalShutdown(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) ||
		strings.Contains(err.Error(), "server is closing: EOF")
}
