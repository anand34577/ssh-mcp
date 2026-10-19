package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"example.com/ssh-mcp-server/internal/config"
	"example.com/ssh-mcp-server/internal/setup"
	"example.com/ssh-mcp-server/internal/update"
	"golang.org/x/term"
)

const commandHelp = `SSH MCP lets AI apps (Claude, Codex, Cursor, VS Code, Gemini CLI, and others)
work on your servers over SSH. Your passwords and keys stay on this computer.

Commands:
  setup       Add, test, or remove servers and connect your AI apps (guided)
  connect     Connect every AI app found on this computer, without questions
                -app codex,cursor   only these apps
  update      Download and install the latest version (its checksum is verified)
  uninstall   Remove SSH MCP from your AI apps and delete the program
                -purge   also delete your server list    -yes   don't ask
  version     Show the version
  help        Show this help

Check your servers:
  ssh-mcp-server -validate [-connect]

AI apps start it as:
  ssh-mcp-server -config <settings file>

Privacy: no analytics, no telemetry, no data collection. SSH MCP only goes
online to reach your own servers, and to GitHub when you run "update".
`

func runCommand(name string, args []string, stdout io.Writer) int {
	switch name {
	case "setup":
		return runSetup(args, false)
	case "connect":
		return runConnect(args)
	case "update", "upgrade":
		return runUpdate(stdout)
	case "uninstall":
		return runUninstall(args)
	case "version":
		fmt.Fprintln(stdout, "ssh-mcp-server", version)
		return 0
	case "help":
		fmt.Fprint(stdout, commandHelp)
		return 0
	}
	fmt.Fprintf(os.Stderr, "Unknown command %q.\n\n%s", name, commandHelp)
	return 2
}

func commandFlags(name string) (*flag.FlagSet, *string) {
	flags := flag.NewFlagSet("ssh-mcp-server "+name, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	configPath := flags.String("config", "", "settings file (default: the per-user settings file)")
	return flags, configPath
}

// parse returns -1 to continue, or the exit code to stop with.
func parse(flags *flag.FlagSet, args []string) int {
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	return -1
}

func runSetup(args []string, pauseAtEnd bool) int {
	flags, configPath := commandFlags("setup")
	if code := parse(flags, args); code >= 0 {
		return code
	}
	code := 0
	wizard, err := newWizard(*configPath)
	if err == nil {
		err = wizard.Run(context.Background())
	}
	if err != nil {
		if errors.Is(err, setup.ErrCancelled) {
			fmt.Println("\nSetup cancelled. Nothing else was changed.")
		} else {
			fmt.Fprintf(os.Stderr, "\nSetup stopped: %v\n", err)
			code = 1
		}
	}
	if pauseAtEnd {
		// Started by double-click: keep the window open so the result is readable.
		fmt.Print("\nPress Enter to close this window.")
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	}
	return code
}

func runConnect(args []string) int {
	flags, configPath := commandFlags("connect")
	apps := flags.String("app", "", "comma-separated apps to connect: claude-desktop, claude-code, codex, gemini, cursor, windsurf, vscode (default: all found)")
	if code := parse(flags, args); code >= 0 {
		return code
	}
	wizard, err := newWizard(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	var only []string
	for _, app := range strings.Split(*apps, ",") {
		if app = strings.TrimSpace(app); app != "" {
			only = append(only, app)
		}
	}
	if err := wizard.Connect(only); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println("Restart your AI apps to pick up the change.")
	return 0
}

func runUpdate(stdout io.Writer) int {
	updater, err := update.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "Checking for a newer version (you have %s)...\n", version)
	updated, err := updater.Run(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Update stopped: %v\n", err)
		return 1
	}
	if !updated {
		fmt.Fprintln(stdout, "You already have the latest version.")
		return 0
	}
	installed := "the latest version"
	if output, err := exec.Command(updater.Executable, "version").Output(); err == nil {
		installed = strings.TrimSpace(strings.TrimPrefix(string(output), "ssh-mcp-server "))
	}
	fmt.Fprintf(stdout, "Updated to %s. Your servers and AI app connections were kept.\n", installed)
	fmt.Fprintln(stdout, "Restart your AI apps to start using it.")
	return 0
}

func runUninstall(args []string) int {
	flags, configPath := commandFlags("uninstall")
	purge := flags.Bool("purge", false, "also delete your server list")
	yes := flags.Bool("yes", false, "don't ask for confirmation")
	if code := parse(flags, args); code >= 0 {
		return code
	}
	wizard, err := newWizard(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	ask := !*yes && term.IsTerminal(int(os.Stdin.Fd()))
	if ask {
		fmt.Printf("This removes SSH MCP from your AI apps and deletes %s.\n", wizard.Executable)
		sure, err := wizard.Confirm("Continue?", false)
		if err != nil || !sure {
			fmt.Println("Nothing was changed.")
			return 0
		}
	}

	wizard.Disconnect()
	deleteSettings := *purge
	if ask && !deleteSettings {
		deleteSettings, _ = wizard.Confirm("Also delete your list of servers?", false)
	}
	if deleteSettings {
		if err := wizard.RemoveSettings(); err != nil {
			fmt.Fprintf(os.Stderr, "Couldn't delete %s: %v\n", wizard.ConfigPath, err)
		}
	} else if _, err := os.Stat(wizard.ConfigPath); err == nil {
		fmt.Printf("  Kept your server list at %s, in case you reinstall.\n", wizard.ConfigPath)
	}
	removeProgram(wizard.Executable)
	fmt.Println("SSH MCP has been uninstalled. Restart your AI apps to finish.")
	return 0
}

// removeProgram deletes this program. Windows can't delete a running
// program, but it can move it to the temporary folder.
func removeProgram(path string) {
	err := os.Remove(path)
	if err != nil && runtime.GOOS == "windows" {
		err = os.Rename(path, filepath.Join(os.TempDir(), fmt.Sprintf("ssh-mcp-server-removed-%d.exe", os.Getpid())))
	}
	if err != nil {
		fmt.Printf("  Couldn't delete %s (%v). Close your AI apps, then delete it yourself.\n", path, err)
		return
	}
	fmt.Printf("  [ok] Deleted %s.\n", path)
	if dir := filepath.Dir(path); filepath.Base(dir) == "ssh-mcp" {
		_ = os.Remove(dir) // the installer's own folder, only if now empty
	}
}

// newWizard opens the settings file the server would use, or the per-user
// location when none exists yet.
func newWizard(explicit string) (*setup.Wizard, error) {
	path, _ := config.Resolve(explicit)
	if explicit == "" && os.Getenv(config.EnvConfigPath) == "" {
		if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
			userPath, err := config.UserConfigPath()
			if err != nil {
				return nil, err
			}
			path = userPath
		}
	}
	var readSecret func() (string, error)
	if term.IsTerminal(int(os.Stdin.Fd())) {
		readSecret = func() (string, error) {
			secret, err := term.ReadPassword(int(os.Stdin.Fd()))
			return string(secret), err
		}
	}
	return setup.New(path, readSecret)
}
