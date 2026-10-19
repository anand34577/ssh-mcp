package com.example.sshmcp;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ArrayNode;
import com.fasterxml.jackson.databind.node.JsonNodeFactory;
import com.fasterxml.jackson.databind.node.ObjectNode;
import org.apache.sshd.client.SshClient;
import org.apache.sshd.client.channel.ChannelExec;
import org.apache.sshd.client.channel.ClientChannelEvent;
import org.apache.sshd.client.config.hosts.KnownHostEntry;
import org.apache.sshd.client.keyverifier.AcceptAllServerKeyVerifier;
import org.apache.sshd.client.keyverifier.KnownHostsServerKeyVerifier;
import org.apache.sshd.client.session.ClientSession;
import org.apache.sshd.client.session.forward.ExplicitPortForwardingTracker;
import org.apache.sshd.common.config.keys.FilePasswordProvider;
import org.apache.sshd.common.util.net.SshdSocketAddress;
import org.apache.sshd.common.util.security.SecurityUtils;
import org.apache.sshd.core.CoreModuleProperties;
import org.apache.sshd.sftp.client.SftpClient;
import org.apache.sshd.sftp.client.SftpClientFactory;
import org.apache.sshd.sftp.client.extensions.openssh.OpenSSHPosixRenameExtension;
import org.apache.sshd.sftp.common.SftpConstants;
import org.apache.sshd.sftp.common.SftpException;

import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.OutputStream;
import java.net.InetAddress;
import java.net.UnknownHostException;
import java.nio.ByteBuffer;
import java.nio.charset.CharacterCodingException;
import java.nio.charset.CodingErrorAction;
import java.nio.charset.StandardCharsets;
import java.security.KeyPair;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.time.Duration;
import java.time.Instant;
import java.time.temporal.ChronoUnit;
import java.util.ArrayList;
import java.util.Base64;
import java.util.Collection;
import java.util.Comparator;
import java.util.EnumSet;
import java.util.Iterator;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Set;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.TimeoutException;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicLong;
import java.util.regex.Pattern;

/**
 * Implements the SSH, SFTP, and tunnel tools. Every operation opens its own SSH connection, is
 * bounded by a per-server concurrency slot and timeout, and is audited to stderr without secrets
 * or command text. Tool result shapes match the Go implementation.
 */
final class SshService implements AutoCloseable {
    private static final int MAX_COMMAND_LENGTH = 32768;
    private static final int DEFAULT_MAX_ENTRIES = 1000;
    private static final int MAX_MAX_ENTRIES = 10000;
    private static final int SFTP_CHUNK = 32768;
    /** Extra time the outer guard allows past a command timeout so the timed-out result is returned. */
    private static final long EXEC_GUARD_GRACE_MS = 5000L;
    private static final Duration TUNNEL_HEARTBEAT = Duration.ofSeconds(30);
    private static final int TUNNEL_HEARTBEAT_MAX_MISSED = 3;

    private final ObjectMapper mapper;
    private final Config.ServerCatalog catalog;
    private final Map<String, Tunnel> tunnels = new ConcurrentHashMap<>();
    private final AtomicLong nextTunnel = new AtomicLong();
    private final AtomicBoolean closed = new AtomicBoolean();

    SshService(ObjectMapper mapper, Config.ServerCatalog catalog) {
        this.mapper = mapper;
        this.catalog = catalog;
    }

    ToolResult call(String name, JsonNode arguments) {
        ToolSpecs.Spec spec = ToolSpecs.get(name);
        if (spec == null) {
            return ToolResult.error("unknown_tool", "Unknown tool: " + name);
        }
        try {
            JsonNode args = validateArguments(spec, arguments);
            switch (name) {
                case "ssh_list_servers": return ToolResult.ok(listServers());
                case "ssh_exec": return execute(args);
                case "sftp_list": return sftpList(args);
                case "sftp_stat": return sftpStat(args);
                case "sftp_read_file": return sftpRead(args);
                case "sftp_write_file": return sftpWrite(args);
                case "sftp_mkdir": return sftpMkdir(args);
                case "sftp_delete": return sftpDelete(args);
                case "sftp_rename": return sftpRename(args);
                case "ssh_tunnel_open": return tunnelOpen(args);
                case "ssh_tunnel_close": return tunnelClose(args);
                case "ssh_tunnel_list": return tunnelList();
                default: return ToolResult.error("unknown_tool", "Unknown tool: " + name);
            }
        }
        catch (InvalidToolArguments ex) {
            return ToolResult.error("invalid_arguments", ex.getMessage());
        }
        catch (OperationRejected ex) {
            return ToolResult.error("operation_rejected", ex.getMessage());
        }
        catch (InterruptedException ex) {
            Thread.currentThread().interrupt();
            return ToolResult.error("cancelled", "The operation was cancelled");
        }
        catch (Exception ex) {
            AuditLog.failure(name, ex);
            return ToolResult.error("ssh_operation_failed", describe(ex));
        }
    }

    private static JsonNode validateArguments(ToolSpecs.Spec spec, JsonNode arguments) throws InvalidToolArguments {
        if (arguments == null || arguments.isNull()) {
            arguments = JsonNodeFactory.instance.objectNode();
        }
        if (!arguments.isObject()) {
            throw new InvalidToolArguments("Tool arguments must be a JSON object");
        }
        Iterator<String> fields = arguments.fieldNames();
        while (fields.hasNext()) {
            String field = fields.next();
            if (!spec.paramNames.contains(field)) {
                throw new InvalidToolArguments("Unknown argument \"" + field + "\" for " + spec.name
                        + "; expected: " + String.join(", ", spec.paramNames));
            }
        }
        return arguments;
    }

    ObjectNode listServers() {
        ObjectNode result = mapper.createObjectNode();
        ArrayNode servers = result.putArray("servers");
        for (Config.ServerConfig server : sortedServers()) {
            ObjectNode item = servers.addObject();
            item.put("id", server.id());
            item.put("host", server.host());
            item.put("port", server.port());
            item.put("username", server.username());
            ArrayNode auth = item.putArray("configuredAuthMethods");
            if (server.privateKeyPath() != null) {
                auth.add("publicKey");
            }
            if (server.passwordEnv() != null) {
                auth.add("password");
            }
            item.put("strictHostKeyChecking", server.strictHostKeyChecking());
            item.put("effectiveHostKeyPolicy", server.effectiveHostKeyPolicy());
            item.put("allowSftp", server.allowSftp());
            item.put("allowPortForwarding", server.allowPortForwarding());
            item.put("sftpEnabled", server.allowSftp());
            item.put("portForwardingEnabled", server.allowPortForwarding());
            item.put("commandTimeoutMs", server.commandTimeoutMs());
            item.put("maxOutputBytes", server.maxOutputBytes());
            item.put("maxConcurrentOperations", server.maxConcurrentOperations());
            item.put("maxConcurrentTunnels", server.maxConcurrentTunnels());
            ArrayNode prefixes = item.putArray("allowedCommandPrefixes");
            server.allowedCommandPrefixes().forEach(prefixes::add);
            item.put("deniedCommandRuleCount", server.deniedCommandPatterns().size());
            item.put("protectSystem", server.protectSystem());
        }
        result.put("secretsExposedToModel", false);
        return result;
    }

    private ToolResult execute(JsonNode arguments) throws Exception {
        Config.ServerConfig server = server(arguments);
        String command = required(arguments, "command");
        if (command.length() > MAX_COMMAND_LENGTH) {
            throw new InvalidToolArguments("command must be at most " + MAX_COMMAND_LENGTH + " characters");
        }
        String commandHash = "commandHash=" + sha256(command);
        try {
            checkCommandPolicy(server, command);
        }
        catch (OperationRejected ex) {
            AuditLog.operation("ssh_exec", server, false, 0, "policy_rejected " + commandHash);
            throw ex;
        }
        long timeout = longArg(arguments, "timeoutMs", server.commandTimeoutMs(), 100, server.commandTimeoutMs());
        int maxOutput = intArg(arguments, "maxOutputBytes", server.maxOutputBytes(), 1024, server.maxOutputBytes());

        return withConnection(server, "ssh_exec", commandHash, timeout + EXEC_GUARD_GRACE_MS, connection -> {
            long deadline = System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(timeout);
            LimitedOutputStream stdout = new LimitedOutputStream(maxOutput);
            LimitedOutputStream stderr = new LimitedOutputStream(maxOutput);
            long started = System.nanoTime();
            boolean timedOut = false;
            try (ChannelExec channel = connection.session.createExecChannel(command)) {
                channel.setOut(stdout);
                channel.setErr(stderr);
                try {
                    channel.open().verify(timeout);
                    // CLOSED (not EXIT_STATUS) guarantees all output has been delivered.
                    Set<ClientChannelEvent> events = channel.waitFor(EnumSet.of(ClientChannelEvent.CLOSED),
                            Math.max(1L, TimeUnit.NANOSECONDS.toMillis(deadline - System.nanoTime())));
                    timedOut = events.contains(ClientChannelEvent.TIMEOUT);
                }
                finally {
                    channel.close(timedOut);
                }
                ObjectNode result = mapper.createObjectNode();
                result.put("serverId", server.id());
                result.put("stdout", stdout.text());
                result.put("stderr", stderr.text());
                Integer exitStatus = channel.getExitStatus();
                String exitSignal = channel.getExitSignal();
                if (exitStatus == null || (exitSignal != null && !exitSignal.isEmpty())) {
                    result.putNull("exitCode");
                }
                else {
                    result.put("exitCode", exitStatus);
                }
                result.put("exitSignal", exitSignal == null ? "" : exitSignal);
                result.put("timedOut", timedOut);
                result.put("stdoutTruncated", stdout.isTruncated());
                result.put("stderrTruncated", stderr.isTruncated());
                result.put("durationMs", elapsedMs(started));
                return ToolResult.ok(result);
            }
        });
    }

    private ToolResult sftpList(JsonNode arguments) throws Exception {
        Config.ServerConfig server = sftpServer(arguments);
        String path = required(arguments, "path");
        int maxEntries = intArg(arguments, "maxEntries", DEFAULT_MAX_ENTRIES, 1, MAX_MAX_ENTRIES);
        return withSftp(server, "sftp_list", sftp -> {
            List<SftpClient.DirEntry> entries = new ArrayList<>();
            for (SftpClient.DirEntry entry : sftp.readDir(path)) {
                if (!".".equals(entry.getFilename()) && !"..".equals(entry.getFilename())) {
                    entries.add(entry);
                }
            }
            entries.sort(Comparator.comparing(SftpClient.DirEntry::getFilename));
            ArrayNode items = mapper.createArrayNode();
            for (SftpClient.DirEntry entry : entries.subList(0, Math.min(entries.size(), maxEntries))) {
                ObjectNode item = items.addObject();
                item.put("name", entry.getFilename());
                addAttributes(item, entry.getAttributes());
            }
            ObjectNode result = mapper.createObjectNode();
            result.put("serverId", server.id());
            result.put("path", path);
            result.set("entries", items);
            result.put("totalEntries", entries.size());
            result.put("truncated", entries.size() > maxEntries);
            return ToolResult.ok(result);
        });
    }

    private ToolResult sftpStat(JsonNode arguments) throws Exception {
        Config.ServerConfig server = sftpServer(arguments);
        String path = required(arguments, "path");
        return withSftp(server, "sftp_stat", sftp -> {
            ObjectNode result = mapper.createObjectNode();
            result.put("name", "");
            addAttributes(result, sftp.stat(path));
            result.put("serverId", server.id());
            result.put("path", path);
            return ToolResult.ok(result);
        });
    }

    private ToolResult sftpRead(JsonNode arguments) throws Exception {
        Config.ServerConfig server = sftpServer(arguments);
        String path = required(arguments, "path");
        long offset = longArg(arguments, "offset", 0, 0, Long.MAX_VALUE);
        int maxBytes = intArg(arguments, "maxBytes", server.maxOutputBytes(), 1, server.maxOutputBytes());
        String encoding = readEncoding(optional(arguments, "encoding", ""));
        return withSftp(server, "sftp_read_file", sftp -> {
            SftpClient.Attributes attributes = sftp.stat(path);
            if (attributes.isDirectory()) {
                throw new OperationRejected("path is a directory; use sftp_list");
            }
            ByteArrayOutputStream data = new ByteArrayOutputStream(Math.min(maxBytes + 1, 65536));
            try (SftpClient.CloseableHandle handle = sftp.open(path, SftpClient.OpenMode.Read)) {
                byte[] buffer = new byte[SFTP_CHUNK];
                // Read one byte past maxBytes to learn whether more data follows.
                while (data.size() <= maxBytes) {
                    int wanted = Math.min(buffer.length, maxBytes + 1 - data.size());
                    int read = sftp.read(handle, offset + data.size(), buffer, 0, wanted);
                    if (read <= 0) {
                        break;
                    }
                    data.write(buffer, 0, read);
                }
            }
            byte[] bytes = data.toByteArray();
            boolean truncated = bytes.length > maxBytes;
            int length = Math.min(bytes.length, maxBytes);
            if (truncated && "utf8".equals(encoding)) {
                length = completeUtf8Length(bytes, length);
            }
            ObjectNode result = mapper.createObjectNode();
            result.put("serverId", server.id());
            result.put("path", path);
            result.put("size", attributes.getSize());
            result.put("offset", offset);
            result.put("bytes", length);
            result.put("nextOffset", offset + length);
            result.put("truncated", truncated);
            result.put("encoding", encoding);
            if ("base64".equals(encoding)) {
                result.put("contentBase64", Base64.getEncoder().encodeToString(java.util.Arrays.copyOf(bytes, length)));
            }
            else {
                result.put("content", new String(bytes, 0, length, StandardCharsets.UTF_8));
                result.put("validUtf8", isValidUtf8(bytes, length));
            }
            return ToolResult.ok(result);
        });
    }

    private ToolResult sftpWrite(JsonNode arguments) throws Exception {
        Config.ServerConfig server = sftpServer(arguments);
        String path = required(arguments, "path");
        String content = optional(arguments, "content", null);
        String contentBase64 = optional(arguments, "contentBase64", null);
        if ((content == null) == (contentBase64 == null)) {
            throw new InvalidToolArguments("provide exactly one of content or contentBase64");
        }
        byte[] bytes = content != null ? content.getBytes(StandardCharsets.UTF_8) : decodeBase64(contentBase64);
        if (bytes.length > server.maxOutputBytes()) {
            throw new InvalidToolArguments("file content is " + bytes.length + " bytes, which exceeds maxOutputBytes ("
                    + server.maxOutputBytes() + ") for this server");
        }
        checkPathGuard(server, "sftp_write_file", path, true);
        return withSftp(server, "sftp_write_file", sftp -> {
            // Closing the stream flushes outstanding writes and reports their errors.
            try (OutputStream output = sftp.write(path, SFTP_CHUNK, SftpClient.OpenMode.Create,
                    SftpClient.OpenMode.Write, SftpClient.OpenMode.Truncate)) {
                output.write(bytes);
            }
            ObjectNode result = mapper.createObjectNode();
            result.put("serverId", server.id());
            result.put("path", path);
            result.put("bytesWritten", bytes.length);
            return ToolResult.ok(result);
        });
    }

    private ToolResult sftpMkdir(JsonNode arguments) throws Exception {
        Config.ServerConfig server = sftpServer(arguments);
        String path = required(arguments, "path");
        boolean parents = bool(arguments, "parents", false);
        return withSftp(server, "sftp_mkdir", sftp -> {
            if (parents) {
                mkdirs(sftp, path);
            }
            else {
                sftp.mkdir(path);
            }
            return simpleResult(server.id(), "path", path, "created");
        });
    }

    private ToolResult sftpDelete(JsonNode arguments) throws Exception {
        Config.ServerConfig server = sftpServer(arguments);
        String path = required(arguments, "path");
        boolean directory = bool(arguments, "directory", false);
        checkPathGuard(server, "sftp_delete", path, false);
        return withSftp(server, "sftp_delete", sftp -> {
            if (directory) {
                sftp.rmdir(path);
            }
            else {
                sftp.remove(path);
            }
            return simpleResult(server.id(), "path", path, "deleted");
        });
    }

    private ToolResult sftpRename(JsonNode arguments) throws Exception {
        Config.ServerConfig server = sftpServer(arguments);
        String from = required(arguments, "from");
        String to = required(arguments, "to");
        boolean overwrite = bool(arguments, "overwrite", false);
        checkPathGuard(server, "sftp_rename", from, false);
        checkPathGuard(server, "sftp_rename", to, true);
        return withSftp(server, "sftp_rename", sftp -> {
            if (overwrite) {
                OpenSSHPosixRenameExtension posixRename = sftp.getExtension(OpenSSHPosixRenameExtension.class);
                if (posixRename == null || !posixRename.isSupported()) {
                    throw new OperationRejected("overwrite=true requires the posix-rename@openssh.com SFTP extension, which this server does not support");
                }
                posixRename.posixRename(from, to);
            }
            else {
                sftp.rename(from, to);
            }
            ObjectNode result = mapper.createObjectNode();
            result.put("serverId", server.id());
            result.put("from", from);
            result.put("to", to);
            result.put("renamed", true);
            return ToolResult.ok(result);
        });
    }

    private ToolResult tunnelOpen(JsonNode arguments) throws Exception {
        if (closed.get()) {
            throw new OperationRejected("SSH service is shutting down");
        }
        Config.ServerConfig server = server(arguments);
        if (!server.allowPortForwarding()) {
            throw new OperationRejected("port forwarding is disabled for server \"" + server.id() + "\" (allowPortForwarding is false)");
        }
        String localHost = stripBrackets(optional(arguments, "localHost", "127.0.0.1").trim());
        if (localHost.isEmpty()) {
            localHost = "127.0.0.1";
        }
        if (!isLoopbackHost(localHost)) {
            throw new OperationRejected("for safety, localHost must be a loopback address (127.0.0.1, ::1, or localhost)");
        }
        int localPort = intArg(arguments, "localPort", 0, 0, 65535);
        String remoteHost = required(arguments, "remoteHost");
        int remotePort = intArg(arguments, "remotePort", 0, 1, 65535);
        if (remotePort < 1) {
            throw new InvalidToolArguments("remotePort is required and must be between 1 and 65535");
        }

        if (!server.tunnelSlots().tryAcquire(server.connectTimeoutMs(), TimeUnit.MILLISECONDS)) {
            throw new OperationRejected("tunnel limit reached for server \"" + server.id() + "\": " + server.maxConcurrentTunnels()
                    + " tunnels already open; close one with ssh_tunnel_close or raise maxConcurrentTunnels");
        }
        long started = System.nanoTime();
        Connection connection = null;
        try {
            connection = openConnection(server, true);
            ExplicitPortForwardingTracker tracker = connection.session.createLocalPortForwardingTracker(
                    new SshdSocketAddress(localHost, localPort), new SshdSocketAddress(remoteHost, remotePort));
            Tunnel tunnel = new Tunnel("tunnel-" + nextTunnel.incrementAndGet(), server, connection, tracker, remoteHost, remotePort);
            synchronized (tunnels) {
                if (closed.get()) {
                    tracker.close();
                    throw new OperationRejected("SSH service is shutting down");
                }
                tunnels.put(tunnel.id, tunnel);
            }
            // Close the tunnel when the SSH session ends (including missed heartbeats). Run it off the
            // I/O thread that delivers the event, because closing waits for that same session.
            connection.session.addCloseFutureListener(future -> {
                Thread closer = new Thread(() -> tunnel.close("SSH connection lost"), "ssh-mcp-tunnel-close");
                closer.setDaemon(true);
                closer.start();
            });
            AuditLog.operation("ssh_tunnel_open", server, true, elapsedMs(started), "tunnel=" + tunnel.id
                    + " local=" + tracker.getBoundAddress() + " remote=" + remoteHost + ":" + remotePort);
            ObjectNode result = tunnel.info();
            result.put("warning", "The tunnel stays open until ssh_tunnel_close, the SSH connection drops, or the MCP server exits");
            return ToolResult.ok(result);
        }
        catch (Exception ex) {
            if (connection != null) {
                connection.close();
            }
            server.tunnelSlots().release();
            AuditLog.operation("ssh_tunnel_open", server, false, elapsedMs(started), "");
            throw ex;
        }
    }

    private ToolResult tunnelClose(JsonNode arguments) throws InvalidToolArguments {
        String id = required(arguments, "tunnelId");
        Tunnel tunnel = tunnels.get(id);
        if (tunnel == null) {
            throw new InvalidToolArguments("unknown tunnelId \"" + id + "\" (it may already be closed); call ssh_tunnel_list to see open tunnels");
        }
        tunnel.close("closed by request");
        ObjectNode result = mapper.createObjectNode();
        result.put("tunnelId", id);
        result.put("closed", true);
        return ToolResult.ok(result);
    }

    private ToolResult tunnelList() {
        ObjectNode result = mapper.createObjectNode();
        ArrayNode items = result.putArray("tunnels");
        List<Tunnel> active = new ArrayList<>(tunnels.values());
        active.sort(Comparator.comparing((Tunnel tunnel) -> tunnel.openedAt).thenComparing(tunnel -> tunnel.id));
        for (Tunnel tunnel : active) {
            if (!tunnel.tracker.isOpen() || !tunnel.connection.session.isOpen()) {
                tunnel.close("found closed");
                continue;
            }
            items.add(tunnel.info());
        }
        return ToolResult.ok(result);
    }

    /** Diagnoses every server's setup for --validate; see the Go implementation's Check. */
    List<CheckResult> check(boolean connect) {
        List<CheckResult> results = new ArrayList<>();
        for (Config.ServerConfig server : sortedServers()) {
            CheckResult result = new CheckResult(server.id(), server.address());
            try {
                resolveCredentials(server);
            }
            catch (Exception ex) {
                result.problems.add(describe(ex));
            }
            String policy = server.effectiveHostKeyPolicy();
            if ("strict".equals(policy)) {
                try {
                    boolean known = false;
                    List<KnownHostEntry> entries = KnownHostEntry.readKnownHostEntries(server.knownHostsPath());
                    for (KnownHostEntry entry : entries == null ? new ArrayList<KnownHostEntry>() : entries) {
                        known |= entry.isHostMatch(server.host(), server.port());
                    }
                    if (!known) {
                        result.problems.add("host " + server.address() + " is not in " + server.knownHostsPath()
                                + "; verify its fingerprint, then run: ssh-keyscan -p " + server.port() + " "
                                + server.host() + " >> " + server.knownHostsPath());
                    }
                }
                catch (Exception ex) {
                    result.problems.add("known_hosts file " + server.knownHostsPath() + " could not be loaded: " + describe(ex));
                }
            }
            else {
                result.warnings.add("host-key verification is " + policy + "; connections are not protected against man-in-the-middle attacks");
            }
            if (server.allowPortForwarding()) {
                result.warnings.add("port forwarding is enabled");
            }
            if (connect && result.problems.isEmpty()) {
                try (Connection ignored = openConnection(server, false)) {
                    result.connected = true;
                }
                catch (Exception ex) {
                    result.problems.add(describe(ex));
                }
            }
            results.add(result);
        }
        return results;
    }

    private Config.ServerConfig server(JsonNode arguments) throws InvalidToolArguments {
        String id = optional(arguments, "serverId", "").trim();
        if (id.isEmpty()) {
            throw new InvalidToolArguments("serverId is required; call ssh_list_servers to see configured ids");
        }
        Config.ServerConfig server = catalog.get(id);
        if (server == null) {
            throw new InvalidToolArguments("unknown serverId \"" + id + "\"; configured ids: " + String.join(", ", catalog.servers().keySet()));
        }
        return server;
    }

    private Config.ServerConfig sftpServer(JsonNode arguments) throws InvalidToolArguments, OperationRejected {
        Config.ServerConfig server = server(arguments);
        if (!server.allowSftp()) {
            throw new OperationRejected("SFTP is disabled for server \"" + server.id() + "\" (allowSftp is false)");
        }
        return server;
    }

    private List<Config.ServerConfig> sortedServers() {
        List<Config.ServerConfig> servers = new ArrayList<>(catalog.servers().values());
        servers.sort(Comparator.comparing(Config.ServerConfig::id));
        return servers;
    }

    /** Applies system protection to an SFTP change. */
    static void checkPathGuard(Config.ServerConfig server, String operation, String path, boolean write) throws OperationRejected {
        if (!server.protectSystem()) {
            return;
        }
        String reason = SystemGuard.pathViolation(path, write);
        if (reason != null) {
            AuditLog.operation(operation, server, false, 0, "policy_rejected");
            throw new OperationRejected(SystemGuard.message(server.id(), reason));
        }
    }

    private <T> T withSftp(Config.ServerConfig server, String operation, SftpOperation<T> body) throws Exception {
        return withConnection(server, operation, "", server.commandTimeoutMs(), connection -> {
            SftpClient sftp;
            try {
                sftp = SftpClientFactory.instance().createSftpClient(connection.session);
            }
            catch (IOException ex) {
                throw new OperationFailed("SFTP subsystem unavailable: " + describe(ex));
            }
            try (SftpClient client = sftp) {
                return body.run(client);
            }
            catch (SftpException ex) {
                throw new OperationFailed("SFTP " + operation.replace("sftp_", "") + " failed: " + describeSftp(ex));
            }
        });
    }

    /**
     * Acquires an operation slot, connects, and runs the operation under a guard of guardTimeoutMs.
     * Connection time does not count against the guard.
     */
    private <T> T withConnection(Config.ServerConfig server, String operation, String detail, long guardTimeoutMs,
            ConnectionOperation<T> body) throws Exception {
        if (closed.get()) {
            throw new OperationRejected("SSH service is shutting down");
        }
        long started = System.nanoTime();
        if (!server.operationSlots().tryAcquire(server.connectTimeoutMs() + server.commandTimeoutMs(), TimeUnit.MILLISECONDS)) {
            AuditLog.operation(operation, server, false, elapsedMs(started), "busy " + detail);
            throw new OperationRejected("server \"" + server.id() + "\" is busy: all " + server.maxConcurrentOperations()
                    + " operation slots are in use; retry later or raise maxConcurrentOperations");
        }
        boolean success = false;
        Connection connection = null;
        ExecutorService executor = null;
        try {
            connection = openConnection(server, false);
            Connection active = connection;
            executor = Executors.newSingleThreadExecutor(runnable -> {
                Thread thread = new Thread(runnable, "ssh-mcp-" + operation);
                thread.setDaemon(true);
                return thread;
            });
            Future<T> future = executor.submit(() -> body.run(active));
            try {
                T result = future.get(guardTimeoutMs, TimeUnit.MILLISECONDS);
                success = true;
                return result;
            }
            catch (TimeoutException ex) {
                future.cancel(true);
                throw new OperationFailed(operation + " timed out after " + guardTimeoutMs + "ms");
            }
            catch (InterruptedException ex) {
                future.cancel(true);
                throw ex;
            }
            catch (ExecutionException ex) {
                Throwable cause = ex.getCause();
                if (cause instanceof Exception) {
                    throw (Exception) cause;
                }
                throw new IOException("SSH operation failed", cause);
            }
        }
        finally {
            if (executor != null) {
                executor.shutdownNow();
            }
            if (connection != null) {
                connection.close();
            }
            server.operationSlots().release();
            AuditLog.operation(operation, server, success, elapsedMs(started),
                    (connection == null ? "connection_failed " : "") + detail);
        }
    }

    private Connection openConnection(Config.ServerConfig server, boolean keepAlive) throws Exception {
        Credentials credentials = resolveCredentials(server);
        HostKeyOutcome hostKey = new HostKeyOutcome();
        SshClient client = SshClient.setUpDefaultClient();
        if (server.strictHostKeyChecking() && !Config.autoAcceptHostKeys()) {
            KnownHostsServerKeyVerifier verifier = new KnownHostsServerKeyVerifier((session, remote, key) -> {
                hostKey.unknown = true;
                return false;
            }, server.knownHostsPath());
            verifier.setModifiedServerKeyAcceptor((session, remote, entry, expected, actual) -> {
                hostKey.mismatch = true;
                return false;
            });
            client.setServerKeyVerifier(verifier);
        }
        else {
            client.setServerKeyVerifier(AcceptAllServerKeyVerifier.INSTANCE);
        }
        if (keepAlive) {
            CoreModuleProperties.HEARTBEAT_INTERVAL.set(client, TUNNEL_HEARTBEAT);
            CoreModuleProperties.HEARTBEAT_NO_REPLY_MAX.set(client, TUNNEL_HEARTBEAT_MAX_MISSED);
        }
        client.start();
        try {
            ClientSession session = client.connect(server.username(), server.host(), server.port())
                    .verify(server.connectTimeoutMs()).getSession();
            for (KeyPair keyPair : credentials.keyPairs) {
                session.addPublicKeyIdentity(keyPair);
            }
            if (credentials.password != null) {
                // Also answers password-style keyboard-interactive prompts.
                session.addPasswordIdentity(credentials.password);
            }
            session.auth().verify(server.connectTimeoutMs());
            return new Connection(client, session);
        }
        catch (Exception ex) {
            client.stop();
            if (hostKey.mismatch) {
                throw new OperationFailed("HOST KEY MISMATCH for " + server.address() + ": the server presented a key that differs from "
                        + server.knownHostsPath() + ". This can indicate a man-in-the-middle attack; verify the new fingerprint out of band before updating known_hosts");
            }
            if (hostKey.unknown) {
                throw new OperationFailed("host key for " + server.address() + " is not in " + server.knownHostsPath()
                        + "; verify the server fingerprint out of band, then add it (for example: ssh-keyscan -p "
                        + server.port() + " " + server.host() + " >> " + server.knownHostsPath() + ")");
            }
            throw new OperationFailed("cannot connect to " + server.address() + " as " + server.username() + ": " + describe(ex));
        }
    }

    private static Credentials resolveCredentials(Config.ServerConfig server) throws OperationRejected {
        Credentials credentials = new Credentials();
        if (server.privateKeyPath() != null) {
            String passphrase = null;
            if (server.privateKeyPassphraseEnv() != null) {
                passphrase = env(server.privateKeyPassphraseEnv(), "privateKeyPassphraseEnv", server);
            }
            FilePasswordProvider provider = passphrase == null ? FilePasswordProvider.EMPTY : FilePasswordProvider.of(passphrase);
            try {
                Collection<KeyPair> keyPairs = SecurityUtils.getKeyPairResourceParser().loadKeyPairs(null, server.privateKeyPath(), provider);
                if (keyPairs == null || keyPairs.isEmpty()) {
                    throw new IOException("no private key found in the file");
                }
                credentials.keyPairs.addAll(keyPairs);
            }
            catch (Exception ex) {
                String hint = passphrase == null ? " (if the key is encrypted, set privateKeyPassphraseEnv)" : "";
                throw new OperationRejected("private key " + server.privateKeyPath() + " for server \"" + server.id()
                        + "\" could not be loaded: " + describe(ex) + hint);
            }
        }
        if (server.passwordEnv() != null) {
            credentials.password = env(server.passwordEnv(), "passwordEnv", server);
        }
        return credentials;
    }

    private static String env(String name, String field, Config.ServerConfig server) throws OperationRejected {
        String value = System.getenv(name);
        if (value == null || value.isEmpty()) {
            throw new OperationRejected("environment variable " + name + " (" + field + " for server \"" + server.id()
                    + "\") is not set or empty in the MCP server process");
        }
        return value;
    }

    /**
     * Enforces system protection first, then deniedCommandRegexes, then the allow-list. With an
     * allow-list configured, shell metacharacters are rejected so a prefix cannot be chained.
     */
    static void checkCommandPolicy(Config.ServerConfig server, String command) throws OperationRejected {
        if (server.protectSystem()) {
            String reason = SystemGuard.commandViolation(command);
            if (reason != null) {
                throw new OperationRejected(SystemGuard.message(server.id(), reason));
            }
        }
        for (Pattern pattern : server.deniedCommandPatterns()) {
            if (pattern.matcher(command).find()) {
                throw new OperationRejected("command rejected by the server command policy (matched a deniedCommandRegexes rule)");
            }
        }
        if (server.allowedCommandPrefixes().isEmpty()) {
            return;
        }
        if (hasShellOperators(command)) {
            throw new OperationRejected("command contains shell metacharacters (; | & < > $ ` ( ) { } ' \" \\ or a newline), which are not allowed when allowedCommandPrefixes is configured");
        }
        String trimmed = command.trim();
        for (String prefix : server.allowedCommandPrefixes()) {
            if (trimmed.equals(prefix) || (trimmed.startsWith(prefix)
                    && Character.isWhitespace(trimmed.charAt(prefix.length())))) {
                return;
            }
        }
        throw new OperationRejected("command is not allowed by the server command policy; it must start with one of: "
                + String.join(", ", server.allowedCommandPrefixes()));
    }

    private static boolean hasShellOperators(String command) {
        return Config.ServerConfig.containsAny(command, Config.SHELL_METACHARACTERS + "\u0000\n\r");
    }

    private static void mkdirs(SftpClient sftp, String path) throws IOException, OperationRejected {
        StringBuilder current = new StringBuilder(path.startsWith("/") ? "/" : "");
        for (String part : path.split("/")) {
            if (part.isEmpty()) {
                continue;
            }
            if (current.length() > 0 && current.charAt(current.length() - 1) != '/') {
                current.append('/');
            }
            current.append(part);
            String directory = current.toString();
            try {
                if (!sftp.stat(directory).isDirectory()) {
                    throw new OperationRejected(directory + " exists and is not a directory");
                }
            }
            catch (SftpException ex) {
                if (ex.getStatus() != SftpConstants.SSH_FX_NO_SUCH_FILE) {
                    throw ex;
                }
                sftp.mkdir(directory);
            }
        }
    }

    private static boolean isLoopbackHost(String host) {
        if ("localhost".equalsIgnoreCase(host)) {
            return true;
        }
        // Only literal addresses: never resolve a name that could point elsewhere.
        if (!host.matches("[0-9a-fA-F:.]+")) {
            return false;
        }
        try {
            return InetAddress.getByName(host).isLoopbackAddress();
        }
        catch (UnknownHostException ex) {
            return false;
        }
    }

    private static String stripBrackets(String host) {
        return host.startsWith("[") && host.endsWith("]") ? host.substring(1, host.length() - 1) : host;
    }

    private static String readEncoding(String value) throws InvalidToolArguments {
        String encoding = value.trim().toLowerCase(Locale.ROOT);
        if (encoding.isEmpty() || "utf8".equals(encoding) || "utf-8".equals(encoding)) {
            return "utf8";
        }
        if ("base64".equals(encoding)) {
            return "base64";
        }
        throw new InvalidToolArguments("encoding must be 'utf8' or 'base64'");
    }

    /** Standard base64 with or without padding; whitespace such as line breaks is ignored. */
    static byte[] decodeBase64(String value) throws InvalidToolArguments {
        try {
            return Base64.getDecoder().decode(value.replaceAll("[ \\t\\r\\n]", ""));
        }
        catch (IllegalArgumentException ex) {
            throw new InvalidToolArguments("contentBase64 is not valid base64");
        }
    }

    /** Length without a UTF-8 sequence cut off at the end, so nextOffset resumes on a boundary. */
    static int completeUtf8Length(byte[] data, int length) {
        for (int back = 1; back <= 3 && back <= length; back++) {
            int b = data[length - back] & 0xff;
            if ((b & 0xC0) == 0x80) {
                continue;
            }
            int needed = b < 0x80 ? 1 : (b & 0xE0) == 0xC0 ? 2 : (b & 0xF0) == 0xE0 ? 3 : (b & 0xF8) == 0xF0 ? 4 : 1;
            return back < needed ? length - back : length;
        }
        return length;
    }

    private static boolean isValidUtf8(byte[] data, int length) {
        try {
            StandardCharsets.UTF_8.newDecoder().onMalformedInput(CodingErrorAction.REPORT)
                    .onUnmappableCharacter(CodingErrorAction.REPORT).decode(ByteBuffer.wrap(data, 0, length));
            return true;
        }
        catch (CharacterCodingException ex) {
            return false;
        }
    }

    private static long elapsedMs(long startedNanos) {
        return TimeUnit.NANOSECONDS.toMillis(System.nanoTime() - startedNanos);
    }

    private static void addAttributes(ObjectNode node, SftpClient.Attributes attributes) {
        node.put("size", attributes.getSize());
        node.put("type", attributes.isDirectory() ? "directory"
                : attributes.isRegularFile() ? "file" : attributes.isSymbolicLink() ? "symlink" : "other");
        node.put("permissions", Integer.toOctalString(attributes.getPermissions() & 0777));
        if (attributes.getModifyTime() != null) {
            node.put("modified", attributes.getModifyTime().toInstant().truncatedTo(ChronoUnit.SECONDS).toString());
        }
    }

    private ToolResult simpleResult(String serverId, String key, String value, String flag) {
        ObjectNode result = mapper.createObjectNode();
        result.put("serverId", serverId);
        result.put(key, value);
        result.put(flag, true);
        return ToolResult.ok(result);
    }

    private static String required(JsonNode arguments, String name) throws InvalidToolArguments {
        JsonNode value = arguments.get(name);
        if (value == null || !value.isTextual() || value.asText().trim().isEmpty()) {
            throw new InvalidToolArguments(name + " is required and must be a non-empty string");
        }
        String text = value.asText().trim();
        if (text.indexOf('\u0000') >= 0) {
            throw new InvalidToolArguments(name + " must not contain NUL characters");
        }
        return text;
    }

    private static String optional(JsonNode arguments, String name, String defaultValue) throws InvalidToolArguments {
        JsonNode value = arguments.get(name);
        if (value == null || value.isNull()) {
            return defaultValue;
        }
        if (!value.isTextual()) {
            throw new InvalidToolArguments(name + " must be a string");
        }
        return value.asText();
    }

    private static boolean bool(JsonNode arguments, String name, boolean defaultValue) throws InvalidToolArguments {
        JsonNode value = arguments.get(name);
        if (value == null || value.isNull()) {
            return defaultValue;
        }
        if (!value.isBoolean()) {
            throw new InvalidToolArguments(name + " must be true or false");
        }
        return value.asBoolean();
    }

    private static int intArg(JsonNode arguments, String name, int defaultValue, int min, int max) throws InvalidToolArguments {
        return (int) longArg(arguments, name, defaultValue, min, max);
    }

    private static long longArg(JsonNode arguments, String name, long defaultValue, long min, long max) throws InvalidToolArguments {
        JsonNode value = arguments.get(name);
        if (value == null || value.isNull()) {
            return defaultValue;
        }
        if (!value.isIntegralNumber() || !value.canConvertToLong()) {
            throw new InvalidToolArguments(name + " must be an integer");
        }
        long result = value.asLong();
        // 0 means "use the default" for optional limits, matching the Go implementation.
        if (result == 0 && min > 0) {
            return defaultValue;
        }
        if (result < min || result > max) {
            throw new InvalidToolArguments(name + " must be between " + min + " and " + max);
        }
        return result;
    }

    /** A short, secret-free explanation of a failure, preferring the root cause's message. */
    static String describe(Throwable error) {
        Throwable root = error;
        while (root.getCause() != null && root.getCause() != root) {
            root = root.getCause();
        }
        String message = error.getMessage();
        if (root != error && root.getMessage() != null && (message == null || !message.contains(root.getMessage()))) {
            message = (message == null ? "" : message + ": ") + root.getMessage();
        }
        if (message == null || message.trim().isEmpty()) {
            message = error.getClass().getSimpleName();
        }
        if (message.contains("No more authentication methods available")) {
            message = "authentication failed (the server rejected the configured credentials)";
        }
        return message.length() > 500 ? message.substring(0, 500) + "..." : message;
    }

    private static String describeSftp(SftpException error) {
        switch (error.getStatus()) {
            case SftpConstants.SSH_FX_NO_SUCH_FILE: return "no such file or directory";
            case SftpConstants.SSH_FX_PERMISSION_DENIED: return "permission denied";
            case SftpConstants.SSH_FX_FILE_ALREADY_EXISTS: return "target already exists";
            case SftpConstants.SSH_FX_DIR_NOT_EMPTY: return "directory not empty";
            case SftpConstants.SSH_FX_OP_UNSUPPORTED: return "operation not supported by the remote SFTP server";
            case SftpConstants.SSH_FX_FAILURE:
                return "the remote SFTP server reported a failure (for example: target exists, directory not empty, or parent missing)";
            default: return describe(error);
        }
    }

    private static String sha256(String value) {
        try {
            byte[] digest = MessageDigest.getInstance("SHA-256").digest(value.getBytes(StandardCharsets.UTF_8));
            StringBuilder result = new StringBuilder(16);
            for (int i = 0; i < 8; i++) {
                result.append(String.format("%02x", digest[i]));
            }
            return result.toString();
        }
        catch (NoSuchAlgorithmException ex) {
            return "unavailable";
        }
    }

    @Override
    public void close() {
        if (!closed.compareAndSet(false, true)) {
            return;
        }
        List<Tunnel> open;
        synchronized (tunnels) {
            open = new ArrayList<>(tunnels.values());
        }
        for (Tunnel tunnel : open) {
            tunnel.close("service shutdown");
        }
    }

    @FunctionalInterface
    private interface ConnectionOperation<T> {
        T run(Connection connection) throws Exception;
    }

    @FunctionalInterface
    private interface SftpOperation<T> {
        T run(SftpClient sftp) throws Exception;
    }

    static final class ToolResult {
        private final ObjectNode payload;
        private final boolean error;

        private ToolResult(ObjectNode payload, boolean error) {
            this.payload = payload;
            this.error = error;
        }

        static ToolResult ok(ObjectNode payload) {
            return new ToolResult(payload, false);
        }

        static ToolResult error(String code, String message) {
            ObjectNode payload = JsonNodeFactory.instance.objectNode();
            payload.put("errorCode", code);
            payload.put("message", message);
            return new ToolResult(payload, true);
        }

        ObjectNode payload() { return payload; }
        boolean isError() { return error; }
    }

    static final class CheckResult {
        final String serverId;
        final String address;
        final List<String> problems = new ArrayList<>();
        final List<String> warnings = new ArrayList<>();
        boolean connected;

        CheckResult(String serverId, String address) {
            this.serverId = serverId;
            this.address = address;
        }

        boolean ok() { return problems.isEmpty(); }
    }

    private static final class Credentials {
        final List<KeyPair> keyPairs = new ArrayList<>();
        String password;
    }

    private static final class HostKeyOutcome {
        volatile boolean unknown;
        volatile boolean mismatch;
    }

    private static final class Connection implements AutoCloseable {
        private final SshClient client;
        private final ClientSession session;
        private final AtomicBoolean closed = new AtomicBoolean();

        private Connection(SshClient client, ClientSession session) {
            this.client = client;
            this.session = session;
        }

        @Override
        public void close() {
            if (!closed.compareAndSet(false, true)) {
                return;
            }
            try {
                session.close(false).await(5000L);
            }
            catch (Exception ignored) {
                // Cleanup must not hide the operation result.
            }
            try {
                client.stop();
            }
            catch (Exception ignored) {
                // Cleanup must not hide the operation result.
            }
        }
    }

    /** An SSH local port forward; registered in {@link #tunnels} until closed. */
    private final class Tunnel {
        private final String id;
        private final Config.ServerConfig server;
        private final Connection connection;
        private final ExplicitPortForwardingTracker tracker;
        private final String remoteHost;
        private final int remotePort;
        private final Instant openedAt = Instant.now().truncatedTo(ChronoUnit.SECONDS);
        private final AtomicBoolean closed = new AtomicBoolean();

        private Tunnel(String id, Config.ServerConfig server, Connection connection, ExplicitPortForwardingTracker tracker,
                String remoteHost, int remotePort) {
            this.id = id;
            this.server = server;
            this.connection = connection;
            this.tracker = tracker;
            this.remoteHost = remoteHost;
            this.remotePort = remotePort;
        }

        ObjectNode info() {
            SshdSocketAddress bound = tracker.getBoundAddress();
            ObjectNode item = mapper.createObjectNode();
            item.put("tunnelId", id);
            item.put("serverId", server.id());
            item.put("localHost", bound.getHostName());
            item.put("localPort", bound.getPort());
            item.put("remoteHost", remoteHost);
            item.put("remotePort", remotePort);
            item.put("openedAt", openedAt.toString());
            item.put("open", true);
            return item;
        }

        void close(String reason) {
            if (!closed.compareAndSet(false, true)) {
                return;
            }
            tunnels.remove(id, this);
            try {
                tracker.close();
            }
            catch (Exception ignored) {
                // Cleanup is best effort.
            }
            connection.close();
            server.tunnelSlots().release();
            AuditLog.event("operation=ssh_tunnel_close server=" + server.id() + " tunnel=" + id + " reason=\"" + reason + "\"");
        }
    }

    private static final class LimitedOutputStream extends OutputStream {
        private final int maxBytes;
        private final ByteArrayOutputStream delegate = new ByteArrayOutputStream();
        private boolean truncated;

        private LimitedOutputStream(int maxBytes) {
            this.maxBytes = maxBytes;
        }

        @Override
        public synchronized void write(int value) {
            write(new byte[] { (byte) value }, 0, 1);
        }

        @Override
        public synchronized void write(byte[] bytes, int offset, int length) {
            int remaining = maxBytes - delegate.size();
            if (remaining > 0) {
                delegate.write(bytes, offset, Math.min(length, remaining));
            }
            if (length > remaining) {
                truncated = true;
            }
        }

        synchronized String text() {
            return new String(delegate.toByteArray(), StandardCharsets.UTF_8);
        }

        synchronized boolean isTruncated() {
            return truncated;
        }
    }

    /** The arguments are invalid; the model should change them. */
    static class InvalidToolArguments extends Exception {
        InvalidToolArguments(String message) { super(message); }
    }

    /** The request is valid but refused by configuration, policy, or capacity. */
    static class OperationRejected extends Exception {
        OperationRejected(String message) { super(message); }
    }

    /** The operation was attempted and failed; the message is safe to show the model. */
    static class OperationFailed extends Exception {
        OperationFailed(String message) { super(message); }
    }
}
