package com.example.sshmcp;

import com.fasterxml.jackson.core.JsonParser;
import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;

import java.io.IOException;
import java.io.Reader;
import java.net.URISyntaxException;
import java.nio.file.Files;
import java.nio.file.InvalidPathException;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.Collections;
import java.util.HashSet;
import java.util.Iterator;
import java.util.LinkedHashMap;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Set;
import java.util.concurrent.Semaphore;
import java.util.regex.Pattern;
import java.util.regex.PatternSyntaxException;

/**
 * Loads and validates the SSH MCP configuration file. Validation is strict: unknown fields, inline
 * secrets, and out-of-range limits are rejected at startup so a typo can never silently fall back
 * to an insecure default.
 */
final class Config {
    static final String ENV_CONFIG_PATH = "SSH_MCP_CONFIG";
    static final String ENV_ALLOW_INSECURE_HOST_KEYS = "SSH_MCP_ALLOW_INSECURE_HOST_KEYS";
    static final String ENV_AUTO_ACCEPT_HOST_KEYS = "SSH_MCP_AUTO_ACCEPT_HOST_KEYS";
    static final String CONFIG_PROPERTY = "ssh.mcp.config";
    /** Rejected in commands (and allow-list prefixes) when an allow-list is configured. */
    static final String SHELL_METACHARACTERS = ";|&<>$`(){}'\"\\";

    // "$schema" lets editors validate against config/servers.schema.json; it has no runtime effect.
    private static final Set<String> ALLOWED_ROOT_FIELDS = new HashSet<>(Arrays.asList("$schema", "servers"));
    private static final Set<String> ALLOWED_SERVER_FIELDS = new HashSet<>(Arrays.asList(
            "id", "host", "port", "username", "passwordEnv", "privateKeyPath",
            "privateKeyPassphraseEnv", "knownHostsPath", "strictHostKeyChecking",
            "connectTimeoutMs", "commandTimeoutMs", "maxOutputBytes",
            "maxConcurrentOperations", "maxConcurrentTunnels", "allowSftp", "allowPortForwarding",
            "allowedCommandPrefixes", "deniedCommandRegexes", "protectSystem"));
    private static final List<String> FORBIDDEN_SECRET_FIELDS = Arrays.asList(
            "password", "passphrase", "privateKey", "privateKeyContent", "secret");
    private static final Pattern SERVER_ID = Pattern.compile("[A-Za-z0-9._-]{1,64}");
    private static final Pattern ENV_NAME = Pattern.compile("[A-Za-z_][A-Za-z0-9_]{0,127}");

    private Config() {
    }

    static boolean autoAcceptHostKeys() {
        return envTrue(ENV_AUTO_ACCEPT_HOST_KEYS);
    }

    static boolean allowInsecureHostKeys() {
        return envTrue(ENV_ALLOW_INSECURE_HOST_KEYS);
    }

    private static boolean envTrue(String name) {
        String value = System.getenv(name);
        return value != null && "true".equalsIgnoreCase(value.trim());
    }

    /**
     * Loads the configuration. Precedence: the explicit path (--config), the ssh.mcp.config system
     * property, SSH_MCP_CONFIG, then the first existing default location.
     */
    static ServerCatalog load(ObjectMapper mapper, String explicitPath) throws IOException {
        String configured = firstNonBlank(explicitPath, System.getProperty(CONFIG_PROPERTY), System.getenv(ENV_CONFIG_PATH));
        Path path;
        if (configured != null) {
            path = toPath(expand(configured));
            if (!Files.isRegularFile(path)) {
                throw new IOException("Configuration file not found: " + path);
            }
        }
        else {
            List<Path> searched = defaultLocations();
            path = searched.stream().filter(Files::isRegularFile).findFirst().orElse(null);
            if (path == null) {
                throw new IOException("No configuration file found; searched " + joinPaths(searched)
                        + ". Pass --config <path>, set " + ENV_CONFIG_PATH
                        + ", or copy config/servers.example.json to one of those locations");
            }
        }
        try (Reader reader = Files.newBufferedReader(path)) {
            return parse(mapper, reader, path);
        }
        catch (IOException ex) {
            throw new IOException(path + ": " + ex.getMessage(), ex);
        }
    }

    static ServerCatalog parse(ObjectMapper mapper, Reader reader, Path source) throws IOException {
        JsonNode root;
        try (JsonParser parser = mapper.getFactory().createParser(reader)) {
            root = mapper.readTree(parser);
            if (parser.nextToken() != null) {
                throw new IOException("Configuration contains trailing JSON data");
            }
        }
        if (root == null || !root.isObject()) {
            throw new IOException("Configuration must be a JSON object");
        }
        Iterator<String> rootFields = root.fieldNames();
        while (rootFields.hasNext()) {
            String field = rootFields.next();
            if (!ALLOWED_ROOT_FIELDS.contains(field)) {
                throw new IOException("Configuration contains unknown field \"" + field + "\"");
            }
        }
        JsonNode serverNodes = root.get("servers");
        if (serverNodes == null || !serverNodes.isArray() || serverNodes.size() == 0) {
            throw new IOException("Configuration must contain at least one server in \"servers\"");
        }

        Map<String, ServerConfig> servers = new LinkedHashMap<>();
        for (int index = 0; index < serverNodes.size(); index++) {
            try {
                ServerConfig server = ServerConfig.from(serverNodes.get(index));
                if (servers.put(server.id, server) != null) {
                    throw new IOException("duplicate server id \"" + server.id + "\"");
                }
            }
            catch (IOException ex) {
                throw new IOException("servers[" + index + "]: " + ex.getMessage(), ex);
            }
        }
        return new ServerCatalog(source, servers);
    }

    /** Where servers.json is looked for when no path is configured, in priority order. */
    static List<Path> defaultLocations() {
        Set<Path> locations = new LinkedHashSet<>();
        locations.add(Paths.get("config", "servers.json").toAbsolutePath().normalize());
        Path appDirectory = applicationDirectory();
        if (appDirectory != null) {
            locations.add(appDirectory.resolve("servers.json"));
            locations.add(appDirectory.resolve("config").resolve("servers.json"));
            // Packaged layouts put the jar in app/ next to bin/ and runtime/.
            if (appDirectory.getParent() != null) {
                locations.add(appDirectory.getParent().resolve("config").resolve("servers.json"));
            }
        }
        Path userConfig = userConfigDirectory();
        if (userConfig != null) {
            locations.add(userConfig.resolve("ssh-mcp").resolve("servers.json"));
        }
        return new ArrayList<>(locations);
    }

    private static Path applicationDirectory() {
        try {
            Path location = Paths.get(Config.class.getProtectionDomain().getCodeSource().getLocation().toURI());
            return (Files.isRegularFile(location) ? location.getParent() : location).toAbsolutePath().normalize();
        }
        catch (URISyntaxException | RuntimeException ex) {
            return null;
        }
    }

    /** Mirrors Go's os.UserConfigDir so both implementations search the same place. */
    private static Path userConfigDirectory() {
        String os = System.getProperty("os.name", "").toLowerCase(Locale.ROOT);
        String home = System.getProperty("user.home");
        try {
            if (os.contains("win")) {
                String appData = System.getenv("APPDATA");
                return appData == null || appData.isEmpty() ? null : Paths.get(appData);
            }
            if (os.contains("mac")) {
                return home == null ? null : Paths.get(home, "Library", "Application Support");
            }
            String xdg = System.getenv("XDG_CONFIG_HOME");
            if (xdg != null && !xdg.isEmpty()) {
                return Paths.get(xdg);
            }
            return home == null ? null : Paths.get(home, ".config");
        }
        catch (InvalidPathException ex) {
            return null;
        }
    }

    /** Resolves ${user.home}, ${ENV_VAR} (or system property), and a leading ~/ or ~\ in a path. */
    static String expand(String value) {
        if (value == null) {
            return null;
        }
        String home = System.getProperty("user.home", "");
        StringBuilder result = new StringBuilder();
        int position = 0;
        while (position < value.length()) {
            int start = value.indexOf("${", position);
            int end = start < 0 ? -1 : value.indexOf('}', start + 2);
            if (end < 0) {
                result.append(value, position, value.length());
                break;
            }
            result.append(value, position, start);
            String variable = value.substring(start + 2, end);
            String replacement = "user.home".equals(variable) ? home : System.getenv(variable);
            if (replacement == null) {
                replacement = System.getProperty(variable, "");
            }
            // Replacements are not re-scanned, so a value containing "${" cannot loop.
            result.append(replacement);
            position = end + 1;
        }
        String expanded = result.toString();
        if ("~".equals(expanded)) {
            return home;
        }
        if (expanded.startsWith("~/") || expanded.startsWith("~\\")) {
            return Paths.get(home, expanded.substring(2)).toString();
        }
        return expanded;
    }

    private static Path toPath(String value) throws IOException {
        try {
            return Paths.get(value).toAbsolutePath().normalize();
        }
        catch (InvalidPathException ex) {
            throw new IOException("Invalid path: " + value);
        }
    }

    private static String firstNonBlank(String... values) {
        for (String value : values) {
            if (value != null && !value.trim().isEmpty()) {
                return value.trim();
            }
        }
        return null;
    }

    private static String joinPaths(List<Path> paths) {
        StringBuilder joined = new StringBuilder();
        for (Path path : paths) {
            if (joined.length() > 0) {
                joined.append(", ");
            }
            joined.append(path);
        }
        return joined.toString();
    }

    static final class ServerCatalog {
        private final Path source;
        private final Map<String, ServerConfig> servers;

        ServerCatalog(Path source, Map<String, ServerConfig> servers) {
            this.source = source;
            this.servers = Collections.unmodifiableMap(new LinkedHashMap<>(servers));
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
        private final Path privateKeyPath;
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
        private final boolean protectSystem;

        private ServerConfig(Builder builder) {
            this.id = builder.id;
            this.host = builder.host;
            this.port = builder.port;
            this.username = builder.username;
            this.passwordEnv = builder.passwordEnv;
            this.privateKeyPath = builder.privateKeyPath;
            this.privateKeyPassphraseEnv = builder.privateKeyPassphraseEnv;
            this.knownHostsPath = builder.knownHostsPath;
            this.strictHostKeyChecking = builder.strictHostKeyChecking;
            this.connectTimeoutMs = builder.connectTimeoutMs;
            this.commandTimeoutMs = builder.commandTimeoutMs;
            this.maxOutputBytes = builder.maxOutputBytes;
            this.maxConcurrentOperations = builder.maxConcurrentOperations;
            this.maxConcurrentTunnels = builder.maxConcurrentTunnels;
            this.operationSlots = new Semaphore(maxConcurrentOperations, true);
            this.tunnelSlots = new Semaphore(maxConcurrentTunnels, true);
            this.allowSftp = builder.allowSftp;
            this.allowPortForwarding = builder.allowPortForwarding;
            this.allowedCommandPrefixes = Collections.unmodifiableList(builder.allowedCommandPrefixes);
            this.deniedCommandPatterns = Collections.unmodifiableList(builder.deniedCommandPatterns);
            this.protectSystem = builder.protectSystem;
        }

        static ServerConfig from(JsonNode node) throws IOException {
            if (node == null || !node.isObject()) {
                throw new IOException("each server entry must be a JSON object");
            }
            validateFields(node);
            Builder b = new Builder();
            b.id = required(node, "id");
            if (!SERVER_ID.matcher(b.id).matches()) {
                throw new IOException("server id \"" + b.id + "\" must match [A-Za-z0-9._-]{1,64}");
            }
            String prefix = "server \"" + b.id + "\" ";
            b.host = stripBrackets(required(node, "host"));
            b.username = required(node, "username");
            if (hasSpaceOrControl(b.host + b.username)) {
                throw new IOException(prefix + "host and username must not contain whitespace or control characters");
            }
            b.port = integer(node, "port", 22, 1, 65535);
            b.passwordEnv = optional(node, "passwordEnv");
            String privateKey = optional(node, "privateKeyPath");
            b.privateKeyPassphraseEnv = optional(node, "privateKeyPassphraseEnv");
            if (b.passwordEnv == null && privateKey == null) {
                throw new IOException(prefix + "must configure passwordEnv, privateKeyPath, or both");
            }
            if (b.passwordEnv != null && !ENV_NAME.matcher(b.passwordEnv).matches()) {
                throw new IOException(prefix + "passwordEnv must be an environment variable name, not a value");
            }
            if (b.privateKeyPassphraseEnv != null && !ENV_NAME.matcher(b.privateKeyPassphraseEnv).matches()) {
                throw new IOException(prefix + "privateKeyPassphraseEnv must be an environment variable name, not a value");
            }
            if (b.privateKeyPassphraseEnv != null && privateKey == null) {
                throw new IOException(prefix + "sets privateKeyPassphraseEnv without privateKeyPath");
            }
            b.privateKeyPath = privateKey == null ? null : toPath(expand(privateKey));

            b.strictHostKeyChecking = bool(node, "strictHostKeyChecking", true);
            String knownHosts = optional(node, "knownHostsPath");
            boolean autoAccept = autoAcceptHostKeys();
            if (b.strictHostKeyChecking && knownHosts == null && !autoAccept) {
                throw new IOException(prefix + "must configure knownHostsPath when strictHostKeyChecking is true");
            }
            if (!b.strictHostKeyChecking && !allowInsecureHostKeys() && !autoAccept) {
                throw new IOException(prefix + "disables host-key checking; set " + ENV_ALLOW_INSECURE_HOST_KEYS
                        + "=true only for an explicit exception");
            }
            b.knownHostsPath = knownHosts == null ? null : toPath(expand(knownHosts));
            if (b.strictHostKeyChecking && !autoAccept && !Files.isRegularFile(b.knownHostsPath)) {
                throw new IOException(prefix + "knownHostsPath is not a regular file: " + b.knownHostsPath
                        + " (create it with ssh-keyscan after verifying the host fingerprint)");
            }

            b.connectTimeoutMs = longValue(node, "connectTimeoutMs", 10000L, 1000L, 120000L);
            b.commandTimeoutMs = longValue(node, "commandTimeoutMs", 60000L, 100L, 3600000L);
            b.maxOutputBytes = integer(node, "maxOutputBytes", 1048576, 1024, 104857600);
            b.maxConcurrentOperations = integer(node, "maxConcurrentOperations", 4, 1, 64);
            b.maxConcurrentTunnels = integer(node, "maxConcurrentTunnels", 4, 1, 64);
            b.allowSftp = bool(node, "allowSftp", true);
            b.allowPortForwarding = bool(node, "allowPortForwarding", false);
            // Refuses commands and file changes that could wipe, break, or switch off the server.
            b.protectSystem = bool(node, "protectSystem", true);

            List<String> denied = strings(node, "deniedCommandRegexes");
            for (int i = 0; i < denied.size(); i++) {
                try {
                    b.deniedCommandPatterns.add(Pattern.compile(denied.get(i)));
                }
                catch (PatternSyntaxException ex) {
                    throw new IOException(prefix + "deniedCommandRegexes[" + i + "] is invalid: " + ex.getDescription(), ex);
                }
            }
            b.allowedCommandPrefixes = strings(node, "allowedCommandPrefixes");
            for (int i = 0; i < b.allowedCommandPrefixes.size(); i++) {
                if (containsAny(b.allowedCommandPrefixes.get(i), SHELL_METACHARACTERS)) {
                    throw new IOException(prefix + "allowedCommandPrefixes[" + i
                            + "] contains shell metacharacters and could never match");
                }
            }
            return new ServerConfig(b);
        }

        private static void validateFields(JsonNode node) throws IOException {
            Iterator<String> fields = node.fieldNames();
            while (fields.hasNext()) {
                String field = fields.next();
                for (String forbidden : FORBIDDEN_SECRET_FIELDS) {
                    if (field.equalsIgnoreCase(forbidden)) {
                        throw new IOException("secrets must not be stored directly in configuration; use an environment variable reference instead of \"" + field + "\"");
                    }
                }
                if (!ALLOWED_SERVER_FIELDS.contains(field)) {
                    throw new IOException("unknown field \"" + field + "\"");
                }
            }
        }

        private static String required(JsonNode node, String name) throws IOException {
            String value = optional(node, name);
            if (value == null) {
                throw new IOException("missing required field \"" + name + "\"");
            }
            return value;
        }

        private static String optional(JsonNode node, String name) throws IOException {
            JsonNode value = node.get(name);
            if (value == null || value.isNull()) {
                return null;
            }
            if (!value.isTextual() || value.asText().trim().isEmpty()) {
                throw new IOException("field \"" + name + "\" must be a non-empty string");
            }
            return value.asText().trim();
        }

        private static boolean bool(JsonNode node, String name, boolean defaultValue) throws IOException {
            JsonNode value = node.get(name);
            if (value == null || value.isNull()) {
                return defaultValue;
            }
            if (!value.isBoolean()) {
                throw new IOException("field \"" + name + "\" must be true or false");
            }
            return value.asBoolean();
        }

        private static int integer(JsonNode node, String name, int defaultValue, int min, int max) throws IOException {
            return (int) longValue(node, name, defaultValue, min, max);
        }

        private static long longValue(JsonNode node, String name, long defaultValue, long min, long max) throws IOException {
            JsonNode value = node.get(name);
            if (value == null || value.isNull()) {
                return defaultValue;
            }
            if (!value.isIntegralNumber() || !value.canConvertToLong()) {
                throw new IOException("field \"" + name + "\" must be an integer");
            }
            long result = value.asLong();
            if (result < min || result > max) {
                throw new IOException("field \"" + name + "\" must be between " + min + " and " + max);
            }
            return result;
        }

        private static List<String> strings(JsonNode node, String name) throws IOException {
            JsonNode value = node.get(name);
            List<String> result = new ArrayList<>();
            if (value == null || value.isNull()) {
                return result;
            }
            if (!value.isArray()) {
                throw new IOException("field \"" + name + "\" must be an array of strings");
            }
            for (int i = 0; i < value.size(); i++) {
                JsonNode item = value.get(i);
                if (!item.isTextual() || item.asText().trim().isEmpty()) {
                    throw new IOException("field \"" + name + "[" + i + "]\" must be a non-empty string");
                }
                result.add(item.asText().trim());
            }
            return result;
        }

        private static String stripBrackets(String host) {
            return host.startsWith("[") && host.endsWith("]") ? host.substring(1, host.length() - 1) : host;
        }

        private static boolean hasSpaceOrControl(String value) {
            for (int i = 0; i < value.length(); i++) {
                char c = value.charAt(i);
                if (c <= ' ' || c == 0x7f) {
                    return true;
                }
            }
            return false;
        }

        static boolean containsAny(String value, String characters) {
            for (int i = 0; i < value.length(); i++) {
                if (characters.indexOf(value.charAt(i)) >= 0) {
                    return true;
                }
            }
            return false;
        }

        String id() { return id; }
        String host() { return host; }
        int port() { return port; }
        String address() { return (host.indexOf(':') >= 0 ? "[" + host + "]" : host) + ":" + port; }
        String username() { return username; }
        String passwordEnv() { return passwordEnv; }
        Path privateKeyPath() { return privateKeyPath; }
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
        boolean protectSystem() { return protectSystem; }

        String effectiveHostKeyPolicy() {
            if (autoAcceptHostKeys()) {
                return "auto-accept";
            }
            return strictHostKeyChecking ? "strict" : "disabled";
        }

        private static final class Builder {
            String id;
            String host;
            int port;
            String username;
            String passwordEnv;
            Path privateKeyPath;
            String privateKeyPassphraseEnv;
            Path knownHostsPath;
            boolean strictHostKeyChecking;
            long connectTimeoutMs;
            long commandTimeoutMs;
            int maxOutputBytes;
            int maxConcurrentOperations;
            int maxConcurrentTunnels;
            boolean allowSftp;
            boolean allowPortForwarding;
            List<String> allowedCommandPrefixes = new ArrayList<>();
            final List<Pattern> deniedCommandPatterns = new ArrayList<>();
            boolean protectSystem;
        }
    }
}
