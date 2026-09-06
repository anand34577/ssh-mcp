package com.example.sshmcp;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.core.JsonParser;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.Collections;
import java.util.HashSet;
import java.util.Iterator;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.concurrent.Semaphore;
import java.util.regex.Pattern;
import java.util.regex.PatternSyntaxException;

final class Config {
    private static final String AUTO_ACCEPT_HOST_KEYS_ENV = "SSH_MCP_AUTO_ACCEPT_HOST_KEYS";
    private static final Set<String> ALLOWED_ROOT_FIELDS = new HashSet<String>(Collections.singleton("servers"));
    private static final Set<String> ALLOWED_SERVER_FIELDS = new HashSet<String>(Arrays.asList(
            "id", "host", "port", "username", "passwordEnv", "privateKeyPath",
            "privateKeyPassphraseEnv", "knownHostsPath", "strictHostKeyChecking",
            "connectTimeoutMs", "commandTimeoutMs", "maxOutputBytes",
            "maxConcurrentOperations", "maxConcurrentTunnels", "allowSftp", "allowPortForwarding",
            "allowedCommandPrefixes", "deniedCommandRegexes"));

    private Config() {
    }

    static boolean autoAcceptHostKeys() {
        return "true".equalsIgnoreCase(System.getenv(AUTO_ACCEPT_HOST_KEYS_ENV));
    }

    static ServerCatalog load(ObjectMapper mapper) throws IOException {
        String configuredPath = System.getProperty("ssh.mcp.config");
        if (configuredPath == null || configuredPath.trim().isEmpty()) {
            configuredPath = System.getenv("SSH_MCP_CONFIG");
        }
        if (configuredPath == null || configuredPath.trim().isEmpty()) {
            configuredPath = "config/servers.json";
        }

        Path path = Paths.get(expand(configuredPath)).toAbsolutePath().normalize();
        if (!Files.isRegularFile(path)) {
            throw new IOException("Configuration file not found: " + path
                    + ". Copy config/servers.example.json to config/servers.json and configure it.");
        }

        JsonNode root;
        try (java.io.Reader reader = Files.newBufferedReader(path);
                JsonParser parser = mapper.getFactory().createParser(reader)) {
            root = mapper.readTree(parser);
            if (parser.nextToken() != null) {
                throw new IOException("Configuration contains trailing JSON data");
            }
        }
        if (root == null || !root.isObject() || !root.has("servers") || !root.get("servers").isArray()) {
            throw new IOException("Configuration must contain a JSON array named 'servers'");
        }
        Iterator<String> rootFields = root.fieldNames();
        while (rootFields.hasNext()) {
            String field = rootFields.next();
            if (!ALLOWED_ROOT_FIELDS.contains(field)) {
                throw new IOException("Unknown configuration field: " + field);
            }
        }

        Map<String, ServerConfig> servers = new LinkedHashMap<String, ServerConfig>();
        for (JsonNode node : root.get("servers")) {
            ServerConfig server = ServerConfig.from(node);
            if (servers.put(server.id, server) != null) {
                throw new IOException("Duplicate server id: " + server.id);
            }
        }
        if (servers.isEmpty()) {
            throw new IOException("Configuration must contain at least one SSH server");
        }
        return new ServerCatalog(path, servers);
    }

    static String expand(String value) {
        if (value == null) {
            return null;
        }
        String result = value.replace("${user.home}", System.getProperty("user.home", ""));
        int start;
        while ((start = result.indexOf("${")) >= 0) {
            int end = result.indexOf('}', start + 2);
            if (end < 0) {
                break;
            }
            String variable = result.substring(start + 2, end);
            String replacement = System.getenv(variable);
            if (replacement == null) {
                replacement = System.getProperty(variable, "");
            }
            result = result.substring(0, start) + replacement + result.substring(end + 1);
        }
        if (result.startsWith("~/")) {
            result = Paths.get(System.getProperty("user.home", ""), result.substring(2)).toString();
        }
        return result;
    }

    static final class ServerCatalog {
        private final Path source;
        private final Map<String, ServerConfig> servers;

        ServerCatalog(Path source, Map<String, ServerConfig> servers) {
            this.source = source;
            this.servers = Collections.unmodifiableMap(new LinkedHashMap<String, ServerConfig>(servers));
        }

        Path source() {
            return source;
        }

        Map<String, ServerConfig> servers() {
            return servers;
        }

        ServerConfig get(String id) {
            return servers.get(id);
        }
    }

    static final class ServerConfig {
        private final String id;
        private final String host;
        private final int port;
        private final String username;
        private final String passwordEnv;
        private final String privateKeyPath;
        private final String privateKeyPassphraseEnv;
        private final Path knownHostsPath;
        private final boolean strictHostKeyChecking;
        private final long connectTimeoutMs;
        private final long commandTimeoutMs;
        private final int maxOutputBytes;
        private final int maxConcurrentOperations;
        private final int maxConcurrentTunnels;
        private final Semaphore operationSlots;
        private final Semaphore tunnelSlots;
        private final boolean allowSftp;
        private final boolean allowPortForwarding;
        private final List<String> allowedCommandPrefixes;
        private final List<Pattern> deniedCommandPatterns;

        private ServerConfig(String id, String host, int port, String username, String passwordEnv,
                String privateKeyPath, String privateKeyPassphraseEnv, Path knownHostsPath,
                boolean strictHostKeyChecking, long connectTimeoutMs, long commandTimeoutMs,
                int maxOutputBytes, int maxConcurrentOperations, int maxConcurrentTunnels, boolean allowSftp,
                boolean allowPortForwarding, List<String> allowedCommandPrefixes,
                List<Pattern> deniedCommandPatterns) {
            this.id = id;
            this.host = host;
            this.port = port;
            this.username = username;
            this.passwordEnv = passwordEnv;
            this.privateKeyPath = privateKeyPath;
            this.privateKeyPassphraseEnv = privateKeyPassphraseEnv;
            this.knownHostsPath = knownHostsPath;
            this.strictHostKeyChecking = strictHostKeyChecking;
            this.connectTimeoutMs = connectTimeoutMs;
            this.commandTimeoutMs = commandTimeoutMs;
            this.maxOutputBytes = maxOutputBytes;
            this.maxConcurrentOperations = maxConcurrentOperations;
            this.maxConcurrentTunnels = maxConcurrentTunnels;
            this.operationSlots = new Semaphore(maxConcurrentOperations, true);
            this.tunnelSlots = new Semaphore(maxConcurrentTunnels, true);
            this.allowSftp = allowSftp;
            this.allowPortForwarding = allowPortForwarding;
            this.allowedCommandPrefixes = Collections.unmodifiableList(allowedCommandPrefixes);
            this.deniedCommandPatterns = Collections.unmodifiableList(deniedCommandPatterns);
        }

        static ServerConfig from(JsonNode node) throws IOException {
            if (node == null || !node.isObject()) {
                throw new IOException("Each server entry must be a JSON object");
            }
            validateFields(node);
            String id = required(node, "id");
            if (!id.matches("[A-Za-z0-9._-]{1,64}")) {
                throw new IOException("Server id must contain only letters, numbers, '.', '_' or '-': " + id);
            }
            String host = required(node, "host");
            String username = required(node, "username");
            int port = integer(node, "port", 22, 1, 65535);
            String passwordEnv = optional(node, "passwordEnv");
            String privateKeyPath = optional(node, "privateKeyPath");
            String privateKeyPassphraseEnv = optional(node, "privateKeyPassphraseEnv");
            if (passwordEnv == null && privateKeyPath == null) {
                throw new IOException("Server '" + id + "' must configure passwordEnv or privateKeyPath");
            }
            if (passwordEnv != null && !passwordEnv.matches("[A-Za-z_][A-Za-z0-9_]{0,127}")) {
                throw new IOException("Server '" + id + "' has an invalid passwordEnv name");
            }
            if (privateKeyPassphraseEnv != null
                    && !privateKeyPassphraseEnv.matches("[A-Za-z_][A-Za-z0-9_]{0,127}")) {
                throw new IOException("Server '" + id + "' has an invalid privateKeyPassphraseEnv name");
            }

            boolean strict = bool(node, "strictHostKeyChecking", true);
            String knownHosts = optional(node, "knownHostsPath");
            boolean autoAcceptHostKeys = autoAcceptHostKeys();
            if (strict && knownHosts == null && !autoAcceptHostKeys) {
                throw new IOException("Server '" + id + "' must configure knownHostsPath when strictHostKeyChecking is true");
            }
            if (!strict && !"true".equalsIgnoreCase(System.getenv("SSH_MCP_ALLOW_INSECURE_HOST_KEYS")) && !autoAcceptHostKeys) {
                throw new IOException("Server '" + id + "' disables host-key checking. Set SSH_MCP_ALLOW_INSECURE_HOST_KEYS=true only for an explicit exception.");
            }
            Path knownHostsPath = knownHosts == null ? null : Paths.get(expand(knownHosts)).toAbsolutePath().normalize();
            if (strict && !autoAcceptHostKeys && !Files.isRegularFile(knownHostsPath)) {
                throw new IOException("Server '" + id + "' knownHostsPath is not a regular file: " + knownHostsPath);
            }

            long connectTimeout = longValue(node, "connectTimeoutMs", 10000L, 1000L, 120000L);
            long commandTimeout = longValue(node, "commandTimeoutMs", 60000L, 100L, 3600000L);
            int maxOutput = integer(node, "maxOutputBytes", 1048576, 1024, 104857600);
            int maxConcurrent = integer(node, "maxConcurrentOperations", 4, 1, 64);
            int maxConcurrentTunnels = integer(node, "maxConcurrentTunnels", 4, 1, 64);
            boolean allowSftp = bool(node, "allowSftp", true);
            boolean allowForwarding = bool(node, "allowPortForwarding", false);

            List<String> prefixes = strings(node, "allowedCommandPrefixes");
            List<Pattern> denied = new ArrayList<Pattern>();
            for (String regex : strings(node, "deniedCommandRegexes")) {
                try {
                    denied.add(Pattern.compile(regex));
                }
                catch (PatternSyntaxException ex) {
                    throw new IOException("Server '" + id + "' has an invalid denied command regex", ex);
                }
            }

            for (int i = 0; i < prefixes.size(); i++) {
                String prefix = prefixes.get(i).trim();
                if (prefix.isEmpty()) {
                    throw new IOException("Server '" + id + "' has an empty allowed command prefix");
                }
                prefixes.set(i, prefix);
            }

            return new ServerConfig(id, host, port, username, passwordEnv, privateKeyPath,
                    privateKeyPassphraseEnv, knownHostsPath, strict, connectTimeout, commandTimeout,
                    maxOutput, maxConcurrent, maxConcurrentTunnels, allowSftp, allowForwarding, prefixes, denied);
        }

        private static void validateFields(JsonNode node) throws IOException {
            String[] forbidden = { "password", "passphrase", "privateKey", "privateKeyContent", "secret" };
            Iterator<String> fields = node.fieldNames();
            while (fields.hasNext()) {
                String field = fields.next();
                for (String forbiddenField : forbidden) {
                    if (field.equalsIgnoreCase(forbiddenField)) {
                        throw new IOException("Secrets must not be stored directly in configuration; use an environment variable reference for '" + field + "'");
                    }
                }
                if (!ALLOWED_SERVER_FIELDS.contains(field)) {
                    throw new IOException("Unknown server field: " + field);
                }
            }
        }

        private static String required(JsonNode node, String name) throws IOException {
            String value = optional(node, name);
            if (value == null) {
                throw new IOException("Missing required server field: " + name);
            }
            return value;
        }

        private static String optional(JsonNode node, String name) throws IOException {
            JsonNode value = node.get(name);
            if (value == null || value.isNull()) {
                return null;
            }
            if (!value.isTextual() || value.asText().trim().isEmpty()) {
                throw new IOException("Server field '" + name + "' must be a non-empty string");
            }
            return value.asText().trim();
        }

        private static boolean bool(JsonNode node, String name, boolean defaultValue) throws IOException {
            JsonNode value = node.get(name);
            if (value == null || value.isNull()) {
                return defaultValue;
            }
            if (!value.isBoolean()) {
                throw new IOException("Server field '" + name + "' must be boolean");
            }
            return value.asBoolean();
        }

        private static int integer(JsonNode node, String name, int defaultValue, int min, int max) throws IOException {
            JsonNode value = node.get(name);
            if (value == null || value.isNull()) {
                return defaultValue;
            }
            if (!value.isIntegralNumber() || !value.canConvertToInt()) {
                throw new IOException("Server field '" + name + "' must be an integer");
            }
            int result = value.asInt();
            if (result < min || result > max) {
                throw new IOException("Server field '" + name + "' must be between " + min + " and " + max);
            }
            return result;
        }

        private static long longValue(JsonNode node, String name, long defaultValue, long min, long max) throws IOException {
            JsonNode value = node.get(name);
            if (value == null || value.isNull()) {
                return defaultValue;
            }
            if (!value.isIntegralNumber()) {
                throw new IOException("Server field '" + name + "' must be an integer");
            }
            long result = value.asLong();
            if (result < min || result > max) {
                throw new IOException("Server field '" + name + "' must be between " + min + " and " + max);
            }
            return result;
        }

        private static List<String> strings(JsonNode node, String name) throws IOException {
            JsonNode value = node.get(name);
            if (value == null || value.isNull()) {
                return new ArrayList<String>();
            }
            if (!value.isArray()) {
                throw new IOException("Server field '" + name + "' must be an array of strings");
            }
            List<String> result = new ArrayList<String>();
            for (JsonNode item : value) {
                if (!item.isTextual() || item.asText().trim().isEmpty()) {
                    throw new IOException("Server field '" + name + "' must contain non-empty strings");
                }
                result.add(item.asText().trim());
            }
            return result;
        }

        String id() { return id; }
        String host() { return host; }
        int port() { return port; }
        String username() { return username; }
        String passwordEnv() { return passwordEnv; }
        String privateKeyPath() { return privateKeyPath == null ? null : expand(privateKeyPath); }
        String privateKeyPassphraseEnv() { return privateKeyPassphraseEnv; }
        Path knownHostsPath() { return knownHostsPath; }
        boolean strictHostKeyChecking() { return strictHostKeyChecking; }
        long connectTimeoutMs() { return connectTimeoutMs; }
        long commandTimeoutMs() { return commandTimeoutMs; }
        int maxOutputBytes() { return maxOutputBytes; }
        Semaphore operationSlots() { return operationSlots; }
        Semaphore tunnelSlots() { return tunnelSlots; }
        int maxConcurrentOperations() { return maxConcurrentOperations; }
        int maxConcurrentTunnels() { return maxConcurrentTunnels; }
        boolean allowSftp() { return allowSftp; }
        boolean allowPortForwarding() { return allowPortForwarding; }
        List<String> allowedCommandPrefixes() { return allowedCommandPrefixes; }
        List<Pattern> deniedCommandPatterns() { return deniedCommandPatterns; }
    }
}
