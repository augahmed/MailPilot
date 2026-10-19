# Legacy feature and compatibility notes

Consolidated 2026-10-19. Historical reference only. The sole current scope/milestone
source is [PRODUCT_PLAN.md](../PRODUCT_PLAN.md); implemented/planned status is in
[ARCHITECTURE.md](../ARCHITECTURE.md). These notes never authorize a retired command,
Gmail draft operation, user-data deletion or replay of a historical preview.

## Semantic and Ask retirement

The 2026-10-16 contraction deleted active Ask/mailbox-Q&A, semantic query/index
services, vector/passage adapters, hybrid/semantic classifiers, MiniLM/Python/ONNX
worker, downloader and their exclusive tests/corpora. Shared bounded text cleanup
was moved to `internal/mail`; current composition uses deterministic rules directly.
No current install/startup path requires a model/interpreter. No user interpreter,
model directory, cached mail or recovery evidence was deleted by that contraction.

The old `ask.manage`/`ask_manage` and `search.semantic`/`semantic_search` handlers are
absent and rejected. Historical JSON fixtures remain in [contracts](../../contracts/README.md)
for compatibility/negative tests, not as public APIs. The new shared retrieval
architecture is planned work, not reactivation of those implementations.

## Immutable schema and evidence

Migrations 001–014 remain unchanged by the reset. The original contraction inventory
mentions 001–013 because it predates the later provider-selection addition; this is
not permission to drop migration 014. Current executable DDL remains authoritative.

- 008/009/010/013 retain historical email/category vectors, semantic candidate/score
  fields, scan index and passage/retrieval identities. These rows are inert. Source
  content changes invalidate affected derived rows in the import transaction;
  unchanged imports/reopen preserve them. Confirmed deletion and category triggers
  retain the existing cleanup semantics. The new complete chunk representation
  must not reinterpret these identities or overwrite historical migrations.
- Stored classification codecs preserve old engine/candidate metadata and score
  kinds (`rule_confidence` versus `cosine_similarity`). Current deterministic review
  exposes no live semantic candidates. Old engine identities are stale for new
  organization previews; historical organization recovery still uses saved evidence.
- 006/007 organization journals retain immutable digests, before/after membership
  evidence, nullable category references for phishing plans and undo provenance.
- 011 draft journals retain original plan formats, source references, routing/body
  evidence and digests. Read-only reconciliation never becomes draft creation.
- 014 preserves existing model/enabled settings and OpenAI key identity while adding
  provider selection to historical non-secret `reply_ai_settings`. Keys are OS-only.

Do not purge unresolved journals or assume that a new local-data clear operation
can erase every referenced row. Explain retained evidence, WAL/backups and the lack
of forensic-erasure guarantees.

## Draft and AI compatibility

Reply generation, new draft-plan insertion/dispatch and Gmail `CreateDraft` were
removed. Current `reply.manage` permits status/reconcile/resolve/cancel only and
has no model/key dependency. No-match, edited or duplicate remote draft observations
must preserve uncertainty/conflict. The precise retained contract is in
[REPLY_DRAFTING.md](../REPLY_DRAFTING.md).

The current `SummarizeEmail` workflow is implemented and still callable. Its
one-selected-email 6,000-byte approval, settings and key slots must not be silently
reinterpreted as consent for multi-email retrieval. The intended next external-AI
workflow is questions answered from explicitly approved retrieved evidence; composition
is outside the immediate scope. See [GENERATIVE_AI.md](../GENERATIVE_AI.md).

## Superseded intermediate plans

Before this reset, `RETRIEVAL_READER_PLAN.md` and `IMPLEMENTATION_PROMPTS.md` overlapped
with `PRODUCT_PLAN.md`, prioritized formatted reading, and included composition and
a later Chrome phase. Their unique useful content is consolidated into the current
architecture/product plan: original-source preservation, chunk metadata, exact cosine,
model/source generations, transactional invalidation, safe reading, bounded consent,
evaluation and compatibility constraints. English-first, about 10,000 messages,
one primary category/uncertainty and bundled model delivery remain compatible choices.

Those two documents and their old numbered prompts were removed. Their broader
composition/Chrome direction and proposed ComposeWithEvidence-style operation no
longer define current work. Do not execute remembered prompt numbers or retain a
second embedding system for a hypothetical editor. Prior candidate numeric budgets
were tuning proposals, not implemented limits; current settings must come from
source and new targets from the product plan.

`ASK_MAILPILOT.md`, `SEMANTIC_SEARCH.md`, `SEMANTIC_CLASSIFICATION.md` and
`SEMANTIC_EVALUATION.md` contained short overlapping retirement notes. Their useful
compatibility content is preserved above. Old corpora/measurements do not qualify
the future model or prove current performance; do not claim they were rerun.

## Preserved detailed records

- [Contraction record](CONTRACTION_2026-10-16.md): original file disposition, deletion
  order, migration/journal compatibility and hidden-coupling inventory. Paths in its
  historical inventory are snapshots, not a promise that every path still exists.
- [Earlier maintainability review](ENGINEERING_REVIEW.md): original review observations,
  including subsequently retired reply/Ask/runtime dependencies; not current status.

Neither archival document overrides the current engineering contract or supplies
new implementation authorization.
