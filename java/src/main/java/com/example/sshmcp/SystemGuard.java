package com.example.sshmcp;

import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.Collections;
import java.util.Deque;
import java.util.HashMap;
import java.util.HashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/**
 * System protection ("protectSystem", on by default) refuses a short list of commands and file
 * operations that could wipe, brick, or switch off a server: recursive deletes of system or home
 * folders, formatting disks, writing over a raw disk, shutting down, and similar. It is a safety
 * net against mistakes, not a sandbox: a determined caller can disguise a command. Real
 * restriction comes from allowedCommandPrefixes and a least-privilege account.
 *
 * <p>The Go implementation (go/internal/sshservice/guard.go) follows the same rules; keep the two
 * in step.
 */
final class SystemGuard {
    private static final Pattern FORK_BOMB = Pattern.compile(":\\s*\\(\\s*\\)\\s*\\{[^}]*:\\s*\\|\\s*:");
    private static final Pattern DISK_DEVICE = Pattern.compile(
            "(>|\\bof=)\\s*/dev/(sd[a-z]|hd[a-z]|vd[a-z]|xvd[a-z]|nvme[0-9]|mmcblk[0-9]|md[0-9]|dm-[0-9]|disk[0-9]|mapper/|root\\b)");
    private static final Pattern BRACED_VARIABLE = Pattern.compile("\\$\\{([A-Za-z_][A-Za-z0-9_]*)\\}");
    private static final Pattern ENV_ASSIGN = Pattern.compile("^[A-Za-z_][A-Za-z0-9_]*=.*");
    private static final Pattern WRAPPER_ARG = Pattern.compile("^[0-9]+(\\.[0-9]+)?[smhd]?$");
    private static final Pattern HOME_FOLDER = Pattern.compile("^/(home|Users)/[^/]+$");
    private static final String COMMAND_SEPARATORS = ";&|(){}<>`\n\r";
    private static final String QUOTE_CHARACTERS = "'\"\\";
    private static final String GLOB_CHARACTERS = "*?[";
    private static final List<String> DEVICE_WRITE_ALLOWED = Arrays.asList(
            "/dev/null", "/dev/zero", "/dev/stdout", "/dev/stderr", "/dev/tty");

    /** May never be deleted, moved, or recursively re-permissioned. */
    private static final Set<String> PROTECTED_FOLDERS = new HashSet<>(Arrays.asList(
            "/bin", "/boot", "/dev", "/etc", "/etc/ssh", "/home", "/lib", "/lib32", "/lib64", "/libx32",
            "/media", "/mnt", "/opt", "/proc", "/root", "/run", "/sbin", "/snap", "/srv", "/sys", "/usr",
            "/usr/bin", "/usr/include", "/usr/lib", "/usr/lib64", "/usr/libexec", "/usr/local", "/usr/sbin",
            "/usr/share", "/var", "/var/lib",
            // macOS
            "/Applications", "/Library", "/System", "/Users", "/private", "/private/etc", "/private/var"));

    /** May not be overwritten, replaced, or deleted over SFTP: a bad edit can lock everyone out. */
    private static final Set<String> CRITICAL_FILES = new HashSet<>(Arrays.asList(
            "/etc/passwd", "/etc/shadow", "/etc/group", "/etc/gshadow", "/etc/sudoers", "/etc/fstab"));

    /** Kernel, boot files, and live device/process views; nothing is written there over SFTP. */
    private static final List<String> SYSTEM_TREES = Arrays.asList("/boot/", "/dev/", "/proc/", "/sys/");

    /**
     * Programs that run the command after them, so the guard looks past them (sudo rm -rf / is
     * checked as rm -rf /). The values name options that take a separate argument.
     */
    private static final Map<String, List<String>> WRAPPERS = new HashMap<>();

    static {
        wrapper("sudo", "-u", "-g", "-U", "-C", "-D", "-h", "-p", "-r", "-t");
        wrapper("doas", "-u", "-C");
        wrapper("env", "-u", "-C", "-S");
        wrapper("nice", "-n");
        wrapper("ionice", "-c", "-n", "-p");
        wrapper("timeout", "-s", "-k");
        wrapper("xargs", "-I", "-n", "-P", "-L", "-d", "-E", "-s", "-a");
        wrapper("su", "-s", "-g", "-G");
        for (String name : Arrays.asList("nohup", "time", "stdbuf", "exec", "command", "builtin", "eval", "busybox",
                "sh", "bash", "dash", "zsh", "ksh", "ash", "then", "do", "else", "if", "while", "until", "!")) {
            wrapper(name);
        }
    }

    private SystemGuard() {
    }

    private static void wrapper(String name, String... valueOptions) {
        WRAPPERS.put(name, Arrays.asList(valueOptions));
    }

    /** Explains a refusal in words a person (or a small model) can act on. */
    static String message(String serverId, String reason) {
        return "blocked by system protection: " + reason + ". This safety check stops actions that could wipe, break, or switch off the server. "
                + "Do not try another way; tell the user what was blocked. Only the user can turn this check off, by setting \"protectSystem\": false for server \""
                + serverId + "\" in servers.json";
    }

    /** Returns why a shell command is refused, or null if it is allowed. */
    static String commandViolation(String command) {
        // Quotes and backslashes are dropped first, so r'm' -rf "/" reads as rm -rf /.
        StringBuilder builder = new StringBuilder(command.length());
        for (int i = 0; i < command.length(); i++) {
            char c = command.charAt(i);
            if (QUOTE_CHARACTERS.indexOf(c) < 0) {
                builder.append(c);
            }
        }
        // ${HOME} reads as $HOME, so its braces are not taken for a command group.
        String unquoted = BRACED_VARIABLE.matcher(builder).replaceAll("\\$$1");
        if (FORK_BOMB.matcher(unquoted).find()) {
            return "a fork bomb would freeze the server";
        }
        Matcher device = DISK_DEVICE.matcher(unquoted);
        if (device.find()) {
            return "writing straight to a disk device (" + device.group().replaceFirst("^>+", "").trim() + ") destroys its data";
        }
        StringBuilder segment = new StringBuilder();
        for (int i = 0; i <= unquoted.length(); i++) {
            if (i == unquoted.length() || COMMAND_SEPARATORS.indexOf(unquoted.charAt(i)) >= 0) {
                String reason = segmentViolation(words(segment.toString()));
                if (reason != null) {
                    return reason;
                }
                segment.setLength(0);
            }
            else {
                segment.append(unquoted.charAt(i));
            }
        }
        return null;
    }

    private static String segmentViolation(List<String> words) {
        List<String> valueOptions = Collections.emptyList();
        for (int i = 0; i < words.size(); i++) {
            String word = words.get(i);
            String name = baseName(word);
            if (ENV_ASSIGN.matcher(word).matches() || WRAPPER_ARG.matcher(word).matches()) {
                continue;
            }
            if (word.startsWith("-")) {
                if (valueOptions.contains(word)) {
                    i++;
                }
                continue;
            }
            List<String> options = WRAPPERS.get(name);
            if (options != null) {
                valueOptions = options;
                continue;
            }
            return programViolation(name, words.subList(i + 1, words.size()));
        }
        return null;
    }

    private static String programViolation(String name, List<String> args) {
        List<String> operands = nonOptions(args);
        switch (name) {
            case "rm":
                if (args.contains("--no-preserve-root")) {
                    return "rm --no-preserve-root deletes the whole system";
                }
                if (hasFlag(args, "r", "R", "--recursive")) {
                    String target = firstProtected(operands);
                    if (target != null) {
                        return "deleting " + target + " and everything in it";
                    }
                }
                return null;
            case "shred":
                for (String operand : operands) {
                    if (operand.startsWith("/dev/") || isProtected(operand)) {
                        return "shredding " + operand;
                    }
                }
                return null;
            case "find":
                if (args.contains("-delete") || findExecRemoves(args)) {
                    String target = firstProtected(findRoots(args));
                    if (target != null) {
                        return "find deleting files under " + target;
                    }
                }
                return null;
            case "chmod":
            case "chown":
            case "chgrp":
                if (hasFlag(args, "R", "", "--recursive")) {
                    String target = firstProtected(operands);
                    if (target != null) {
                        return "changing permissions or owner of everything under " + target;
                    }
                }
                return null;
            case "mv":
                if (operands.size() >= 2) {
                    String target = firstProtected(operands.subList(0, operands.size() - 1));
                    if (target != null) {
                        return "moving " + target + " away";
                    }
                    if ("/dev/null".equals(operands.get(operands.size() - 1))) {
                        return "moving files into /dev/null deletes them";
                    }
                }
                return null;
            case "dd":
                for (String arg : args) {
                    if (arg.startsWith("of=/dev/") && !DEVICE_WRITE_ALLOWED.contains(arg.substring(3))) {
                        return "dd writing over the device " + arg.substring(3);
                    }
                }
                return null;
            case "mkfs":
            case "mke2fs":
            case "mkswap":
            case "wipefs":
            case "blkdiscard":
                return name + " formats or wipes a disk";
            case "shutdown":
            case "reboot":
            case "poweroff":
            case "halt":
                return name + " would switch off or restart the server";
            case "init":
            case "telinit":
                if (args.contains("0") || args.contains("6")) {
                    return name + " would switch off or restart the server";
                }
                return null;
            case "systemctl":
                for (String action : Arrays.asList("poweroff", "reboot", "halt", "kexec")) {
                    if (args.contains(action)) {
                        return "systemctl " + action + " would switch off or restart the server";
                    }
                }
                return null;
            default:
                return name.startsWith("mkfs.") ? name + " formats or wipes a disk" : null;
        }
    }

    /**
     * Returns why an SFTP change to target is refused, or null if allowed. write is true for
     * creating, overwriting, or renaming onto target; false for deleting or renaming it away.
     */
    static String pathViolation(String target, boolean write) {
        if (!target.startsWith("/")) {
            // Relative SFTP paths start in the user's home folder.
            return ".".equals(clean(target)) ? "changing the home folder itself" : null;
        }
        String cleaned = clean(target);
        if (CRITICAL_FILES.contains(cleaned)) {
            return "changing " + cleaned + " can lock everyone out or stop the server starting";
        }
        if (isProtected(cleaned)) {
            return "changing the system folder " + cleaned;
        }
        if (write) {
            for (String tree : SYSTEM_TREES) {
                if (cleaned.startsWith(tree)) {
                    return "writing inside " + tree.substring(0, tree.length() - 1);
                }
            }
        }
        return null;
    }

    /** True for the root, a system folder, or a home folder (including /*, /etc/, ~ and $HOME/*). */
    static boolean isProtected(String target) {
        for (String home : Arrays.asList("~", "$HOME")) {
            if (target.equals(home)) {
                return true;
            }
            if (target.startsWith(home + "/")) {
                String rest = clean("/" + target.substring(home.length() + 1));
                return "/".equals(rest) || "/*".equals(rest);
            }
        }
        if (target.startsWith("~") && target.indexOf('/') < 0) {
            return true; // ~user
        }
        if (!target.startsWith("/")) {
            return false;
        }
        String cleaned = clean(target);
        while (cleaned.endsWith("/*")) {
            cleaned = clean(cleaned.substring(0, cleaned.length() - 2) + "/");
        }
        if ("/".equals(cleaned) || PROTECTED_FOLDERS.contains(cleaned) || HOME_FOLDER.matcher(cleaned).matches()) {
            return true;
        }
        // A wildcard right under the root, such as /e* or /[a-z]*, can match system folders.
        return cleaned.indexOf('/', 1) < 0 && Config.ServerConfig.containsAny(cleaned, GLOB_CHARACTERS);
    }

    /** Lexical path cleanup with the same results as Go's path.Clean. */
    static String clean(String path) {
        boolean rooted = path.startsWith("/");
        Deque<String> parts = new ArrayDeque<>();
        for (String part : path.split("/")) {
            if (part.isEmpty() || ".".equals(part)) {
                continue;
            }
            if ("..".equals(part)) {
                if (!parts.isEmpty() && !"..".equals(parts.peekLast())) {
                    parts.removeLast();
                }
                else if (!rooted) {
                    parts.addLast("..");
                }
                continue;
            }
            parts.addLast(part);
        }
        String joined = String.join("/", parts);
        if (rooted) {
            return "/" + joined;
        }
        return joined.isEmpty() ? "." : joined;
    }

    private static String firstProtected(List<String> targets) {
        for (String target : targets) {
            if (isProtected(target)) {
                return target;
            }
        }
        return null;
    }

    /** True when args contain a short flag letter (alone or combined, such as -rf) or the long form. */
    private static boolean hasFlag(List<String> args, String shortFlag, String altShort, String longFlag) {
        for (String arg : args) {
            if (arg.equals(longFlag)) {
                return true;
            }
            if (arg.startsWith("-") && !arg.startsWith("--")
                    && (arg.contains(shortFlag) || (!altShort.isEmpty() && arg.contains(altShort)))) {
                return true;
            }
        }
        return false;
    }

    private static List<String> nonOptions(List<String> args) {
        List<String> operands = new ArrayList<>();
        for (String arg : args) {
            if (!arg.startsWith("-")) {
                operands.add(arg);
            }
        }
        return operands;
    }

    /** The starting folders of a find command (the arguments before the first expression). */
    private static List<String> findRoots(List<String> args) {
        List<String> roots = new ArrayList<>();
        for (String arg : args) {
            if (arg.startsWith("-") || "!".equals(arg)) {
                break;
            }
            roots.add(arg);
        }
        return roots;
    }

    private static boolean findExecRemoves(List<String> args) {
        for (int i = 0; i + 1 < args.size(); i++) {
            String arg = args.get(i);
            if ("-exec".equals(arg) || "-execdir".equals(arg) || "-ok".equals(arg)) {
                String name = baseName(args.get(i + 1));
                if ("rm".equals(name) || "shred".equals(name)) {
                    return true;
                }
            }
        }
        return false;
    }

    private static String baseName(String word) {
        return word.substring(word.lastIndexOf('/') + 1);
    }

    private static List<String> words(String segment) {
        List<String> words = new ArrayList<>();
        for (String word : segment.trim().split("\\s+")) {
            if (!word.isEmpty()) {
                words.add(word);
            }
        }
        return words;
    }
}
