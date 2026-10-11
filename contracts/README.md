# Bootstrap IPC, protocol version 1

The trusted native host launches the fixed bundled Go executable and owns its private stdin/stdout pipes. UTF-8 JSON-RPC 2.0 objects are newline-delimited, with a 64 KiB frame limit, string request IDs (1–64 characters), and no batches or notifications in this bootstrap. One request executes at a time. The Go request timeout is five seconds; the host waits at most seven seconds. A broken or timed-out transport is closed and requires an application restart.

Initialize before database access:

```json
{
  "jsonrpc": "2.0",
  "id": "1",
  "method": "initialize",
  "params": { "protocolVersion": 1 }
}
```

```json
{ "jsonrpc": "2.0", "id": "1", "result": { "protocolVersion": 1 } }
```

The only frontend command is Tauri `health`, without arguments. Rust sends:

```json
{ "jsonrpc": "2.0", "id": "2", "method": "health", "params": {} }
```

```json
{
  "jsonrpc": "2.0",
  "id": "2",
  "result": {
    "name": "MailPilot",
    "version": "0.1.0",
    "protocolVersion": 1,
    "backend": "connected",
    "database": "connected",
    "schemaVersion": 2
  }
}
```

`database` may also be `disconnected`. The application version comes from root `package.json` when building the sidecar and must match the Tauri/Cargo versions. Rust validates the response and the frontend validates it again with Zod. Schema version 2 includes normalized mailbox persistence and the action journal; migration 1 remains the unchanged settings foundation. The wire protocol remains version 1.

The host may send `shutdown` with empty parameters, receiving `{"stopping":true}`, or close stdin for clean shutdown. The UI cannot invoke initialize, shutdown, SQL, shell commands, filesystem paths, or arbitrary method names. Unknown fields/methods, mismatched protocol versions and malformed requests are rejected. Standard JSON-RPC error codes are used, plus `-32001` for a missing handshake; errors contain no underlying database paths or diagnostics. Logs are structured JSON on stderr.

Streaming progress, cancellable background jobs, provider commands, and large payload transport are deliberately not implemented. Add these only when required, with contract and failure-mode tests.
