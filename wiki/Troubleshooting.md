# Troubleshooting

**First, run the built-in check.** It looks at every server and tells you exactly what's wrong:

```bash
ssh-mcp-server -validate -connect
```

Or open `ssh-mcp-server setup` and choose **Test my servers**.

## My AI app doesn't see SSH MCP

1. **Restart the app completely.** Most apps only load tools at startup. On Windows and macOS, quit from the tray or menu bar, not just the window.
2. Run `ssh-mcp-server connect` and look for `[ok] Connected to ...` next to your app.
3. Moved or reinstalled the program? `ssh-mcp-server connect` points every app at the new location.
4. Still nothing? Check the app's MCP or developer log. Messages from SSH MCP start with `ssh-mcp`.

## "No configuration file found"

You haven't added a server yet, or the app is looking in the wrong place. Run `ssh-mcp-server setup`.

## "Host ... is not in known_hosts" / the server isn't trusted yet

SSH MCP hasn't seen this server's fingerprint. Run `ssh-mcp-server setup`, add (or re-add) the server, and confirm the fingerprint.

## "HOST KEY MISMATCH"

The server's ID card changed since you trusted it. Usually that's because the server was rebuilt or reinstalled, but it can also mean someone is pretending to be your server.

1. Ask your provider or admin, or check the server's dashboard, for the new fingerprint.
2. If it's legitimate, remove the old entry and add the server again:

   ```bash
   ssh-keygen -R web.example.com
   ssh-mcp-server setup
   ```

Never switch off fingerprint checks to make this go away.

## "Environment variable ... is not set"

The server uses a password, but your AI app didn't pass it along. Run `ssh-mcp-server setup`, choose **Connect to my AI apps**, and save the password when asked. Or switch that server to a key file.

## "Unable to authenticate" / sign-in failed

- **Password:** check the username and password.
- **Key file:** the server may not have your public key yet. Add it with:

  ```bash
  ssh-copy-id -i ~/.ssh/id_ed25519.pub you@server
  ```

- **Two-factor (one-time codes):** not supported. Use a key file for an account without 2FA.

## "Cannot reach ..." / "timed out"

The computer can't reach the server. Check the address and port, your internet connection, VPN, and firewall. Try `ssh you@server` in a terminal: if that doesn't work either, the problem isn't SSH MCP.

## "Command is not allowed by the server command policy"

That server is in look-only mode or has an allow-list, and the command isn't on it. That's working as intended. To allow more, edit `allowedCommandPrefixes` in the [Settings File](Settings-File), or re-add the server with full access.

## "Server ... is busy"

Too many actions are running at once on that server. Wait a moment, or raise `maxConcurrentOperations`.

## Windows: "Windows protected your PC"

The program is new and not signed by a large company. Click **More info**, then **Run anyway**. You can verify the file against `SHA256SUMS.txt` on the release page.

## macOS: "cannot be opened because the developer cannot be verified"

Use the installer, which handles this. Or run once in Terminal:

```bash
xattr -d com.apple.quarantine ./ssh-mcp-server-darwin-arm64
```

## Linux/macOS: "permission denied" when running it

```bash
chmod +x ./ssh-mcp-server-*
```

## Still stuck?

[Open an issue](https://github.com/anand34577/ssh-mcp/issues/new) with the output of `ssh-mcp-server -validate -connect`. **Remove server addresses you'd rather keep private, and never include passwords or keys.**
