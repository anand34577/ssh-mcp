package com.example.sshmcp;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ObjectNode;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.io.ByteArrayInputStream;
import java.io.ByteArrayOutputStream;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.Collections;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

class McpStdioServerTest {
    private final ObjectMapper mapper = new ObjectMapper();

    @TempDir
    Path tempDir;

    @Test
    void servesInitializeToolsAndNonSecretServerListingOverStdout() throws Exception {
        Path knownHosts = Files.createFile(tempDir.resolve("known_hosts"));
        ObjectNode config = mapper.createObjectNode();
        config.put("id", "test");
        config.put("host", "example.test");
        config.put("username", "operator");
        config.put("passwordEnv", "SSH_MCP_TEST_PASSWORD");
        config.put("knownHostsPath", knownHosts.toString());
        Config.ServerConfig server = Config.ServerConfig.from(config);
        Config.ServerCatalog catalog = new Config.ServerCatalog(tempDir.resolve("servers.json"),
                Collections.singletonMap("test", server));
        SshService ssh = new SshService(mapper, catalog);

        String input = "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{\"protocolVersion\":\"2024-11-05\"}}\n"
                + "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/list\",\"params\":{}}\n"
                + "{\"jsonrpc\":\"2.0\",\"id\":3,\"method\":\"tools/call\",\"params\":{\"name\":\"ssh_list_servers\",\"arguments\":{}}}\n";
        ByteArrayOutputStream output = new ByteArrayOutputStream();
        new McpStdioServer(mapper, ssh).run(new ByteArrayInputStream(input.getBytes(StandardCharsets.UTF_8)), output);

        String[] lines = output.toString(StandardCharsets.UTF_8.name()).trim().split("\\R");
        assertEquals(3, lines.length);
        assertEquals("2024-11-05", mapper.readTree(lines[0]).path("result").path("protocolVersion").asText());
        assertTrue(mapper.readTree(lines[1]).path("result").path("tools").size() >= 10);
        String listing = mapper.readTree(lines[2]).path("result").path("content").get(0).path("text").asText();
        assertTrue(listing.contains("secretsExposedToModel"));
        assertTrue(!listing.contains("SSH_MCP_TEST_PASSWORD"));
    }
}
