# Local IPC, protocol version 1

Status reconciled with source 2026-10-26. This file documents implemented IPC, not future feature
authorization. [The architecture](../docs/ARCHITECTURE.md) describes the implemented
shared local chunk/vector pipeline; [the product plan](../docs/PRODUCT_PLAN.md)
defines scope/milestones. Local index controls, vector/keyword/hybrid search and
explicitly approved evidence questions are implemented through their narrow typed
commands below. Summary remains implemented. Historical fixtures are
compatibility/negative-test examples; see [legacy notes](../docs/archive/LEGACY_FEATURES.md).

Retired operations: `ask.manage` / `ask_manage` and
`search.semantic` / `semantic_search` are removed along with their old indexing paths.
Current local indexing uses the separate `index.local` contract below. Retired
fixtures and sections below are historical references. `reply.manage` / `reply_manage`
accepts only historical status/reconcile/resolve/cancel. Its request contains no
prompt, body, thread, model, key or creation fields. Old drafting fixtures are archival
and rejected at the active request boundary. Historical migrations/journals remain.
AI settings, selected-email Summary and approved evidence questions are defined
below. Keyword, semantic and hybrid retrieval share `search.local`; there is no
separate keyword endpoint.

The trusted native host launches the fixed bundled Go executable and owns its private stdin/stdout pipes. UTF-8 JSON-RPC 2.0 objects are newline-delimited, with a 64 KiB frame limit, string request IDs (1–64 characters), and no batches or notifications in this bootstrap. One request executes at a time. The Go request timeout is normally five seconds and the host waits seven seconds; classification.review uses 30 seconds in Go and 32 seconds in the host. A broken or timed-out transport is closed and requires an application restart.

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
    "schemaVersion": 21
  }
}
```

`database` may also be `disconnected`. The application version comes from root `package.json` when building the sidecar and must match the Tauri/Cargo versions. Rust validates the response and the frontend validates it again with Zod. The current migration set ends at schema version 21 (email-scoped index/classification invalidation); the example reflects the current schema. Migration 2 introduced normalized mail/journal storage, and migration 1 remains the unchanged settings foundation. The wire protocol remains version 1. Historical fixtures retain their original versions.

The host may send `shutdown` with empty parameters, receiving `{"stopping":true}`, or close stdin for clean shutdown. The UI cannot invoke initialize, shutdown, SQL, shell commands, filesystem paths, or arbitrary method names. Unknown fields/methods, mismatched protocol versions and malformed requests are rejected. Standard JSON-RPC error codes are used, plus `-32001` for a missing handshake; errors contain no underlying database paths or diagnostics. Logs are structured JSON on stderr.

Manual sync now prepares local cached chunks without inference, including a bounded
old/stale-cache pass and post-commit processing of imported/updated messages.
`sync` progress may report `code: "chunking_failed"`: the cache/checkpoint commit
succeeded but local processing could not complete. Original mail stays readable;
missing/stale chunks are retried during later manual sync. This code is accepted by
the strict frontend schema and produces a specific cache-preservation message.
The separate local index command below explicitly authorizes local inference only. Tokenization
and SQLite work are bounded and cancelled with the existing sync session.

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

Each row optionally adds `classification: {state,categoryId,categoryName}`; see
[mailbox-classification-v1.json](mailbox-classification-v1.json). State is unclassified,
suggested, assigned, multiple_suggestions, multiple_assignments or stale. Only a
current single category includes its ID/name (at most 100/101 characters); stale
assessments never expose an old category. This reads saved classifications without
inference. Legacy rows without the field remain decodable. The Your Emails UI
assembles up to 25 rows from bounded ten-row responses, filters by category or
unclassified across the cached list, and sorts the displayed page by category,
unclassified first or newest first. Filtered pagination retains source offsets.

An optional `code` reports local storage failure. Disconnected results have an empty accountId and rows. Go chooses the account from the authenticated session; JavaScript cannot select another account. Rust commands are `sync_start`, `sync_status`, `sync_cancel`, `mailbox_list`. Unknown params fail in Go. Strict Rust/TypeScript result DTOs preserve the protocol-1 boundary; health reports the current migration version documented above. Chunk storage adds no public IPC.

## Category management (protocol v1 additive extension)

`categories_manage({request})` maps only to `categories.manage`. [categories-v1.json](categories-v1.json) is a shared synthetic request/response fixture checked by Go, Rust and TypeScript tests. Category fields are `id`, `name`, `description`, `enabled`, `created_at`, `updated_at`; timestamps are UTC epoch milliseconds. Account context is returned separately as `accountId`.

Requests include `operation`, `accountId`, `offset`, `id`, `name`, `description`, `enabled`. `list` accepts offset 0–1,000,000, empty edit fields and null enabled; an empty account ID resolves the current session. `save` requires current account ID, offset 0, nonblank name (maximum 100 UTF-8 bytes), nonblank description (maximum 1,000 UTF-8 bytes), and boolean enabled. Empty ID creates; nonempty ID edits an existing same-account category. `delete` requires current account ID and category ID, offset 0, empty name/description, and null enabled. Unknown fields and invalid operation combinations are rejected. No classification or assignment operation is exposed.

Responses contain accountId, rows (at most five), hasMore, and optional safe code: validation, unauthorized, conflict, not_found, storage_error. Successful mutations return the first page. Referenced deletions and duplicate names produce conflict; no raw SQL errors leave Go. Disabled categories remain listed. Pagination offsets can shift after edits or deletion; mutation success resets the UI to page one. The existing fixed packaged host/core are updated together; protocol-v1 health/auth/mail DTOs are unchanged.

## Classification review (protocol v1 additive extension)

`classification_review({request})` maps only to `classification.review`. [classification-v1.json](classification-v1.json) is the shared synthetic compatibility fixture checked by Go, Rust and TypeScript.

Requests contain exactly operation (`list` or `run`), accountId and offset (0–1,000,000). An empty accountId is permitted for list to resolve the current session; run requires the current account’s ID. Go checks authorization and account ownership. Calls have a 30-second Go deadline (32-second native wait) and process/list at most five cached emails, with hasMore for pagination. Run affects only local assessments. It cannot accept email content, category IDs, provider instructions or model output from JavaScript.

Responses contain accountId, rows, hasMore, processed (successfully saved assessments, including abstentions), errors (per-row failures), and optional safe code. Each row includes email_id, sender, subject, bodyState, state, predictions, nullable timestamp and optional code. Row states are not_reviewed, unclassified, classified, stale, error. At most one prediction is returned per email. Only classified rows carry predictions; only classified/unclassified rows carry a timestamp. A prediction includes email_id, category_id, category_name, confidence (greater than zero through one), classifier_name, classifier_version, reasons (one to six nonblank strings, maximum 160 UTF-8 bytes each), and timestamp (UTC epoch milliseconds). IDs and classifier metadata are bounded; raw message bodies/HTML are never returned. A prediction’s email identity and timestamp must match its row.

Top-level safe codes are validation, unauthorized, conflict, cancelled, invalid_message, storage_error. Row error codes are conflict, cancelled, invalid_message, storage_error. Successful page processing can have per-row errors, and previously saved rows survive partial failure. Stale results are hidden until explicit recomputation. List does not rerun classification. Existing protocol-v1 DTOs are unchanged; bundled core/host/UI are shipped together.

## Gmail organization (protocol v1 additive extension)

`organization_manage({request})` maps only to `organization.manage`. [organization-v1.json](organization-v1.json) is the shared synthetic Go/Rust/TypeScript compatibility fixture. Requests contain operation, accountId, offset (0–1,000,000), emailIds (unique local IDs, at most 25), actionId and digest. Unknown fields and unsafe combinations are rejected in Go and Rust. Operations are list/status, preview/dry_run with a nonempty selection, undo_preview, apply with the saved action ID and 64-character digest, reconcile and cancel. Any network operation requires the current explicit account ID. No provider IDs, content, SQL, arbitrary deltas or target label names can be submitted by JavaScript. Optional `suggestionMode` is `choose` (default) or `highest` for classification list/preview/dry_run only. Optional preview/dry_run `choices` are unique `{emailId,categoryId}` pairs for selected emails; Go accepts a category only when it is an eligible member of that email’s fresh assessment, preserving manual-preference priority. These fields must be absent for apply/undo/status/reconcile/cancel and phishing operations. The local `suggestions` read requires one selected email and the explicit account, returning all current automatic options for that email without network access or inference.

Responses always include accountId, state, code, id, digest, kind, expiresAt (UTC milliseconds), dryRun, rows, steps and hasMore. Rows contain emailId, sender, subject, currentLabels, proposedLabel, confidence and change (candidate/unclassified/add/remove/none). Optional updatedLabels contains the current cached Gmail label names following a verified addition/removal; currentLabels retains the immutable before-preview snapshot. Optional display enrichment is omitted rather than hiding a saved action when it would exceed the frame cap. Steps contain kind (create_label/add/remove), name, emailId, state and safe code. Rows/steps are bounded to 25/50. Local list pages contain up to 25 emails. Dry run has no action ID/digest and cannot be applied. No message bodies or credentials are returned. Codes are validation, unauthorized, busy, stale_preview, not_found, cancelled, reauth_required, unsupported, operation_failed and reconciliation_required, or the empty string on success. Step codes describe conflicts/uncertainty/cache failures without provider bodies.

[organization-batch-v1.json](organization-batch-v1.json) qualifies the 25-email/50-step extension in Go, Rust and TypeScript. The older five-email fixtures and journal digests remain unchanged. This raises batch bounds only; authorization, fresh-plan validation, journal-before-dispatch, uncertainty reconciliation and undo ownership checks remain in force. The UI uses three stages: select up to 25 emails, review only those emails with Confirm Changes/Go Back, and apply the exact confirmed saved action/digest. Successful apply returns to selection with a verified confirmation and Undo as the only result action. Undo requires its own saved preview and confirmation. Uncertain or partial outcomes retain recovery controls. The read-only dry_run operation remains supported for compatibility but is not a separate UI step. Preview fetches and validates full Gmail snapshots for the selected emails and publishes that bounded set through the existing source repository before reading classification evidence. This repairs cached representations retained by sync without requiring another sync. Source changes invalidate derived data transactionally; shared text preparation repairs the refreshed source snapshot and reuses exact compatible vectors before reassessment, with no new embedding inference. An unavailable fresh category remains ineligible for a label plan. Incomplete provider reads cannot replace a complete cached body. Source, classification and label freshness are checked again before dispatch, so changes after review still reject apply. Category filters scan the account-scoped cached list using the existing list operation and the complete paginated category source. No filter grants write authority.

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
active commands. The legacy rule branch accepts deterministic `rule_confidence` scores and an empty
`assessment.candidates` compatibility array. Current semantic review uses the separately
versioned extension below; the retired fixture is not revived. Historical SQLite
candidate/score data remains decodable, but old engine versions are stale and
are not exposed as current predictions. No `embedding_unavailable` error is emitted.

## AI settings and selected-email summaries (protocol v1)

[ai-settings-v1.json](ai-settings-v1.json) defines `ai.settings` / `ai_settings`:
status, configure, disable. Fields: operation, accountId, provider, model, apiKey.
Configure requires the current account, a registered provider and model; blank key
uses only that provider's saved OS key. Status accepts no model/provider/key fields;
disable accepts only accountId. Unknown fields reject. Responses return account,
selected provider/model, enabled, registered providers `{id, external}` and safe code;
API keys are never returned. Provider selection is persisted by additive migration 014.

[email-summary-v1.json](email-summary-v1.json) defines `email.summarize` /
`email_summarize`: preview, approve, status, cancel, clear. Fields: operation,
accountId, emailId, previewId, jobId, approved. All operations require exactly one
selected cached account/email ID. Approve requires its preview ID and `approved:true`;
cancel requires its job ID; other operations reject approval/job/preview fields.
There are no prompts, raw email bodies, thread selectors, tools or model overrides.

Responses have state idle/preview/running/completed/error, scoped identities,
provider/model/external, preview/job ID, expiry, exact email/policy disclosure,
truncated flag, nullable result and safe code. Result is
`{summary, requests: string[], deadlines: string[]}`. Input is capped at 6,000 UTF-8
bytes; summary at 4,000; each extraction array at ten verbatim source excerpts of
500 bytes each. Unknown/unsupported output rejects. Raw provider response/error
bodies never cross IPC. Responses are capped at 48 KiB after JSON escaping.
Consent is single use, expires in five minutes and is invalidated by source,
settings, saved-key identity, account or reader-lifecycle changes. The key fingerprint
stays in backend memory; it is not an IPC field. The adapter verifies the loaded
key immediately before dispatch. Provider calls are bounded background
jobs; status never uploads mail and cancellation suppresses late output.

## Approved email questions (additive protocol-v1 extension)

Tauri `email_answer` forwards to Go `email.answer`. [email-answer-v1.json](email-answer-v1.json)
contains shared retrieve/approve/preview/cited-result fixtures. [EMAIL_QUESTIONS.md](../docs/EMAIL_QUESTIONS.md)
defines exact consent, evidence, cancellation and validation behavior. Ship strict
Go/Rust/TypeScript components together. Retired `ask.manage` remains rejected.

Requests have exactly `operation`, `accountId`, `question`, `filter` (the same sender,
after, before, assigned-category fields as local search), `jobId`, `previewId`,
`digest`, `sourceIds`, `approved`. Operations are retrieve/select/approve/status/cancel/clear.
Retrieve requires current nonempty account, question (1–2,048 UTF-8 bytes), valid
filter and empty controls/false approval. Status may use empty account to discover
the authorized view, optional 32-character job ID, empty question/filter/controls.
Clear requires account and empty controls. Cancel requires account/job. Select
requires the current 64-character preview ID and 1–8 unique retrieved source aliases
S1–S8; other controls are empty/false. Approve requires current account, preview ID,
64-character exact digest and `approved:true`; it accepts no question, filter,
sources, provider or model override. All unused scalar fields are empty/zero,
arrays empty and booleans false. No source text, SQL, vectors, endpoint, tool list,
thread expansion, account selection or action permission is accepted.

Responses require accountId, state (idle/retrieving/preview/running/completed/error),
jobId, previewId, digest, expiresAt, question, provider, model, external, policy,
payload, evidence, selectedIds, coverage, mode, warning, exhaustive, aggregate,
nullable result and code. `exhaustive` is always false. Coverage `{eligible,semantic,keyword}`
is account cache coverage; mode is empty/keyword/hybrid and warning empty or
semantic_unavailable. A preview includes fixed policy, complete canonical external
user-message JSON, selected source aliases and five-minute expiry. Source selection
issues a fresh preview/digest. Only explicit approve can dispatch a generative request.
Running/completed results omit payload/policy/token/digest. Results remain memory-only.

Evidence is at most eight records: sourceId, emailId, chunkId, sourceRevision,
readerRevision, subject, sender, receivedAt, excerpt, partialContent. Source aliases
are Go-issued S1–S8, revisions lowercase SHA-256, IDs at most 100/128 bytes,
subject/sender 120, excerpt 600 UTF-8 bytes, date UTC epoch milliseconds. Selection
and citations must reference actual returned evidence. Canonical user-message JSON
is at most 12 KiB after escaping; all IPC responses at most 48 KiB. Go reconstructs
it only from account-scoped current shared search results, never frontend text.

Result is `{outcome,statements,limitations}`; outcome answered/insufficient/conflicting.
At most six statements `{text,citations}` with text up to 800 bytes and 1–3 citations
`{sourceId,quote}` with a nonempty exact substring of that approved excerpt, at most
300 bytes. Unknown IDs/quotes, uncited/unsupported statements and output fields reject.
Insufficient outcomes require a limitation. Conflicting outcomes require two distinct
source/quote pairs. Providers return up to five 300-byte limitations; Go adds a sixth
mandatory source-scope warning. Native/frontend validation rechecks known IDs and
quote membership before escaped rendering/source navigation. Citation provenance
is not factual entailment. Common mailbox-wide aggregate/completeness/absence
questions cannot authorize paid generation (`non_exhaustive`).

Safe codes include unauthorized, validation, cancelled, unsupported, busy, ai_disabled,
ai_credentials_unavailable, stale_preview, not_found, ai_authentication_failed,
ai_rate_limited, ai_refused, ai_incomplete, ai_invalid_response, operation_failed,
non_exhaustive, response_too_large and local search's safe retrieval/storage codes.
Errors expose no raw provider response/body/diagnostics. Failed calls consume consent;
no automatic paid retry. Session/settings/key/source/filter changes revoke the
relevant context; actual dispatch key identity is checked. Source/current generation
is checked before dispatch/display. Status does not infer or generate. Existing
5/7-second transport deadlines serve prompt control responses while one transient
AIService worker performs bounded retrieval or generation. No new durable queue,
Gmail/SQL/search tool, mailbox write or reply/sending authority is added.

## Historical draft recovery (restricted protocol v1)

[draft-recovery-v1.json](draft-recovery-v1.json) defines the active `reply.manage` /
`reply_manage` subset. Requests contain operation, accountId, jobId, actionId,
approved. Status rejects control fields; reconcile requires account/action; resolve
also requires explicit approval after reconciliation; cancel requires account/job.
Responses contain accountId, state, jobId, to, subject, actionId, draftId, reconciled,
code. No historical body, consent context, editable preview or AI settings is exposed.
Recovery does not need AI and cannot create drafts. The old
[reply-drafting-v1.json](reply-drafting-v1.json) remains an archival/rejection fixture.

### Cached message reader (compatible v1)

`mailbox_read` forwards to `mail.read`. The strict request is
`{ "accountId": "fixture-account", "emailId": "fixture-message", "offset": 0, "revision": "" }`.
Both IDs are required and limited to 100 UTF-8 bytes. Offset is a UTF-8 byte
boundary from 0 to 1 MiB. Continuations require the 64-character opaque SHA-256 of projection version plus
source hash returned as revision. It is not the original email content hash.
Changed content or cleanup version returns `stale`; mismatched accounts return
`unauthorized`. No provider fetch or mailbox write is available from this command.

[mail-reader-v1.json](mail-reader-v1.json) is the synthetic success contract,
checked by Rust and TypeScript tests. Responses carry up to 16 KiB of body text
and at most 48 KiB of encoded JSON. `nextOffset` may advance less than 16 KiB
because JSON escaping expands text. Continue until `nextOffset === totalBytes`.
Metadata includes sender, To, CC, cached labels, attachment metadata, assigned
categories, a fresh saved category suggestion and local versioned phishing analysis.
`metadataTruncated` explicitly reports bounded metadata omissions. Timestamps are
UTC epoch milliseconds. No renderable HTML or attachment bytes cross this command. Reader body text uses
the same versioned artifact cleanup as the chunker, preserving useful image
descriptions and quoted/forwarded text. Raw cached mail remains unchanged. See
[clean text contract](../docs/CLEAN_TEXT.md).

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
needed), and `snapshotLimited` (older Inbox mail remains in the durable scan).
Omitted counts/flags mean zero/false. Each run can import/refresh up to `limit`
Inbox identities plus a separate `limit` history/recovery/retry observations.
`discovered` counts attempted observations, including failures and repeated history
changes. `capped` means the bounded Inbox scan has more work; another manual run
continues older mail. A completed run with nonzero errors is shown as partial success.

The private checkpoint adds versioned Inbox pagination, exact pending page IDs and
at most 500 failed-read IDs. Existing JSON checkpoints decode without a migration;
a legacy limited snapshot restarts enumeration and skips already cached IDs, while
an interrupted legacy snapshot first repairs its newest bounded batch. Successful
observations and failed-read retry identities commit atomically with their position.
Per-message read failures continue the batch; account access, rate-limit, storage
and cancellation failures stop. Failed source/checkpoint writes roll back together.
After history drains, the maintenance allowance retries at most 50 failed-read IDs,
then rotates remaining work across incomplete cached bodies. Same-run repeated
incomplete retries are suppressed. These operations perform no inference.
See the [source lifecycle](../docs/SYNC_LIFECYCLE.md).
No history cursor, provider IDs or email content is exposed by status.

[sync-incremental-v1.json](sync-incremental-v1.json) is a shared synthetic example
validated by Rust and TypeScript alongside the existing fields.

### Retired Ask and passage-search examples

The [Ask fixture](ask-v1.json) and semantic-search fixture above remain historical
examples only. Their frontend schemas, application services and native handlers
are removed; `ask.manage` and `search.semantic` always reject as unknown methods.
They provide no arbitrary prompt, model configuration or indexing entry point.

## Local Email Index (additive protocol-v1 extension)

Tauri `local_index` forwards the strictly typed `request` to Go `index.local`.
See [local-index-v1.json](local-index-v1.json) for the shared fixture and
[LOCAL_INDEXING.md](../docs/LOCAL_INDEXING.md) for persistence/coverage semantics.
The request has exactly `operation` (`status`, `start`, `pause`, `resume`, `cancel`,
`rebuild`) and `accountId` (at most 100 bytes). Mutations require a nonempty account
matching the current Go session. Status may use an empty ID to select that session.
No caller-controlled source text, model, path, vectors, limits or executable exists.

This remains protocol 1 because it adds a separately allowlisted method and capability
with version-matched packaged components; old semantic/Ask methods remain
rejected. Commands use the ordinary five/seven-second transport deadlines and
return promptly while one local worker runs. The response is bounded scalar metadata
validated by Go/Rust/TypeScript fixtures, not streamed vectors or generic jobs.

`status` is `idle`, `pending`, `running`, `paused`, `cancelled`, `interrupted`,
`complete`, `partial` or `failed`. `pending` is projected when a finished run has
new/stale work. Account/model/fingerprint/dimension/generation identify the compatible
view. Counts cover eligible/indexed/complete/partial/failed/pending/unavailable
messages, current chunks and current saved embeddings. See the fixture for exact
camelCase names. Coverage counts are mutually exclusive; indexed includes complete
plus partial-body messages whose available vectors are all saved. No unavailable
body can produce complete coverage. A failed provider can leave valid source mail
pending. Every field is required, including `code` (empty on success).

Safe codes are `unauthorized`, `validation`, `unavailable`, `busy`, `storage_error`,
`interrupted`, `cache_changed`, `model_changed`, `preprocessing_changed`, `model_unavailable`,
`inference_failed`, `pending_changes`. Errors never contain mail, paths or native
runtime diagnostics. Disconnect/session replacement cancels work. Neither startup
nor polling invokes inference; explicit Resume is required after interruption.
Indexing calls neither Gmail nor external AI and changes no existing AI consent.

## Local search (additive protocol-v1 extension)

Tauri `vector_search` forwards to `search.local`; see [vector-search-v1.json](vector-search-v1.json),
[hybrid-search-v1.json](hybrid-search-v1.json) and [behavior/evaluation](../docs/HYBRID_SEARCH.md).
The retired `search.semantic` operation remains rejected. Protocol remains 1;
ship version-matched strict Go/Rust/TypeScript components together.

Requests retain `operation`, `accountId`, `jobId`, `query`, `limit`, `filter`
(`sender`, `after`, `before`, `categoryId`). Start optionally adds `mode`:
`keyword`, `semantic`, `hybrid`; omission preserves semantic behavior. Start requires
an authorized nonempty account, empty job ID, bounded nonempty query and 1–20 maximum
emails. Query is limited to 2,048 UTF-8 bytes; semantic inference also enforces the
pinned 254-token budget. Dates are UTC epoch milliseconds, after inclusive/before
exclusive; zero omits. Sender is a literal substring and category means assigned
local category. Status/cancel omit mode and use empty query, zero limit, empty filter.
Cancel requires account/job; status permits empty account/job. Job IDs are 32 characters.
No raw vectors, model, path, SQL/FTS syntax, endpoint or AI options exist.

Responses retain `accountId`, `jobId`, `status` (idle/running/complete/cancelled/error),
`code`, `emails`, `scannedChunks`, `elapsedMs`, `searchableMessages`, `eligibleMessages`.
Additional required fields are `mode`, `requestedMode` (empty or keyword/semantic/hybrid),
`warning` (empty or `semantic_unavailable`), and `keywordMessages`. Hybrid can complete
in keyword mode with that warning when local query inference is unavailable. Keyword
mode performs no inference. Semantic-only retains its safe errors. Coverage describes
the authorized whole cache, independent of query filters: searchable is complete
compatible semantic coverage; keyword includes current prepared partial text.
`scannedChunks` counts branch candidate visits, so hybrid can count a chunk twice.

Emails retain original source metadata, reader revision and 1–3 evidence records;
additional required email fields are `rankScore` (finite), `keywordRank`, `semanticRank`
(0–100; zero means absent), `exactMatch`, `partialContent`. Rank score is passage-local term relevance
in keyword mode (no cross-account corpus statistics), cosine in semantic mode, RRF sum in hybrid mode;
exact literal hits receive priority in keyword/hybrid. Each branch votes once per
email, with deterministic ties. Evidence retains `chunkId`, `score`, `excerpt`,
`sourceRevision` and adds `scoreKind: cosine | keyword`. Score is raw cosine [-1,1]
for cosine evidence and zero for keyword evidence, never a confidence probability.
Reader revision is the shared readable projection cursor; source revision is the
original content SHA-256. Both branches recheck source/generation/filter membership
before publication and completed-result access; retained vector evidence still
requires complete compatible vectors, while keyword evidence does not.

Safe codes remain empty, unauthorized, validation, unavailable, busy, storage_error,
model_unavailable, inference_failed, stale, cancelled, timeout, too_large. Mail is
returned only with complete/empty-code status. Bounds remain 20 emails, 3 excerpts
of 600 UTF-8 bytes each, 240 bytes per subject/sender and 48 KiB encoded JSON.
Larger output fails closed. One cancellable 30-second worker serves explicit starts;
status/cancel retain ordinary 5/7-second transport deadlines and never infer.
Completed snapshots expire in five minutes and clear on session/source revocation.
No Gmail request, generative AI call, persistent search history or automatic indexing.

## Local category representations (protocol-v1 extension)

Tauri `category_vectors` forwards strictly typed requests to `categories.vectors`.
The [shared fixture](category-vectors-v1.json) and [category contract](../docs/CATEGORY_VECTORS.md)
define the account-scoped representation. Required request fields are `operation`,
`accountId`, `categoryId`, `emailId`, `revision`, `approved`. Inspect requires empty
email/revision and false approval. `confirm_example` requires a current reader
revision (64 lowercase hex), email ID and true explicit approval; `remove_example`
requires email ID, empty revision and false approval. Go rechecks ownership and
original source revision transactionally. None of these operations invoke inference.

Every response requires `accountId`, `categoryId`, `status`, `code`, `sourceHash`,
`modelId`, `modelVersion`, `dimension`, `recipe`, `definitionInputs`, `examples`.
Status is pending/ready/partial/disabled, or unavailable on error. Safe codes are
empty, validation, unauthorized, unavailable, not_found, stale, cancelled,
storage_error, too_large. Errors return empty account/category and no source text.
Only ready/partial include a current source hash. Up to twenty exact definition
inputs (16 KiB JSON), ten examples, three chunks each, 120-byte subjects, full
subject/section context up to 1,024 bytes, and 160-byte UTF-8 passage excerpts
with a `truncated` flag. Examples carry email ID, confirmed reader revision and
included/index_pending/source_changed status. Chunk IDs refer to the shared store.
The entire encoded response is capped at 48 KiB, otherwise fails closed.

`index.local` now also requires `eligibleCategories`, `readyCategories`,
`partialCategories`, `pendingCategories`; ready + partial + pending equals eligible.
These fields extend the packaged v1 response and are updated in Go/Rust/TypeScript
together. Existing email counts retain their meaning. Start/Resume builds pending
category prototypes after shared mail vectors; existing Pause/Cancel controls apply.
No model, vectors, caller-provided text, paths, SQL, classification or external AI
authority crosses this boundary.

## Shared semantic classification (protocol-v1 extension)

[classification-local-v1.json](classification-local-v1.json) is the new active shared
fixture. `classification.review` / `classification_review` retains list/run, account
authorization, five-email IPC pages and existing deadlines. The UI assembles up to
25 emails from five sequential responses, rechecks account ownership and duplicate
IDs, and directs local overrides to the corresponding five-email subpage. Optional `emailId`,
`categoryId`, `revision` are forbidden on list/run. New assigned/unclassified/auto
operations require the explicit current account, email and its 64-hex reader
revision. Only assigned accepts/requires category ID. These mutate a local primary
preference, not Gmail, examples or embedding configuration.

Responses optionally add up to 100 enabled `categoryOptions` `{id,name}`. Semantic
rows include `readerRevision` and `semantic` with state, modelVersion,
classifierVersion, sourceRevision, minimumScore, minimumMargin, assigned and scores.
New row states are suggested and assigned; unclassified remains an abstention.
A single assignment has one typed user-assignment display result, multiple legacy
assignments have none. Legacy classified rows remain decodable. Reviewed semantic
rows require a timestamp; stale/errors hide evidence. Prediction score kinds are
rule_confidence, cosine_similarity or user_assignment. Numeric confidence retains
the existing storage/wire field name but is never a probability; assignment 1 is a
typed explicit choice. Signed cosine candidate values remain in [-1,1].

Semantic suggestions include every enabled category above the 0.20 score floor and
within 0.03 (inclusive) of the best score, ordered by score then category ID. Up to
100 predictions are allowed only for suggested semantic rows; rule and manual
assignment display results remain single-category. Include the top three category
scores plus every suggested category, up to 100, with four chunk evidence records per score,
160 UTF-8 bytes per excerpt, 4096 contributing chunks and 48 KiB encoded page.
Scores include category ID/name, score, prototype hash/status, contributing chunk
count and evidence `{chunkId,section,text,similarity,weight}`. Full category scores
are stored with the new semantic evidence version. The compatibility
`assessment.candidates` field remains empty; old semantic fixtures still reject.

[organization-suggestions-v1.json](organization-suggestions-v1.json) qualifies the additive request/response fields in Go, Rust and TypeScript. In default `choose` mode, rows with multiple automatic suggestions use `multiple_suggestions` and `suggestionCount` (1–100). A separate local `suggestions` request for one email returns its complete `suggestions` array of `{categoryId,name,confidence}` (at most 100), keeping ordinary 25-email pages below the frame cap. A preview choice is batch-specific and never writes a local primary preference. In `highest` mode the existing leader is proposed directly (score then category ID). Single automatic suggestions and existing single manual assignments work in either mode. Legacy multiple manual assignments remain unresolved, with no `suggestionCount`; they are never collapsed automatically. Suggestion options grant no Gmail write authority. Organization rows optionally add scoreKind for clear cosine/rule/user presentation.
New immutable preview rows privately persist `semantic-organization-v1` evidence;
that field is omitted from old journal rows and is never caller supplied. Existing
apply confirmation/digest, cancellation, journaling, reconciliation and undo remain.
`index.local` additionally requires assessedMessages and pendingAssessments.
These count current assessments, including abstention, separately from vectors.
No new native command, capability, external AI permission or classification queue
is added. See [semantic classification](../docs/SEMANTIC_CLASSIFICATION.md).

Organization freshness after migration 021 uses per-email index revisions while
shared category/example changes retain account-wide invalidation. Identical semantic
reassessment timestamps do not revoke otherwise identical reviewed evidence. Legacy
plan digests/timestamp checks remain unchanged. The UI reloads current selection
data on Go Back and page return; batch-specific choices must be selected again.

[organization-conflict-v1.json](organization-conflict-v1.json) adds optional
`conflictReason` to a `stale_preview` response: classification_changed, mail_changed,
labels_changed, target_label_changed, expired or plan_changed. These fixed identifiers
contain no email or provider diagnostics. The UI explains the specific conflict and
requires a new preview; no rejected confirmation authorizes a write. A valid changed
full Gmail read updates the local source/chunk cache so returning to selection can
show current data, preserving the immutable plan and complete cached bodies.

A successful label-create response lacking owner type is verified by GET of its
exact returned label ID, requiring matching ID/name and user type. POST is never
retried. Failure to verify remains uncertain and requires reconciliation. A prior
uncertain creation is not retroactively claimed; a fresh preview may reuse a
currently existing user label with separately reviewed membership additions.
