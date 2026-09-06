package com.example.sshmcp;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ArrayNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.nio.file.Files;
import java.nio.file.Path;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

class SshServiceTest {
    private final ObjectMapper mapper = new ObjectMapper();

    @TempDir
    Path tempDir;

    @Test
    void rejectsShellOperatorsAndPrefixLookalikes() throws Exception {
        ObjectNode node = baseServer();
        ArrayNode prefixes = node.putArray("allowedCommandPrefixes");
        prefixes.add("systemctl status");
        Config.ServerConfig server = Config.ServerConfig.from(node);

        assertThrows(SshService.OperationRejected.class,
                () -> SshService.checkCommandPolicy(server, "systemctl status ; rm -rf /"));
        assertThrows(SshService.OperationRejected.class,
                () -> SshService.checkCommandPolicy(server, "systemctl status-malicious"));
        SshService.checkCommandPolicy(server, "systemctl status\tunit");
    }

    @Test
    void deniedRegexesMatchWithinTheCommand() throws Exception {
        ObjectNode node = baseServer();
        ArrayNode denied = node.putArray("deniedCommandRegexes");
        denied.add("\\brm\\b");
        Config.ServerConfig server = Config.ServerConfig.from(node);

        assertThrows(SshService.OperationRejected.class,
                () -> SshService.checkCommandPolicy(server, "echo rm"));
    }

    @Test
    void rejectsUnknownToolArguments() throws Exception {
        ObjectNode node = baseServer();
        Config.ServerConfig server = Config.ServerConfig.from(node);
        Config.ServerCatalog catalog = new Config.ServerCatalog(tempDir.resolve("servers.json"),
                java.util.Collections.singletonMap("test", server));
        SshService ssh = new SshService(mapper, catalog);
        try {
            ObjectNode arguments = mapper.createObjectNode();
            arguments.put("serverId", "test");
            arguments.put("command", "true");
            arguments.put("unexpected", true);

            SshService.ToolResult result = ssh.call("ssh_exec", arguments);
            assertTrue(result.isError());
            assertEquals("invalid_arguments", result.payload().path("errorCode").asText());
        }
        finally {
            ssh.close();
        }
    }

    private ObjectNode baseServer() throws Exception {
        Path knownHosts = Files.createFile(tempDir.resolve("known_hosts_" + System.nanoTime()));
        ObjectNode node = mapper.createObjectNode();
        node.put("id", "test");
        node.put("host", "localhost");
        node.put("username", "test-user");
        node.put("privateKeyPath", tempDir.resolve("id_ed25519").toString());
        node.put("knownHostsPath", knownHosts.toString());
        return node;
    }
}
