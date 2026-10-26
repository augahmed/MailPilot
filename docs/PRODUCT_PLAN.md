# MailPilot product scope and implemented capabilities

Reviewed 2026-10-21. This file records current product scope and implementation
limits. [ARCHITECTURE.md](ARCHITECTURE.md) defines runtime boundaries;
[VECTOR_ARCHITECTURE_PLAN.md](VECTOR_ARCHITECTURE_PLAN.md) records concrete shared
storage/reuse decisions; [AGENTS.md](../AGENTS.md) governs engineering changes.
Historical plans are evidence, not additional implementation authorization.

## Implemented product

MailPilot is a local-first Gmail desktop manager. One Go process owns one SQLite
database, Gmail access and local/optional external AI policy. React/TypeScript
presents the product through a narrow Tauri/Rust bridge.

```text
Gmail → original cached mail → shared readable sections and chunks
      → shared local vectors + passage FTS → semantic/keyword/hybrid retrieval
      → shared category assessment or separately approved evidence answers
```

| Capability                  | Current behavior                                                                                                                                                                    | Limits                                                                                                                                                                                     |
| --------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Gmail connection            | System-browser OAuth with PKCE/state, bounded loopback callback, serialized refresh and OS-only credentials.                                                                        | One account/provider; restoring access at startup verifies Gmail online.                                                                                                                   |
| Manual sync/cache           | Resumable newest-first Inbox import, incremental history, authoritative deletion/recovery, durable source/checkpoint commits, bounded failed/incomplete-body retries and local chunk preparation. | Default 500 Inbox emails plus 500 maintenance observations per manual run, configured limit 1–10,000 per allowance; no complete mailbox/Sent/Spam/Trash promise, background scheduler or attachment download. Sync performs no inference or Gmail writes. |
| Cached reader               | Revision-checked paged escaped text from shared cleaned sections, original metadata, category/risk explanations and source navigation.                                              | No rich HTML renderer, remote resources or attachment execution. Original cached source remains separate from cleanup.                                                                     |
| Local index                 | Explicit Start/Pause/Resume/Cancel/Rebuild over live cached mail, one bundled CPU MiniLM provider/tokenizer and durable generation-fenced vector batches.                           | Inference is optional; startup/polling never starts it. Partial/unavailable/failed coverage remains explicit.                                                                              |
| Search                      | Keyword, exact semantic and hybrid retrieval over shared chunks; sender, UTC date and assigned-category filters; revision-bound excerpts and reader navigation.                     | Keyword works without embeddings; hybrid can disclose keyword fallback. Label filters and empty-query browsing are absent. Top-K is not exhaustive mailbox evidence.                       |
| Categories                  | Editable definitions, shared category prototypes, explicitly confirmed examples, semantic/rule assessment, abstention, close suggestions and manual primary preferences.            | Suggestions do not assign Gmail labels or silently become examples. Similarity is not calibrated probability. Legacy multiple assignments remain intact.                                   |
| Reviewed Gmail organization | Preview/dry run, separate confirmation, namespaced label additions, journal-before-dispatch, reconciliation and verified best-effort undo.                                          | No automatic labels, arbitrary mailbox actions, deletion, archive or mark-as-read. Undo preserves pre-existing/unrelated labels and reports uncertainty.                                   |
| Phishing explanations       | Independent bounded deterministic extraction, versioned scoring/persistence and explained risk; separately reviewed risk-label proposals.                                           | No model verdict, URL visit, DNS lookup or guarantee of safety/authentication.                                                                                                             |
| Optional evidence answers   | Shared local hybrid retrieval, bounded source selection, exact question/payload/policy preview, one-use approval and normalized cited answers.                                      | No hidden source expansion, model-controlled search, actions or background AI. Provenance validation does not prove factual accuracy.                                                      |
| AI settings/providers       | Provider-neutral Summary/evidence-answer contract, adapter registration/substitution and provider-scoped OS keys.                                                                   | OpenAI is the only shipped functional adapter; a second synthetic adapter verifies substitution/consent/key isolation. No provider or key is required for local features.                  |
| Selected-email Summary      | Independently approved bounded summary/request/deadline extraction from one selected cached message.                                                                                | Separate from evidence-question approval; remains a live feature.                                                                                                                          |
| Historical recovery         | Existing reviewed-label recovery/undo and GET-only draft matching with local status/reconcile/resolve/cancel.                                                                       | No reply generation, new draft plan/dispatch, creation, sending, remote draft deletion or preview revival.                                                                                 |

The tokenizer is embedded in Go; fixed checksummed model/runtime resources ship
with the app. Installed users need no Python, runtime setup or post-install model
download. English and approximately 10,000 cached messages are the evaluation
baseline, not an import cap or complete-mailbox claim.

## Shared lifecycle and privacy

Source/checkpoint commits invalidate incompatible derived state atomically.
Chunk preparation follows committed source updates; failures preserve source mail
and leave stale passages ineligible. Exact compatible text/context inputs reuse
vectors. Index publication rechecks source, model/preprocessing and job generation.
Combined job/vector/category/assessment coverage uses one SQLite transaction.
Cancellation retains committed work; restart revokes interrupted workers and
requires explicit Resume. See [source lifecycle](SYNC_LIFECYCLE.md) and
[local indexing](LOCAL_INDEXING.md).

Go retains all retrieval and payload construction. External AI receives only the
separately approved bounded evidence and fixed policy through the selected adapter.
Approval is bound to account/session, exact payload, source generations,
provider/model/settings/key identity and expiry. Relevant changes revoke it.
No frontend storage of mail/answers, no tools or mailbox credentials for a model,
no automatic paid retry and no cloud embeddings are implemented.

SQLite/WAL/backups contain sensitive unencrypted local data. Disconnect revokes
access but retains cache and immutable recovery evidence. There is no cache-purge
UI, encryption claim or forensic-erasure guarantee.

The storage-hardening pass (2026-10-26) validates SQLite sidecars before opening,
repairs private Unix modes, and enables memory-only query temporaries and ordinary
row secure-delete on every connection. Privacy disclosures are available before
sign-in and in the account menu; failed AI-key removal is retryable while disabled.
These protections do not encrypt the cache or establish release compliance. See
[current privacy behavior and limits](PRIVACY.md).

## Qualification and remaining debt

The implementation has synthetic Go/Rust/frontend and native offline checks.
Current contracts/evaluations record bounds, fixtures, denominators and limitations:

- [Embedding/runtime](LOCAL_EMBEDDING.md), [readable text](CLEAN_TEXT.md) and
  [source/index lifecycle](SYNC_LIFECYCLE.md).
- [Vector search](VECTOR_SEARCH.md) and [keyword/hybrid search](HYBRID_SEARCH.md).
  Exact/hybrid dense 50,000-chunk scans exceed the proposed 500 ms p95 target.
  No ANN, alternate vector database or replacement runtime was introduced.
- [Category vectors](CATEGORY_VECTORS.md) and [classification](SEMANTIC_CLASSIFICATION.md).
  Definition-only held-out per-suggestion precision is 60.7%, below the 90% target.
  This is published synthetic evidence, not production-mailbox qualification.
- [Evidence questions](EMAIL_QUESTIONS.md), [AI boundaries](GENERATIVE_AI.md) and
  [historical draft recovery](REPLY_DRAFTING.md). Paid-model factual quality and
  real-mailbox behavior are not qualified by synthetic provider tests.

Apple Silicon macOS is the native qualification target; local inference requires
macOS 13.4+ while ordinary app features retain the 13.0 baseline. Other platforms
have an unavailable native adapter and lack equivalent inference qualification.
Local ad-hoc signing/offline smoke is not Developer ID distribution/notarization.
Cold-start offline authorization, rich HTML rendering, attachment/OCR support,
additional shipped generative adapters and release qualification remain absent.

## Scope exclusions

AI replies/composition, new Gmail drafts, sending, autonomous agents, arbitrary
mailbox tools, unconfirmed Gmail changes, additional email accounts/providers,
Chrome integration, a hosted backend/cloud vectors, cloud embeddings, attachments/OCR
and unrelated generative workflows are outside the implemented scope. Existing
compatibility records do not authorize their reactivation. Further changes need
an explicit scoped implementation request; no replacement framework is implied.
