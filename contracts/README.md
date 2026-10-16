# Local IPC, protocol version 1

Active contraction status (2026-10-16): `ask.manage` / `ask_manage` and
`search.semantic` / `semantic_search` are removed, including indexing. Their
fixtures and sections below are historical references. `reply.manage` / `reply_manage`
accepts only status, configure/disable for retained provider settings, and
reconcile/resolve/cancel for historical actions. All previous prepare/generate/
preview_draft/create/undo_preview requests reject even with approval. Thread or
arbitrary prompt fields cannot be supplied to retained operations. Historical
preview bodies are never restored as editable/creatable sessions; returned preview
and review IDs are empty. Existing outcome records and immutable migrations remain.
A cached-message summary endpoint and keyword-search endpoint do not yet exist.

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

The Tauri `health` command takes no arguments. Rust sends:

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

Streaming progress, generic background-job APIs and large payload transport remain unimplemented. Bounded sync and mailbox commands are described below. Authentication uses its own bounded background lifecycle, described below.

## Gmail connection commands (additive protocol-v1 extension)

| Tauri command      | Go method         | Result                                                                       |
| ------------------ | ----------------- | ---------------------------------------------------------------------------- |
| `gmail_status`     | `auth.status`     | Current status; starts a serialized refresh when access-token expiry is near |
| `gmail_connect`    | `auth.begin`      | Starts browser authorization and returns status immediately                  |
| `gmail_check`      | `auth.check`      | Starts saved-credential refresh/profile verification and returns status      |
| `gmail_disconnect` | `auth.disconnect` | Cancels pending work and starts local removal/revocation                     |

Every command accepts **no arguments**; Go methods accept absent params or `{}` only. Configuration, callback data, token values, endpoints, provider IDs and account selectors cannot be supplied by the WebView.

Status examples:

```json
{ "state": "connecting" }
```

```json
{ "state": "connected", "email": "reader@example.test" }
```

```json
{ "state": "error", "code": "consent_denied" }
```

```json
{ "state": "disconnected", "code": "revocation_failed" }
```

States: `unconfigured`, `disconnected`, `checking`, `connecting`, `connected`, `disconnecting`, `error`, `reauth_required`. Only `connected` carries an email address. Optional `code` values are allowlisted by the frontend: `oauth_not_configured`, `authentication_failed`, `credential_store_unavailable`, `consent_denied`, `required_scope_missing`, `reauth_required`, `authentication_timeout`, `authentication_cancelled`, `account_storage_failed`, `account_cleanup_failed`, `revocation_failed`. Operational failures are statuses, so expected consent/network/keyring failures do not kill the bridge.

The **host-only** `auth.begin` result is `{ "status": { "state": "connecting" }, "authorizationUrl": "https://accounts.google.com/o/oauth2/v2/auth?..." }`. The URL is omitted when no flow starts. Rust validates the exact HTTPS origin/path and opens it using a fixed system-browser launcher; it strips the URL before returning status to React. No generic opener capability is granted. On launch failure, Rust invokes host-only `auth.cancel` to cancel the pending sign-in without deleting existing credentials. Frontend disconnect also cancels, then removes credentials and attempts revocation.

Long-running work has a three-minute context and 20-second HTTP timeouts. Status polling is read-only except scheduled refresh; it does not fetch mail. Native keyring prompts may outlast context cancellation because the library does not expose interruptible calls. Unknown result fields are rejected by Rust/TypeScript DTOs. Ordinary OAuth errors never carry provider response bodies, token payloads or callback query strings across IPC. Protocol tests verify handshake/parameter restrictions and reject token retrieval methods.

## Protocol 1 additive sync/mailbox methods

`sync.start` and `sync.status` accept only empty params. Both return, for example:

```json
{
  "state": "completed",
  "accountId": "synthetic-account",
  "limit": 500,
  "discovered": 2,
  "imported": 1,
  "updated": 1,
  "errors": 0,
  "capped": false
}
```

States: idle, running, completed, error, cancelled. Optional `code` is a safe diagnostic identifier. No provider IDs, tokens or body content are exposed. `updated` counts refreshed existing records, including identical snapshots. Start requires an authenticated account session; disconnect cancels the worker.

`mail.list` accepts `{"offset":0}` (integer 0–1,000,000) and returns at most ten rows:

```json
{
  "accountId": "synthetic-account",
  "rows": [
    {
      "id": "local-id",
      "sender": "Example",
      "subject": "Synthetic subject",
      "receivedAt": 1700000000123,
      "bodyState": "complete"
    }
  ],
  "hasMore": false
}
```

An optional `code` reports local storage failure. Disconnected results have an empty accountId and rows. Go chooses the account from the authenticated session; JavaScript cannot select another account. Rust commands are `sync_start`, `sync_status`, `sync_cancel`, `mailbox_list`. Unknown params fail in Go. Strict Rust/TypeScript result DTOs preserve the protocol-1 boundary; schemaVersion in health now reports 12.

## Category management (protocol v1 additive extension)

`categories_manage({request})` maps only to `categories.manage`. [categories-v1.json](categories-v1.json) is a shared synthetic request/response fixture checked by Go, Rust and TypeScript tests. Category fields are `id`, `name`, `description`, `enabled`, `created_at`, `updated_at`; timestamps are UTC epoch milliseconds. Account context is returned separately as `accountId`.

Requests include `operation`, `accountId`, `offset`, `id`, `name`, `description`, `enabled`. `list` accepts offset 0–1,000,000, empty edit fields and null enabled; an empty account ID resolves the current session. `save` requires current account ID, offset 0, nonblank name (maximum 100 UTF-8 bytes), nonblank description (maximum 1,000 UTF-8 bytes), and boolean enabled. Empty ID creates; nonempty ID edits an existing same-account category. `delete` requires current account ID and category ID, offset 0, empty name/description, and null enabled. Unknown fields and invalid operation combinations are rejected. No classification or assignment operation is exposed.

Responses contain accountId, rows (at most five), hasMore, and optional safe code: validation, unauthorized, conflict, not_found, storage_error. Successful mutations return the first page. Referenced deletions and duplicate names produce conflict; no raw SQL errors leave Go. Disabled categories remain listed. Pagination offsets can shift after edits or deletion; mutation success resets the UI to page one. The existing fixed packaged host/core are updated together; protocol-v1 health/auth/mail DTOs are unchanged.

## Classification review (protocol v1 additive extension)

`classification_review({request})` maps only to `classification.review`. [classification-v1.json](classification-v1.json) is the shared synthetic compatibility fixture checked by Go, Rust and TypeScript.

Requests contain exactly operation (`list` or `run`), accountId and offset (0–1,000,000). An empty accountId is permitted for list to resolve the current session; run requires the current account’s ID. Go checks authorization and account ownership. Calls have the existing five-second deadline and process/list at most five cached emails, with hasMore for pagination. Run affects only local assessments. It cannot accept email content, category IDs, provider instructions or model output from JavaScript.

Responses contain accountId, rows, hasMore, processed (successfully saved assessments, including abstentions), errors (per-row failures), and optional safe code. Each row includes email_id, sender, subject, bodyState, state, predictions, nullable timestamp and optional code. Row states are not_reviewed, unclassified, classified, stale, error. At most one prediction is returned per email. Only classified rows carry predictions; only classified/unclassified rows carry a timestamp. A prediction includes email_id, category_id, category_name, confidence (greater than zero through one), classifier_name, classifier_version, reasons (one to six nonblank strings, maximum 160 UTF-8 bytes each), and timestamp (UTC epoch milliseconds). IDs and classifier metadata are bounded; raw message bodies/HTML are never returned. A prediction’s email identity and timestamp must match its row.

Top-level safe codes are validation, unauthorized, conflict, cancelled, invalid_message, storage_error. Row error codes are conflict, cancelled, invalid_message, storage_error. Successful page processing can have per-row errors, and previously saved rows survive partial failure. Stale results are hidden until explicit recomputation. List does not rerun classification. Existing protocol-v1 DTOs are unchanged; bundled core/host/UI are shipped together.

## Gmail organization (protocol v1 additive extension)

`organization_manage({request})` maps only to `organization.manage`. [organization-v1.json](organization-v1.json) is the shared synthetic Go/Rust/TypeScript compatibility fixture. Requests contain operation, accountId, offset (0–1,000,000), emailIds (unique local IDs, at most five), actionId and digest. Unknown fields and unsafe combinations are rejected in Go and Rust. Operations are list/status, preview/dry_run with a nonempty selection, undo_preview, apply with the saved action ID and 64-character digest, reconcile and cancel. Any network operation requires the current explicit account ID. No category/provider IDs, content, SQL, arbitrary deltas or target label names can be submitted by JavaScript.

Responses always include accountId, state, code, id, digest, kind, expiresAt (UTC milliseconds), dryRun, rows, steps and hasMore. Rows contain emailId, sender, subject, currentLabels, proposedLabel, confidence and change (candidate/unclassified/add/remove/none). Steps contain kind (create_label/add/remove), name, emailId, state and safe code. Rows/steps are bounded to five/ten. Dry run has no action ID/digest and cannot be applied. No message bodies or credentials are returned. Codes are validation, unauthorized, busy, stale_preview, not_found, cancelled, reauth_required, unsupported, operation_failed and reconciliation_required, or the empty string on success. Step codes describe conflicts/uncertainty/cache failures without provider bodies.

Network jobs return running immediately and are polled with status; short IPC calls retain the five-second deadline. Jobs have a three-minute timeout and session-bound cancellation. Go owns all authorization, preview persistence, digest/expiry checks, dispatch/reconciliation and undo. Rust exposes only the specific allowlisted command. A preview does not authorize a write; the frontend requires explicit confirmation for apply and for undo. Legacy protocol-v1 DTOs remain unchanged and the fixed core/host/frontend are packaged together.

## Protocol 1 additive phishing assessment

Each `mail.list` row may include `phishing` (absent means analysis unavailable):

```json
{
  "score": 7,
  "risk": "LOW",
  "version": "risk-1/phishing-features-1/<configuration-sha256>",
  "reasons": [
    {
      "code": "credentials",
      "points": 15,
      "explanation": "Credential-related language (may also be legitimate)"
    },
    {
      "code": "aligned_links",
      "points": -8,
      "explanation": "All observed web links align with the sender site (weak signal)"
    },
    {
      "code": "authentication_unknown",
      "points": 0,
      "explanation": "Authentication is unverified; header claims do not affect this score"
    }
  ]
}
```

Score is an integer 0–100; risk is LOW, MEDIUM or HIGH. Reasons contain signed integer points, stable codes and plain-text explanations, with at most 12 entries. Positive contributions sort descending, then reductions, then zero-point limitations. The total is clamped. The version includes the exact weights/thresholds fingerprint. These are heuristic indicators, never confirmed phishing or guaranteed safety. Results are local, account-scoped, persisted and recomputed on content/configuration changes. Mailbox polling only scores missing/stale assessments; there is no new command or remote write. Older rows without assessments remain valid. Old strict clients/hosts must be upgraded with the core to accept this additive field.

## Reviewed phishing organization (protocol 1 additive extension)

[organization-phishing-v1.json](organization-phishing-v1.json) is checked by Go, Rust and TypeScript. Organization requests accept optional `source: "phishing"` only for list, preview and dry_run. Absent/empty source retains category organization. Apply, undo, status, reconcile and cancel must omit it; their saved action determines the evidence. Unknown sources, supplied labels/scores/thresholds and automatic-apply fields are rejected.

Risk candidate/preview rows propose the exact `Potential Phishing` label and include optional `phishing: {score, risk: "HIGH", threshold, version, reasons}`. Reasons have the same signed-points shape as mailbox assessments. `score >= threshold`, with threshold 31–100, default 60. Classification confidence is zero for risk rows. Additional row changes are `below_threshold` and `risk_unavailable`, neither selectable. Other row fields, steps, batch limits and explicit digest-based confirmation remain unchanged; user-label step names now also accept the exact `Potential Phishing` name. Dry Run has no applyable action ID and makes no Gmail or action-journal writes, but may update local risk assessments.

Migration 7 retains old category plans; optional phishing evidence is stored in the immutable plan JSON. Old fixtures without source/evidence remain valid. Ship the updated core, strict host DTOs and frontend together. No new native command or permission was added.

## Retired semantic contracts (historical protocol-v1 fixtures)

[classification-semantic-v1.json](classification-semantic-v1.json) and
[search-semantic-v1.json](search-semantic-v1.json) remain archival synthetic examples.
Their query/index services, handlers, DTOs and UI are deleted. They do not define
active commands. Active classification accepts deterministic `rule_confidence`
scores and an empty `assessment.candidates` compatibility array. Historical SQLite
candidate/score data remains decodable, but old engine versions are stale and
are not exposed as current predictions. No `embedding_unavailable` error is emitted.

## Reviewed reply drafting (protocol v1 additive extension)

The main-window `reply_manage` command maps only to `reply.manage`.
[reply-drafting-v1.json](reply-drafting-v1.json) is the shared Go/Rust/TypeScript
compatibility fixture. Ship the strict host, frontend and core together.

Requests use operation, accountId, emailId, includeThread, instruction, tone,
previewId, jobId, actionId, approved, body, model and apiKey. Operations are status,
configure, disable, prepare, generate, preview_draft, create, reconcile, resolve,
undo_preview and cancel. Unknown fields/operations and unrelated nonempty fields
are rejected. IDs are at most 100 UTF-8 bytes, instruction 1,000, reviewed body
6,000, model 128, inbound API key 2,048. Tones are Professional, Friendly, Concise
and Formal. Status may omit account identity; every other operation requires the
authorized account. No sending or destructive operation is accepted.

Prepare selects cached context locally. Generate requires the exact AI preview
ID and approved=true; preview_draft requires the current review ID and edited
body; create requires the saved action ID, digest and approved=true, with no
replacement body. Resolve requires a completed reconciliation and explicit
approval. Cancel requires the current job ID. Settings, generation, creation and
reconciliation run as bounded cancellable jobs polled through status.

Responses contain accountId, state, configured, provider, model, external, jobId,
previewId, reviewId, expiresAt (UTC epoch milliseconds), context, instruction,
policy, to, subject, body, sourceCount, truncated, actionId, draftId, reconciled,
undoAvailable and a fixed safe code. There is no API key response field.
Context is at most 6,000 bytes, instruction 1,500, policy 1,000, recipient 320,
subject 512, body 6,000 and sourceCount 0–5; total responses are capped at 48 KiB.
States are idle, configuring, ai_preview, generating, editing, draft_preview,
creating, reconciling, created, uncertain, conflict, resolved and error.
UndoAvailable is always false; undo_preview reports undo_unavailable.

Preview contents are sensitive and rendered only as escaped text. Pending AI
consent is memory-only. Migration 11 persists reviewed Gmail draft plans and
outcomes; no secret or AI prompt history is stored. Disconnect revokes consent,
cancels work and suppresses late results. Uncertain remote writes remain in the
journal and cannot be automatically replayed. The native smoke test exercises
this command's unauthorized-account rejection.

### Cached message reader (compatible v1)

`mailbox_read` forwards to `mail.read`. The strict request is
`{ "accountId": "fixture-account", "emailId": "fixture-message", "offset": 0, "revision": "" }`.
Both IDs are required and limited to 100 UTF-8 bytes. Offset is a UTF-8 byte
boundary from 0 to 1 MiB. Continuations require the 64-character content hash
returned as revision. Changed content returns `stale`; mismatched accounts return
`unauthorized`. No provider fetch or mailbox write is available from this command.

[mail-reader-v1.json](mail-reader-v1.json) is the synthetic success contract,
checked by Rust and TypeScript tests. Responses carry up to 16 KiB of body text
and at most 48 KiB of encoded JSON. `nextOffset` may advance less than 16 KiB
because JSON escaping expands text. Continue until `nextOffset === totalBytes`.
Metadata includes sender, To, CC, cached labels, attachment metadata, assigned
categories, a fresh saved category suggestion and local versioned phishing analysis.
`metadataTruncated` explicitly reports bounded metadata omissions. Timestamps are
UTC epoch milliseconds. No HTML or attachment bytes cross this command.

Errors use `code`: unauthorized, validation, not_found, stale, cancelled,
storage_error or too_large; all content fields are empty and message is null.
The UI clears content on stale/session errors, ignores late unmounted responses,
and rechecks cache authorization every five seconds while open. Suggestions are
separate from assigned categories. Reading never runs classification or embeddings.

### Incremental sync status (compatible v1)

`sync.cancel` accepts `{}` and returns the same status DTO as start/status. The
native command is `sync_cancel`; it cancels the active job without clearing saved
observations. The UI polls until the worker reports cancelled. Unknown parameters
remain rejected. Start/cancel do not accept an account ID; the current authorized
account session owns the job.

Optional additive status fields are `mode` (initial, incremental, recovery),
`removed` (nonnegative confirmed cache removals), `more` (another manual run is
needed), and `snapshotLimited` (older uncached Inbox mail was outside the bounded
snapshot). Omitted counts/flags mean zero/false. `discovered` now counts change
observations, which may refresh one message multiple times across history records.
`capped` retains its initial snapshot meaning. Body-unavailable warnings can coexist
with a successfully advanced checkpoint; failed reads/writes leave unfinished work.
No history cursor, provider IDs or email content is exposed by status.

[sync-incremental-v1.json](sync-incremental-v1.json) is a shared synthetic example
validated by Rust and TypeScript alongside the existing fields.

### Retired Ask and passage-search examples

The [Ask fixture](ask-v1.json) and semantic-search fixture above remain historical
examples only. Their frontend schemas, application services and native handlers
are removed; `ask.manage` and `search.semantic` always reject as unknown methods.
They provide no arbitrary prompt, model configuration or indexing entry point.
