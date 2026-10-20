# MailPilot product plan

Updated 2026-10-20. This is the single authoritative product scope and milestone
plan. [ARCHITECTURE.md](ARCHITECTURE.md) defines technical boundaries;
[AGENTS.md](../AGENTS.md) defines engineering rules; [IPC contracts](../contracts/README.md)
describe implemented transport only. [Legacy compatibility notes](archive/LEGACY_FEATURES.md)
record the disposition of older plans and migration/recovery constraints.

The [vector architecture plan](VECTOR_ARCHITECTURE_PLAN.md) supplies the concrete
reuse/removal map, logical schema, shared interfaces and compatibility gates for
these same milestones. It is a technical supplement, not a second scope/prompt plan.

Status: M1 source/chunking and M2 persistence plus a lazy packaged CPU MiniLM
provider plus explicit resumable indexing/UI are implemented. The provider has real synthetic/native and fake-model
checks; [qualification details](LOCAL_EMBEDDING.md) cover resource/signing/OS limits.
The coordinator now publishes bounded vector batches through generation-fenced
SQLite writes. Exact vector search and its UI are now implemented ([details and measurements](VECTOR_SEARCH.md)); Local category prototypes and explicit confirmed examples are implemented through the same coordinator ([contract](CATEGORY_VECTORS.md)), without classifying messages. Keyword/hybrid search, semantic classification and AI milestones remain planned.
The additive `index.local` control returns metadata only; external AI consent is
unchanged. See [local indexing](LOCAL_INDEXING.md) for scope and qualification. Further product work requires a scoped implementation request.

## 1. Product purpose

MailPilot is a local-first intelligent Gmail management application. One shared
local pipeline supplies semantic search, category suggestions and optional
retrieval-augmented answers:

```text
Gmail API → SQLite email cache → Preprocessing → Chunks → Local embeddings
          → SQLite vectors → Semantic/hybrid retrieval
```

Preserve original cached mail independently from derived chunks. Reuse shared
chunk vectors across consumers; avoid separate search/classification/AI indexes
with incompatible preprocessing or model runtimes.

## 2. Release assumptions and scope

Retain the compatible decisions confirmed earlier on 2026-10-19: English-first
semantic evaluation, approximately 10,000 locally cached emails, one primary
category with uncertainty allowed, and model/tokenizer artifacts bundled with the
installer. No Python installation or model download after installation. Semantic
processing remains optional. Apple Silicon macOS is the initial qualification target;
no untested Windows/Linux or multilingual semantic-quality claims.

The cache target is not an expansion of sync coverage. Initial sync is currently
bounded Inbox import (default 500, per-run limit 1–10,000); incremental history
refresh is implemented. Do not promise complete mailbox history, Sent, Spam, Trash,
uncached bodies or attachment contents. Broader Gmail import needs its own request.

### Core capabilities

| Capability                   | Intended behavior                                                                                                                                                                                                     |
| ---------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Semantic search              | Embed a natural-language query locally; compare compatible chunk vectors using exact cosine similarity; group relevant excerpts under their original cached messages.                                                 |
| Complementary keyword search | Local literal/full-text search, exact identifiers and useful sender/date/label/category filters; remains available without embeddings.                                                                                |
| Intelligent classification   | Embed category names/descriptions and optional confirmed examples using the shared adapter; aggregate email chunk evidence with useful deterministic/header rules into one primary suggestion or an uncertain result. |
| Review and correction        | Explain suggestions, let users review/correct local category state and deliberately manage examples. A correction is not a Gmail label write or automatic training permission.                                        |
| Retrieval-augmented answers  | Retrieve candidates locally, approve the exact relevant evidence and question, then obtain an optional answer citing source emails. Report insufficient/conflicting evidence honestly.                                |
| Provider support             | Preserve one shared bounded generative-provider abstraction and provider-scoped OS keys. OpenAI is currently the only functional installed adapter; additional adapters need their own implementation/evaluation.     |

### Existing features to preserve

Keep Gmail OAuth and keyring, manual bounded initial/incremental sync, SQLite
cache, cached reading, category management, useful deterministic suggestions,
reviewed Gmail label application and best-effort undo/reconciliation, independent
phishing scoring/explanations, secure AI settings and necessary historical recovery.
Do not rewrite these to introduce retrieval. No existing functionality is removed
by this documentation reset.

The selected-email Summary workflow still exists in the working tree. It is no
longer the central product direction. Plan its later transition to evidence-based
questions after the replacement is implemented and validated, preserving reusable
provider settings/keys/consent/transport. Do not remove it now, silently convert
old approvals or allow its historical enabled setting to disclose retrieved mail.

### Supporting reader work

Preserve cached reading throughout. The prior formatted-reader request remains a
supporting improvement: safe cached HTML formatting with text fallback, separate
from model preprocessing. It must not block or redesign the shared vector foundation.
Preserve quotes/signatures and block remote resources; exact Gmail visual parity,
inline image downloads and interactive HTML are not release promises. The current
reader uses shared cleaned sections (including meaningful image descriptions)
and escaped plain text; rich HTML rendering remains supporting work. See
[readable text](CLEAN_TEXT.md).

### Outside the immediate scope

AI replies or new-email composition, sending, new Gmail draft creation, autonomous
agents, automatic/unconfirmed mailbox changes, Chrome extension integration,
additional email providers/accounts, hosted databases/backends, cloud vectors or
cloud embeddings, multiple unrelated embedding pipelines, attachments/OCR, remote
email resources and general-purpose AI workflows. Composition may be reconsidered
later through a new product decision; no editor/tool contract is prebuilt for it.
Chrome is deferred without a milestone or dependency in the core plan.

## 3. End-to-end user behavior

### Connect, cache and enable local processing

Use existing system-browser OAuth/keyring and explicit sync. Show cache scope,
last successful sync and missing bodies. Explain local model disk/CPU use before
semantic enablement. After enablement, bounded local work processes the existing
eligible cache and subsequent synced changes. Show queued/current/partial/error
counts, pause/cancel/rebuild controls and ordinary-feature fallback if inference
is disabled, missing or fails. Sync must not call external generative providers.

### Find information

Search by natural language and/or literal terms with filters. Return original
messages plus revision-bound excerpts and source navigation. Show keyword,
semantic or hybrid mode, index coverage and incomplete content. Nearest neighbors
are relevance candidates, not proof or an exhaustive answer. Aggregate requests
such as all historical invoices cannot be satisfied truthfully from top-K alone.
Opening results preserves list/filter/focus state and changes no Gmail read status.

### Categorize and organize

Users create/edit categories with names and meaningful descriptions. All eligible
cached messages can be assessed locally, but uncertain messages stay unclassified.
Show rule/header evidence and semantic candidates/scores separately; similarity is
not a probability. User corrections take precedence; conflicting explicit rules
abstain. Do not silently self-train from unreviewed suggestions or MailPilot's own labels.

Keep local assigned categories distinct from suggested categories and Gmail labels.
A Gmail label addition requires a fresh preview, confirmation, journal and existing
reconciliation/undo safeguards. Changed source/category/example/model evidence
invalidates pending new-write previews. Preserve historical plans and uncertain
write recovery independently from current classifier availability.

### Ask a bounded email-information question

Go retrieves locally using the same semantic/hybrid search. Users review/select
sources and inspect the exact question, passages, included metadata, provider/model,
policy, omissions and expiry before a separate approval. Send no unapproved results,
attachments, hidden thread context or prior conversations. One approved bundle
permits one bounded generation request; no model-controlled search loop.

Answers carry links to approved source emails and an insufficient-evidence outcome.
Validate citation identities and quoted text, while explaining that citations alone
do not prove the answer correct. Preserve conflicting statements/dates and uncertainty.
Keep previews/answers memory-only initially; no browser storage or automatic history.
Generation is optional and no provider/key is needed for local search/classification.

## 4. Intended milestones and acceptance evidence

| Milestone                                  | Scope                                                                                                                                                                                                                              | Gate before claiming completion                                                                                                                                                           |
| ------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| M0 — Foundation decisions                  | Recheck the dirty working baseline, preserve migration/journal fixtures, evaluate one bundled model/tokenizer/native runtime and necessary dependency/license/resource budgets. Freeze synthetic tuning/held-out evaluation cases. | Chosen compatible model conventions and native packaging feasibility are documented; no parallel embedding architectures or runtime download requirement.                                 |
| M1 — Shared source/chunk contract          | Loss-preserving text sections, source mapping, content/preprocessing hashes and one shared bounded tokenizer-aware chunker.                                                                                                        | Forwarded-only evidence, quotes, multipart alternatives, Unicode, missing/oversized content and stable source/order tests pass without changing original cached mail.                     |
| M2 — Storage, local inference and indexing | Required additive schema, bundled local model adapter, finite vectors, durable bounded work, atomic invalidation/publication and rebuild/purge lifecycle.                                                                          | Fresh/upgrade/account-isolation, stale-worker, cancellation/restart, model-switch and no-network native inference tests pass; original journals/migrations remain intact.                 |
| M3 — Search                                | Complementary full-text and exact cosine retrieval, optional hybrid rank fusion, filters, message deduplication/excerpts and typed accessible UI.                                                                                  | Keyword fallback, exact-identifier retrieval, coverage and held-out relevance improve measurably; latency/memory/disk measured on about 10,000 cached messages.                           |
| M4 — Classification                        | Category/example vectors from the same adapter, bounded chunk aggregation, useful rules/header evidence, one-primary abstention and corrections.                                                                                   | Held-out per-category quality/coverage is published; no forced assignments or unconfirmed Gmail writes; stale organization previews reject.                                               |
| M5 — Optional evidence answers             | Exact evidence approval, bounded provider-neutral answers/citations, source/settings revocation and later Summary transition.                                                                                                      | Zero hidden disclosures in consent tests; disabled/no-key behavior, malformed output, invented citations, injection, cancellation and independent recovery checks pass.                   |
| M6 — Reader and lifecycle completion       | Supporting safe reader improvements, local-data controls and explicitly designed cold-start cache authorization where needed for offline restart.                                                                                  | Native renderer/IPC isolation, focus/fallback/resource blocking and cache/credential/purge boundaries are validated. Do not call existing session-local reads cold-start offline support. |
| M7 — Desktop qualification                 | Coordinate core/host/UI contracts and packaging; document privacy/licenses, real resource budgets and known limitations.                                                                                                           | Required language/toolchain checks, native smoke/upgrade tests, measured evaluations and accessible end-to-end workflows pass or exact blockers remain reported.                          |

Milestones are coherent slices, not a second list of copyable implementation prompts.
M1 and the M2 storage/provider/coordinator slice are implemented and tested,
including real offline indexing on synthetic mail. A 10,000-message/50,000-chunk exact-scan benchmark and a small native synthetic search evaluation are recorded in [VECTOR_SEARCH.md](VECTOR_SEARCH.md). Held-out retrieval/classification quality and keyword fallback remain open; M3 is only partially implemented. Supporting reading fixes may be separately requested. Do not add AI routes before reliable local retrieval/evidence.

## 5. Evaluation, operational limits and decisions still open

Freeze synthetic relevance/category/evidence fixtures before tuning. Split related
threads/templates together to avoid leakage; keep user examples local. Measure
search Recall@10/nDCG@10 against keyword/vector baselines, exact-identifier misses
and no-answer behavior. Measure per-category precision/recall and abstention, including
cold-start descriptions, confirmed examples and rule conflicts. Evaluate AI support,
citation provenance, conflicting evidence and prompt injection separately.

Earlier candidate targets remain experiments, not implementation or guarantees:
256-token chunks/32-token overlap (subject to tokenizer limits), p95 warm search
under 500 ms on a named 10,000-message/50,000-chunk fixture, and at least 90% precision
among accepted semantic category suggestions with denominators/coverage reported.
Actual chunk counts may differ; never cap coverage merely to fit a benchmark.
Measure indexing throughput, startup, peak RSS, installer size and disk as well.

Choose exact model/tokenizer/runtime, redistribution license, necessary schema,
chunk sizing/aggregation thresholds, query/output/resource bounds, rank weights and
wire-version strategy from evidence. Do not hard-code future migration numbers.
Begin with exact cosine scans; ANN/reranking requires a demonstrated need and
compatibility review, not a speculative release dependency. No more user product
choices are required for this documentation reset.

## 6. Retention, authorization and delivery limits

One Go process owns SQLite and account authorization. Preserve composite FKs,
numbered migration checksums, source/checkpoint atomicity and journal evidence.
No transaction spans inference or network calls. The current session is restored
through online Gmail refresh/profile verification; offline cold-start access needs
an explicit secure design, not trust in an account row or a browser Gmail login.

Credentials stay in OS storage; local model processing uploads no mail. External
AI needs fresh feature acknowledgment and exact per-request consent. Disconnect
revokes sessions/consent, cancels work and hides retained cache. Authorized local
clear/purge covers active chunks/vectors/FTS/examples/jobs while reporting any
journal-required retained evidence. Local clearing is distinct from deleting Gmail
mail. SQLite/WAL/backups are sensitive; no encryption or forensic-erasure claim.

Use synthetic tests by default, no live writes/paid AI calls in ordinary validation.
Public signing/notarization, applicable Google data-use/provider retention review
and qualification of each claimed platform are release gates. No commit/push,
feature implementation or dependency installation is part of this reset.
