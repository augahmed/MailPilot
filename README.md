# MailPilot

Local-first email assistant built with Go, React + TypeScript, Tauri 2, and SQLite.

This bootstrap opens a native desktop window and checks the real Go backend and SQLite database. A healthy startup displays:

```text
MailPilot
Local-first email assistant

Status:
Backend connected
Database connected
```

Gmail, OAuth, credential storage, mailbox import, organization, phishing analysis, search, and AI are intentionally not implemented yet. No account, password, API key, or external service is needed. Failed connections are shown as disconnected; the browser-only preview does not simulate a backend.

## Prerequisites

- Go **1.26+** (verified with 1.27.1).
- Node.js **24.13.1+** and npm (verified with Node 24.13.1 / npm 11.8.0).
- Rust **1.99+**, Cargo, rustfmt and Clippy (verified with Rust 1.99.0).
- Native platform prerequisites from [Tauri](https://v2.tauri.app/start/prerequisites/). On macOS, install Xcode Command Line Tools with `xcode-select --install` if missing. Windows requires MSVC build tools and WebView2; Linux requires the documented WebKitGTK and system development packages.

For a new Rust installation, use the official [rustup installer](https://rustup.rs/). MailPilot's npm development/build scripts automatically find Rust in `~/.cargo/bin` (or `$CARGO_HOME/bin`) when it is missing from PATH, while preserving an already configured toolchain. For direct `cargo` and `rustup` commands on macOS/Linux, load its environment in your current terminal:

```sh
source "$HOME/.cargo/env"
rustup component add rustfmt clippy
go version
node --version
rustc --version
```

The native build and smoke test have been verified on Apple Silicon macOS. Windows/Linux targets are planned and need native qualification. Cross-compilation is deliberately rejected by the sidecar build script; build on the target OS/architecture.

## Install and run

From the repository root:

```sh
npm ci
cd backend
go mod download
cd ..
npm run dev
```

`npm run dev` generates the application icons from the checked-in SVG, builds the Go sidecar for the host target, starts Vite at `http://127.0.0.1:1420`, and launches Tauri. Rust dependencies are downloaded on the first run. Keep this terminal open; stop development with Ctrl+C. Port 1420 must be available.

React changes use hot reload. After changing Go code, stop and rerun `npm run dev` to rebuild/relaunch the sidecar. Tauri handles Rust rebuilds. A normal desktop quit closes the core's stdin and gives it a grace period to close SQLite before termination. Reopening the app preserves the local database.

For a presentation-only browser preview:

```sh
npm run dev:frontend
```

This preview correctly reports that the backend is disconnected; only the native host can launch and communicate with the core.

## Build a desktop application

On macOS, from the repository root:

```sh
npm run build -- --bundles app
open src-tauri/target/release/bundle/macos/MailPilot.app
```

The build includes frontend type checking, a production Vite build, the Go executable, and native Rust compilation. The resulting application runs without Vite, Node, Go or Cargo installed. `npm run build` without `--bundles app` builds the platform's default installer formats; installer signing/notarization and public distribution are not configured in this bootstrap. The local development bundle is not a signed/notarized production release.

The packaged macOS executable is `src-tauri/target/release/bundle/macos/MailPilot.app/Contents/MacOS/mailpilot`. Native binaries and generated assets are ignored by Git; the source SVG, manifests, `package-lock.json`, `backend/go.sum` and `src-tauri/Cargo.lock` are source-controlled.

## Configuration and local storage

Configuration is non-secret. No `.env` file is loaded and no credential is accepted or stored. Defaults come from the OS application data directory; the native host passes that default to Go. Standalone core startup resolves the corresponding platform directory itself.

| Setting        | Default                                                 | Override                                                                      |
| -------------- | ------------------------------------------------------- | ----------------------------------------------------------------------------- |
| Data directory | Per-user app-data directory for `com.mailpilot.desktop` | `MAILPILOT_DATA_DIR` (absolute dedicated directory) or core `--data-dir`      |
| Log level      | `info`                                                  | `MAILPILOT_LOG_LEVEL` or core `--log-level`: `debug`, `info`, `warn`, `error` |

Core flags take precedence over environment variables, then defaults. Environment overrides also work when launching the desktop executable from a terminal. Use a dedicated directory: the core creates it with private permissions and enforces those permissions on existing directories.

Default desktop locations:

- macOS: `~/Library/Application Support/com.mailpilot.desktop/mailpilot.db`
- Windows: `%APPDATA%\com.mailpilot.desktop\mailpilot.db`
- Linux: `${XDG_DATA_HOME:-~/.local/share}/com.mailpilot.desktop/mailpilot.db`

SQLite uses WAL, a five-second busy timeout, foreign keys on each connection, FULL synchronization, and one pooled connection. Startup applies transactional numbered SQL migrations with recorded checksums; altered migration history and future schemas are rejected. Version 1 contains only `schema_migrations` and non-secret `settings`. The full email schema in the architecture remains future work. SQLite files are not encrypted.

Go emits structured JSON diagnostics to stderr; stdout belongs exclusively to private IPC. Logs omit secrets, message content and filesystem paths. This phase has no persistent log files or telemetry.

## Verification

Run from the repository root after installing dependencies:

```sh
gofmt -w backend
cd backend
go vet ./...
go test -race ./...
go build ./...
cd ..
npm run format:check
npm run typecheck
npm run lint
npm test
npm run build -- --bundles app
cargo fmt --manifest-path src-tauri/Cargo.toml -- --check
cargo clippy --manifest-path src-tauri/Cargo.toml --all-targets -- -D warnings
cargo test --manifest-path src-tauri/Cargo.toml
npm run test:desktop
```

Use `npm run format` to apply formatting. On Windows/Linux, replace the macOS-specific build command with `npm run build`; native smoke tests on those platforms still need qualification. Run native tests in a graphical desktop session. Close other MailPilot windows before running the smoke test because the app enforces a single instance.

The native smoke test launches the packaged application with `--smoke-test` and a temporary data directory. It requires a real frontend invocation to traverse WebView → Tauri → Go → SQLite, then automatically exits and verifies clean core shutdown. It does not inspect pixels; React tests separately verify the status text and failure display. The temporary test database is removed afterward.

Go tests cover configuration precedence/validation, private database creation, persisted settings, SQLite pragmas and FTS availability, migration checksum/future-schema rejection, transactional rollback, corrupt databases, health failure reporting, protocol initialization and method/frame validation. Frontend tests cover health response validation and real success/error display behavior. Rust tests cover bounded pipe framing with fragmented reads and truncated/oversized messages.

To exercise the Go core without Tauri, from `backend/`:

```sh
printf '%s\n' \
  '{"jsonrpc":"2.0","id":"1","method":"initialize","params":{"protocolVersion":1}}' \
  '{"jsonrpc":"2.0","id":"2","method":"health"}' \
  '{"jsonrpc":"2.0","id":"3","method":"shutdown"}' \
  | go run ./cmd/mailpilot-core
```

This uses the normal local data directory. Supply `--data-dir /absolute/path/to/a/dedicated/test-directory` after the Go package argument for isolation. Standalone `go run` reports a `-dev` version; the packaged sidecar uses the root package version.

## Layout and boundaries

```text
backend/
  cmd/mailpilot-core/           Go entry point and composition
  internal/
    app/ config/ domain/ ipc/ logging/
    store/sqlite/migrations/   SQLite initialization and tests
    actions/ ai/ auth/ credentials/ mail/ organize/
    provider/gmail/ risk/ search/ sync/   Documented future boundaries only
frontend/
  src/app/                     Status screen, styles and UI tests
  src/lib/ipc/                 Validated typed health client
src-tauri/
  src/                         Native lifecycle and private pipe bridge
  capabilities/                Main-window health permission only
  icons/source.svg             Icon source; raster assets generated at build
contracts/                     Versioned bootstrap IPC examples
scripts/                       Sidecar build and native smoke test
docs/ARCHITECTURE.md            Overall design and implementation status
AGENTS.md                      Engineering rules
```

Go owns business behavior and persistence. Rust only starts the fixed packaged binary, validates a bounded protocol, and handles native lifecycle; it uses `std::process` rather than installing a shell plugin. The frontend cannot run shell commands, access arbitrary files/SQL, select executables, or invoke arbitrary Go methods. A versioned handshake precedes database initialization; the only frontend capability is `health`.

The architecture's dependency recommendations are adopted only as needed: modernc SQLite avoids a Go C toolchain, React/TypeScript/Vite build the UI, Zod validates runtime IPC, and Tauri/Serde provide native integration. The single-instance plugin prevents duplicate desktop data owners. Vitest/Testing Library, ESLint, Prettier and native Go/Rust tests cover this phase. TanStack Query, email/OAuth/keyring SDKs and AI/vector dependencies are intentionally deferred. Dependency purposes and official documentation are recorded in [the architecture](docs/ARCHITECTURE.md); read [AGENTS.md](AGENTS.md) before future implementation.
