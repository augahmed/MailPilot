# Approved email questions with cited evidence

Implemented 2026-10-20 through `email.answer` / Tauri `email_answer`. The existing
hybrid retriever, SQLite owner, local embedding provider, AI provider registry,
selected provider/model and provider-scoped OS key storage are reused. Summary and
historical recovery remain available. There are no new schema migrations,
dependencies, hosted search services, persistent answer tables, autonomous actions
or reply/sending capabilities.

## User workflow

1. In **Ask about email**, submit a bounded email-information question. Retrieval
   runs locally against current SQLite chunks with the existing hybrid search.
   The form supports literal sender and UTC date filters; the typed Go operation
   also preserves the existing assigned-category filter.
2. Inspect up to eight source excerpts from the top five distinct emails. Select
   the sources to disclose; at least one is required. Each source shows sender,
   subject, date, original email/chunk IDs, source and reader revisions and partial
   cached-content status. Open an original cached message without changing Gmail
   read state. Search material outside the selected sources is never included in
   the provider request.
3. Inspect the **Exact external request**: the fixed policy and complete canonical
   user-message JSON, question, selected source metadata/text, provider/model and
   five-minute expiry. Selection creates a new preview/token/digest and clears
   approval. Nothing is sent to a generative provider during retrieval, selection,
   preview, status or settings.
4. Check the per-request disclosure approval and explicitly choose **Approve and
   generate cited answer**. One approval permits one bounded provider dispatch.
   Existing Summary enablement/approvals cannot authorize this separate operation.
5. Read a source-limited answer, insufficient-evidence outcome or conflicting
   evidence. Each generated statement requires citations to approved source aliases
   and exact quotes. Citation buttons open only the original Go-validated sources.

Provider charges and retention policies apply. Cancellation suppresses late output
and revokes local permission; it cannot recall content already dispatched. Failures
consume approval and are never automatically retried. A new retrieval/preview and
explicit approval are required to try again.

## Shared implementation and bounds

`app.AIService` now supports both Summary and questions through its existing single
transient worker, mutex, cancellation, generation and shutdown join. It creates no
durable question queue or separate index. `EnableAnswers` receives the same
`retrieval.VectorSearch` instance as search. Shutdown joins AI retrieval/generation
before closing the local index/provider or SQLite.

The retrieval request is account-scoped and uses `SearchMode(..., "hybrid")` with
the existing filters and compatibility gates. Keyword fallback works when local
query embeddings fail or are unavailable. Coverage describes the authorized cached
account, not Gmail history. The account-statistics side channel discovered in the
keyword engine is fixed: FTS supplies account-filtered candidates; passage-local
term coverage/saturated frequency scores use no global corpus statistics. See
[hybrid retrieval](HYBRID_SEARCH.md) for its formula and regression measurements.

Go builds evidence from real returned chunk/source fences, verifies ownership,
source/reader revisions and email/chunk identity, and preserves email deduplication.
It takes each email's best excerpt before extra passages, at most eight total.
It does not load complete email bodies, hidden threads, attachments or the mailbox
as generation context. Each excerpt is at most 600 UTF-8 bytes; subject/sender are
at most 120 each; questions are at most 2,048. Canonical evidence JSON is capped at
12 KiB after JSON escaping. Oversized contexts fail closed. No silent expansion or
provider-side retrieval is possible.

Normalized `AIProvider` adds:

```go
AnswerWithEvidence(context.Context, ai.AnswerRequest) (ai.AnswerResult, error)
```

The request contains the question, selected evidence and a private key fingerprint;
`Input()` builds the complete canonical user-message JSON. Evidence includes source
alias `S1`–`S8`, email ID, chunk ID, source/reader SHA-256 revisions, bounded metadata,
excerpt and partial-content flag. Input declares `selected_cached_excerpts_only`
and `exhaustive:false`. Fingerprints are not sent externally or exposed in IPC;
generic request formatting/JSON redacts sensitive input. Adapters receive neither
repositories nor Gmail credentials, SQL, search tools or action permissions.

OpenAI remains the only installed functional external adapter; tests register a
second synthetic adapter to validate provider substitution and separate keys.
The OpenAI adapter reuses its existing bounded HTTPS Responses transport, fixed
endpoint, selected model, no proxy forwarding, redirect rejection, `store:false`,
non-streaming response and no history/tools. Fixed instructions and exact approved
input are the only model messages. The response envelope rejects tool calls and
unsupported content. Transport behavior follows the [official OpenAI documentation](https://developers.openai.com/api/docs/guides/migrate-to-responses);
`store:false` does not claim zero provider retention.

## Consent and invalidation

One-use random preview IDs and digests bind the account, private client/session,
exact payload/order/policy, destination/model, expiry and server-held key identity.
Question/filter changes clear the workflow; selection issues a new token/digest.
Provider/model/key changes and disable operations clear pending questions through
the existing settings path. Same-account session replacement also invalidates.

Before approval, immediately before dispatch, after generation and on result access,
Go rechecks session, settings, OS key identity and selected source/generation/filter
fences. The OpenAI adapter additionally compares the actual dispatch key with the
approved fingerprint after loading it. Changed, deleted, purged, incompatible or
filter-excluded selected sources revoke results. Unselected source changes cannot
silently expand the approved context and do not revoke otherwise valid selected
evidence; selecting a changed candidate fails closed.

Retrieval has a 30-second deadline. Generation has a 75-second outer job and the
adapter's existing one-minute HTTP bound. Both share the same single-worker gate
with Summary. Clear/cancel/session changes suppress late results, including a
provider that returns after cancellation. Expiry is enforced on access/dispatch;
the UI polls and clears on unmount. Pending requests/answers are bounded, memory-only
and cleared on lifecycle changes. No browser storage, logging of content, SQL answer
history or application telemetry is added. This is not a forensic-erasure claim.

## Citation validation and uncertainty

Provider output is strict JSON containing `outcome`, `statements` and `limitations`.
Allowed outcomes are `answered`, `insufficient`, `conflicting`. At most six statements
of 800 UTF-8 bytes each, each with one to three `{sourceId,quote}` citations.
Every source must be in the approved selection; every quote must be a nonempty
verbatim substring of that approved excerpt, at most 300 bytes. Unknown/invented
IDs, invented quotes, uncited statements, duplicate citations within a statement,
unknown output fields and unsupported tool calls reject the response. Conflicting
outcomes require at least two distinct supporting source/quote pairs. Insufficient
outcomes require an explicit limitation and may contain no statements.

Adapters may return five limitations of 300 bytes each; Go appends its mandatory
scope limitation without dropping provider text. All displayed text is escaped;
model URLs/HTML are never activated. Whole IPC responses are capped at 48 KiB
after JSON escaping, otherwise fail closed.

No bounded top-K selection proves mailbox-wide completeness, even at 100% cached
index coverage. Common aggregate/completeness/absence questions are conservatively
blocked locally before paid generation (`non_exhaustive`); the fixed policy also
forbids global claims. The heuristic can reject some bounded wording and is not a
general intent classifier. Every answer is visibly limited to selected excerpts.
Zero retrieved sources produce a local insufficient-evidence outcome with no paid
request. Partial bodies/indexes remain explicitly visible. A provider must report
missing or contradictory evidence; citation checks verify provenance, **not factual
entailment or reliable recognition of every conflict/prompt injection**. This work
does not claim paid-model answer quality or a calibrated answerability threshold.

## IPC, compatibility and verification

See [email-answer-v1.json](../contracts/email-answer-v1.json) and the
[IPC contract](../contracts/README.md). This is an additive protocol-1 command with
strict version-matched Go/Rust/TypeScript DTOs and one native permission. Legacy
`ask.manage`, `search.semantic`, draft generation and sending remain unavailable.
No arbitrary provider/model, raw source text, SQL, endpoint, tool list or mailbox
write option can be supplied to the question operation.

Adversarial tests cover email prompt injection as literal untrusted data; fixed
policy/exact request serialization/no tools; invented source IDs/quotes and uncited
claims; wrong-account retrieval/approval; stale sources, confirmed deletion,
selection, expiry, session/settings/key changes; incomplete evidence; conservative
aggregate refusal; source-limited conflicts/insufficiency; cancellation/late results;
single use, safe errors/no retries; Unicode/escaped payload bounds and provider
substitution. React tests cover approval, exact inspection, invalidation, source links,
partial coverage, aggregate refusal and cancellation. Strict shared fixtures are
checked in all three languages. Existing summary/recovery tests remain in force.

```sh
cd backend
go test ./...
go vet ./...
go test -race ./...
# From the repository root:
npm run typecheck
npm run lint
npm test
npm run build:frontend
npm run test:answers:native
npm run test:search:hybrid
cargo fmt --manifest-path src-tauri/Cargo.toml --check
cargo clippy --manifest-path src-tauri/Cargo.toml --all-targets -- -D warnings
cargo test --manifest-path src-tauri/Cargo.toml
```

The native question script uses real pinned local embeddings/hybrid retrieval,
synthetic SQLite mail and a **synthetic generation provider**, with strict CGO checks
and all network denied. It verifies exactly one approved dispatch and exact selected
payload/citations, with **zero paid calls**. The ignored report is
`.cache/email-answer-qualification.json`. Native startup/IPC/rendering and signed
offline model/package checks use the existing smoke scripts.

## Validation record (2026-10-20)

Passed on this Apple Silicon implementation host:

- Full Go tests, vet and race suite; affected AI/app/IPC/retrieval/store checks
  repeated after final production changes. Synthetic local HTTP listeners only;
  no paid generation or live mailbox access.
- Frontend strict typecheck, lint with zero warnings, 101 tests across 25 files,
  production build and repository formatting check.
- Rust fmt, clippy with warnings denied and 21 tests, including the shared strict
  question fixture and native command authorization.
- Network-denied native local indexing, category vectors, semantic classification,
  vector search, hybrid held-out retrieval and approved-question qualification,
  all with pinned resources and strict CGO checks. The question test records one
  explicit synthetic generation, one selected chunk and zero accepted invented
  citations. See [retrieval accuracy and latency](HYBRID_SEARCH.md) for fixed-query
  results and the 50,000-chunk scale run, including the unmet latency target.
- Fresh desktop `.app` build, development ad-hoc signature and signed/offline
  embedding package smoke, plus copied debug/release resource layouts. Native
  WebView-to-Rust-to-Go lifecycle/authorization smoke includes `email_answer`.
- Go formatting, `git diff --check` and changed-document link paths.

No dependency installation, secret/database/model build output, paid model call or
live mailbox mutation was added to source. Paid-model factual answer quality,
Windows/Linux packages, Developer ID release signing and notarization were not
qualified. Prompt/citation validation cannot prove semantic entailment or model
resistance to every adversarial email; the tests validate the bounded disclosure,
provenance and lifecycle controls with synthetic generation.
