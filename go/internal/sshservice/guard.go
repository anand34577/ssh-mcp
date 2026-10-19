package sshservice

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"example.com/ssh-mcp-server/internal/config"
)

// System protection ("protectSystem", on by default) refuses a short list of
// commands and file operations that could wipe, brick, or switch off a server:
// recursive deletes of system or home folders, formatting disks, writing over
// a raw disk, shutting down, and similar. It is a safety net against mistakes,
// not a sandbox: a determined caller can disguise a command. Real restriction
// comes from allowedCommandPrefixes and a least-privilege account.
//
// The Java implementation (SystemGuard.java) follows the same rules; keep the
// two in step.

var (
	forkBombPattern       = regexp.MustCompile(`:\s*\(\s*\)\s*\{[^}]*:\s*\|\s*:`)
	diskDevicePattern     = regexp.MustCompile(`(>|\bof=)\s*/dev/(sd[a-z]|hd[a-z]|vd[a-z]|xvd[a-z]|nvme[0-9]|mmcblk[0-9]|md[0-9]|dm-[0-9]|disk[0-9]|mapper/|root\b)`)
	bracedVariablePattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)
	envAssignPattern      = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
	wrapperArgPattern     = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?[smhd]?$`)
	homeFolderPattern     = regexp.MustCompile(`^/(home|Users)/[^/]+$`)
	commandSeparators     = ";&|(){}<>`\n\r"
	quoteCharacters       = "'\"\\"
	globCharacters        = "*?["
	deviceWriteAllowed    = []string{"/dev/null", "/dev/zero", "/dev/stdout", "/dev/stderr", "/dev/tty"}
)

// protectedFolders may never be deleted, moved, or recursively re-permissioned.
var protectedFolders = map[string]bool{
	"/bin": true, "/boot": true, "/dev": true, "/etc": true, "/etc/ssh": true, "/home": true,
	"/lib": true, "/lib32": true, "/lib64": true, "/libx32": true, "/media": true, "/mnt": true,
	"/opt": true, "/proc": true, "/root": true, "/run": true, "/sbin": true, "/snap": true,
	"/srv": true, "/sys": true, "/usr": true, "/usr/bin": true, "/usr/include": true,
	"/usr/lib": true, "/usr/lib64": true, "/usr/libexec": true, "/usr/local": true,
	"/usr/sbin": true, "/usr/share": true, "/var": true, "/var/lib": true,
	// macOS
	"/Applications": true, "/Library": true, "/System": true, "/Users": true,
	"/private": true, "/private/etc": true, "/private/var": true,
}

// criticalFiles may not be overwritten, replaced, or deleted over SFTP:
// a bad edit to any of them can lock everyone out or stop the server booting.
var criticalFiles = map[string]bool{
	"/etc/passwd": true, "/etc/shadow": true, "/etc/group": true, "/etc/gshadow": true,
	"/etc/sudoers": true, "/etc/fstab": true,
}

// systemTrees hold the kernel, boot files, and live device/process views;
// nothing should be written there over SFTP.
var systemTrees = []string{"/boot/", "/dev/", "/proc/", "/sys/"}

// wrappers run the command that follows them, so the guard looks past them
// (sudo rm -rf / is checked as rm -rf /). The value lists name options that
// take a separate argument, which must be skipped too.
var wrappers = map[string][]string{
	"sudo": {"-u", "-g", "-U", "-C", "-D", "-h", "-p", "-r", "-t"}, "doas": {"-u", "-C"},
	"env": {"-u", "-C", "-S"}, "nice": {"-n"}, "ionice": {"-c", "-n", "-p"}, "nohup": nil,
	"time": nil, "timeout": {"-s", "-k"}, "stdbuf": nil, "exec": nil, "command": nil,
	"builtin": nil, "eval": nil, "busybox": nil, "xargs": {"-I", "-n", "-P", "-L", "-d", "-E", "-s", "-a"},
	"sh": nil, "bash": nil, "dash": nil, "zsh": nil, "ksh": nil, "ash": nil, "su": {"-s", "-g", "-G"},
	"then": nil, "do": nil, "else": nil, "if": nil, "while": nil, "until": nil, "!": nil,
}

// guardError explains a refusal in words a person (or a small model) can act on.
func guardError(server *config.ServerConfig, reason string) error {
	return fmt.Errorf("blocked by system protection: %s. This safety check stops actions that could wipe, break, or switch off the server. "+
		"Do not try another way; tell the user what was blocked. Only the user can turn this check off, by setting \"protectSystem\": false for server %q in servers.json",
		reason, server.ID)
}

// commandViolation returns why a shell command is refused, or "" if it is allowed.
func commandViolation(command string) string {
	// Quotes and backslashes are dropped first, so r'm' -rf "/" reads as rm -rf /.
	unquoted := strings.Map(func(r rune) rune {
		if strings.ContainsRune(quoteCharacters, r) {
			return -1
		}
		return r
	}, command)
	// ${HOME} reads as $HOME, so its braces are not taken for a command group.
	unquoted = bracedVariablePattern.ReplaceAllString(unquoted, "$$$1")
	if forkBombPattern.MatchString(unquoted) {
		return "a fork bomb would freeze the server"
	}
	if match := diskDevicePattern.FindString(unquoted); match != "" {
		return "writing straight to a disk device (" + strings.TrimSpace(strings.TrimLeft(match, ">")) + ") destroys its data"
	}
	segments := strings.FieldsFunc(unquoted, func(r rune) bool { return strings.ContainsRune(commandSeparators, r) })
	for _, segment := range segments {
		if reason := segmentViolation(strings.Fields(segment)); reason != "" {
			return reason
		}
	}
	return ""
}

func segmentViolation(words []string) string {
	var valueOptions []string
	for i := 0; i < len(words); i++ {
		word := words[i]
		name := word[strings.LastIndex(word, "/")+1:]
		switch {
		case envAssignPattern.MatchString(word), wrapperArgPattern.MatchString(word):
			continue
		case strings.HasPrefix(word, "-"):
			if slices.Contains(valueOptions, word) {
				i++
			}
			continue
		}
		if options, ok := wrappers[name]; ok {
			valueOptions = options
			continue
		}
		return programViolation(name, words[i+1:])
	}
	return ""
}

func programViolation(name string, args []string) string {
	operands := nonOptions(args)
	switch {
	case name == "rm":
		if slices.Contains(args, "--no-preserve-root") {
			return "rm --no-preserve-root deletes the whole system"
		}
		if hasFlag(args, "r", "R", "--recursive") {
			if target := firstProtected(operands); target != "" {
				return "deleting " + target + " and everything in it"
			}
		}
	case name == "shred":
		for _, operand := range operands {
			if strings.HasPrefix(operand, "/dev/") || isProtected(operand) {
				return "shredding " + operand
			}
		}
	case name == "find":
		if slices.Contains(args, "-delete") || findExecRemoves(args) {
			if target := firstProtected(findRoots(args)); target != "" {
				return "find deleting files under " + target
			}
		}
	case name == "chmod" || name == "chown" || name == "chgrp":
		if hasFlag(args, "R", "", "--recursive") {
			if target := firstProtected(operands); target != "" {
				return "changing permissions or owner of everything under " + target
			}
		}
	case name == "mv":
		if len(operands) >= 2 {
			if target := firstProtected(operands[:len(operands)-1]); target != "" {
				return "moving " + target + " away"
			}
			if operands[len(operands)-1] == "/dev/null" {
				return "moving files into /dev/null deletes them"
			}
		}
	case name == "dd":
		for _, arg := range args {
			if device, ok := strings.CutPrefix(arg, "of="); ok && strings.HasPrefix(device, "/dev/") && !slices.Contains(deviceWriteAllowed, device) {
				return "dd writing over the device " + device
			}
		}
	case name == "mkfs" || strings.HasPrefix(name, "mkfs.") || name == "mke2fs" || name == "mkswap" ||
		name == "wipefs" || name == "blkdiscard":
		return name + " formats or wipes a disk"
	case name == "shutdown" || name == "reboot" || name == "poweroff" || name == "halt":
		return name + " would switch off or restart the server"
	case name == "init" || name == "telinit":
		if slices.Contains(args, "0") || slices.Contains(args, "6") {
			return name + " would switch off or restart the server"
		}
	case name == "systemctl":
		for _, action := range []string{"poweroff", "reboot", "halt", "kexec"} {
			if slices.Contains(args, action) {
				return "systemctl " + action + " would switch off or restart the server"
			}
		}
	}
	return ""
}

// pathViolation returns why an SFTP change to target is refused, or "" if
// allowed. write is true for creating, overwriting, or renaming onto target;
// false for deleting or renaming it away.
func pathViolation(target string, write bool) string {
	if !strings.HasPrefix(target, "/") {
		// Relative SFTP paths start in the user's home folder.
		if rest := path.Clean(target); rest == "." {
			return "changing the home folder itself"
		}
		return ""
	}
	cleaned := path.Clean(target)
	switch {
	case criticalFiles[cleaned]:
		return "changing " + cleaned + " can lock everyone out or stop the server starting"
	case isProtected(cleaned):
		return "changing the system folder " + cleaned
	case write:
		for _, tree := range systemTrees {
			if strings.HasPrefix(cleaned, tree) {
				return "writing inside " + strings.TrimSuffix(tree, "/")
			}
		}
	}
	return ""
}

// isProtected reports whether target names the root, a system folder, or a
// home folder (including forms such as /*, /etc/, ~ and $HOME/*).
func isProtected(target string) bool {
	for _, home := range []string{"~", "$HOME"} {
		if target == home {
			return true
		}
		if rest, ok := strings.CutPrefix(target, home+"/"); ok {
			rest = path.Clean("/" + rest)
			return rest == "/" || rest == "/*"
		}
	}
	if strings.HasPrefix(target, "~") && !strings.Contains(target, "/") {
		return true // ~user
	}
	if !strings.HasPrefix(target, "/") {
		return false
	}
	cleaned := path.Clean(target)
	for strings.HasSuffix(cleaned, "/*") {
		cleaned = path.Clean(strings.TrimSuffix(cleaned, "/*") + "/")
	}
	if cleaned == "/" || protectedFolders[cleaned] || homeFolderPattern.MatchString(cleaned) {
		return true
	}
	// A wildcard right under the root, such as /e* or /[a-z]*, can match system folders.
	return strings.Count(cleaned, "/") == 1 && strings.ContainsAny(cleaned, globCharacters)
}

func firstProtected(targets []string) string {
	for _, target := range targets {
		if isProtected(target) {
			return target
		}
	}
	return ""
}

// hasFlag reports whether args contain a short flag letter (alone or combined,
// such as -rf) or the long form.
func hasFlag(args []string, short, altShort, long string) bool {
	for _, arg := range args {
		if arg == long {
			return true
		}
		if strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") &&
			(strings.Contains(arg, short) || (altShort != "" && strings.Contains(arg, altShort))) {
			return true
		}
	}
	return false
}

func nonOptions(args []string) []string {
	var operands []string
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			operands = append(operands, arg)
		}
	}
	return operands
}

// findRoots returns the starting folders of a find command (the arguments
// before the first expression).
func findRoots(args []string) []string {
	var roots []string
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") || arg == "!" {
			break
		}
		roots = append(roots, arg)
	}
	return roots
}

func findExecRemoves(args []string) bool {
	for i, arg := range args {
		if (arg == "-exec" || arg == "-execdir" || arg == "-ok") && i+1 < len(args) {
			name := args[i+1][strings.LastIndex(args[i+1], "/")+1:]
			if name == "rm" || name == "shred" {
				return true
			}
		}
	}
	return false
}
