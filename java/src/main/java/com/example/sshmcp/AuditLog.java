package com.example.sshmcp;

import java.io.PrintStream;
import java.time.Instant;

/** Audit and diagnostic records on stderr. Never log secrets or command text. */
final class AuditLog {
    private static volatile PrintStream out = System.err;

    private AuditLog() {
    }

    static void operation(String operation, Config.ServerConfig server, boolean success, long durationMs, String detail) {
        String suffix = detail == null || detail.trim().isEmpty() ? "" : " " + detail.trim();
        event("operation=" + operation + " server=" + server.id() + " success=" + success + " durationMs=" + durationMs + suffix);
    }

    static void event(String message) {
        write("audit " + message);
    }

    static void failure(String operation, Throwable error) {
        write("WARN " + operation + " failed type=" + error.getClass().getSimpleName());
    }

    static void info(String message) {
        write(message);
    }

    /** Redirects output; used by Main to keep the original stderr after protecting stdout. */
    static void setOutput(PrintStream stream) {
        out = stream;
    }

    private static synchronized void write(String message) {
        out.println(Instant.now() + " ssh-mcp " + message);
    }
}
