package com.example.sshmcp;

import com.fasterxml.jackson.core.JsonParser;
import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ArrayNode;
import com.fasterxml.jackson.databind.node.ObjectNode;

import java.io.BufferedInputStream;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.io.OutputStreamWriter;
import java.io.Writer;
import java.nio.ByteBuffer;
import java.nio.charset.CharacterCodingException;
import java.nio.charset.CodingErrorAction;
import java.nio.charset.StandardCharsets;
import java.util.Arrays;
import java.util.List;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.Future;
import java.util.concurrent.FutureTask;
import java.util.concurrent.RejectedExecutionException;
import java.util.concurrent.SynchronousQueue;
import java.util.concurrent.ThreadPoolExecutor;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;

/**
 * A minimal MCP server over newline-delimited JSON-RPC on stdio. Tool calls run concurrently on a
 * bounded worker pool, so a slow command never blocks ping or calls to other servers, and
 * notifications/cancelled interrupts the matching call.
 */
final class McpStdioServer {
    private static final int MAX_MESSAGE_BYTES = 128 * 1024 * 1024;
    private static final int MAX_CONCURRENT_CALLS = 64;
    private static final long SHUTDOWN_WAIT_SECONDS = 10;
    private static final String SERVER_NAME = "ssh-mcp-server";
    private static final String SERVER_TITLE = "SSH MCP Server";
    /** Newest first; the first entry is offered when the client asks for an unsupported version. */
    private static final List<String> SUPPORTED_PROTOCOLS = Arrays.asList(
            "2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05");
    private static final String OVERSIZED = "MCP message exceeds the maximum allowed size";

    private final ObjectMapper mapper;
    private final SshService ssh;
    private final String version;
    private final Map<String, Future<?>> inFlight = new ConcurrentHashMap<>();
    private final ThreadPoolExecutor workers;
    private Writer writer;

    McpStdioServer(ObjectMapper mapper, SshService ssh, String version) {
        this.mapper = mapper;
        this.ssh = ssh;
        this.version = version;
        AtomicInteger threadNumber = new AtomicInteger();
        this.workers = new ThreadPoolExecutor(0, MAX_CONCURRENT_CALLS, 60L, TimeUnit.SECONDS, new SynchronousQueue<>(), runnable -> {
            Thread thread = new Thread(runnable, "ssh-mcp-call-" + threadNumber.incrementAndGet());
            thread.setDaemon(true);
            return thread;
        });
    }

    /** Serves requests until input ends, then waits briefly for in-flight calls to answer. */
    void run(InputStream input, OutputStream output) throws IOException {
        BufferedInputStream reader = new BufferedInputStream(input, 64 * 1024);
        writer = new OutputStreamWriter(output, StandardCharsets.UTF_8);
        try {
            byte[] frame;
            while (true) {
                try {
                    frame = readLineLimited(reader);
                }
                catch (IOException ex) {
                    if (!OVERSIZED.equals(ex.getMessage())) {
                        throw ex;
                    }
                    AuditLog.failure("mcp_frame", ex);
                    send(error(null, -32600, OVERSIZED));
                    return;
                }
                if (frame == null) {
                    return;
                }
                handleFrame(frame);
            }
        }
        finally {
            workers.shutdown();
            try {
                if (!workers.awaitTermination(SHUTDOWN_WAIT_SECONDS, TimeUnit.SECONDS)) {
                    workers.shutdownNow();
                }
            }
            catch (InterruptedException ex) {
                workers.shutdownNow();
                Thread.currentThread().interrupt();
            }
        }
    }

    private void handleFrame(byte[] frame) throws IOException {
        String line;
        try {
            line = StandardCharsets.UTF_8.newDecoder()
                    .onMalformedInput(CodingErrorAction.REPORT)
                    .onUnmappableCharacter(CodingErrorAction.REPORT)
                    .decode(ByteBuffer.wrap(frame)).toString();
        }
        catch (CharacterCodingException ex) {
            AuditLog.failure("mcp_request", ex);
            send(error(null, -32700, "Invalid UTF-8 in JSON-RPC message"));
            return;
        }
        if (line.trim().isEmpty()) {
            return;
        }
        JsonNode request;
        try (JsonParser parser = mapper.getFactory().createParser(line)) {
            request = mapper.readTree(parser);
            if (parser.nextToken() != null) {
                throw new IOException("Trailing JSON-RPC data");
            }
        }
        catch (Exception ex) {
            AuditLog.failure("mcp_request", ex);
            send(error(null, -32700, "Parse error: invalid JSON"));
            return;
        }
        if (request == null || !request.isObject()) {
            send(error(null, -32600, "Invalid Request: expected a JSON object (batches are not supported)"));
            return;
        }
        if (!request.has("method")) {
            // A response from the client (we never send requests) or a malformed message.
            if (!request.has("result") && !request.has("error")) {
                send(error(request.get("id"), -32600, "Invalid Request: method is required"));
            }
            return;
        }
        ObjectNode response = handle(request);
        if (response != null) {
            send(response);
        }
    }

    private ObjectNode handle(JsonNode request) {
        String method = text(request, "method");
        JsonNode id = request.get("id");
        boolean hasId = id != null && !id.isNull();
        try {
            if (!"2.0".equals(text(request, "jsonrpc"))) {
                return hasId ? error(id, -32600, "jsonrpc must be \"2.0\"") : null;
            }
            if (method == null) {
                return hasId ? error(id, -32600, "method must be a string") : null;
            }
            switch (method) {
                case "initialize":
                    return hasId ? result(id, initialize(request.get("params"))) : null;
                case "notifications/initialized":
                case "initialized":
                    return null;
                case "notifications/cancelled":
                    cancel(request.get("params"));
                    return null;
                case "ping":
                    return hasId ? result(id, mapper.createObjectNode()) : null;
                case "tools/list": {
                    ObjectNode listing = mapper.createObjectNode();
                    listing.set("tools", ToolSpecs.toolsList());
                    return hasId ? result(id, listing) : null;
                }
                case "tools/call":
                    return hasId ? startToolCall(id, request.get("params")) : null;
                default:
                    return hasId ? error(id, -32601, "Method not found: " + method) : null;
            }
        }
        catch (Exception ex) {
            AuditLog.failure("mcp_method=" + method, ex);
            return hasId ? error(id, -32603, "Internal error") : null;
        }
    }

    /** Runs the call on a worker and returns null; the worker sends the response itself. */
    private ObjectNode startToolCall(JsonNode id, JsonNode params) {
        if (params == null || !params.isObject() || text(params, "name") == null) {
            return error(id, -32602, "tools/call requires params.name (a string)");
        }
        String name = text(params, "name");
        JsonNode arguments = params.get("arguments");
        String key = id.toString();
        FutureTask<Void> task = new FutureTask<>(() -> {
            ObjectNode response = result(id, toolPayload(ssh.call(name, arguments)));
            // A cancelled call has already been removed; per MCP it gets no response.
            if (inFlight.remove(key) != null) {
                send(response);
            }
            return null;
        });
        if (inFlight.putIfAbsent(key, task) != null) {
            return error(id, -32600, "A request with this id is already in progress");
        }
        try {
            workers.execute(task);
        }
        catch (RejectedExecutionException ex) {
            inFlight.remove(key);
            return error(id, -32000, "Too many concurrent tool calls; retry shortly");
        }
        return null;
    }

    private void cancel(JsonNode params) {
        JsonNode requestId = params == null ? null : params.get("requestId");
        if (requestId != null) {
            Future<?> call = inFlight.remove(requestId.toString());
            if (call != null) {
                call.cancel(true);
            }
        }
    }

    private ObjectNode toolPayload(SshService.ToolResult toolResult) throws IOException {
        ObjectNode payload = mapper.createObjectNode();
        ArrayNode content = payload.putArray("content");
        ObjectNode text = content.addObject();
        text.put("type", "text");
        text.put("text", mapper.writeValueAsString(toolResult.payload()));
        payload.set("structuredContent", toolResult.payload());
        payload.put("isError", toolResult.isError());
        return payload;
    }

    private ObjectNode initialize(JsonNode params) {
        String requested = params == null ? null : text(params, "protocolVersion");
        String selected = requested != null && SUPPORTED_PROTOCOLS.contains(requested) ? requested : SUPPORTED_PROTOCOLS.get(0);
        ObjectNode result = mapper.createObjectNode();
        result.put("protocolVersion", selected);
        result.putObject("capabilities").putObject("tools").put("listChanged", false);
        ObjectNode serverInfo = result.putObject("serverInfo");
        serverInfo.put("name", SERVER_NAME);
        serverInfo.put("title", SERVER_TITLE);
        serverInfo.put("version", version);
        result.put("instructions", ToolSpecs.INSTRUCTIONS);
        return result;
    }

    /** Writes one message per line. Always "\n": MCP stdio framing must not depend on the platform. */
    private void send(ObjectNode message) throws IOException {
        String encoded = mapper.writeValueAsString(message);
        synchronized (this) {
            writer.write(encoded);
            writer.write('\n');
            writer.flush();
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
                throw new IOException(OVERSIZED);
            }
            line.write(value);
        }
        return line.size() == 0 ? null : line.toByteArray();
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
