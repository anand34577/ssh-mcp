package com.example.sshmcp;

import com.fasterxml.jackson.databind.ObjectMapper;

import java.io.IOException;

public final class Main {
    private Main() {
    }

    public static void main(String[] args) throws Exception {
        System.setProperty("org.slf4j.simpleLogger.logFile", "System.err");
        System.setProperty("org.slf4j.simpleLogger.showDateTime", "true");
        System.setProperty("org.slf4j.simpleLogger.showThreadName", "false");
        System.setProperty("org.slf4j.simpleLogger.defaultLogLevel",
                System.getProperty("ssh.mcp.logLevel", "warn"));

        ObjectMapper mapper = new ObjectMapper();
        Config.ServerCatalog catalog;
        try {
            catalog = Config.load(mapper);
        }
        catch (IOException ex) {
            System.err.println("SSH MCP configuration error: " + ex.getMessage());
            System.exit(2);
            return;
        }

        final SshService ssh = new SshService(mapper, catalog);
        Runtime.getRuntime().addShutdownHook(new Thread(ssh::close, "ssh-mcp-shutdown"));
        new McpStdioServer(mapper, ssh).run(System.in, System.out);
        ssh.close();
    }
}
