# MailPilot

Local-first intelligent Gmail management built with Go, React + TypeScript,
Tauri 2 and SQLite. The intended core is one shared local preprocessing, chunking,
embedding and vector-retrieval pipeline supporting semantic search, category
suggestions and optional answers grounded in approved email evidence.

**Current status:** Gmail OAuth/keyring, bounded initial/incremental sync, cached
plain-text reading, category management, deterministic review/risk explanations,
confirmed label organization, selected-email Summary and historical draft recovery
are implemented. Manual sync also prepares deterministic local text chunks with
bundled MiniLM WordPiece tokenization, section provenance and pending embedding
rows. The Local Email Index card explicitly starts/resumes a durable local MiniLM
indexing run over all live cached mail, with pause/cancel/rebuild and coverage counts.
Keyword/semantic search, semantic classification and evidence questions remain planned. Historical vector tables are inert; installed users need no model/runtime setup. OpenAI is the only installed functional generative adapter.

The tokenizer is embedded in Go; the MiniLM model/native CPU runtime ship as fixed app resources; no user Python or
post-install model download. Target English first and about 10,000 cached messages.
AI replies/composition, new Gmail drafts, sending and Chrome integration are outside
the immediate scope. Existing Summary remains until a later tested transition.

Read [AGENTS.md](AGENTS.md) for engineering rules, the single
[product plan](docs/PRODUCT_PLAN.md) for scope/milestones and
[architecture](docs/ARCHITECTURE.md) for implemented versus intended boundaries.
The [documentation audit](docs/DOCUMENTATION_AUDIT.md) explains the reset;
[legacy records](docs/archive/LEGACY_FEATURES.md) preserve migration/recovery history.

This bootstrap opens a native desktop window and checks the real Go backend and SQLite database. A healthy startup displays:

```text
MailPilot
Local-first email assistant

Status:
Backend connected
Database connected
```

Gmail OAuth, OS credential storage, bounded initial/incremental sync, cached reading,
editable categories, deterministic category review, explained phishing risk and
reviewed label organization remain active. Follow [Gmail setup](docs/GMAIL_SETUP.md)
with a dedicated test account. AI settings and read-only historical draft recovery
remain available independently of retired generation workflows.

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

Installed users need no Python, native runtime installation or model download.
The model/runtime are bundled; local inference is optional and explicitly started.

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

The app does not automatically load `.env`; a developer may explicitly source local OAuth client configuration in their shell. OAuth client configuration comes from `MAILPILOT_GOOGLE_CLIENT_ID` and `MAILPILOT_GOOGLE_CLIENT_SECRET` at runtime, never frontend build configuration. User access/refresh tokens are never read from environment variables; refresh tokens use the OS keyring. See [setup instructions](docs/GMAIL_SETUP.md). Defaults come from the OS application data directory; the native host passes that default to Go. Standalone core startup resolves the corresponding platform directory itself.

| Setting        | Default                                                 | Override                                                                      |
| -------------- | ------------------------------------------------------- | ----------------------------------------------------------------------------- |
| Data directory | Per-user app-data directory for `com.mailpilot.desktop` | `MAILPILOT_DATA_DIR` (absolute dedicated directory) or core `--data-dir`      |
| Log level      | `info`                                                  | `MAILPILOT_LOG_LEVEL` or core `--log-level`: `debug`, `info`, `warn`, `error` |

Core flags take precedence over environment variables, then defaults. Environment overrides also work when launching the desktop executable from a terminal. Use a dedicated directory: the core creates it with private permissions and enforces those permissions on existing directories.

Default desktop locations:

- macOS: `~/Library/Application Support/com.mailpilot.desktop/mailpilot.db`
- Windows: `%APPDATA%\com.mailpilot.desktop\mailpilot.db`
- Linux: `${XDG_DATA_HOME:-~/.local/share}/com.mailpilot.desktop/mailpilot.db`

SQLite uses WAL, a five-second busy timeout, foreign keys on each connection, FULL synchronization, and one pooled connection. Startup applies transactional numbered SQL migrations with recorded checksums; altered migration history and future schemas are rejected. Current migrations 001–017 retain mail/cache, category/review, label/risk journals, incremental checkpoints, historical vector/draft structures and selected AI-provider settings. Migration 015 adds shared chunk/embedding storage and revision state; 016 adds chunk provenance/context and projected body coverage. Migration 017 adds durable local jobs and model identities without changing historical migrations. Manual sync prepares cached chunks; the explicit local index job generates/persists vectors. Retrieval remains unimplemented. Historical vector tables are not an active index. See [schema responsibilities](docs/ARCHITECTURE.md#implemented-persistence-and-immutable-history) for the preserved migration groups. SQLite files are not encrypted.

Go emits structured JSON diagnostics to stderr; stdout belongs exclusively to private IPC. Logs omit secrets, message content and filesystem paths. This phase has no persistent log files or telemetry.

## Verification

Run from the repository root after installing dependencies:

```sh
gofmt -l backend
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

The architecture's dependency recommendations are adopted only as needed: SQLite itself uses pure-Go modernc (Apple Silicon inference additionally needs CGO/Xcode command-line tools), React/TypeScript/Vite build the UI, Zod validates runtime IPC, and Tauri/Serde provide native integration. The single-instance plugin prevents duplicate desktop data owners. Vitest/Testing Library, ESLint, Prettier and native Go/Rust tests cover this phase. Go OAuth2 and go-keyring supply PKCE/token exchange and secure refresh-token storage. TanStack Query, the full Gmail SDK and AI-provider and the single pinned ONNX binding are adopted; additional AI/vector dependencies remain deferred. Current and planned package responsibilities are recorded in [the architecture](docs/ARCHITECTURE.md); read [AGENTS.md](AGENTS.md) before future implementation.

## Inbox synchronization

Connect Gmail, then select **Sync inbox**. The screen reports messages discovered, imported, updated and errors, and lists cached sender, subject and received time. Configure the per-run maximum before launching:

```sh
MAILPILOT_SYNC_LIMIT=1000 npm run dev
```

The default is 500; accepted values are 1–10,000. The first run imports a bounded Inbox snapshot and stores a history baseline captured before enumeration. Later runs use Gmail history to fetch only changed messages, including label changes and deletions of cached mail. Metadata-only reads preserve valid cached bodies; a no-change run makes no message fetches. Updated counts include replayed observations. Run again when the UI reports more work; large history records and recovery scans resume from saved progress. Expired/invalid history triggers individual reconciliation of cached messages and a fresh bounded Inbox snapshot, never deletion inferred from an incomplete listing. Stop sync cancels pending work; completed transactions remain saved. Older mail outside the initial subset is not backfilled automatically. Sync does not modify labels, classify mail or send messages. Manual classification is available separately below. Attachments are not downloaded; separately stored or unsupported/oversized bodies are reported rather than silently treated as complete. The cache is sensitive, unencrypted local data and persists after disconnect. A disconnected account's cache is hidden until that account reconnects.

Automated sync tests use synthetic JSON fixtures and local HTTP servers with temporary databases; they never access a live Gmail account. To test manually with a dedicated account, connect, sync twice, verify a no-change second run imports nothing, then change a test message’s labels and verify a metadata update without duplicate imports, and disconnect during a run. Native smoke tests run without Google configuration.

## User-defined categories

Connect an account and use **Refresh categories** to manage its local definitions. Seven descriptive defaults are created once per account. Create/edit a name and meaningful description, enable or disable a category, or confirm deletion. A category referenced by cached email assignments or stored analysis cannot be deleted; disable it instead. Definition changes persist in SQLite migration 4 and remain local. Changing categories does not run classification or change email assignments. Manual review uses the starter rule engine described below.

## Local classification review

After connecting and syncing an account, open **Classification review** and choose **Classify this page**. MailPilot reviews up to five cached emails, saves the results, and shows each email’s predicted category, confidence and reasons. Use the previous/next review-page controls to cover more downloaded messages. **Refresh reviews** loads saved assessments without running the classifier. Reading results uses local SQLite and requires no AI provider/key.

Review uses deterministic rules with the existing conservative direct-rule gate
(0.60). No model is constructed, configured or invoked. Rule strength is a heuristic,
not a probability. Ambiguous or weak evidence abstains. Older semantic/hybrid
assessments with a different engine identity/version are stale and need explicit
re-review before a new organization preview. Category descriptions do not train a
model; custom categories without a matching rule may remain unsuggested.

## Safe Gmail organization

After classifying mail, open **Gmail organization**, refresh the selection table, and select predictions. **Dry Run** is on by default: it reads live Gmail labels and shows each proposed label creation/addition without writing Gmail or the action journal. Choose **Back to selection**, turn Dry Run off, and generate a fresh apply preview. Review the current labels, proposed MailPilot label, confidence and operation list; check the confirmation box and apply. Previews expire after five minutes and changed mail/predictions/categories/labels require a fresh preview. Each batch contains at most five emails.

Categories map to `MailPilot/<category name>` user labels. Existing exact-name labels are reused; missing ones are created when a preview is confirmed. MailPilot never deletes labels/emails, archives messages or changes system labels. Definition/name changes affect future mappings without renaming old Gmail labels. Completed writes and partial/uncertain outcomes persist under mailbox_actions. **Stop organization** cancels remaining work; a dispatched write can remain uncertain. **Reconcile interrupted batch** reads Gmail and records what can be established without replaying a write.

**Preview undo last batch** shows only label memberships MailPilot successfully added and verified. Confirm undo separately. Inbox, pre-existing and unrelated Gmail labels are preserved; created labels stay in Gmail. Ambiguous additions are excluded from undo because ownership cannot be proven. Repeated confirmed undo is rejected, and partial/conflicting outcomes remain visible. Concurrent changes to the same label membership cannot be perfectly attributed. Organization uses synthetic tests; no personal mailbox is modified by the validation suite.

## Phishing feature extraction

The Go extractor returns structured address/authentication, URL/link, attachment, HTML-form and language observations with parser limitations. It is separate from scoring and uses no network, LLM or external threat service. Authentication header claims remain unverified; missing content/evidence never establishes safety. The extractor itself produces observations; the separate implemented risk engine scores them, and cached-mail views persist/display the explained result. See [supported signals, limits and synthetic fixtures](docs/PHISHING_FEATURES.md).

### Review high-risk mail in Gmail organization

Choose **Potential Phishing** in **Review suggestions**. HIGH-risk messages show their score, threshold and ordered reasons. Select messages and run the default **Dry Run** to inspect live labels without Gmail or action-journal writes. To apply, turn off Dry Run, generate a fresh preview, review the explanations and exact operations, then explicitly confirm. Missing `Potential Phishing` user labels are created through the existing label workflow. No message is deleted, archived or moved to Spam.

Set `MAILPILOT_RISK_HIGH_THRESHOLD=75` before starting the desktop app to change HIGH from its default of 60; the core also accepts `--risk-high-threshold 75`. Supported values are 31–100 (MEDIUM starts at 30). Restart after changing configuration; existing previews with the old scoring version cannot be applied. Scores are heuristics, not proof of phishing or guarantees of safety. Dry Run may refresh local risk assessments.

**Preview undo last batch** and its separate confirmation remove only verified additions, preserving pre-existing and unrelated labels. Created labels remain in Gmail. Interrupted or uncertain writes require reconciliation first. Schema migration 7 preserves existing plans and makes category references optional for risk proposals.

## Optional AI summaries and historical draft recovery

In **Optional AI settings**, choose an installed provider and an explicit model,
then save its API key. OpenAI is the only currently installed adapter. Keys use
OS credential storage and are scoped by profile, account and provider; settings
never upload mail. AI-disabled mode leaves ordinary mail features available.

Open a cached message and choose **Preview email summary**. Review the exact
bounded payload, provider/model, fixed policy and any truncation; explicitly
approve before choosing **Summarize selected email**. The result contains a brief
summary, explicit requests and stated deadlines. No thread or unrelated mail is
included, and no free-form prompt is accepted. Complete cached bodies are required.
Model output can be wrong; verify it against the source. See [AI boundaries and
limits](docs/GENERATIVE_AI.md).

**Historical draft recovery** can reconcile existing uncertain/conflicting actions
using Gmail reads and record explicit local acknowledgment. It needs no AI key and
cannot create, resume, resend or delete drafts. The only active AI workflow is the
selected-email summary; no reply generation, thread summaries or mailbox chat remain.

Cached message subjects in the mailbox open a reader with
sender, To/CC, timestamp, body, attachment metadata, cached Gmail labels,
MailPilot categories and phishing explanations. Back returns to the existing
results. Reading uses local cached text, including text extracted from HTML-only
messages; email links remain inactive and remote content is never loaded. Long
bodies offer “Load more cached text.” Missing content is reported without a Gmail
fetch. Attachment downloads and rich HTML display are not implemented.

### Documentation and historical compatibility

- [Product plan](docs/PRODUCT_PLAN.md): the sole scope/milestone authority.
- [Architecture](docs/ARCHITECTURE.md): current implementation, intended shared
  pipeline, ownership, source/index lifecycle and security boundaries.
- [IPC contracts](contracts/README.md): implemented methods and synthetic examples;
  archival fixtures do not reactivate retired Ask/search/draft operations.
- [Gmail setup](docs/GMAIL_SETUP.md), [AI boundaries](docs/GENERATIVE_AI.md),
  [risk features](docs/PHISHING_FEATURES.md) and
  [historical draft recovery](docs/REPLY_DRAFTING.md): focused technical references.
- [Documentation audit](docs/DOCUMENTATION_AUDIT.md) and
  [legacy compatibility records](docs/archive/LEGACY_FEATURES.md): disposition of
  superseded plans, migration evidence and earlier technical reviews.

Installed users perform no model installation; native developers prepare pinned
artifacts during the build as described in the local embedding contract. Archived plans and old prompt
numbers are not implementation instructions.

Local inference development, measured model/memory sizes, offline packaging checks
and macOS 13.4+ limitations: [local embedding provider](docs/LOCAL_EMBEDDING.md).

Resumable indexing behavior, coverage definitions, synthetic measurements and native
verification: [local email indexing](docs/LOCAL_INDEXING.md).
