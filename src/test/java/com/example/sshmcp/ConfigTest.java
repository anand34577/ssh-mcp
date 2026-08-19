package com.example.sshmcp;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ObjectNode;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.nio.file.Files;
import java.nio.file.Path;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.junit.jupiter.api.Assertions.assertThrows;

class ConfigTest {
    private final ObjectMapper mapper = new ObjectMapper();

    @TempDir
    Path tempDir;

    @Test
    void rejectsInlineSecrets() {
        ObjectNode node = baseServer();
        node.put("password", "must-not-be-here");

        assertThrows(Exception.class, () -> Config.ServerConfig.from(node));
    }

    @Test
    void loadsMultipleServersWithSecretReferencesOnly() throws Exception {
        Path knownHosts = Files.createFile(tempDir.resolve("known_hosts"));
        ObjectNode first = baseServer();
        first.put("id", "one");
        first.put("knownHostsPath", knownHosts.toString());
        ObjectNode second = baseServer();
        second.put("id", "two");
        second.put("knownHostsPath", knownHosts.toString());
        second.remove("privateKeyPath");
        second.put("passwordEnv", "SSH_MCP_PASSWORD");

        Config.ServerConfig one = Config.ServerConfig.from(first);
        Config.ServerConfig two = Config.ServerConfig.from(second);

        assertEquals("one", one.id());
        assertEquals("two", two.id());
        assertTrue(one.strictHostKeyChecking());
    }

    @Test
    void requiresAnExistingKnownHostsFile() {
        ObjectNode node = baseServer();
        assertThrows(Exception.class, () -> Config.ServerConfig.from(node));
    }

    private ObjectNode baseServer() {
        ObjectNode node = mapper.createObjectNode();
        node.put("id", "test");
        node.put("host", "localhost");
        node.put("username", "test-user");
        node.put("privateKeyPath", tempDir.resolve("id_ed25519").toString());
        node.put("knownHostsPath", tempDir.resolve("known_hosts").toString());
        return node;
    }
}
