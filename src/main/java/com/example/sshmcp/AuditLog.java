package com.example.sshmcp;

import java.time.Instant;

final class AuditLog {
    private AuditLog() {
    }

    static void success(String operation, Config.ServerConfig server, long durationMs, String detail) {
        write("INFO", operation + " server=" + server.id() + " durationMs=" + durationMs + " " + detail);
    }

    static void duration(Config.ServerConfig server, long durationMs) {
        write("DEBUG", "connection server=" + server.id() + " durationMs=" + durationMs);
    }

    static void failure(String operation, Throwable error) {
        write("WARN", operation + " failed type=" + error.getClass().getSimpleName());
    }

    private static synchronized void write(String level, String message) {
        System.err.println(Instant.now() + " " + level + " " + message);
    }
}
