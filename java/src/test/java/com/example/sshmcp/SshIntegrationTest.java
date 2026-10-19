package com.example.sshmcp;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ObjectNode;
import org.apache.sshd.common.config.keys.PublicKeyEntry;
import org.apache.sshd.common.config.keys.writer.openssh.OpenSSHKeyPairResourceWriter;
import org.apache.sshd.common.file.virtualfs.VirtualFileSystemFactory;
import org.apache.sshd.common.util.security.SecurityUtils;
import org.apache.sshd.server.Environment;
import org.apache.sshd.server.ExitCallback;
import org.apache.sshd.server.SshServer;
import org.apache.sshd.server.channel.ChannelSession;
import org.apache.sshd.server.command.Command;
import org.apache.sshd.server.forward.AcceptAllForwardingFilter;
import org.apache.sshd.server.keyprovider.SimpleGeneratorHostKeyProvider;
import org.apache.sshd.sftp.server.SftpSubsystemFactory;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.io.BufferedReader;
import java.io.IOException;
import java.io.InputStream;
import java.io.InputStreamReader;
import java.io.OutputStream;
import java.net.InetAddress;
import java.net.ServerSocket;
import java.net.Socket;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.KeyPair;
import java.security.KeyPairGenerator;
import java.security.PublicKey;
import java.util.Base64;
import java.util.Collections;
import java.util.concurrent.CountDownLatch;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * End-to-end tests against an in-process Apache MINA SSH server with fake commands, an SFTP
 * subsystem over a temporary directory, and port forwarding. The password comes from the
 * SSH_MCP_TEST_PASSWORD environment variable configured in the surefire plugin.
 */
class SshIntegrationTest {
    private static final String PASSWORD_ENV = "SSH_MCP_TEST_PASSWORD";
    private static final String USER = "tester";

    private final ObjectMapper mapper = new ObjectMapper();

    @TempDir
    Path tempDir;

    private SshServer sshd;
    private KeyPair clientKey;
    private PublicKey hostKey;

    @BeforeEach
    void startServer() throws Exception {
        KeyPairGenerator generator = KeyPairGenerator.getInstance("EC");
        generator.initialize(256);
        clientKey = generator.generateKeyPair();
        Path root = Files.createDirectories(tempDir.resolve("root"));

        sshd = SshServer.setUpDefaultServer();
        sshd.setHost("127.0.0.1");
        sshd.setPort(0);
        sshd.setKeyPairProvider(new SimpleGeneratorHostKeyProvider(tempDir.resolve("hostkey.ser")));
        sshd.setPasswordAuthenticator((user, password, session) ->
                USER.equals(user) && password.equals(System.getenv(PASSWORD_ENV)));
        sshd.setPublickeyAuthenticator((user, key, session) -> USER.equals(user) && key.equals(clientKey.getPublic()));
        sshd.setCommandFactory((channel, command) -> new FakeCommand(command));
        sshd.setSubsystemFactories(Collections.singletonList(new SftpSubsystemFactory()));
        sshd.setFileSystemFactory(new VirtualFileSystemFactory(root));
        sshd.setForwardingFilter(AcceptAllForwardingFilter.INSTANCE);
        sshd.start();
        hostKey = sshd.getKeyPairProvider().loadKeys(null).iterator().next().getPublic();
    }

    @AfterEach
    void stopServer() throws IOException {
        sshd.stop(true);
    }

    @Test
    void execReportsOutputExitCodesTruncationAndTimeouts() throws Exception {
        try (SshService ssh = service(baseConfig(knownHosts(hostKey)))) {
            JsonNode echo = ok(ssh.call("ssh_exec", args("command", "echo hello world")));
            assertEquals("hello world\n", echo.path("stdout").asText());
            assertEquals(0, echo.path("exitCode").asInt());
            assertFalse(echo.path("timedOut").asBoolean());

            JsonNode fail = ok(ssh.call("ssh_exec", args("command", "fail")));
            assertEquals(3, fail.path("exitCode").asInt());
            assertEquals("boom\n", fail.path("stderr").asText());

            ObjectNode flood = args("command", "flood 5000");
            flood.put("maxOutputBytes", 1024);
            JsonNode truncated = ok(ssh.call("ssh_exec", flood));
            assertEquals(1024, truncated.path("stdout").asText().length());
            assertTrue(truncated.path("stdoutTruncated").asBoolean());

            ObjectNode sleep = args("command", "sleep");
            sleep.put("timeoutMs", 400);
            long started = System.nanoTime();
            JsonNode timedOut = ok(ssh.call("ssh_exec", sleep));
            assertTrue(timedOut.path("timedOut").asBoolean(), timedOut.toString());
            assertTrue(timedOut.path("exitCode").isNull());
            assertTrue(System.nanoTime() - started < 4_000_000_000L, "timeout took too long");
        }
    }

    @Test
    void sftpRoundTripPagingRenameAndDelete() throws Exception {
        try (SshService ssh = service(baseConfig(knownHosts(hostKey)))) {
            String text = "line one\nline two — with unicode\n";
            ObjectNode mkdir = args("path", "/work/nested");
            mkdir.put("parents", true);
            ok(ssh.call("sftp_mkdir", mkdir));
            ObjectNode write = args("path", "/work/nested/a.txt");
            write.put("content", text);
            assertEquals(text.getBytes(StandardCharsets.UTF_8).length, ok(ssh.call("sftp_write_file", write)).path("bytesWritten").asInt());

            StringBuilder assembled = new StringBuilder();
            long offset = 0;
            while (true) {
                ObjectNode read = args("path", "/work/nested/a.txt");
                read.put("offset", offset);
                read.put("maxBytes", 7);
                JsonNode chunk = ok(ssh.call("sftp_read_file", read));
                assertTrue(chunk.path("validUtf8").asBoolean());
                assembled.append(chunk.path("content").asText());
                offset = chunk.path("nextOffset").asLong();
                if (!chunk.path("truncated").asBoolean()) {
                    break;
                }
            }
            assertEquals(text, assembled.toString());

            String encoded = Base64.getEncoder().encodeToString(new byte[] { 0, 1, 2, (byte) 255 });
            ObjectNode binary = args("path", "/work/bin.dat");
            binary.put("contentBase64", encoded);
            ok(ssh.call("sftp_write_file", binary));
            ObjectNode readBinary = args("path", "/work/bin.dat");
            readBinary.put("encoding", "base64");
            assertEquals(encoded, ok(ssh.call("sftp_read_file", readBinary)).path("contentBase64").asText());

            JsonNode listing = ok(ssh.call("sftp_list", args("path", "/work")));
            assertEquals(2, listing.path("entries").size(), listing.toString());
            assertEquals("bin.dat", listing.path("entries").get(0).path("name").asText());
            assertEquals("directory", listing.path("entries").get(1).path("type").asText());

            ObjectNode rename = args("from", "/work/bin.dat");
            rename.put("to", "/work/moved.dat");
            ok(ssh.call("sftp_rename", rename));
            JsonNode stat = ok(ssh.call("sftp_stat", args("path", "/work/moved.dat")));
            assertEquals(4, stat.path("size").asInt());
            assertEquals("file", stat.path("type").asText());

            ok(ssh.call("sftp_delete", args("path", "/work/moved.dat")));
            SshService.ToolResult missing = ssh.call("sftp_stat", args("path", "/work/moved.dat"));
            assertTrue(missing.isError());
            assertTrue(missing.payload().path("message").asText().contains("no such file"), missing.payload().toString());
        }
    }

    @Test
    void tunnelForwardsLocalConnectionsAndReleasesItsSlot() throws Exception {
        try (ServerSocket echo = new ServerSocket(0, 50, InetAddress.getLoopbackAddress())) {
            Thread echoThread = new Thread(() -> {
                while (!echo.isClosed()) {
                    try (Socket socket = echo.accept()) {
                        String line = new BufferedReader(new InputStreamReader(socket.getInputStream(), StandardCharsets.UTF_8)).readLine();
                        socket.getOutputStream().write(("echo:" + line + "\n").getBytes(StandardCharsets.UTF_8));
                    }
                    catch (IOException ignored) {
                        // Socket closed at the end of the test.
                    }
                }
            });
            echoThread.setDaemon(true);
            echoThread.start();

            ObjectNode config = baseConfig(knownHosts(hostKey));
            config.put("allowPortForwarding", true);
            config.put("maxConcurrentTunnels", 1);
            try (SshService ssh = service(config)) {
                ObjectNode open = args("remoteHost", "127.0.0.1");
                open.put("remotePort", echo.getLocalPort());
                JsonNode tunnel = ok(ssh.call("ssh_tunnel_open", open));
                try (Socket client = new Socket(tunnel.path("localHost").asText(), tunnel.path("localPort").asInt())) {
                    client.setSoTimeout(5000);
                    client.getOutputStream().write("ping\n".getBytes(StandardCharsets.UTF_8));
                    String reply = new BufferedReader(new InputStreamReader(client.getInputStream(), StandardCharsets.UTF_8)).readLine();
                    assertEquals("echo:ping", reply);
                }
                assertEquals(1, ok(ssh.call("ssh_tunnel_list", null)).path("tunnels").size());

                SshService.ToolResult limited = ssh.call("ssh_tunnel_open", open);
                assertTrue(limited.isError());
                assertTrue(limited.payload().path("message").asText().contains("tunnel limit"));

                ObjectNode close = mapper.createObjectNode();
                close.put("tunnelId", tunnel.path("tunnelId").asText());
                ok(ssh.call("ssh_tunnel_close", close));
                assertEquals(0, ok(ssh.call("ssh_tunnel_list", null)).path("tunnels").size());

                JsonNode reopened = ok(ssh.call("ssh_tunnel_open", open));
                close.put("tunnelId", reopened.path("tunnelId").asText());
                ok(ssh.call("ssh_tunnel_close", close));
            }
        }
    }

    @Test
    void hostKeyMismatchAndUnknownHostAreExplained() throws Exception {
        KeyPairGenerator generator = KeyPairGenerator.getInstance("EC");
        generator.initialize(256);
        PublicKey otherKey = generator.generateKeyPair().getPublic();

        try (SshService ssh = service(baseConfig(knownHosts(otherKey)))) {
            SshService.ToolResult result = ssh.call("ssh_exec", args("command", "echo hi"));
            assertTrue(result.payload().path("message").asText().contains("MISMATCH"), result.payload().toString());
        }

        Path unrelated = tempDir.resolve("known_hosts_unrelated");
        Files.write(unrelated, ("unrelated.example " + PublicKeyEntry.toString(otherKey) + "\n").getBytes(StandardCharsets.UTF_8));
        try (SshService ssh = service(baseConfig(unrelated))) {
            SshService.ToolResult result = ssh.call("ssh_exec", args("command", "echo hi"));
            assertTrue(result.payload().path("message").asText().contains("is not in"), result.payload().toString());
            assertFalse(ssh.check(false).get(0).ok());
        }
    }

    @Test
    void privateKeyAuthenticationAndPreflightCheck() throws Exception {
        Path keyFile = tempDir.resolve("id_ecdsa");
        try (OutputStream out = Files.newOutputStream(keyFile)) {
            OpenSSHKeyPairResourceWriter.INSTANCE.writePrivateKey(clientKey, "test", null, out);
        }
        ObjectNode config = baseConfig(knownHosts(hostKey));
        config.remove("passwordEnv");
        config.put("privateKeyPath", keyFile.toString());
        try (SshService ssh = service(config)) {
            assertEquals("hello\n", ok(ssh.call("ssh_exec", args("command", "echo hello"))).path("stdout").asText());
            SshService.CheckResult check = ssh.check(true).get(0);
            assertTrue(check.ok() && check.connected, String.valueOf(check.problems));
        }
    }

    @Test
    void missingCredentialEnvironmentVariableIsNamed() throws Exception {
        ObjectNode config = baseConfig(knownHosts(hostKey));
        config.put("passwordEnv", "SSH_MCP_TEST_UNSET_VARIABLE");
        try (SshService ssh = service(config)) {
            SshService.ToolResult result = ssh.call("ssh_exec", args("command", "echo hi"));
            assertTrue(result.payload().path("message").asText().contains("SSH_MCP_TEST_UNSET_VARIABLE"), result.payload().toString());
        }
    }

    @Test
    void completeUtf8LengthNeverSplitsACharacter() {
        byte[] text = "ab€".getBytes(StandardCharsets.UTF_8);
        assertEquals(2, SshService.completeUtf8Length(text, 3));
        assertEquals(2, SshService.completeUtf8Length(text, 4));
        assertEquals(5, SshService.completeUtf8Length(text, 5));
        assertThrows(SshService.InvalidToolArguments.class, () -> SshService.decodeBase64("not base64!"));
        assertEquals("hello", new String(assertDoesNotThrowDecode("aGVs\nbG8"), StandardCharsets.UTF_8));
    }

    private static byte[] assertDoesNotThrowDecode(String value) {
        try {
            return SshService.decodeBase64(value);
        }
        catch (SshService.InvalidToolArguments ex) {
            throw new AssertionError(ex);
        }
    }

    private SshService service(ObjectNode serverConfig) throws IOException {
        Config.ServerConfig server = Config.ServerConfig.from(serverConfig);
        return new SshService(mapper, new Config.ServerCatalog(tempDir.resolve("servers.json"),
                Collections.singletonMap(server.id(), server)));
    }

    private ObjectNode baseConfig(Path knownHosts) {
        ObjectNode node = mapper.createObjectNode();
        node.put("id", "test");
        node.put("host", "127.0.0.1");
        node.put("port", sshd.getPort());
        node.put("username", USER);
        node.put("passwordEnv", PASSWORD_ENV);
        node.put("knownHostsPath", knownHosts.toString());
        node.put("connectTimeoutMs", 5000);
        node.put("commandTimeoutMs", 5000);
        node.put("maxOutputBytes", 4096);
        return node;
    }

    private Path knownHosts(PublicKey key) throws IOException {
        Path path = Files.createTempFile(tempDir, "known_hosts", "");
        String line = "[127.0.0.1]:" + sshd.getPort() + " " + PublicKeyEntry.toString(key) + "\n";
        Files.write(path, line.getBytes(StandardCharsets.UTF_8));
        return path;
    }

    private ObjectNode args(String key, String value) {
        ObjectNode node = mapper.createObjectNode();
        node.put("serverId", "test");
        node.put(key, value);
        return node;
    }

    private static JsonNode ok(SshService.ToolResult result) {
        assertFalse(result.isError(), result.payload().toString());
        return result.payload();
    }

    /** "echo ARGS", "fail" (stderr + exit 3), "flood N", and "sleep" (until destroyed). */
    private static final class FakeCommand implements Command {
        private final String command;
        private final CountDownLatch destroyed = new CountDownLatch(1);
        private OutputStream out;
        private OutputStream err;
        private ExitCallback exit;

        FakeCommand(String command) {
            this.command = command;
        }

        @Override public void setInputStream(InputStream in) { }
        @Override public void setOutputStream(OutputStream out) { this.out = out; }
        @Override public void setErrorStream(OutputStream err) { this.err = err; }
        @Override public void setExitCallback(ExitCallback callback) { this.exit = callback; }

        @Override
        public void start(ChannelSession channel, Environment env) {
            Thread thread = new Thread(this::run, "fake-command");
            thread.setDaemon(true);
            thread.start();
        }

        private void run() {
            String[] parts = command.split(" ", 2);
            String rest = parts.length > 1 ? parts[1] : "";
            try {
                switch (parts[0]) {
                    case "echo":
                        out.write((rest + "\n").getBytes(StandardCharsets.UTF_8));
                        out.flush();
                        exit.onExit(0);
                        break;
                    case "fail":
                        err.write("boom\n".getBytes(StandardCharsets.UTF_8));
                        err.flush();
                        exit.onExit(3);
                        break;
                    case "flood":
                        byte[] data = new byte[Integer.parseInt(rest)];
                        java.util.Arrays.fill(data, (byte) 'x');
                        out.write(data);
                        out.flush();
                        exit.onExit(0);
                        break;
                    case "sleep":
                        destroyed.await();
                        break;
                    default:
                        exit.onExit(127);
                }
            }
            catch (Exception ex) {
                exit.onExit(1, ex.toString());
            }
        }

        @Override
        public void destroy(ChannelSession channel) {
            destroyed.countDown();
        }
    }

    static {
        // Ensure the BouncyCastle provider registration happens before key generation in tests.
        SecurityUtils.isBouncyCastleRegistered();
    }
}
