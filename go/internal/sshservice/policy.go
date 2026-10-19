package sshservice

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"example.com/ssh-mcp-server/internal/config"
)

// checkCommandPolicy enforces deniedCommandRegexes first, then the allow-list.
// With an allow-list configured, shell metacharacters are rejected outright so
// an allowed prefix cannot be chained into an arbitrary command.
func checkCommandPolicy(server *config.ServerConfig, command string) error {
	for _, pattern := range server.DeniedPatterns {
		if pattern.MatchString(command) {
			return errors.New("command rejected by the server command policy (matched a deniedCommandRegexes rule)")
		}
	}
	if len(server.AllowedCommandPrefixes) == 0 {
		return nil
	}
	if hasShellOperators(command) {
		return errors.New("command contains shell metacharacters (; | & < > $ ` ( ) { } ' \" \\ or a newline), which are not allowed when allowedCommandPrefixes is configured")
	}
	trimmed := strings.TrimSpace(command)
	for _, prefix := range server.AllowedCommandPrefixes {
		if trimmed == prefix {
			return nil
		}
		if rest, ok := strings.CutPrefix(trimmed, prefix); ok {
			if next, _ := utf8.DecodeRuneInString(rest); unicode.IsSpace(next) {
				return nil
			}
		}
	}
	return errors.New("command is not allowed by the server command policy; it must start with one of: " + strings.Join(server.AllowedCommandPrefixes, ", "))
}

func hasShellOperators(command string) bool {
	return strings.ContainsAny(command, config.ShellMetacharacters+"\x00\n\r")
}
