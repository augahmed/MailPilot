# MailPilot

Local-first intelligent Gmail management built with Go, React + TypeScript,
Tauri 2 and SQLite. The core uses one shared local preprocessing, chunking,
embedding and vector-retrieval pipeline supporting semantic search, category
suggestions and optional answers grounded in approved email evidence.

For a demonstration with no Google or OpenAI setup, run **`npm run dev:demo`**.
The isolated synthetic mailbox supports the real reader, keyword search,
classification and label previews. Explicit local indexing enables semantic and
hybrid retrieval; Gmail writes and external AI are disabled. **`npm run build:demo`**
creates a separate MailPilot Demo application. See the [demo guide](docs/DEMO.md)
for prerequisites, signing, sample restoration and a walkthrough.

**Current status:** Gmail OAuth/keyring, bounded initial/incremental sync, cached
cleaned plain-text reading, category management, deterministic review/risk explanations,
confirmed label organization, selected-email Summary and historical draft recovery
are implemented. Manual sync also prepares deterministic local text chunks with
bundled MiniLM WordPiece tokenization, section provenance and pending embedding
rows. The Local Email Index card explicitly starts/resumes a durable local MiniLM
indexing run over all live cached mail, with pause/cancel/rebuild and coverage counts.
Semantic search now uses the shared local vectors, with sender/date/assigned-category filters and revision-bound excerpts that open the original cached email. Shared semantic classification now consumes these vectors and category prototypes; keyword and hybrid search now share the same passages; bounded evidence questions now use separately approved selected excerpts and validated citations ([contract](docs/EMAIL_QUESTIONS.md)). Historical vector tables are inert; installed users need no model/runtime setup. OpenAI is the only installed functional generative adapter.

Use **Ask about email** to retrieve local excerpts, inspect/select the exact evidence and approve one cited answer through your selected AI provider. Ordinary features work with external AI disabled.

Use **Email search** to choose keyword, semantic or hybrid retrieval. Manual sync prepares keyword passages without inference; **Start indexing** in Local Email Index supplies semantic vectors. Hybrid falls back to keywords if query embeddings are unavailable. Coverage and partial content are explicit. See [hybrid behavior, held-out evaluation and latency](docs/HYBRID_SEARCH.md).

The tokenizer is embedded in Go; the MiniLM model/native CPU runtime ship as fixed app resources; no user Python or
post-install model download. Target English first and about 10,000 cached messages.
AI replies/composition, new Gmail drafts, sending and Chrome integration are outside
the immediate scope. Existing Summary remains until a later tested transition.

Start with the [documentation index](docs/README.md):
[Gmail setup](docs/GMAIL_SETUP.md), [architecture](docs/ARCHITECTURE.md),
[privacy and local data](docs/PRIVACY.md), and
[dated quality/security evaluation](docs/EVALUATION.md). The evaluation records
unresolved findings; this developer build is not a verified public release.
See [SECURITY.md](SECURITY.md) for the threat model, reporting route and known
limits, and the [2026-10-26 security follow-up](docs/EVALUATION.md#security-follow-up-2026-10-26)
for the targeted dependency patches and remaining platform warnings. The mail
cache remains unencrypted by MailPilot; use a dedicated test account for demos.
Engineering scope remains in [AGENTS.md](AGENTS.md) and the
[product plan](docs/PRODUCT_PLAN.md). [Historical records](docs/archive/README.md)
preserve migration/recovery evidence without defining current capabilities.

The desktop application opens a native window, starts the fixed Go backend and
checks SQLite. Before Gmail is connected it shows the sign-in screen; a healthy
storage check appears in the footer as **Local storage ready · v0.1.0**. Connecting
Gmail opens the system browser. Reading cached mail requires an authorized session.

Gmail OAuth, OS credential storage, bounded initial/incremental sync, cached reading,
editable categories, deterministic category review, explained phishing risk and
reviewed label organization remain active. Follow [Gmail setup](docs/GMAIL_SETUP.md)
with a dedicated test account. AI settings and read-only historical draft recovery
remain available independently of retired generation workflows.

## Prerequisites

- Go **1.27.3+** (the security patch required by `backend/go.mod`; Go may download it automatically).
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

The native build and smoke test target Apple Silicon macOS. Local inference needs
macOS 13.4+; ordinary features retain the app's macOS 13.0 baseline. Windows/Linux
and other architectures require native qualification and currently have an
unavailable embedding adapter. Cross-compilation is deliberately rejected by the
sidecar build script; build on the target OS/architecture.

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

For a personal Apple Silicon macOS app that opens from Finder with your own
Google Desktop client configuration, follow [Gmail setup](docs/GMAIL_SETUP.md):

```sh
npm run dev:personal -- --google-client-json "/absolute/path/outside-repo/client.json"
# Stop development before building:
npm run build:personal -- --google-client-json "/absolute/path/outside-repo/client.json"
codesign --force --deep --sign - .local/personal/MailPilot.app
open .local/personal/MailPilot.app
```

Personal builds contain extractable desktop-client configuration and belong only
to their owner. Tokens and OpenAI keys are never packaged. The following standard
build omits the personal default and still needs runtime Google configuration for
sign-in. Neither workflow completes public Google verification or notarization.

On macOS, from the repository root:

```sh
npm run build -- --bundles app
codesign --force --deep --sign - src-tauri/target/release/bundle/macos/MailPilot.app
open src-tauri/target/release/bundle/macos/MailPilot.app
```

The build includes frontend type checking, a production Vite build, the Go executable, and native Rust compilation. The resulting application runs without Vite, Node, Go or Cargo installed. The `codesign` step above adds an **ad-hoc signature for local developer testing**; it does not provide Developer ID signing, notarization or public-distribution qualification. `npm run build` without `--bundles app` builds the platform's default installer formats; public distribution signing/notarization is not configured.

The packaged macOS executable is `src-tauri/target/release/bundle/macos/MailPilot.app/Contents/MacOS/mailpilot`. Native binaries and generated assets are ignored by Git; the source SVG, manifests, `package-lock.json`, `backend/go.sum` and `src-tauri/Cargo.lock` are source-controlled.

## Configuration and local storage

The app does not automatically load `.env`. Personal helpers accept an external Google Desktop JSON and embed only the client ID/secret in Go; standard builds exclude that default. A directly launched app accepts a complete runtime `MAILPILOT_GOOGLE_CLIENT_ID` / `MAILPILOT_GOOGLE_CLIENT_SECRET` override pair; partial overrides fail, and two explicit empty values disable OAuth. Configuration never enters the frontend. User access/refresh tokens are never read from environment variables; refresh tokens use the OS keyring. See [setup and precedence](docs/GMAIL_SETUP.md#configuration-precedence-and-standard-builds). Defaults come from the OS application data directory; the native host passes that default to Go. Standalone core startup resolves the corresponding platform directory itself.

| Setting        | Default                                                 | Override                                                                      |
| -------------- | ------------------------------------------------------- | ----------------------------------------------------------------------------- |
| Data directory | Per-user app-data directory for `com.mailpilot.desktop` | `MAILPILOT_DATA_DIR` (absolute dedicated directory) or core `--data-dir`      |
| Log level      | `info`                                                  | `MAILPILOT_LOG_LEVEL` or core `--log-level`: `debug`, `info`, `warn`, `error` |

Core flags take precedence over environment variables, then defaults. Environment overrides also work when launching the desktop executable from a terminal. Use a dedicated directory: the core creates it with private permissions and enforces those permissions on existing directories.

Default desktop locations:

- macOS: `~/Library/Application Support/com.mailpilot.desktop/mailpilot.db`
- Windows: `%APPDATA%\com.mailpilot.desktop\mailpilot.db`
- Linux: `${XDG_DATA_HOME:-~/.local/share}/com.mailpilot.desktop/mailpilot.db`

SQLite uses WAL, a five-second busy timeout, foreign keys on each connection, FULL synchronization, and one pooled connection. Startup applies transactional numbered SQL migrations with recorded checksums; altered migration history and future schemas are rejected. Current migrations 001–021 retain mail/cache, category/review, label/risk journals, incremental checkpoints, historical vector/draft structures and selected AI-provider settings. Migration 015 adds shared chunk/embedding storage and revision state; 016 adds chunk provenance/context and projected body coverage. Migration 017 adds durable local jobs and model identities without changing historical migrations. Manual sync prepares cached chunks; the explicit local index job generates/persists vectors. Shared exact retrieval is implemented. Migration 018 adds category prototypes and explicit examples through the same local coordinator; 019 adds versioned semantic assessments and manual primary preferences. Migration 020 adds an FTS5 index over the shared passages, maintained by triggers. Migration 021 scopes index-driven classification invalidation to the affected email while retaining shared category/example fences. Historical vector tables are not an active index. See [schema responsibilities](docs/ARCHITECTURE.md#implemented-persistence-and-immutable-history) for the preserved migration groups. SQLite files are not encrypted.

Go emits structured JSON diagnostics to stderr; stdout belongs exclusively to private IPC. Logs omit secrets, message content and filesystem paths. This phase has no persistent log files or telemetry.

SQLite startup also validates and repairs permissions on existing WAL/SHM/rollback
journals, rejects unsafe linked files, keeps query temporaries in memory, and
enables ordinary row secure-delete on every connection. These protections do not
encrypt the cache or guarantee erasure. **Privacy and local data** is available
before sign-in and in the account menu; see [privacy behavior and limits](docs/PRIVACY.md).
Disconnect retains local mail/recovery data and does not remove AI provider keys.
Remove the saved provider key in Optional AI settings before disconnecting; failed
key removal can be retried while AI is disabled.

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
    embedding/                         Shared local MiniLM provider and model identity
    ai/ ai/openai/                     Optional generation and provider transport
    risk/                              Structured offline phishing observations
frontend/
  src/app/                     Status screen, styles and UI tests
  src/features/                Gmail, reader, categories, review, indexing, AI and recovery
  src/lib/ipc/                 Validated typed application commands
src-tauri/
  src/                         Native lifecycle and private pipe bridge
  capabilities/                Narrow main-window application permissions
  icons/source.svg             Icon source; raster assets generated at build
contracts/                     Versioned IPC and historical rejection fixtures
scripts/                       Sidecar build and native smoke test
docs/ARCHITECTURE.md            Overall design and implementation status
AGENTS.md                      Engineering rules
```

Go owns business behavior and persistence, including reviewed Gmail label plans and undo in `internal/app`. Rust only starts the fixed packaged binary, validates a bounded protocol, and handles native lifecycle; it uses `std::process` rather than installing a shell plugin. The frontend cannot run shell commands, access arbitrary files/SQL, select executables, or invoke arbitrary Go methods. A versioned handshake precedes database initialization; frontend capabilities allow only the commands listed in [the IPC contracts](contracts/README.md). The host alone opens a validated Google authorization URL in the system browser.

The architecture's dependency recommendations are adopted only as needed: SQLite itself uses pure-Go modernc (Apple Silicon inference additionally needs CGO/Xcode command-line tools), React/TypeScript/Vite build the UI, Zod validates runtime IPC, and Tauri/Serde provide native integration. The single-instance plugin prevents duplicate desktop data owners. Vitest/Testing Library, ESLint, Prettier and native Go/Rust tests cover this phase. Go OAuth2 and go-keyring supply PKCE/token exchange and secure refresh-token storage. Local inference uses the single pinned ONNX binding. Gmail and OpenAI adapters use bounded HTTP transport from Go's standard library. Current package responsibilities are recorded in [the architecture](docs/ARCHITECTURE.md); read [AGENTS.md](AGENTS.md) before future implementation.

## Inbox synchronization

Connect Gmail, then select **Sync Gmail**. The screen reports messages discovered, imported, updated and errors, and lists cached sender, subject and received time. Configure the per-run maximum before launching:

```sh
MAILPILOT_SYNC_LIMIT=1000 npm run dev
```

The default is 500; accepted values are 1–10,000. The first run imports the newest
Inbox messages up to that limit and captures a history baseline before listing.
Each later manual sync continues into older Inbox mail from its durable page
position. A separate allowance of the same size handles mailbox history, recovery
and retries, so label changes do not consume the older-mail import allowance.
New messages use a full read; unchanged cached bodies use metadata-only reads.
Once Inbox enumeration and pending work are complete, a no-change run downloads
no complete messages. This is Inbox coverage, not a full mailbox/Sent/Spam/Trash import.

Page IDs, remaining messages and failed-read identities are stored in the existing
checkpoint transaction. A per-message read failure leaves the original cache intact,
records a bounded retry, and lets later messages proceed. Partial results stay visible.
Account access, rate-limit, storage and cancellation failures stop safely without
skipping unfinished work. Later runs retry failed reads and incomplete cached bodies.
Expired history reconciles cached identities through authoritative reads; missing
items in a listing never imply deletion. Expired Inbox page tokens restart a bounded
scan that walks past cached IDs to continue older imports.

**Stop sync** cancels pending work; completed transactions remain saved. Sync does
not modify Gmail labels, classify mail, generate embeddings or send messages.
Attachments are not downloaded; separately stored or unsupported/oversized bodies
remain explicitly incomplete. The local cache is sensitive and unencrypted, persists
after disconnect, and stays hidden until the same account reconnects.

Automated tests use synthetic mail and GET-only local HTTP servers with temporary
databases. They qualify 500 → 1,000 → 1,250 cached Inbox messages across service
restarts, a failed message in the middle of a batch, durable retry, cancellation,
legacy checkpoints, page-token recovery, history replay and verified deletions.
No live Gmail account is used by these tests. Ordinary native smoke tests disable
Google configuration. `npm run test:personal` checks JSON import;
`npm run test:personal:package` qualifies synthetic personal and standard bundles,
including clean-environment launch and signed/offline resources. See
[the verification boundaries](docs/GMAIL_SETUP.md#verification-boundaries).

## User-defined categories

Connect an account and open **Categories & Classification** to manage its local
definitions. Six descriptive defaults are created once per account: Jobs, School,
Receipts, Finance, Newsletters and Personal. Edited or referenced legacy definitions
are preserved. Create/edit names and descriptions, enable/disable categories, or
confirm deletion of an unreferenced category. Definition changes remain local and
invalidate dependent representations/assessments; they do not authorize Gmail writes
or automatically assign mail. **Show representation** explains contributing text
and explicitly confirmed examples. Start or resume **Local Email Index** to build
pending prototypes; the shared coordinator then assesses mail after prototype work.
See [category vectors](docs/CATEGORY_VECTORS.md) and
[semantic classification](docs/SEMANTIC_CLASSIFICATION.md).

## Local classification review

After connecting and syncing, open **Categories & Classification** and choose
**Classify this page**. The UI reviews up to 25 cached emails using bounded five-email
IPC pages. It reuses saved compatible email vectors and category prototypes, with
decisive rule fallback and abstention. This action does not run embedding inference
or external AI. **Refresh reviews** loads saved results without reassessment.

Review distinguishes Suggested, Assigned and Unclassified mail, explains evidence,
and supports explicit local primary preferences. Cosine similarity and rule strength
are different heuristics, not calibrated probabilities. Missing/incomplete vectors,
weak evidence and conflicts can leave mail unclassified. Category descriptions
are embedded for prototypes rather than training a new model. Changed source,
category or model identities invalidate dependent results before Gmail preview.

## Safe Gmail organization

Open **Gmail Organization**, refresh the selection table, and select up to 25 eligible
emails. **Preview Label Changes** refreshes the selected source snapshots and opens
a separate review screen. Inspect only the selected changes, then choose **Confirm
Changes** to apply that exact saved plan or **Go Back** to revise the selection.
Previews expire after five minutes; changed source/category/classification/label
evidence requires a fresh preview. Successful apply returns to selection with a
verified confirmation and **Undo**. Uncertain or partial outcomes retain recovery
controls. The typed read-only `dry_run` operation remains for compatibility, but
it is not a separate step in the current UI.

Categories map to `MailPilot/<category name>` user labels. Existing exact-name labels are reused; missing ones are created when a preview is confirmed. MailPilot never deletes labels/emails, archives messages or changes system labels. Definition/name changes affect future mappings without renaming old Gmail labels. Completed writes and partial/uncertain outcomes persist in the organization journal. **Stop** cancels remaining work; a dispatched write can remain uncertain. Recovery reads Gmail and records what can be established without replaying a write.

**Undo** prepares a preview of only label memberships MailPilot successfully added and verified. Confirm undo separately. Inbox, pre-existing and unrelated Gmail labels are preserved; created labels stay in Gmail. Ambiguous additions are excluded from undo because ownership cannot be proven. Repeated confirmed undo is rejected, and partial/conflicting outcomes remain visible. Concurrent changes to the same label membership cannot be perfectly attributed. Organization uses synthetic tests; no personal mailbox is modified by the validation suite.

## Phishing feature extraction

The Go extractor returns structured address/authentication, URL/link, attachment, HTML-form and language observations with parser limitations. It is separate from scoring and uses no network, LLM or external threat service. Authentication header claims remain unverified; missing content/evidence never establishes safety. The extractor itself produces observations; the separate implemented risk engine scores them, and cached-mail views persist/display the explained result. See [supported signals, limits and synthetic fixtures](docs/PHISHING_FEATURES.md).

### Review high-risk mail in Gmail organization

Choose **Potential Phishing** in **Review suggestions**. HIGH-risk messages show
their score, threshold and ordered reasons. Select messages, choose **Preview Label
Changes**, inspect the exact operations, and explicitly confirm the saved preview.
Missing `Potential Phishing` user labels are created only through that confirmed
workflow. No message is deleted, archived or moved to Spam.

Set `MAILPILOT_RISK_HIGH_THRESHOLD=75` before starting the desktop app to change HIGH from its default of 60; the core also accepts `--risk-high-threshold 75`. Supported values are 31–100 (MEDIUM starts at 30). Restart after changing configuration; existing previews with the old scoring version cannot be applied. Scores are heuristics, not proof of phishing or guarantees of safety. Preparing a preview may refresh local risk assessments.

**Undo** and its separate preview/confirmation remove only verified additions, preserving pre-existing and unrelated labels. Created labels remain in Gmail. Interrupted or uncertain writes require reconciliation first. Schema migration 7 preserves existing plans and makes category references optional for risk proposals.

## Optional AI questions, summaries and historical draft recovery

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
cannot create, resume, resend or delete drafts. The active AI workflows are the
selected-email summary and separately approved cited evidence questions; no reply generation, thread summaries or unrestricted mailbox chat.

Cached message subjects in the mailbox open a reader with
sender, To/CC, timestamp, body, attachment metadata, cached Gmail labels,
MailPilot categories and phishing explanations. Back returns to the existing
results. Reading uses local cached text, including text extracted from HTML-only
messages; email links remain inactive and remote content is never loaded. Long
bodies offer “Load more cached text.” Missing content is reported without a Gmail
fetch. Attachment downloads and rich HTML display are not implemented.

### Documentation and historical compatibility

Use the [documentation index](docs/README.md) for the short getting-started path
and grouped technical references. The [archive](docs/archive/README.md) keeps
superseded plans and dated review evidence separate from current contracts.
Historical fixtures never reactivate retired Ask/search/draft operations.

Installed users perform no model installation. Developers prepare pinned resources
during the build; [local embedding](docs/LOCAL_EMBEDDING.md) documents offline
verification and native limits. Future task prompts are engineering plans, not
current features or implementation authorization.

## Semantic classification

Start or resume **Local Email Index** to assess cached email after shared vectors
and category prototypes are ready. **Classification review** also offers explicit
25-email review without inference, using bounded five-email IPC responses. It shows Suggested, Assigned and Unclassified
states, raw cosine/rule evidence, matching passages and local primary overrides.
Confirming examples remains a separate deliberate action. Gmail label preview,
confirmation, journaling, reconciliation and verified undo remain required.
See [the classification contract and held-out evaluation](docs/SEMANTIC_CLASSIFICATION.md):
the user-selected 0.03 lead returns multiple close suggestions above the 0.20 score
floor. Held-out per-suggestion precision is 60.7% without examples and 66.0% with
one confirmed example per category. Coverage is 100% on this small synthetic fixture,
including its out-of-scope cases; this does not establish production accuracy.
