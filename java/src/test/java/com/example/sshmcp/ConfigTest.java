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
    void rejectsUnknownServerFields() {
        ObjectNode node = baseServer();
        node.put("allowSFTP", false);

        assertThrows(Exception.class, () -> Config.ServerConfig.from(node));
    }

    @Test
    void rejectsCaseVariantInlineSecrets() {
        ObjectNode node = baseServer();
        node.put("Password", "must-not-be-here");

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

    @Test
    void rejectsUnusableValues() {
        String[][] cases = {
                { "port", "0" }, { "commandTimeoutMs", "50" }, { "maxOutputBytes", "\"big\"" },
                { "passwordEnv", "\"hunter2 secret\"" }, { "allowedCommandPrefixes", "[\"ls;\"]" },
                { "deniedCommandRegexes", "[\"(\"]" },
        };
        for (String[] item : cases) {
            ObjectNode node = baseServer();
            try {
                node.set(item[0], mapper.readTree(item[1]));
            }
            catch (Exception ex) {
                throw new AssertionError(ex);
            }
            assertThrows(Exception.class, () -> Config.ServerConfig.from(node), item[0] + "=" + item[1]);
        }
    }

    @Test
    void parseAcceptsSchemaAndRejectsUnknownRootFields() throws Exception {
        Path knownHosts = Files.createFile(tempDir.resolve("known_hosts_schema"));
        String server = "{\"id\":\"a\",\"host\":\"h\",\"username\":\"u\",\"passwordEnv\":\"A\",\"knownHostsPath\":"
                + mapper.writeValueAsString(knownHosts.toString()) + "}";
        Config.ServerCatalog catalog = Config.parse(mapper,
                new java.io.StringReader("{\"$schema\":\"./servers.schema.json\",\"servers\":[" + server + "]}"), tempDir);
        assertEquals(1, catalog.servers().size());
        assertThrows(Exception.class, () -> Config.parse(mapper,
                new java.io.StringReader("{\"servers\":[" + server + "],\"extra\":1}"), tempDir));
        assertThrows(Exception.class, () -> Config.parse(mapper,
                new java.io.StringReader("{\"servers\":[" + server + "," + server + "]}"), tempDir));
    }

    @Test
    void expandResolvesHomeAndNeverLoops() {
        String home = System.getProperty("user.home");
        assertEquals(home + "/k", Config.expand("${user.home}/k"));
        assertEquals(java.nio.file.Paths.get(home, "k").toString(), Config.expand("~/k"));
        // An unresolvable or self-referencing value must terminate.
        assertEquals("/x", Config.expand("${SSH_MCP_SURELY_UNSET_VARIABLE}/x"));
        assertEquals("plain", Config.expand("plain"));
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
