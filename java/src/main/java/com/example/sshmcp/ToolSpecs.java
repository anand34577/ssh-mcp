package com.example.sshmcp;

import com.fasterxml.jackson.databind.node.ArrayNode;
import com.fasterxml.jackson.databind.node.JsonNodeFactory;
import com.fasterxml.jackson.databind.node.ObjectNode;

import java.util.Arrays;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;

/**
 * The single definition of every MCP tool: name, descriptions, input schema, and behavior hints.
 * Both tools/list and argument validation are derived from it, so they cannot drift apart. Keep
 * the wording in sync with the Go implementation (go/internal/mcpserver/server.go).
 */
final class ToolSpecs {
    static final String INSTRUCTIONS = "SSH access to pre-configured servers. Credentials, host keys, and command policy are managed by the server operator and are never visible to you.\n"
            + "Workflow: call ssh_list_servers first and use its \"id\" as serverId. Check each server's sftpEnabled, portForwardingEnabled, allowedCommandPrefixes, commandTimeoutMs, and maxOutputBytes before acting.\n"
            + "Prefer sftp_* tools over shell commands for reading and writing files. ssh_exec runs a non-interactive command with no TTY: avoid commands that prompt (use flags like -y), page output, or run forever. A non-zero exitCode is a normal result, not a tool failure; timedOut=true means the command was killed.\n"
            + "When stdoutTruncated/stderrTruncated or sftp_read_file truncated is true, narrow the command or page with offset=nextOffset instead of retrying unchanged.\n"
            + "Close every tunnel you open with ssh_tunnel_close. Confirm with the user before destructive actions (deleting files, restarting services, overwriting configuration). If a call fails with \"blocked by system protection\" or \"not allowed by the server command policy\", stop and tell the user; never rephrase, encode, or split the command to get around it.";

    private static final String SERVER_ID = "Server id from ssh_list_servers";
    private static final Map<String, Spec> SPECS = new LinkedHashMap<>();

    static {
        add(new Spec("ssh_list_servers", "List SSH servers",
                "List configured SSH servers with their ids, capabilities, limits, and command policy. Call this first; it never returns secrets.",
                Hints.readOnly(false)));
        add(new Spec("ssh_exec", "Run SSH command",
                "Run a non-interactive command on a server and return stdout, stderr, exitCode, exitSignal, timedOut, and truncation flags. Output is capped; a non-zero exit code is returned as a normal result.",
                Hints.mutating(true, false),
                Param.required("serverId", "string", SERVER_ID),
                Param.required("command", "string", "Command line run by the remote user's login shell without a TTY (max 32768 bytes)"),
                Param.optional("timeoutMs", "integer", "Optional timeout in milliseconds, 100 up to the server's commandTimeoutMs (the default). Connection time is not counted"),
                Param.optional("maxOutputBytes", "integer", "Optional cap on captured bytes per stream, 1024 up to the server's maxOutputBytes (the default)")));
        add(new Spec("sftp_list", "List remote directory",
                "List a remote directory (name, type, size, permissions, modified time), sorted by name.",
                Hints.readOnly(true),
                Param.required("serverId", "string", SERVER_ID),
                Param.required("path", "string", "Remote directory path; relative paths start in the login directory"),
                Param.optional("maxEntries", "integer", "Maximum entries to return, 1-10000 (default 1000); entries are sorted by name")));
        add(new Spec("sftp_stat", "Stat remote path",
                "Get metadata for a remote file or directory (type, size, permissions, modified time); follows symlinks.",
                Hints.readOnly(true),
                Param.required("serverId", "string", SERVER_ID),
                Param.required("path", "string", "Remote file or directory path")));
        add(new Spec("sftp_read_file", "Read remote file",
                "Read a remote file as UTF-8 text or base64, up to maxBytes from offset. Returns size, nextOffset, and truncated so large files can be paged.",
                Hints.readOnly(true),
                Param.required("serverId", "string", SERVER_ID),
                Param.required("path", "string", "Remote file path"),
                Param.optional("offset", "integer", "Byte offset to start reading from (default 0); pass the previous nextOffset to page through a large file"),
                Param.optional("maxBytes", "integer", "Maximum bytes to return, 1 up to the server's maxOutputBytes (the default)"),
                Param.optional("encoding", "string", "utf8 (default, for text) or base64 (for binary files)")));
        add(new Spec("sftp_write_file", "Write remote file",
                "Create or overwrite a remote file with UTF-8 content or base64 contentBase64 (exactly one). Size is capped at the server's maxOutputBytes.",
                Hints.mutating(true, true),
                Param.required("serverId", "string", SERVER_ID),
                Param.required("path", "string", "Remote file path; the file is created or fully replaced and the parent directory must exist"),
                Param.optional("content", "string", "Text content written as UTF-8; provide exactly one of content or contentBase64"),
                Param.optional("contentBase64", "string", "Binary content as standard base64; provide exactly one of content or contentBase64")));
        add(new Spec("sftp_mkdir", "Create remote directory",
                "Create a remote directory; set parents=true to create missing parents (like mkdir -p).",
                Hints.mutating(false, false),
                Param.required("serverId", "string", SERVER_ID),
                Param.required("path", "string", "Remote directory path to create"),
                Param.optional("parents", "boolean", "Create missing parent directories and succeed if the directory already exists (like mkdir -p)")));
        add(new Spec("sftp_delete", "Delete remote path",
                "Delete one remote file, or an empty directory when directory=true. Not recursive.",
                Hints.mutating(true, true),
                Param.required("serverId", "string", SERVER_ID),
                Param.required("path", "string", "Remote path to delete"),
                Param.optional("directory", "boolean", "Set true to remove an empty directory; non-empty directories are never removed")));
        add(new Spec("sftp_rename", "Rename remote path",
                "Rename or move a remote file or directory. Fails if the target exists unless overwrite=true.",
                Hints.mutating(true, false),
                Param.required("serverId", "string", SERVER_ID),
                Param.required("from", "string", "Existing remote path"),
                Param.required("to", "string", "New remote path"),
                Param.optional("overwrite", "boolean", "Atomically replace an existing target (requires the OpenSSH posix-rename extension); default false fails if the target exists")));
        add(new Spec("ssh_tunnel_open", "Open SSH tunnel",
                "Open a local port forward: a loopback port on the MCP host relays to remoteHost:remotePort as reached from the SSH server. Requires portForwardingEnabled. Close it with ssh_tunnel_close when done.",
                Hints.mutating(false, false),
                Param.required("serverId", "string", SERVER_ID),
                Param.optional("localHost", "string", "Loopback address to listen on: 127.0.0.1 (default), ::1, or localhost"),
                Param.optional("localPort", "integer", "Local port, or 0 (default) to pick a free port"),
                Param.required("remoteHost", "string", "Destination host as seen from the SSH server, for example 127.0.0.1 for a service on the server itself"),
                Param.required("remotePort", "integer", "Destination TCP port, 1-65535")));
        add(new Spec("ssh_tunnel_close", "Close SSH tunnel",
                "Close an open tunnel by tunnelId.",
                Hints.mutating(false, true),
                Param.required("tunnelId", "string", "Tunnel id returned by ssh_tunnel_open or ssh_tunnel_list")));
        add(new Spec("ssh_tunnel_list", "List SSH tunnels",
                "List open tunnels with their local and remote endpoints.",
                Hints.readOnly(false)));
    }

    private ToolSpecs() {
    }

    private static void add(Spec spec) {
        SPECS.put(spec.name, spec);
    }

    static Spec get(String name) {
        return SPECS.get(name);
    }

    static ArrayNode toolsList() {
        ArrayNode tools = JsonNodeFactory.instance.arrayNode();
        for (Spec spec : SPECS.values()) {
            tools.add(spec.toJson());
        }
        return tools;
    }

    static final class Spec {
        final String name;
        final String title;
        final String description;
        final Hints hints;
        final List<Param> params;
        final Set<String> paramNames = new LinkedHashSet<>();

        private Spec(String name, String title, String description, Hints hints, Param... params) {
            this.name = name;
            this.title = title;
            this.description = description;
            this.hints = hints;
            this.params = Collections.unmodifiableList(Arrays.asList(params));
            for (Param param : params) {
                paramNames.add(param.name);
            }
        }

        ObjectNode toJson() {
            JsonNodeFactory json = JsonNodeFactory.instance;
            ObjectNode tool = json.objectNode();
            tool.put("name", name);
            tool.put("title", title);
            tool.put("description", description);
            ObjectNode schema = tool.putObject("inputSchema");
            schema.put("type", "object");
            ObjectNode properties = schema.putObject("properties");
            ArrayNode required = schema.putArray("required");
            for (Param param : params) {
                ObjectNode property = properties.putObject(param.name);
                property.put("type", param.type);
                property.put("description", param.description);
                if (param.required) {
                    required.add(param.name);
                }
            }
            schema.put("additionalProperties", false);
            ObjectNode annotations = tool.putObject("annotations");
            annotations.put("title", title);
            annotations.put("readOnlyHint", hints.readOnly);
            if (!hints.readOnly) {
                annotations.put("destructiveHint", hints.destructive);
            }
            annotations.put("idempotentHint", hints.idempotent);
            annotations.put("openWorldHint", hints.openWorld);
            return tool;
        }
    }

    static final class Param {
        final String name;
        final String type;
        final String description;
        final boolean required;

        private Param(String name, String type, String description, boolean required) {
            this.name = name;
            this.type = type;
            this.description = description;
            this.required = required;
        }

        static Param required(String name, String type, String description) {
            return new Param(name, type, description, true);
        }

        static Param optional(String name, String type, String description) {
            return new Param(name, type, description, false);
        }
    }

    private static final class Hints {
        final boolean readOnly;
        final boolean destructive;
        final boolean idempotent;
        final boolean openWorld;

        private Hints(boolean readOnly, boolean destructive, boolean idempotent, boolean openWorld) {
            this.readOnly = readOnly;
            this.destructive = destructive;
            this.idempotent = idempotent;
            this.openWorld = openWorld;
        }

        static Hints readOnly(boolean openWorld) {
            return new Hints(true, false, true, openWorld);
        }

        static Hints mutating(boolean destructive, boolean idempotent) {
            return new Hints(false, destructive, idempotent, true);
        }
    }
}
