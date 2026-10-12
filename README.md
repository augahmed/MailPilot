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

Gmail OAuth, OS-secure refresh-token storage, and connection controls are implemented. Follow [Gmail development setup](docs/GMAIL_SETUP.md) to connect a dedicated test account. Bounded manual Inbox import and a cached mailbox summary are implemented. User-defined categories, local rule classification, reviewed Gmail label organization and structured offline phishing feature extraction are implemented. Phishing scoring/review and local semantic classification/search are implemented; external AI remains unimplemented. The local application works without an account or external service. Failed connections are shown as disconnected; the browser-only preview does not simulate a backend.

## Prerequisites

- Go **1.27.2+** (the patched version required by `backend/go.mod`; Go may download it automatically).
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

No `.env` file is loaded. OAuth client configuration comes from `MAILPILOT_GOOGLE_CLIENT_ID` and `MAILPILOT_GOOGLE_CLIENT_SECRET` at runtime, never frontend build configuration. User access/refresh tokens are never read from environment variables; refresh tokens use the OS keyring. See [setup instructions](docs/GMAIL_SETUP.md). Defaults come from the OS application data directory; the native host passes that default to Go. Standalone core startup resolves the corresponding platform directory itself.

| Setting        | Default                                                 | Override                                                                      |
| -------------- | ------------------------------------------------------- | ----------------------------------------------------------------------------- |
| Data directory | Per-user app-data directory for `com.mailpilot.desktop` | `MAILPILOT_DATA_DIR` (absolute dedicated directory) or core `--data-dir`      |
| Log level      | `info`                                                  | `MAILPILOT_LOG_LEVEL` or core `--log-level`: `debug`, `info`, `warn`, `error` |

Core flags take precedence over environment variables, then defaults. Environment overrides also work when launching the desktop executable from a terminal. Use a dedicated directory: the core creates it with private permissions and enforces those permissions on existing directories.

Default desktop locations:

- macOS: `~/Library/Application Support/com.mailpilot.desktop/mailpilot.db`
- Windows: `%APPDATA%\com.mailpilot.desktop\mailpilot.db`
- Linux: `${XDG_DATA_HOME:-~/.local/share}/com.mailpilot.desktop/mailpilot.db`

SQLite uses WAL, a five-second busy timeout, foreign keys on each connection, FULL synchronization, and one pooled connection. Startup applies transactional numbered SQL migrations with recorded checksums; altered migration history and future schemas are rejected. Version 1 creates the migration ledger and non-secret settings; version 2 adds normalized email storage, accounts, labels, local categories, analysis results and a journal for future reversible actions. SQL-free repositories support transactional idempotent writes and account-scoped retrieval; version 3 adds cached body completeness and provider content identity for bounded Gmail imports. Version 4 adds category metadata/defaults, and version 5 adds structured rule evidence and complete classification assessments, including unclassified results. Version 6 adds immutable organization plans and remote-write evidence under the action journal. SQLite files are not encrypted.

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

The native smoke test launches the packaged application with `--smoke-test` and a temporary data directory. It requires a real frontend invocation to traverse WebView → Tauri → Go → SQLite, then automatically exits and verifies clean core shutdown. It does not inspect pixels; React tests separately verify the status text and failure display. The native smoke test also verifies the category, classification and organization commands reject access without an account. The temporary test database is removed afterward.

Go tests cover configuration precedence/validation, private database creation, persisted settings, SQLite pragmas and FTS availability, migration checksum/future-schema rejection, transactional rollback, corrupt databases, health failure reporting, protocol initialization and method/frame validation. Frontend tests cover health response validation and real success/error display behavior. Rust tests cover bounded pipe framing, strict auth DTOs, and Google browser-URL restrictions. OAuth tests cover state/PKCE, denial, callback shutdown, refresh/rotation, restart, disconnect races, scope checks, and secret redaction. Native Google consent remains an explicit manual test with a developer-owned client.

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
    app/ config/ domain/ ipc/ logging/ repository/
    store/sqlite/migrations/   SQLite initialization and tests
    auth/ credentials/ provider/gmail/   OAuth lifecycle, keyring, Google adapter
    mail/ sync/                    MIME normalization and bounded imports
    classification/                    Local rule engine and starter configuration
    organize/                          Reviewed Gmail label plans and undo
    risk/                              Structured offline phishing observations
frontend/
  src/app/                     Status screen, styles and UI tests
  src/features/accounts/       Gmail connection controls
  src/lib/ipc/                 Validated typed health/authentication client
src-tauri/
  src/                         Native lifecycle and private pipe bridge
  capabilities/                Main-window health and Gmail connection permissions
  icons/source.svg             Icon source; raster assets generated at build
contracts/                     Versioned bootstrap IPC examples
scripts/                       Sidecar build and native smoke test
docs/ARCHITECTURE.md            Overall design and implementation status
AGENTS.md                      Engineering rules
```

Go owns business behavior and persistence. Rust only starts the fixed packaged binary, validates a bounded protocol, and handles native lifecycle; it uses `std::process` rather than installing a shell plugin. The frontend cannot run shell commands, access arbitrary files/SQL, select executables, or invoke arbitrary Go methods. A versioned handshake precedes database initialization; frontend capabilities allow health, Gmail connection, sync, cached-mail, category management, classification review and organization commands. The host alone opens a validated Google authorization URL in the system browser.

The architecture's dependency recommendations are adopted only as needed: modernc SQLite avoids a Go C toolchain, React/TypeScript/Vite build the UI, Zod validates runtime IPC, and Tauri/Serde provide native integration. The single-instance plugin prevents duplicate desktop data owners. Vitest/Testing Library, ESLint, Prettier and native Go/Rust tests cover this phase. Go OAuth2 and go-keyring supply PKCE/token exchange and secure refresh-token storage. TanStack Query, the full Gmail SDK and AI/vector dependencies remain deferred. Dependency purposes and official documentation are recorded in [the architecture](docs/ARCHITECTURE.md); read [AGENTS.md](AGENTS.md) before future implementation.

## Inbox synchronization

Connect Gmail, then select **Sync inbox**. The screen reports messages discovered, imported, updated and errors, and lists cached sender, subject and received time. Configure the per-run maximum before launching:

```sh
MAILPILOT_SYNC_LIMIT=1000 npm run dev
```

The default is 500; accepted values are 1–10,000. Repeated runs refresh the same bounded Inbox subset without duplicate rows and use metadata-only requests for already-complete cached bodies. Updated counts include unchanged snapshots. This is not full-mailbox or history-based incremental sync. Interrupted/partial runs retain successful imports; rerun to retry. Sync does not modify labels, classify mail or send messages. Manual classification is available separately below. Attachments are not downloaded; separately stored or unsupported/oversized bodies are reported rather than silently treated as complete. The cache is sensitive, unencrypted local data and persists after disconnect. A disconnected account's cache is hidden until that account reconnects.

Automated sync tests use synthetic JSON fixtures and local HTTP servers with temporary databases; they never access a live Gmail account. To test manually with a dedicated account, connect, sync twice, verify the second run reports updates rather than duplicate imports, and disconnect during a run. Native smoke tests run without Google configuration.

## User-defined categories

Connect an account and use **Refresh categories** to manage its local definitions. Seven descriptive defaults are created once per account. Create/edit a name and meaningful description, enable or disable a category, or confirm deletion. A category referenced by cached email assignments or stored analysis cannot be deleted; disable it instead. Definition changes persist in SQLite migration 4 and remain local. Changing categories does not run classification or change email assignments. Manual review uses the starter rule engine described below.

## Local classification review

After connecting and syncing an account, open **Classification review** and choose **Classify this page**. MailPilot reviews up to five cached emails, saves the results, and shows each email’s predicted category, confidence and reasons. Use the previous/next review-page controls to cover more downloaded messages. **Refresh reviews** loads saved assessments without running the classifier. Reading results uses local SQLite and requires no AI provider/key.

The hybrid classifier first accepts deterministic rules at strength 0.85 or above, then falls back to locally computed category-description cosine similarity. The existing conservative starter rules cover Job Applications, Job Interviews, Receipts and Newsletters. They require multiple signals and use confidence scores of 55–65%; these scores are heuristic strengths rather than measured probabilities. The starter scores fall below the new high-confidence cutoff. Weak, conflicting or incomplete evidence can leave mail unclassified; without a local model, weak matches abstain. Renamed, disabled or custom categories have no starter mapping; rule configuration is centralized in [starter_rules.json](backend/internal/classification/starter_rules.json) and can be supplied through validated Go configuration. Changes to that configuration require a new classifier version.

Category definitions and label changes can make previous predictions stale; rerun the page to refresh them. Partial storage failures report successful saves and row errors separately. Classification saves suggestions only: it does not assign categories, modify Gmail or use an LLM. The explicitly configured local MiniLM provider computes and caches embeddings during manual hybrid review. See [semantic setup](docs/SEMANTIC_CLASSIFICATION.md) and [measured synthetic evaluation](docs/SEMANTIC_EVALUATION.md). The existing account session is required to access retained cached mail; reconnect and refresh after disconnect. Automated tests use synthetic mail and temporary databases.

## Safe Gmail organization

After classifying mail, open **Gmail organization**, refresh the selection table, and select predictions. **Dry Run** is on by default: it reads live Gmail labels and shows each proposed label creation/addition without writing Gmail or the action journal. Choose **Back to selection**, turn Dry Run off, and generate a fresh apply preview. Review the current labels, proposed MailPilot label, confidence and operation list; check the confirmation box and apply. Previews expire after five minutes and changed mail/predictions/categories/labels require a fresh preview. Each batch contains at most five emails.

Categories map to `MailPilot/<category name>` user labels. Existing exact-name labels are reused; missing ones are created when a preview is confirmed. MailPilot never deletes labels/emails, archives messages or changes system labels. Definition/name changes affect future mappings without renaming old Gmail labels. Completed writes and partial/uncertain outcomes persist under mailbox_actions. **Stop organization** cancels remaining work; a dispatched write can remain uncertain. **Reconcile interrupted batch** reads Gmail and records what can be established without replaying a write.

**Preview undo last batch** shows only label memberships MailPilot successfully added and verified. Confirm undo separately. Inbox, pre-existing and unrelated Gmail labels are preserved; created labels stay in Gmail. Ambiguous additions are excluded from undo because ownership cannot be proven. Repeated confirmed undo is rejected, and partial/conflicting outcomes remain visible. Concurrent changes to the same label membership cannot be perfectly attributed. Organization uses synthetic tests; no personal mailbox is modified by the validation suite.

## Phishing feature extraction

The Go extractor returns structured address/authentication, URL/link, attachment, HTML-form and language observations with parser limitations. It is separate from scoring and uses no network, LLM or external threat service. Authentication header claims remain unverified; missing content/evidence never establishes safety. This stage has no risk score, persistence or UI yet. See [supported signals, limits and synthetic fixtures](docs/PHISHING_FEATURES.md).

### Review high-risk mail in Gmail organization

Choose **Potential Phishing** in **Review suggestions**. HIGH-risk messages show their score, threshold and ordered reasons. Select messages and run the default **Dry Run** to inspect live labels without Gmail or action-journal writes. To apply, turn off Dry Run, generate a fresh preview, review the explanations and exact operations, then explicitly confirm. Missing `Potential Phishing` user labels are created through the existing label workflow. No message is deleted, archived or moved to Spam.

Set `MAILPILOT_RISK_HIGH_THRESHOLD=75` before starting the desktop app to change HIGH from its default of 60; the core also accepts `--risk-high-threshold 75`. Supported values are 31–100 (MEDIUM starts at 30). Restart after changing configuration; existing previews with the old scoring version cannot be applied. Scores are heuristics, not proof of phishing or guarantees of safety. Dry Run may refresh local risk assessments.

**Preview undo last batch** and its separate confirmation remove only verified additions, preserving pre-existing and unrelated labels. Created labels remain in Gmail. Interrupted or uncertain writes require reconciliation first. Schema migration 7 preserves existing plans and makes category references optional for risk proposals.

## Search your mailbox

Configure the local MiniLM runtime using [semantic setup](docs/SEMANTIC_CLASSIFICATION.md), sync cached mail, then choose **Index cached mail** under **Search your mailbox**. Enter a description and choose **Search locally** for cosine-ranked results with sender, subject, date, score and a relevant excerpt. Optional date, sender and category filters use local cached data. Progress, incomplete-body coverage, partial results and cancellation are visible. Saved vectors are reused across searches and restarts; indexing is explicit and only computes missing current-model entries.

Retrieval never sends mail to an external inference service or runs a Gmail keyword query. Gmail keyword search remains a separate future feature. [Search documentation](docs/SEMANTIC_SEARCH.md) explains cache coverage, filters, limits and the real synthetic 4/4 retrieval check. The optional Python/ONNX runtime still requires configured local paths and is not bundled in public installers.
