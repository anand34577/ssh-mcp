package com.example.sshmcp;

import com.fasterxml.jackson.databind.ObjectMapper;

import java.io.FileOutputStream;
import java.io.FileDescriptor;
import java.io.OutputStream;
import java.io.PrintStream;
import java.util.List;

public final class Main {
    private static final String USAGE = "Usage: ssh-mcp-server [--config <path>] [--validate [--connect]] [--version]\n\n"
            + "Runs an MCP server on stdin/stdout. Logs and audit records go to stderr.\n\n"
            + "  --config <path>  servers.json to load (default: -Dssh.mcp.config, $SSH_MCP_CONFIG, then\n"
            + "                   ./config/servers.json, next to the jar, or the user config dir)\n"
            + "  --validate       check configuration, credentials, key files, and known_hosts, then exit\n"
            + "  --connect        with --validate, also connect and authenticate to every server\n"
            + "  --version        print the version and exit\n";

    private Main() {
    }

    public static void main(String[] args) {
        System.exit(run(args));
    }

    static int run(String[] args) {
        // MCP owns stdout. Keep a private handle to it and point System.out at stderr, so stray
        // prints from any library can never corrupt the protocol stream.
        OutputStream protocolOut = new FileOutputStream(FileDescriptor.out);
        PrintStream stderr = System.err;
        System.setOut(stderr);
        AuditLog.setOutput(stderr);
        System.setProperty("org.slf4j.simpleLogger.logFile", "System.err");
        System.setProperty("org.slf4j.simpleLogger.showDateTime", "true");
        System.setProperty("org.slf4j.simpleLogger.showThreadName", "false");
        System.setProperty("org.slf4j.simpleLogger.defaultLogLevel", System.getProperty("ssh.mcp.logLevel", "warn"));

        String configPath = null;
        boolean validate = false;
        boolean connect = false;
        for (int i = 0; i < args.length; i++) {
            switch (args[i]) {
                case "--config":
                case "-config":
                    if (i + 1 >= args.length) {
                        stderr.println("--config requires a path\n\n" + USAGE);
                        return 2;
                    }
                    configPath = args[++i];
                    break;
                case "--validate":
                case "-validate":
                    validate = true;
                    break;
                case "--connect":
                case "-connect":
                    connect = true;
                    break;
                case "--version":
                case "-version":
                    console(protocolOut).println("ssh-mcp-server " + version());
                    return 0;
                case "--help":
                case "-help":
                case "-h":
                    stderr.print(USAGE);
                    return 0;
                default:
                    stderr.println("Unknown argument: " + args[i] + "\n\n" + USAGE);
                    return 2;
            }
        }

        ObjectMapper mapper = new ObjectMapper();
        Config.ServerCatalog catalog;
        try {
            catalog = Config.load(mapper, configPath);
        }
        catch (Exception ex) {
            stderr.println("SSH MCP configuration error: " + ex.getMessage());
            return 2;
        }
        if (Config.autoAcceptHostKeys()) {
            AuditLog.info("WARNING: " + Config.ENV_AUTO_ACCEPT_HOST_KEYS + " is enabled; host-key verification is disabled for every target");
        }

        SshService ssh = new SshService(mapper, catalog);
        Runtime.getRuntime().addShutdownHook(new Thread(ssh::close, "ssh-mcp-shutdown"));
        try {
            if (validate) {
                return report(console(protocolOut), catalog, ssh.check(connect));
            }
            AuditLog.info("ssh-mcp-server " + version() + " serving " + catalog.servers().size()
                    + " server(s) from " + catalog.source());
            new McpStdioServer(mapper, ssh, version()).run(System.in, protocolOut);
            return 0;
        }
        catch (Exception ex) {
            AuditLog.info("MCP server stopped: " + ex);
            return 1;
        }
        finally {
            ssh.close();
        }
    }

    private static int report(PrintStream out, Config.ServerCatalog catalog, List<SshService.CheckResult> results) {
        out.println("Configuration: " + catalog.source());
        int failed = 0;
        for (SshService.CheckResult result : results) {
            String status = result.ok() ? (result.connected ? "OK (connected)" : "OK") : "FAIL";
            if (!result.ok()) {
                failed++;
            }
            out.println();
            out.println("[" + status + "] " + result.serverId + " (" + result.address + ")");
            result.problems.forEach(problem -> out.println("  error:   " + problem));
            result.warnings.forEach(warning -> out.println("  warning: " + warning));
        }
        out.println();
        out.println((results.size() - failed) + " of " + results.size() + " server(s) ready.");
        return failed == 0 ? 0 : 1;
    }

    /** Plain-text stdout for the non-MCP modes (--version, --validate). */
    private static PrintStream console(OutputStream stdout) {
        try {
            return new PrintStream(stdout, true, "UTF-8");
        }
        catch (java.io.UnsupportedEncodingException ex) {
            throw new IllegalStateException(ex);
        }
    }

    static String version() {
        String version = Main.class.getPackage().getImplementationVersion();
        return version == null ? "dev" : version;
    }
}
