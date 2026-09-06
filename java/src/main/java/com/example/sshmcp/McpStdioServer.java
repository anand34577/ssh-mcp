package com.example.sshmcp;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ArrayNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import com.fasterxml.jackson.core.JsonParser;

import java.io.BufferedInputStream;
import java.io.BufferedWriter;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.io.OutputStreamWriter;
import java.nio.ByteBuffer;
import java.nio.charset.StandardCharsets;
import java.nio.charset.CharacterCodingException;
import java.nio.charset.CodingErrorAction;
import java.util.Arrays;
import java.util.HashSet;
import java.util.Set;

final class McpStdioServer {
    private static final int MAX_MESSAGE_BYTES = 128 * 1024 * 1024;
    private static final String SERVER_NAME = "ssh-mcp-server";
    private static final String SERVER_VERSION = "1.0.0";
    private static final String DEFAULT_PROTOCOL_VERSION = "2025-06-18";
    private static final Set<String> SUPPORTED_PROTOCOLS = new HashSet<String>(Arrays.asList(
            "2024-11-05", "2025-03-26", "2025-06-18", "2025-11-25"));

    private final ObjectMapper mapper;
    private final SshService ssh;

    McpStdioServer(ObjectMapper mapper, SshService ssh) {
        this.mapper = mapper;
        this.ssh = ssh;
    }

    void run(InputStream input, OutputStream output) throws IOException {
        BufferedInputStream reader = new BufferedInputStream(input, 64 * 1024);
        BufferedWriter writer = new BufferedWriter(new OutputStreamWriter(output, StandardCharsets.UTF_8));
        byte[] frame;
        while (true) {
            try {
                frame = readLineLimited(reader);
            }
            catch (IOException ex) {
                if (!"MCP message exceeds the maximum allowed size".equals(ex.getMessage())) {
                    throw ex;
                }
                AuditLog.failure("mcp_frame", ex);
                writer.write(mapper.writeValueAsString(error(null, -32600, ex.getMessage())));
                writer.newLine();
                writer.flush();
                return;
            }
            if (frame == null) {
                break;
            }
            String line;
            try {
                line = StandardCharsets.UTF_8.newDecoder()
                        .onMalformedInput(CodingErrorAction.REPORT)
                        .onUnmappableCharacter(CodingErrorAction.REPORT)
                        .decode(ByteBuffer.wrap(frame)).toString();
            }
            catch (CharacterCodingException ex) {
                AuditLog.failure("mcp_request", ex);
                writer.write(mapper.writeValueAsString(error(null, -32700, "Invalid UTF-8 JSON-RPC request")));
                writer.newLine();
                writer.flush();
                continue;
            }
            if (line.trim().isEmpty()) {
                continue;
            }
            ObjectNode response = null;
            JsonNode requestId = null;
            try {
                JsonNode request;
                try (JsonParser parser = mapper.getFactory().createParser(line)) {
                    request = mapper.readTree(parser);
                    if (parser.nextToken() != null) {
                        throw new IOException("Trailing JSON-RPC data");
                    }
                }
                if (request != null && request.isObject()) {
                    requestId = request.get("id");
                    if (!request.has("method")) {
                        response = error(requestId, -32600, "method is required");
                    }
                    else {
                        response = handle(request);
                    }
                }
                else {
                    response = error(requestId, -32600, "Invalid Request");
                }
            }
            catch (Exception ex) {
                AuditLog.failure("mcp_request", ex);
                response = error(null, -32700, "Invalid JSON-RPC request");
            }
            if (response != null) {
                writer.write(mapper.writeValueAsString(response));
                writer.newLine();
                writer.flush();
            }
        }
    }

    private static byte[] readLineLimited(InputStream reader) throws IOException {
        ByteArrayOutputStream line = new ByteArrayOutputStream();
        int value;
        while ((value = reader.read()) >= 0) {
            if (value == '\n') {
                return line.toByteArray();
            }
            if (line.size() >= MAX_MESSAGE_BYTES) {
                throw new IOException("MCP message exceeds the maximum allowed size");
            }
            line.write(value);
        }
        return line.size() == 0 ? null : line.toByteArray();
    }

    private ObjectNode handle(JsonNode request) {
        String method = text(request, "method");
        JsonNode id = request.get("id");
        boolean hasId = request.has("id");
        try {
            if (!"2.0".equals(text(request, "jsonrpc"))) {
                return hasId ? error(id, -32600, "jsonrpc must be '2.0'") : null;
            }
            if (method == null) {
                return hasId ? error(id, -32600, "method is required") : null;
            }
            if ("initialize".equals(method)) {
                return hasId ? result(id, initialize(request)) : null;
            }
            if ("notifications/initialized".equals(method) || "initialized".equals(method)) {
                return null;
            }
            if ("ping".equals(method)) {
                return hasId ? result(id, mapper.createObjectNode()) : null;
            }
            if ("tools/list".equals(method)) {
                return hasId ? result(id, toolsList()) : null;
            }
            if ("tools/call".equals(method)) {
                JsonNode params = request.get("params");
                if (params == null || !params.isObject() || !params.has("name")) {
                    return hasId ? error(id, -32602, "tools/call requires params.name") : null;
                }
                String name = text(params, "name");
                if (name == null) {
                    return hasId ? error(id, -32602, "tools/call params.name must be a string") : null;
                }
                SshService.ToolResult toolResult = ssh.call(name, params.get("arguments"));
                ObjectNode payload = mapper.createObjectNode();
                ArrayNode content = payload.putArray("content");
                ObjectNode contentItem = content.addObject();
                contentItem.put("type", "text");
                contentItem.put("text", mapper.writeValueAsString(toolResult.payload()));
                payload.put("isError", toolResult.isError());
                return hasId ? result(id, payload) : null;
            }
            if (!hasId) {
                return null;
            }
            return error(id, -32601, "Method not found");
        }
        catch (Exception ex) {
            AuditLog.failure("mcp_method=" + method, ex);
            if (id == null) {
                return null;
            }
            return error(id, -32603, "Internal error");
        }
    }

    private ObjectNode initialize(JsonNode request) {
        JsonNode params = request.get("params");
        String requested = params == null ? null : text(params, "protocolVersion");
        String selected = requested != null && SUPPORTED_PROTOCOLS.contains(requested)
                ? requested : DEFAULT_PROTOCOL_VERSION;
        ObjectNode result = mapper.createObjectNode();
        result.put("protocolVersion", selected);
        ObjectNode capabilities = result.putObject("capabilities");
        capabilities.putObject("tools").put("listChanged", false);
        ObjectNode serverInfo = result.putObject("serverInfo");
        serverInfo.put("name", SERVER_NAME);
        serverInfo.put("version", SERVER_VERSION);
        result.put("instructions", "Use serverId from ssh_list_servers. SSH credentials are loaded from the MCP process environment and are never accepted as tool arguments or returned by the server. Host keys are verified against the configured known_hosts file.");
        return result;
    }

    private ObjectNode toolsList() {
        ObjectNode result = mapper.createObjectNode();
        ArrayNode tools = result.putArray("tools");
        tools.add(tool("ssh_list_servers", "List configured SSH targets and non-secret capabilities", objectSchema(
                mapper.createObjectNode())));
        tools.add(tool("ssh_exec", "Execute a command on a configured SSH target", schema(
                property("serverId", "string", "Configured target id", true),
                property("command", "string", "Remote command to execute", true),
                property("timeoutMs", "integer", "Optional timeout, bounded by target policy", false),
                property("maxOutputBytes", "integer", "Optional per-stream output cap", false))));
        tools.add(tool("sftp_list", "List a remote directory", schema(
                property("serverId", "string", "Configured target id", true),
                property("path", "string", "Remote directory path", true),
                property("maxEntries", "integer", "Maximum entries to return", false))));
        tools.add(tool("sftp_stat", "Read remote file or directory metadata", schema(
                property("serverId", "string", "Configured target id", true),
                property("path", "string", "Remote path", true))));
        tools.add(tool("sftp_read_file", "Read a remote file with an explicit byte limit", schema(
                property("serverId", "string", "Configured target id", true),
                property("path", "string", "Remote file path", true),
                property("maxBytes", "integer", "Maximum bytes to read", false),
                property("encoding", "string", "utf8 or base64", false))));
        tools.add(tool("sftp_write_file", "Write a remote file; provide content or contentBase64", schema(
                property("serverId", "string", "Configured target id", true),
                property("path", "string", "Remote file path", true),
                property("content", "string", "UTF-8 content", false),
                property("contentBase64", "string", "Binary content encoded as base64", false))));
        tools.add(tool("sftp_mkdir", "Create a remote directory", schema(
                property("serverId", "string", "Configured target id", true),
                property("path", "string", "Remote directory path", true))));
        tools.add(tool("sftp_delete", "Delete one remote file or an empty directory", schema(
                property("serverId", "string", "Configured target id", true),
                property("path", "string", "Remote path", true),
                property("directory", "boolean", "Set true only for an empty directory", false))));
        tools.add(tool("ssh_tunnel_open", "Open a loopback-only local TCP forward", schema(
                property("serverId", "string", "Configured target id", true),
                property("localHost", "string", "Loopback bind address; defaults to 127.0.0.1", false),
                property("localPort", "integer", "Local port, 0 chooses an available port", false),
                property("remoteHost", "string", "Remote host visible from the SSH target", true),
                property("remotePort", "integer", "Remote TCP port", true))));
        tools.add(tool("ssh_tunnel_close", "Close an SSH local port forward", schema(
                property("tunnelId", "string", "Tunnel id returned by ssh_tunnel_open", true))));
        tools.add(tool("ssh_tunnel_list", "List active SSH tunnels without credentials", objectSchema(
                mapper.createObjectNode())));
        return result;
    }

    private ObjectNode tool(String name, String description, ObjectNode inputSchema) {
        ObjectNode result = mapper.createObjectNode();
        result.put("name", name);
        result.put("description", description);
        result.set("inputSchema", inputSchema);
        return result;
    }

    private ObjectNode schema(ObjectNode... properties) {
        ObjectNode result = mapper.createObjectNode();
        result.put("type", "object");
        ObjectNode propertyMap = result.putObject("properties");
        ArrayNode required = result.putArray("required");
        for (ObjectNode property : properties) {
            String name = property.remove("__name").asText();
            boolean isRequired = property.remove("__required").asBoolean();
            propertyMap.set(name, property);
            if (isRequired) {
                required.add(name);
            }
        }
        result.put("additionalProperties", false);
        return result;
    }

    private ObjectNode property(String name, String type, String description, boolean required) {
        ObjectNode result = mapper.createObjectNode();
        result.put("__name", name);
        result.put("__required", required);
        result.put("type", type);
        result.put("description", description);
        return result;
    }

    private ObjectNode objectSchema(ObjectNode ignored) {
        ObjectNode result = mapper.createObjectNode();
        result.put("type", "object");
        result.putObject("properties");
        result.putArray("required");
        result.put("additionalProperties", false);
        return result;
    }

    private ObjectNode result(JsonNode id, ObjectNode result) {
        ObjectNode response = mapper.createObjectNode();
        response.put("jsonrpc", "2.0");
        response.set("id", id == null ? mapper.nullNode() : id);
        response.set("result", result);
        return response;
    }

    private ObjectNode error(JsonNode id, int code, String message) {
        ObjectNode response = mapper.createObjectNode();
        response.put("jsonrpc", "2.0");
        response.set("id", id == null ? mapper.nullNode() : id);
        ObjectNode error = response.putObject("error");
        error.put("code", code);
        error.put("message", message);
        return response;
    }

    private static String text(JsonNode object, String field) {
        JsonNode value = object == null ? null : object.get(field);
        return value != null && value.isTextual() ? value.asText() : null;
    }
}
