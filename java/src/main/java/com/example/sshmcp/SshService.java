package com.example.sshmcp;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ArrayNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import org.apache.sshd.client.SshClient;
import org.apache.sshd.client.channel.ChannelExec;
import org.apache.sshd.client.channel.ClientChannelEvent;
import org.apache.sshd.client.keyverifier.AcceptAllServerKeyVerifier;
import org.apache.sshd.client.keyverifier.DefaultKnownHostsServerKeyVerifier;
import org.apache.sshd.client.keyverifier.RejectAllServerKeyVerifier;
import org.apache.sshd.client.session.ClientSession;
import org.apache.sshd.client.session.forward.ExplicitPortForwardingTracker;
import org.apache.sshd.common.config.keys.FilePasswordProvider;
import org.apache.sshd.common.util.net.SshdSocketAddress;
import org.apache.sshd.common.util.security.SecurityUtils;
import org.apache.sshd.sftp.client.SftpClient;
import org.apache.sshd.sftp.client.SftpClientFactory;

import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.InetAddress;
import java.net.UnknownHostException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.security.KeyPair;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.util.ArrayList;
import java.util.Base64;
import java.util.Collection;
import java.util.EnumSet;
import java.util.HashSet;
import java.util.Iterator;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.UUID;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import java.util.concurrent.TimeoutException;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.Locale;

final class SshService implements AutoCloseable {
    private final ObjectMapper mapper;
    private final Config.ServerCatalog catalog;
    private final Map<String, Tunnel> tunnels = new ConcurrentHashMap<String, Tunnel>();
    private final AtomicBoolean closed = new AtomicBoolean();

    SshService(ObjectMapper mapper, Config.ServerCatalog catalog) {
        this.mapper = mapper;
        this.catalog = catalog;
    }

    ObjectNode listServers() {
        ObjectNode result = mapper.createObjectNode();
        ArrayNode servers = result.putArray("servers");
        List<Config.ServerConfig> configuredServers = new ArrayList<Config.ServerConfig>(catalog.servers().values());
        configuredServers.sort((left, right) -> left.id().compareTo(right.id()));
        for (Config.ServerConfig server : configuredServers) {
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
            item.put("effectiveHostKeyPolicy", Config.autoAcceptHostKeys() ? "auto-accept"
                    : server.strictHostKeyChecking() ? "strict" : "disabled");
            item.put("allowSftp", server.allowSftp());
            item.put("allowPortForwarding", server.allowPortForwarding());
            item.put("sftpEnabled", server.allowSftp());
            item.put("portForwardingEnabled", server.allowPortForwarding());
            item.put("maxConcurrentOperations", server.maxConcurrentOperations());
            item.put("maxConcurrentTunnels", server.maxConcurrentTunnels());
        }
        result.put("secretsExposedToModel", false);
        return result;
    }

    ToolResult call(String name, JsonNode arguments) {
        try {
            validateArguments(name, arguments);
            if ("ssh_list_servers".equals(name)) {
                return ToolResult.ok(listServers());
            }
            if ("ssh_exec".equals(name)) {
                return execute(arguments);
            }
            if ("sftp_list".equals(name)) {
                return sftpList(arguments);
            }
            if ("sftp_stat".equals(name)) {
                return sftpStat(arguments);
            }
            if ("sftp_read_file".equals(name)) {
                return sftpRead(arguments);
            }
            if ("sftp_write_file".equals(name)) {
                return sftpWrite(arguments);
            }
            if ("sftp_mkdir".equals(name)) {
                return sftpMkdir(arguments);
            }
            if ("sftp_delete".equals(name)) {
                return sftpDelete(arguments);
            }
            if ("ssh_tunnel_open".equals(name)) {
                return tunnelOpen(arguments);
            }
            if ("ssh_tunnel_close".equals(name)) {
                return tunnelClose(arguments);
            }
            if ("ssh_tunnel_list".equals(name)) {
                return tunnelList();
            }
            return ToolResult.error("unknown_tool", "Unknown tool");
        }
        catch (InvalidToolArguments ex) {
            return ToolResult.error("invalid_arguments", ex.getMessage());
        }
        catch (OperationRejected ex) {
            return ToolResult.error("operation_rejected", ex.getMessage());
        }
        catch (Exception ex) {
            AuditLog.failure(name, ex);
            return ToolResult.error("ssh_operation_failed", "The SSH operation failed (" + ex.getClass().getSimpleName() + ")");
        }
    }

    private static void validateArguments(String name, JsonNode arguments) throws InvalidToolArguments {
        String[] allowed;
        if ("ssh_list_servers".equals(name) || "ssh_tunnel_list".equals(name)) {
            allowed = new String[0];
        }
        else if ("ssh_exec".equals(name)) {
            allowed = new String[] { "serverId", "command", "timeoutMs", "maxOutputBytes" };
        }
        else if ("sftp_list".equals(name)) {
            allowed = new String[] { "serverId", "path", "maxEntries" };
        }
        else if ("sftp_stat".equals(name) || "sftp_mkdir".equals(name)) {
            allowed = new String[] { "serverId", "path" };
        }
        else if ("sftp_read_file".equals(name)) {
            allowed = new String[] { "serverId", "path", "maxBytes", "encoding" };
        }
        else if ("sftp_write_file".equals(name)) {
            allowed = new String[] { "serverId", "path", "content", "contentBase64" };
        }
        else if ("sftp_delete".equals(name)) {
            allowed = new String[] { "serverId", "path", "directory" };
        }
        else if ("ssh_tunnel_open".equals(name)) {
            allowed = new String[] { "serverId", "localHost", "localPort", "remoteHost", "remotePort" };
        }
        else if ("ssh_tunnel_close".equals(name)) {
            allowed = new String[] { "tunnelId" };
        }
        else {
            return;
        }

        if (arguments == null || arguments.isNull()) {
            if (allowed.length == 0) {
                return;
            }
            throw new InvalidToolArguments("Tool arguments must be a JSON object");
        }
        if (!arguments.isObject()) {
            throw new InvalidToolArguments("Tool arguments must be a JSON object");
        }
        Set<String> allowedFields = new HashSet<String>(java.util.Arrays.asList(allowed));
        Iterator<String> fields = arguments.fieldNames();
        while (fields.hasNext()) {
            String field = fields.next();
            if (!allowedFields.contains(field)) {
                throw new InvalidToolArguments("Unknown tool argument: " + field);
            }
        }
    }

    private ToolResult execute(JsonNode arguments) throws Exception {
        final Config.ServerConfig server = server(arguments);
        final String command = required(arguments, "command");
        if (command.length() > 32768) {
            throw new InvalidToolArguments("command must be at most 32768 characters");
        }
        checkCommandPolicy(server, command);
        final long timeout = timeout(arguments, server.commandTimeoutMs());
        final int requestedOutput = integer(arguments, "maxOutputBytes", server.maxOutputBytes(), 1024, server.maxOutputBytes());
        final long started = System.nanoTime();
        return withConnection(server, timeout, new ConnectionOperation<ToolResult>() {
            @Override
            public ToolResult run(Connection connection) throws Exception {
                LimitedOutputStream stdout = new LimitedOutputStream(requestedOutput);
                LimitedOutputStream stderr = new LimitedOutputStream(requestedOutput);
                ChannelExec channel = connection.session.createExecChannel(command);
                channel.setOut(stdout);
                channel.setErr(stderr);
                boolean timedOut = false;
                try {
                    channel.open().verify(timeout);
                    Set<ClientChannelEvent> events = channel.waitFor(
                            EnumSet.of(ClientChannelEvent.CLOSED, ClientChannelEvent.EXIT_STATUS,
                                    ClientChannelEvent.EXIT_SIGNAL), timeout);
                    timedOut = events.contains(ClientChannelEvent.TIMEOUT);
                    if (timedOut) {
                        channel.close(true);
                    }
                }
                finally {
                    if (!channel.isClosed()) {
                        channel.close(timedOut);
                    }
                }

                ObjectNode result = mapper.createObjectNode();
                result.put("serverId", server.id());
                result.put("stdout", new String(stdout.toByteArray(), StandardCharsets.UTF_8));
                result.put("stderr", new String(stderr.toByteArray(), StandardCharsets.UTF_8));
                if (channel.getExitStatus() == null) {
                    result.putNull("exitCode");
                }
                else {
                    result.put("exitCode", channel.getExitStatus());
                }
                result.put("exitSignal", channel.getExitSignal() == null ? "" : channel.getExitSignal());
                result.put("timedOut", timedOut);
                result.put("stdoutTruncated", stdout.isTruncated());
                result.put("stderrTruncated", stderr.isTruncated());
                result.put("durationMs", elapsedMs(started));
                AuditLog.success("ssh_exec", server, elapsedMs(started), "commandHash=" + sha256(command));
                return ToolResult.ok(result);
            }
        });
    }

    private ToolResult sftpList(JsonNode arguments) throws Exception {
        final Config.ServerConfig server = sftpServer(arguments);
        final String path = required(arguments, "path");
        final int maxEntries = integer(arguments, "maxEntries", 1000, 1, 10000);
        return withConnection(server, new ConnectionOperation<ToolResult>() {
            @Override
            public ToolResult run(Connection connection) throws Exception {
                SftpClient sftp = openSftp(connection);
                try {
                    ArrayNode entries = mapper.createArrayNode();
                    int count = 0;
                    for (SftpClient.DirEntry entry : sftp.readDir(path)) {
                        if (count++ >= maxEntries) {
                            break;
                        }
                        ObjectNode item = entries.addObject();
                        item.put("name", entry.getFilename());
                        item.put("longName", entry.getLongFilename());
                        addAttributes(item, entry.getAttributes());
                    }
                    ObjectNode result = mapper.createObjectNode();
                    result.put("serverId", server.id());
                    result.put("path", path);
                    result.set("entries", entries);
                    result.put("truncated", count > maxEntries);
                    return ToolResult.ok(result);
                }
                finally {
                    sftp.close();
                }
            }
        });
    }

    private ToolResult sftpStat(JsonNode arguments) throws Exception {
        final Config.ServerConfig server = sftpServer(arguments);
        final String path = required(arguments, "path");
        return withConnection(server, new ConnectionOperation<ToolResult>() {
            @Override
            public ToolResult run(Connection connection) throws Exception {
                SftpClient sftp = openSftp(connection);
                try {
                    ObjectNode result = mapper.createObjectNode();
                    result.put("serverId", server.id());
                    result.put("path", path);
                    addAttributes(result, sftp.stat(path));
                    return ToolResult.ok(result);
                }
                finally {
                    sftp.close();
                }
            }
        });
    }

    private ToolResult sftpRead(JsonNode arguments) throws Exception {
        final Config.ServerConfig server = sftpServer(arguments);
        final String path = required(arguments, "path");
        final int maxBytes = integer(arguments, "maxBytes", server.maxOutputBytes(), 1, server.maxOutputBytes());
        final String encoding = optional(arguments, "encoding", "utf8").trim().toLowerCase(Locale.ROOT);
        if (!"utf8".equals(encoding) && !"base64".equals(encoding)) {
            throw new InvalidToolArguments("encoding must be 'utf8' or 'base64'");
        }
        return withConnection(server, new ConnectionOperation<ToolResult>() {
            @Override
            public ToolResult run(Connection connection) throws Exception {
                SftpClient sftp = openSftp(connection);
                try {
                    ByteArrayOutputStream bytes = new ByteArrayOutputStream(Math.min(maxBytes, 65536));
                    boolean truncated = false;
                    InputStream input = sftp.read(path, 32768);
                    try {
                        byte[] buffer = new byte[8192];
                        int read;
                        while ((read = input.read(buffer)) >= 0) {
                            if (read == 0) {
                                continue;
                            }
                            int remaining = maxBytes - bytes.size();
                            if (read > remaining) {
                                bytes.write(buffer, 0, Math.max(0, remaining));
                                truncated = true;
                                break;
                            }
                            bytes.write(buffer, 0, read);
                        }
                    }
                    finally {
                        input.close();
                    }
                    byte[] content = bytes.toByteArray();
                    ObjectNode result = mapper.createObjectNode();
                    result.put("serverId", server.id());
                    result.put("path", path);
                    result.put("bytes", content.length);
                    result.put("truncated", truncated);
                    if ("base64".equals(encoding)) {
                        result.put("encoding", "base64");
                        result.put("contentBase64", Base64.getEncoder().encodeToString(content));
                    }
                    else {
                        result.put("encoding", "utf8");
                        result.put("content", new String(content, StandardCharsets.UTF_8));
                    }
                    return ToolResult.ok(result);
                }
                finally {
                    sftp.close();
                }
            }
        });
    }

    private ToolResult sftpWrite(JsonNode arguments) throws Exception {
        final Config.ServerConfig server = sftpServer(arguments);
        final String path = required(arguments, "path");
        String content = optional(arguments, "content", null);
        String contentBase64 = optional(arguments, "contentBase64", null);
        if ((content == null) == (contentBase64 == null)) {
            throw new InvalidToolArguments("Provide exactly one of content or contentBase64");
        }
        final byte[] bytes;
        try {
            bytes = contentBase64 == null ? content.getBytes(StandardCharsets.UTF_8)
                    : Base64.getDecoder().decode(contentBase64);
        }
        catch (IllegalArgumentException ex) {
            throw new InvalidToolArguments("contentBase64 is not valid base64");
        }
        if (bytes.length > server.maxOutputBytes()) {
            throw new InvalidToolArguments("file content exceeds maxOutputBytes for this server");
        }
        return withConnection(server, new ConnectionOperation<ToolResult>() {
            @Override
            public ToolResult run(Connection connection) throws Exception {
                SftpClient sftp = openSftp(connection);
                try {
                    OutputStream output = sftp.write(path, 32768,
                            EnumSet.of(SftpClient.OpenMode.Create, SftpClient.OpenMode.Write,
                                    SftpClient.OpenMode.Truncate));
                    try {
                        output.write(bytes);
                    }
                    finally {
                        output.close();
                    }
                    ObjectNode result = mapper.createObjectNode();
                    result.put("serverId", server.id());
                    result.put("path", path);
                    result.put("bytesWritten", bytes.length);
                    return ToolResult.ok(result);
                }
                finally {
                    sftp.close();
                }
            }
        });
    }

    private ToolResult sftpMkdir(JsonNode arguments) throws Exception {
        final Config.ServerConfig server = sftpServer(arguments);
        final String path = required(arguments, "path");
        return withConnection(server, new ConnectionOperation<ToolResult>() {
            @Override
            public ToolResult run(Connection connection) throws Exception {
                SftpClient sftp = openSftp(connection);
                try {
                    sftp.mkdir(path);
                    return simpleResult(server.id(), "path", path, "created", true);
                }
                finally {
                    sftp.close();
                }
            }
        });
    }

    private ToolResult sftpDelete(JsonNode arguments) throws Exception {
        final Config.ServerConfig server = sftpServer(arguments);
        final String path = required(arguments, "path");
        final boolean directory = bool(arguments, "directory", false);
        return withConnection(server, new ConnectionOperation<ToolResult>() {
            @Override
            public ToolResult run(Connection connection) throws Exception {
                SftpClient sftp = openSftp(connection);
                try {
                    if (directory) {
                        sftp.rmdir(path);
                    }
                    else {
                        sftp.remove(path);
                    }
                    return simpleResult(server.id(), "path", path, "deleted", true);
                }
                finally {
                    sftp.close();
                }
            }
        });
    }

    private ToolResult tunnelOpen(JsonNode arguments) throws Exception {
        if (closed.get()) {
            throw new OperationRejected("SSH service is closed");
        }
        final Config.ServerConfig server = server(arguments);
        if (!server.allowPortForwarding()) {
            throw new OperationRejected("Port forwarding is disabled for server '" + server.id() + "'");
        }
        final String localHost = optional(arguments, "localHost", "127.0.0.1").trim();
        if (!isLoopbackHost(localHost)) {
            throw new OperationRejected("For safety, localHost must be loopback");
        }
        final int localPort = integer(arguments, "localPort", 0, 0, 65535);
        final String remoteHost = required(arguments, "remoteHost");
        final int remotePort = integer(arguments, "remotePort", 0, 1, 65535);
        boolean acquired = server.tunnelSlots().tryAcquire(server.connectTimeoutMs(), TimeUnit.MILLISECONDS);
        if (!acquired) {
            throw new OperationRejected("Tunnel limit reached for this server: too many open tunnels");
        }
        Connection connection = null;
        try {
            connection = openConnection(server);
            ExplicitPortForwardingTracker tracker = connection.session.createLocalPortForwardingTracker(
                    new SshdSocketAddress(localHost, localPort), new SshdSocketAddress(remoteHost, remotePort));
            String id = UUID.randomUUID().toString();
            Tunnel tunnel = new Tunnel(id, server, connection, tracker);
            SshdSocketAddress boundAddress = tracker.getBoundAddress();
            ObjectNode result = mapper.createObjectNode();
            result.put("tunnelId", id);
            result.put("serverId", server.id());
            result.put("localHost", boundAddress.getHostName());
            result.put("localPort", boundAddress.getPort());
            result.put("remoteHost", remoteHost);
            result.put("remotePort", remotePort);
            result.put("warning", "The tunnel remains active until ssh_tunnel_close or process exit");
            synchronized (tunnels) {
                if (closed.get()) {
                    tracker.close();
                    throw new OperationRejected("SSH service is closed");
                }
                tunnels.put(id, tunnel);
            }
            AuditLog.success("ssh_tunnel_open", server, 0, "tunnelId=" + id);
            return ToolResult.ok(result);
        }
        catch (Exception ex) {
            if (connection != null) {
                connection.close();
            }
            server.tunnelSlots().release();
            throw ex;
        }
    }

    private ToolResult tunnelClose(JsonNode arguments) throws InvalidToolArguments {
        String id = required(arguments, "tunnelId");
        Tunnel tunnel = tunnels.remove(id);
        if (tunnel == null) {
            throw new InvalidToolArguments("Unknown tunnelId");
        }
        tunnel.close();
        ObjectNode result = mapper.createObjectNode();
        result.put("tunnelId", id);
        result.put("closed", true);
        return ToolResult.ok(result);
    }

    private ToolResult tunnelList() {
        ObjectNode result = mapper.createObjectNode();
        ArrayNode items = result.putArray("tunnels");
        List<Tunnel> activeTunnels = new ArrayList<Tunnel>(tunnels.values());
        activeTunnels.sort((left, right) -> left.id.compareTo(right.id));
        for (Tunnel tunnel : activeTunnels) {
            if (!tunnel.tracker.isOpen()) {
                if (tunnels.remove(tunnel.id, tunnel)) {
                    tunnel.close();
                }
                continue;
            }
            ObjectNode item = items.addObject();
            item.put("tunnelId", tunnel.id);
            item.put("serverId", tunnel.server.id());
            item.put("localHost", tunnel.tracker.getBoundAddress().getHostName());
            item.put("localPort", tunnel.tracker.getBoundAddress().getPort());
            item.put("open", tunnel.tracker.isOpen());
        }
        return ToolResult.ok(result);
    }

    private Config.ServerConfig server(JsonNode arguments) throws InvalidToolArguments {
        String id = required(arguments, "serverId");
        Config.ServerConfig server = catalog.get(id);
        if (server == null) {
            throw new InvalidToolArguments("Unknown serverId");
        }
        return server;
    }

    private Config.ServerConfig sftpServer(JsonNode arguments) throws InvalidToolArguments, OperationRejected {
        Config.ServerConfig server = server(arguments);
        if (!server.allowSftp()) {
            throw new OperationRejected("SFTP is disabled for server '" + server.id() + "'");
        }
        return server;
    }

    private <T> T withConnection(Config.ServerConfig server, ConnectionOperation<T> operation) throws Exception {
        return withConnection(server, server.commandTimeoutMs(), operation);
    }

    private <T> T withConnection(Config.ServerConfig server, long operationTimeoutMs,
            ConnectionOperation<T> operation) throws Exception {
        if (closed.get()) {
            throw new OperationRejected("SSH service is closed");
        }
        boolean acquired = server.operationSlots().tryAcquire(server.connectTimeoutMs() + server.commandTimeoutMs(),
                TimeUnit.MILLISECONDS);
        if (!acquired) {
            throw new OperationRejected("Server operation limit reached");
        }
        long started = System.nanoTime();
        Connection connection = null;
        ExecutorService executor = null;
        try {
            connection = openConnection(server);
            if (closed.get()) {
                throw new OperationRejected("SSH service is closed");
            }
            final Connection activeConnection = connection;
            executor = Executors.newSingleThreadExecutor(runnable -> {
                Thread thread = new Thread(runnable, "ssh-mcp-operation");
                thread.setDaemon(true);
                return thread;
            });
            Future<T> future = executor.submit(() -> operation.run(activeConnection));
            try {
                return future.get(operationTimeoutMs, TimeUnit.MILLISECONDS);
            }
            catch (TimeoutException ex) {
                future.cancel(true);
                activeConnection.close();
                throw new OperationRejected("SSH operation timed out");
            }
            catch (ExecutionException ex) {
                Throwable cause = ex.getCause();
                if (cause instanceof Exception) {
                    throw (Exception) cause;
                }
                if (cause instanceof Error) {
                    throw (Error) cause;
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
            AuditLog.duration(server, elapsedMs(started));
        }
    }

    private Connection openConnection(Config.ServerConfig server) throws Exception {
        SshClient client = SshClient.setUpDefaultClient();
        if (server.strictHostKeyChecking() && !Config.autoAcceptHostKeys()) {
            client.setServerKeyVerifier(new DefaultKnownHostsServerKeyVerifier(
                    RejectAllServerKeyVerifier.INSTANCE, true, server.knownHostsPath()));
        }
        else {
            client.setServerKeyVerifier(AcceptAllServerKeyVerifier.INSTANCE);
        }
        client.start();
        try {
            ClientSession session = client.connect(server.username(), server.host(), server.port())
                    .verify(server.connectTimeoutMs()).getSession();
            if (server.privateKeyPath() != null) {
                Path keyPath = Paths.get(server.privateKeyPath()).toAbsolutePath().normalize();
                String passphrase = env(server.privateKeyPassphraseEnv());
                FilePasswordProvider provider = passphrase == null
                        ? FilePasswordProvider.EMPTY : FilePasswordProvider.of(passphrase);
                Collection<KeyPair> keyPairs = SecurityUtils.getKeyPairResourceParser()
                        .loadKeyPairs(null, keyPath, provider);
                if (keyPairs.isEmpty()) {
                    throw new IOException("No private key found");
                }
                for (KeyPair keyPair : keyPairs) {
                    session.addPublicKeyIdentity(keyPair);
                }
            }
            String password = env(server.passwordEnv());
            if (password != null) {
                session.addPasswordIdentity(password);
            }
            session.auth().verify(server.connectTimeoutMs());
            return new Connection(client, session);
        }
        catch (Exception ex) {
            client.stop();
            throw ex;
        }
    }

    private SftpClient openSftp(Connection connection) throws IOException {
        return SftpClientFactory.instance().createSftpClient(connection.session);
    }

    private static String env(String name) throws IOException {
        if (name == null) {
            return null;
        }
        String value = System.getenv(name);
        if (value == null || value.isEmpty()) {
            throw new IOException("Configured credential environment variable is not set");
        }
        return value;
    }

    static void checkCommandPolicy(Config.ServerConfig server, String command) throws OperationRejected {
        for (java.util.regex.Pattern pattern : server.deniedCommandPatterns()) {
            if (pattern.matcher(command).find()) {
                throw new OperationRejected("Command rejected by the server command policy");
            }
        }
        if (!server.allowedCommandPrefixes().isEmpty()) {
            if (hasShellOperators(command)) {
                throw new OperationRejected("Command contains shell operators that are not allowed with an allowlist");
            }
            String trimmed = command.trim();
            for (String prefix : server.allowedCommandPrefixes()) {
                if (trimmed.equals(prefix)
                        || (trimmed.startsWith(prefix) && trimmed.length() > prefix.length()
                        && Character.isWhitespace(trimmed.charAt(prefix.length())))) {
                    return;
                }
            }
            throw new OperationRejected("Command is not allowed by the server command policy");
        }
    }

    private static boolean hasShellOperators(String command) {
        for (int i = 0; i < command.length(); i++) {
            char value = command.charAt(i);
            if (value == 0 || value == '\n' || value == '\r'
                    || ";|&<>$`(){}'\"\\".indexOf(value) >= 0) {
                return true;
            }
        }
        return false;
    }

    private static boolean isLoopbackHost(String host) {
        if ("localhost".equalsIgnoreCase(host)) {
            return true;
        }
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

    private static long elapsedMs(long started) {
        return TimeUnit.NANOSECONDS.toMillis(System.nanoTime() - started);
    }

    private static void addAttributes(ObjectNode node, SftpClient.Attributes attributes) {
        node.put("size", attributes.getSize());
        node.put("type", attributes.isDirectory() ? "directory"
                : attributes.isRegularFile() ? "file" : attributes.isSymbolicLink() ? "symlink" : "other");
        node.put("permissions", Integer.toOctalString(attributes.getPermissions() & 07777));
        if (attributes.getOwner() != null) {
            node.put("owner", attributes.getOwner());
        }
        if (attributes.getGroup() != null) {
            node.put("group", attributes.getGroup());
        }
        if (attributes.getModifyTime() != null) {
            node.put("modified", attributes.getModifyTime().toInstant().toString());
        }
    }

    private ToolResult simpleResult(String serverId, String key, String value, String flag, boolean flagValue) {
        ObjectNode result = mapper.createObjectNode();
        result.put("serverId", serverId);
        result.put(key, value);
        result.put(flag, flagValue);
        return ToolResult.ok(result);
    }

    private static String required(JsonNode arguments, String name) throws InvalidToolArguments {
        if (arguments == null || !arguments.isObject()) {
            throw new InvalidToolArguments("Tool arguments must be a JSON object");
        }
        JsonNode value = arguments.get(name);
        if (value == null || !value.isTextual() || value.asText().trim().isEmpty()) {
            throw new InvalidToolArguments(name + " is required");
        }
        return value.asText().trim();
    }

    private static String optional(JsonNode arguments, String name, String defaultValue) throws InvalidToolArguments {
        if (arguments == null || !arguments.isObject()) {
            throw new InvalidToolArguments("Tool arguments must be a JSON object");
        }
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
            throw new InvalidToolArguments(name + " must be boolean");
        }
        return value.asBoolean();
    }

    private static int integer(JsonNode arguments, String name, int defaultValue, int min, int max)
            throws InvalidToolArguments {
        JsonNode value = arguments.get(name);
        if (value == null || value.isNull()) {
            return defaultValue;
        }
        if (!value.isIntegralNumber() || !value.canConvertToInt()) {
            throw new InvalidToolArguments(name + " must be an integer");
        }
        int result = value.asInt();
        if (result < min || result > max) {
            throw new InvalidToolArguments(name + " must be between " + min + " and " + max);
        }
        return result;
    }

    private static long timeout(JsonNode arguments, long max) throws InvalidToolArguments {
        JsonNode value = arguments.get("timeoutMs");
        if (value == null || value.isNull()) {
            return max;
        }
        if (!value.isIntegralNumber()) {
            throw new InvalidToolArguments("timeoutMs must be an integer");
        }
        long result = value.asLong();
        if (result < 100 || result > max) {
            throw new InvalidToolArguments("timeoutMs must be between 100 and " + max);
        }
        return result;
    }

    private static String sha256(String value) {
        try {
            byte[] digest = MessageDigest.getInstance("SHA-256").digest(value.getBytes(StandardCharsets.UTF_8));
            StringBuilder result = new StringBuilder(digest.length * 2);
            for (byte item : digest) {
                result.append(String.format("%02x", item));
            }
            return result.toString().substring(0, 16);
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
        synchronized (tunnels) {
            for (String id : new ArrayList<String>(tunnels.keySet())) {
                Tunnel tunnel = tunnels.remove(id);
                if (tunnel != null) {
                    tunnel.close();
                }
            }
        }
    }

    private interface ConnectionOperation<T> {
        T run(Connection connection) throws Exception;
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
            ObjectMapper mapper = new ObjectMapper();
            ObjectNode payload = mapper.createObjectNode();
            payload.put("errorCode", code);
            payload.put("message", message);
            return new ToolResult(payload, true);
        }

        ObjectNode payload() { return payload; }
        boolean isError() { return error; }
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

    private static final class Tunnel {
        private final String id;
        private final Config.ServerConfig server;
        private final Connection connection;
        private final ExplicitPortForwardingTracker tracker;
        private final AtomicBoolean closed = new AtomicBoolean();

        private Tunnel(String id, Config.ServerConfig server, Connection connection,
                ExplicitPortForwardingTracker tracker) {
            this.id = id;
            this.server = server;
            this.connection = connection;
            this.tracker = tracker;
        }

        private void close() {
            if (!closed.compareAndSet(false, true)) {
                return;
            }
            try {
                tracker.close();
            }
            catch (Exception ignored) {
                // Cleanup is best effort.
            }
            connection.close();
            server.tunnelSlots().release();
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
        public void write(int value) {
            if (delegate.size() < maxBytes) {
                delegate.write(value);
            }
            else {
                truncated = true;
            }
        }

        @Override
        public void write(byte[] bytes, int offset, int length) {
            int remaining = maxBytes - delegate.size();
            if (remaining > 0) {
                delegate.write(bytes, offset, Math.min(length, remaining));
            }
            if (length > remaining) {
                truncated = true;
            }
        }

        byte[] toByteArray() { return delegate.toByteArray(); }
        boolean isTruncated() { return truncated; }
    }

    static class InvalidToolArguments extends Exception {
        InvalidToolArguments(String message) { super(message); }
    }

    static class OperationRejected extends Exception {
        OperationRejected(String message) { super(message); }
    }
}
