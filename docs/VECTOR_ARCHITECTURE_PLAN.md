# Shared email chunk-and-vector architecture plan

Reviewed 2026-10-19 against the current working tree. **Persistence slice implemented:**
migration 015 and `Store.Chunks()` now provide the three-table storage contract in
section 4, under a subsequent explicit implementation request. Chunking, model
inference, job coordination, retrieval, classification and IPC remain planned.
Preserve unrelated uncommitted work. This document does not authorize further implementation.

[PRODUCT_PLAN.md](PRODUCT_PLAN.md) remains the sole product scope/milestone authority;
[ARCHITECTURE.md](ARCHITECTURE.md) describes runtime/trust boundaries. This document
supplies the concrete reuse, schema, interface and compatibility design for that
same scope; it is not a second product roadmap. [AGENTS.md](../AGENTS.md) governs
future work. [Current contracts](../contracts/README.md) remain implemented wire
behavior, including historical rejection fixtures.

## 1. Decision and source baseline

Build exactly one local flow:

```text
Gmail sync → normalized SQLite emails → shared text sections/chunks
           → one local embedding adapter → SQLite chunk/vector store
           → exact cosine retrieval
```

Search and AI evidence selection call the same retriever. Classification loads
those same stored email chunks/vectors and compares them with category prototypes
and explicitly confirmed example chunks using the same cosine kernel. It does not
embed the mailbox again, maintain a one-vector-per-email search cache, or run a
separate indexing/classification framework.

Preserve current Go core, React/strict TypeScript, thin Tauri host, Gmail OAuth/
sync, SQLite cache/reader, category management, rule fallback, deterministic risk,
reviewed Gmail labels/journal/reconciliation/undo, provider settings and historical
recovery. Keyword search is a retained target/fallback, **not currently implemented**:
there is an FTS5 test probe but no product keyword index or search endpoint.

Source composition in `backend/cmd/mailpilot-core/main.go` currently wires
`RuleBasedClassifier`, `ClassificationReview`, `Organization`, `CachedReader`,
`AIService` and GET-only `ReplyMaintenance`. It wires no chunker, embedding adapter,
index coordinator or retriever. OpenAI is the only real installed AI adapter;
multiple-provider substitution is supported by the shared boundary and synthetic
adapters, not evidence of additional shipped providers. Summary still exists.

There are 15 migration files; 015 adds the shared chunk persistence layer.
Historical vector tables are inert. The current AI interface only summarizes one
selected cached email. New retrieval/semantic classification/evidence QA cannot be
enabled by changing settings or restoring archived UI handlers.

Target English first, about 10,000 cached emails, one primary category suggestion
with uncertainty allowed, bundled model/tokenizer, no user Python or first-use
model download. Cache size is not permission to widen bounded Inbox sync, fetch
attachments or promise uncached history/Sent coverage. No composition, draft
creation/sending, agents/mailbox tools, Chrome or extra email providers in this slice.

## 2. Exact reuse, extension and retirement map

Paths in this table exist unless explicitly marked absent/planned. "Extend" means
later scoped implementation with tests except the persistence slice in section 4. No
existing application source, migration, journal, database or model data is deleted.

| Current path(s)                                                                                                                                                                                                                                                                                     | Action in the shared design                                                                                                                                                                                                                                           |
| --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `backend/cmd/mailpilot-core/main.go`, `main_test.go`                                                                                                                                                                                                                                                | Reuse composition/lifecycle; later wire one embedding adapter, shared store/coordinator/retriever and shared classifier dependencies. Cancel/join them before SQLite closes. Preserve current pending changes.                                                        |
| `backend/internal/app/app.go`, `app_test.go`, `mailbox.go`                                                                                                                                                                                                                                          | Reuse session authorization, health and use-case entry points. Add only narrow search/index/evidence operations; no generic dispatch or replacement app shell.                                                                                                        |
| `backend/internal/provider/gmail/normalize.go`, `normalize_test.go`, `provider.go`, `history.go`                                                                                                                                                                                                    | Reuse bounded Gmail/MIME translation and opaque identities. Fix only demonstrated normalization defects. Do not add another mail importer, hidden fetch or Gmail thread expansion.                                                                                    |
| `backend/internal/sync/service.go`, `incremental.go`, associated tests; `backend/internal/repository/sync.go`                                                                                                                                                                                       | Reuse manual sync and atomic checkpoints. Enqueue/coalesce local derived work inside existing import/checkpoint transactions; never invoke external AI during sync.                                                                                                   |
| `backend/internal/mail/text.go`, `text_test.go`, `doc.go`                                                                                                                                                                                                                                           | Reuse bounded decoding/HTML-to-text primitives; add loss-preserving versioned sections, tokenizer-aware chunking and source mapping here. HTML extraction is not sanitation.                                                                                          |
| `backend/internal/mail/clean.go`, `clean_test.go`                                                                                                                                                                                                                                                   | Keep rule/Summary cleanup semantics and tests. Do not use its quote/signature/forward trimming as the shared retrieval source. Do not replace it globally as a shortcut.                                                                                              |
| `backend/internal/repository/content.go`                                                                                                                                                                                                                                                            | Retain `EmailContentHash` for normalized source/live-mail evidence. Add separately versioned chunk-input/classification evidence fingerprints; do not redefine historical digest meanings.                                                                            |
| `backend/internal/store/sqlite/sqlite.go`, `repositories.go`, `accounts.go`                                                                                                                                                                                                                         | Reuse one store/connection owner, migration checksums, pragmas, safe errors and typed repository construction. No second database/connection owner or ORM.                                                                                                            |
| `backend/internal/store/sqlite/emails.go`, `sync.go`                                                                                                                                                                                                                                                | Extend existing source-update/deletion transactions with active chunk/FTS invalidation and durable work state. Keep local assignments, original source and journal-retained tombstones.                                                                               |
| `backend/internal/store/sqlite/migrations/001_*.sql` through `014_*.sql`                                                                                                                                                                                                                            | Preserve every byte. Add future numbered checksummed migrations only after the actual maximum; do not repurpose/drop historical vector or journal tables.                                                                                                             |
| `backend/internal/store/sqlite/historical_indexes_test.go`, `historical_classification_test.go`, `legacy_fixture_test.go`, `sqlite_test.go`, `persistence_test.go`, `sync_test.go`                                                                                                                  | Preserve compatibility, unchanged-reimport, invalidation, upgrade/FK and tombstone tests. FTS probe is useful but not a search implementation. Add meaningful new lifecycle cases beside them.                                                                        |
| `backend/internal/classification/engine.go`, `rules.go`, `starter_rules.json`, `rules_test.go`, `direct_rules_test.go`                                                                                                                                                                              | Keep the consumer engine boundary and deterministic rule engine. Preserve exclusions, conflict/abstention behavior and 0.60 rule gate; add a shared-store-backed semantic consumer, not its own embedder/mailbox scan.                                                |
| `backend/internal/app/classification.go`, `classification_test.go`; `backend/internal/store/sqlite/classifications.go`, `classifications_test.go`                                                                                                                                                   | Reuse review/persistence workflows but add version-aware evidence/freshness validation. Current hard-coded rule input hashes and score validation are not sufficient for chunk/model/example revisions.                                                               |
| `backend/internal/app/categories.go`, `categories_test.go`; `backend/internal/store/sqlite/categories.go`, `categories_test.go`                                                                                                                                                                     | Reuse editable category definitions and assignment operations. Add explicit primary preference/example controls and prototype invalidation, retaining legacy multiple assignments until reviewed.                                                                     |
| `backend/internal/app/organization.go`, `organization_preview.go`, `organization_apply.go`, `organization_risk.go`, `organization_dto.go` and organization tests                                                                                                                                    | Preserve all write authorization/journaling/recovery/undo. Extend only new-preview evidence/version checks for shared classification; do not re-embed/reclassify the mailbox during preview/apply.                                                                    |
| `backend/internal/domain/organization.go`, `backend/internal/store/sqlite/organization.go`, `actions.go` and tests                                                                                                                                                                                  | Retain old immutable plan codecs/digests/outcomes. Add a versioned new-plan evidence extension only when needed; never reinterpret old plans or make undo depend on the live index.                                                                                   |
| `backend/internal/risk/*`, `backend/internal/app/risk_test.go`, `backend/internal/domain/phishing.go`                                                                                                                                                                                               | Reuse unchanged independent deterministic risk. No semantic/LLM safety verdict or new external threat service.                                                                                                                                                        |
| `backend/internal/app/reader.go`, `reader_test.go`; `frontend/src/features/reader/MailReader.tsx`, `MailWorkspace.tsx` and tests                                                                                                                                                                    | Preserve cached revision-checked reading and navigation. Search excerpts open the existing reader. Any formatted-reader work remains a separately bounded supporting change.                                                                                          |
| `backend/internal/ai/provider.go`, `service.go`, `settings.go`, `openai/provider.go` and tests                                                                                                                                                                                                      | Reuse provider abstraction, redaction, keyring-only configuration, consent patterns and bounded transport. Later add only typed evidence-question answering; preserve current Summary until an explicit tested transition. No generic prompt/tools or compose method. |
| `backend/internal/app/ai.go`, `ai_dto.go`, `ai_errors.go`, `ai_test.go`; `backend/internal/store/sqlite/ai_settings.go`, `ai_settings_test.go`                                                                                                                                                      | Reuse account/client lifecycle and settings. Add Go-built selected evidence from the shared retriever and fresh approval; current Summary approvals/enablement cannot authorize RAG. These files include existing uncommitted work.                                   |
| `backend/internal/app/replies.go`, `replies_actions.go`, `replies_dto.go`, `replies_maintenance.go`, `replies_maintenance_test.go`; `backend/internal/reply/reply.go`; `backend/internal/store/sqlite/replies.go`, `replies_test.go`; `backend/internal/provider/gmail/drafts.go`, `drafts_test.go` | Preserve narrow GET-only historical draft recovery and immutable journal evidence. No new draft generation/insertion/dispatch/creation/resend/delete path; not dead code merely because the editor is retired.                                                        |
| `backend/internal/auth/*`, `credentials/*`, `provider/provider.go`, `provider/provider_test.go`, `provider/boundary_test.go`                                                                                                                                                                        | Reuse OAuth/keyring, account-bound adapters and boundary tests. No second identity system, token fallback or restored `DraftCreator` capability.                                                                                                                      |
| `backend/internal/ipc/server.go`, `server_test.go`, `ai_test.go`, `replies_test.go`, `ask_test.go`                                                                                                                                                                                                  | Extend strict allowlists only with new typed operations after their core works. Preserve retired-operation negative tests; a file named Ask is not an active Ask service.                                                                                             |
| `frontend/src/features/categories/*`, `classification/*`, `organization/*`, `mailbox/*`, `accounts/*`, `ai/*`, `replies/AIMaintenance*`; `frontend/src/app/App.tsx`, `App.test.tsx`                                                                                                                 | Reuse existing views/settings/recovery. Add bounded search/index/coverage/category/evidence presentation; preserve focus, cancellation and late-response/session clearing. No draft editor or generic chat.                                                           |
| `frontend/src/lib/ipc/{client,classification,organization,reader,reply,ai}.ts`, associated tests and `retired.test.ts`                                                                                                                                                                              | Extend typed schemas with coordinated contracts; keep archival request rejection and provider secrets out of returned DTOs. No frontend vectors, SQL, raw source injection or model paths.                                                                            |
| `src-tauri/src/{main,bridge,ai,replies}.rs`, `build.rs`, `capabilities/main.json`                                                                                                                                                                                                                   | Keep host thin, fixed Go sidecar and strict DTO/permission validation. Later permit trusted resource lookup and typed bounded new commands; no index/database/business logic in Rust.                                                                                 |
| `scripts/build-sidecar.mjs`, `scripts/tauri.mjs`, `scripts/rust-environment.mjs`, `scripts/smoke-desktop.mjs`; `src-tauri/tauri.conf.json`                                                                                                                                                          | Reuse packaging/launch/native smoke. Proposed in-process ONNX requires an explicit future CGO/native-library/resource change; current script forces `CGO_ENABLED=0` and bundles no model resources.                                                                   |
| `contracts/ai-settings-v1.json`, `email-summary-v1.json`, `draft-recovery-v1.json`, `mail-reader-v1.json`, `classification-v1.json`, `organization-v1.json`, `organization-phishing-v1.json`, `sync-incremental-v1.json`                                                                            | Preserve actual fixtures. Add coordinated versioned retrieval/index/new classification/evidence QA fixtures later; do not silently alter v1 semantic meaning.                                                                                                         |
| `contracts/ask-v1.json`, `search-semantic-v1.json`, `classification-semantic-v1.json`, `reply-drafting-v1.json`                                                                                                                                                                                     | Keep historical/negative-test fixtures disabled, not active specifications. Preserve historical score/candidate decoding where required.                                                                                                                              |
| `backend/internal/embedding`, `backend/internal/search`, `backend/internal/ask`, old MiniLM/Python worker/downloader and semantic UI/editor implementations (absent)                                                                                                                                | Do not resurrect historical implementations. Introduce only the minimal new adapter/retriever required below, without copying duplicate workers/caches or a one-vector-per-email search path.                                                                         |

**Removal now: none.** Already staged/unstaged deletions from earlier work remain
untouched. Future cleanup may remove a genuinely superseded temporary adapter or
Summary-only code only after a separately authorized replacement is verified; this
plan does not request that cleanup. Historical/inert rows are not converted or purged.

## 3. Package boundaries and data flow

Use existing packages for normalization, policy and storage. Proposed new paths
below are reserved design names, not directories/stubs to create in this task:

| Owner                               | Responsibility and exclusion                                                                                                                                                   |
| ----------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `internal/mail`                     | Source sections, stable mapping and the single shared chunker. Accept a tokenizer supplied by the selected local model adapter; no DB/network/model download.                  |
| `internal/embedding` (new)          | Small provider-independent batch/tokenizer contract and one concrete local ONNX adapter. No mailbox enumeration, classification, credentials, database or external generation. |
| `internal/store/sqlite`             | One concrete shared chunk/vector/prototype store plus work/publication and keyword SQL; all business consumers use account-scoped repository methods.                          |
| `internal/retrieval` (new)          | Bounded exact cosine kernel, shared semantic/hybrid ranking, chunk/message deduplication and source hits. No independent index or embedding worker.                            |
| `internal/classification`           | Rules plus bounded aggregation of an email's existing chunk vectors against compatible category/example vectors. No mailbox re-embedding or second retriever/store.            |
| `internal/app`                      | Session/account authorization, one bounded index coordinator, search/review/evidence use cases, consent, progress/cancellation and shutdown.                                   |
| `internal/ai` and concrete adapters | Approved evidence-question request and normalized answer/citations only; no retrieval tools or mailbox authority.                                                              |
| React/TypeScript and Tauri/Rust     | Presentation/strict transport and packaged lifecycle. No database/inference ownership or untrusted generic bridge.                                                             |

One coordinator processes durable local work, with small bounded inference batches
and low fixed native thread count initially. After chunk publication it marks/runs
dependent classification using that generation; category edits mark assessments
dirty using the same local lifecycle. Keep existing explicit review available.
No second task bus, per-feature worker pool or generic parallel job framework.

Keyword and semantic ranking may have distinct SQL/scoring paths because they
serve different matching semantics, but share source preprocessing, account filters,
source identities and transaction hooks. Keyword-only mode remains usable if model
loading fails or local semantic processing is disabled.

## 4. Proposed SQLite schema and constraints

Migration [015_chunk_storage.sql](../backend/internal/store/sqlite/migrations/015_chunk_storage.sql)
is the implemented storage contract below. Category/keyword extensions remain logical
proposals. All additions stay in the existing database and use new checksummed
migrations after the actual maximum.
No rewrite, backfill guessing, drop or automatic purge of existing user records.

### Existing authoritative records to retain

- `emails` retains `(account_id,id)` and `(account_id,provider_message_id)` uniqueness,
  original decoded text/HTML/headers, content hash/revision, body completeness,
  timestamps and confirmed `remote_deleted` visibility. `email_addresses`, `labels`
  and `email_labels` remain account-bound metadata; no duplicate mail table.
- `categories` retains account/category identity, editable name/description/enabled
  and timestamps. `email_categories` remains existing accepted local assignments.
  It allows multiple rows per email; do not add a uniqueness constraint that drops
  historical assignments to force one primary category.
- `classification_runs/results` retain old engine/hash/reasons/candidates/score fields
  and codec meanings. `phishing_results`, sync checkpoints and label/draft journals
  remain unchanged evidence formats unless an explicit additive/versioned extension
  is needed. Historical tables 008–010/013 stay inert.

### Implemented minimal shared index records

The explicit persistence request chose separate chunks, model-versioned embeddings
and per-email state. This supersedes the earlier proposal for vectors inline in
chunk rows plus profile/chunk-set/work tables. There is one shared vector store;
`chunk_embeddings` is its sole embedding table, not a second pipeline or cache.

| Table | Implemented fields and ownership |
| --- | --- |
| `email_chunks` | Opaque `id`; `account_id`, `email_id`, contiguous zero-based `chunk_index`; `section_type`, exact `text`, `token_count`; `source_revision`, half-open UTF-8 byte `start_offset`/`end_offset`, text SHA-256 `content_hash`, `preprocessing_version`. Composite email FK with cascade, unique account/email/ordinal and account/chunk identity. |
| `chunk_embeddings` | `account_id`, `chunk_id`, `model_id`, `model_version`, `dimension`, nullable `vector_data`, `status` (`pending`, `ready`, `failed`). Composite account/chunk FK with cascade; primary key account/chunk/model/version. Ready BLOB byte length must equal dimension × 4; other statuses carry no vector. |
| `index_state` | Account/email primary key and cascading composite email FK; source/preprocessing/index versions; active model/version/dimension; opaque `generation` work token; source `body_state`; index `status` and nullable `last_indexed_at`. No scheduler, lease, retry or profile framework. |

SQL-free contract: [repository/chunks.go](../backend/internal/repository/chunks.go).
Concrete implementation: [sqlite/chunks.go](../backend/internal/store/sqlite/chunks.go).
`Store.Chunks()` provides `Replace`, `State`, paged `List`, `SaveEmbeddings` and
`Embedding`. No public IPC, inference, network calls, application coordinator or
historical-vector fallback is added. Stored text remains untrusted. Callers own
faithful normalization/chunking and truthful upstream body completeness.

`Replace` validates and atomically replaces a complete chunk snapshot. Shuffled
input is sorted and must have contiguous ordinals. Identical snapshots retain IDs,
generation and timestamps. Unchanged exact text/section/token-count/preprocessing
inputs reuse vectors, including chunks moved to new ordinals. Matching prefers
original ordinals for duplicates; reuse is bounded to the same account and email.
Removed/changed inputs cascade their vectors; preprocessing changes require new
chunks. Model changes retain compatible chunk identities and versioned vectors;
switching back can reuse the old recipe. Model version identifies the complete
artifact/tokenizer/pooling/input-policy recipe; incompatible dimensions require a
new version. There is no cross-message/global vector deduplication.

Source revision is the existing cached `EmailContentHash`, not provider
`content_revision`, `updated_at` or the chunk hash. Migration triggers participate
in existing import/checkpoint transactions: content changes mark state `stale`,
rotate the generation and clear the timestamp. Old chunks/vectors remain private
as a reuse cache until replacement; `List`, `Embedding` and writes reject stale
snapshots. They are sensitive retained data, not forensic deletion. `State` exposes
the stale status. Confirmed source tombstones delete chunks, embeddings and state;
physical source deletion cascades while historical journal FKs still protect evidence.
Chunk identity cannot be reassigned to another account/email. Defensive SQL triggers
also invalidate state for direct chunk/vector maintenance.

Embedding batches check live account/email/source/model and generation inside the
write transaction, rejecting stale workers including configuration changes followed
by a switch back. All-or-nothing batches reject unrelated/missing chunks. Byte
encoding is little-endian float32; Go checks exact dimensions, finite values and
nonzero norm both on writes and reads, including cached vectors reused by Replace.
SQL checks ownership, supported statuses, dimensions and BLOB length; it cannot
validate floating-point values inside a BLOB. No existing vector row is inferred,
converted or marked ready automatically.

State is `pending` for complete sources with no ready vectors, `partial` for some
ready vectors or any unknown/unavailable body with usable chunks, `ready` only
when all chunks have valid vectors and body state is complete, `unavailable` for
zero chunks, and `stale` after invalidation. Individual failures stay in embedding
status. `last_indexed_at` is set when every stored chunk has a valid vector, including
partial-body coverage; consumers must also inspect body/index status. It is cleared
on invalidation or incomplete vectors. A partial body never becomes complete because
its available chunks were embedded. Paged storage reads can expose pending/partial
chunk metadata; future retrieval must additionally require compatible ready vectors
and report incomplete coverage.

Bounds: 4,096 chunks and 4 MiB aggregate text per snapshot, 64 KiB per chunk,
100 embedding writes per atomic batch, dimension 1–8,192; paged chunk reads use the
existing 1–100 row limit. Offsets measure bytes into a caller-defined normalized
section and must span exactly the stored text. This slice cannot verify mappings
against a not-yet-implemented source projector, tokenize, silently truncate, or
promise coverage for rejected oversized inputs. No transaction crosses inference.

Existing migrations 001–014, original emails, category assignments and label/draft
journals are unchanged. Old noncanonical source hashes remain readable; indexing
requires a current 64-character canonical hash, obtained through normal import, not
an automatic migration rewrite. Semantic rules/consent/recovery formats are unchanged.
There is no automatic chunk backfill and no model installed by this migration.
Future coordinator/profile/category metadata must extend this shared store only as
needed and preserve its account, revision and vector-validation guarantees.

### Category records using the same pipeline

| Proposed extension            | Relationship to the shared store                                                                                                                                                                                                                                                                                                                                                                                                    |
| ----------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `category_prototypes`         | PK `(account_id,category_id,profile_id)`; composite category FK and profile/dimension FK; definition hash, preprocessing provenance, status, dimension and normalized vector. Build the category-name/description representation with the same tokenizer/chunker/embedding adapter. If it spans multiple parts, use the same chunking policy and a versioned bounded aggregation; no silent truncation or different category model. |
| `category_examples`           | PK `(account_id,category_id,email_id)`; composite FKs to category/email; explicitly confirmed source hash and timestamp. Resolve representative chunks from that email's current compatible ready set; store no duplicate example vectors. Changed/deleted sources make examples stale until deliberately reconfirmed.                                                                                                              |
| Primary preference metadata   | Reuse `email_categories` for compatible accepted assignments; add a small `email_category_preferences` account/email PK with nullable primary category composite FK and explicit revision/provenance only when the correction UI is implemented. Preserve multiple legacy assignments and surface unresolved legacy state; never infer consent to delete them.                                                                      |
| Assessment evidence extension | Add only necessary versioned source/profile/generation, category-definition/example/override/rule/header hashes and selected chunk references to current assessment storage. Reuse `used_classifier`, `reason`, `candidates_json` and `score_kind`; preserve the legacy fingerprint branch. New DTOs distinguish raw cosine from rule confidence without a duplicate score store. No separate category mailbox-index tables.        |

One category prototype vector is a category description aggregate, not a revived
one-vector-per-email search cache. Example comparisons reuse email chunk vectors.
Prototype work uses the same adapter/coordinator; category edits do not re-embed mail.

### Complementary keyword index

Add an account-mapped FTS5 index over the versioned searchable subject/body text,
with insert/update/deletion visibility transactional with cached source/checkpoints.
Keep raw FTS grammar/SQL private. Link each result to account/email/current source
revision; handle partial bodies and Unicode/tokenizer limitations honestly. This
lexical index is complementary to the shared vector store, not a second vector
pipeline. Decide table/trigger layout in its implementation migration; FTS5 virtual
tables cannot substitute for composite ownership checks in ordinary mapping rows.

## 5. Chunk/source revisions and local work lifecycle

Define three distinct identities rather than overloading `updated_at`:

1. Source revision: existing `repository.EmailContentHash` binds normalized mail
   evidence. Provider `content_revision` is a remote identity hint, not the sole
   freshness condition. Source timestamps are not monotonic publication tokens.
2. Representation/profile: preprocessing, tokenizer, chunk policy, model/pooling/
   normalization conventions and dimension determine vector compatibility.
3. Request/claim identity: a monotonic revision within an existing work row plus a
   fresh claim nonce and nonreused generation ID prevent cancelled/superseded work
   publishing later. Check the nonce as well as the counter so deleting/recreating
   work state cannot create an ABA race after a purge or disable/re-enable cycle.

Life cycle:

```text
source import/change → transaction: invalidate visibility + coalesce desired work
                     → queued → running/staging → ready or partial/failed
source/profile change, disconnect, cancel or purge → revoke old work epoch
```

- Preserve original reading text. Normalize MIME alternatives once and retain unique
  authored/quoted/forward/signature sections with source mapping. Do not feed the
  shared pipeline through the current destructive `CleanBody` function.
- Queue local work only after explicit semantic enablement. Update source/checkpoint,
  FTS and desired work state atomically; do not add an inference call to the sync
  transaction. Unchanged imports reuse compatible current generations.
- Claim bounded work in a short transaction; copy source/category snapshots and
  release the connection. Tokenize/infer outside all DB transactions. Check Go
  contexts between bounded batches; cancellation cannot be assumed to interrupt
  a native inference call unless the chosen binding proves that capability.
- Stage a complete generation with immutable text/ordinal/mapping and vectors.
  Validate expected counts/dimensions/hashes. In a short publication transaction,
  recheck current account authorization, visible source hash, profile and request/claim identity;
  atomically publish the active pointer and dependent classification state.
- Ready means all eligible supplied text was indexed. Missing/oversized/unsupported
  source remains explicitly partial/unavailable; never silently cap chunk counts
  just to satisfy the 10,000-email benchmark. Keyword results can remain available.
- A source change immediately makes an old ready generation ineligible. A confirmed
  deletion uses existing tombstones and hides all active derived results; never
  infer deletion from incomplete listings. No late worker can undo purge/disconnect.
- Label-only changes refresh consumed rule/filter evidence without regenerating
  unchanged text vectors. Category/example/override changes invalidate prototypes/
  dependent assessments and new-write previews, not unrelated chunk vectors.
- On crash, reclaim unfinished bounded work with a fresh claim/revision; never accept
  abandoned staging rows as current. Rebuild and pause/cancel/clear controls have
  explicit account ownership and truthful progress. Clearing derived data is a
  later user action, not an operation performed by this documentation task.

## 6. Embedding model/runtime decision and packaging

### Proposed first choice; qualification still required

Use **`sentence-transformers/all-MiniLM-L6-v2`**, exported ONNX, with one **CPU ONNX
Runtime session inside the Go core**, behind the narrow embedding adapter. The model
card documents 384-dimensional vectors, an Apache-2.0 license and a default 256-
WordPiece truncation limit. This is a compact English-first baseline, not a measured
MailPilot winner or a promise of retrieval/category accuracy. [Model card](https://huggingface.co/sentence-transformers/all-MiniLM-L6-v2).

Use a maintained Go C-API binding, initially evaluating
[`yalue/onnxruntime_go`](https://github.com/yalue/onnxruntime_go), and a pure-Go
WordPiece tokenizer, initially evaluating [`sugarme/tokenizer`](https://github.com/sugarme/tokenizer).
The binding requires CGO and a matching native runtime library. Tokenizer support
must be proven against the selected artifact's normalization, special tokens,
input IDs, masks and truncation; a library name is not evidence of parity.
These are proposed dependencies, **not installed, pinned or approved by benchmarks**
in this task. Pin one compatible released binding/runtime/tokenizer combination,
immutable model revision and SHA-256 manifest before the implementation lands.

The ONNX transformer output may be token embeddings. Apply the model's attention-
mask-aware mean pooling and L2 normalization exactly once in the Go adapter unless
those operations are verified to be part of the bundled graph. Test against frozen
reference vectors. [Sentence Transformers ONNX guidance](https://www.sbert.net/docs/sentence_transformer/usage/efficiency.html).

Reserve room within the model's total token bound for special tokens and any
subject/section prefix. Start evaluating roughly 224 content tokens with 32-token
overlap, reducing content capacity when prefix/special tokens require it. Queries,
category text and passages use the same documented encoding, with input roles
recorded in policy; do not borrow E5/query-prefix conventions from another model.
Long category descriptions use the same chunker/aggregation instead of truncation.

### Concrete packaging consequences

Current `scripts/build-sidecar.mjs` hardcodes `CGO_ENABLED=0`. The proposed binding
therefore **cannot be added as a drop-in import**. A later native packaging slice
must explicitly enable CGO for this adapter/core, provide a compatible compiler,
bundle the matching Apple Silicon runtime library, and test code signing/library
loading on the named macOS baseline. SQLite can retain its existing driver; do
not replace the database layer merely to enable inference. [Binding requirements](https://github.com/yalue/onnxruntime_go#requirements),
[ONNX Runtime C interface](https://onnxruntime.ai/docs/get-started/with-c.html).

Bundle reviewed ONNX graph/required weights, tokenizer/config/vocabulary, pooling
policy, immutable artifact manifest, notices and runtime library in fixed app
resources. Extend Tauri packaging and trusted Go resource resolution; do not pass
model paths through the WebView or use arbitrary environment library lookup. Build
preparation may fetch fixed checksummed artifacts from reviewed origins; installed
first use never downloads them. Keep generated model/library binaries outside Git.
The trusted packaged origin, dimensions and checksums are verified before loading.

Keep Rust thin and do not add a Python worker, model server or second database.
Load one local session lazily after enablement with bounded batch/thread limits.
A missing/corrupt resource leaves ordinary sync, reader, keyword/rule/risk behavior
available and reports repair/reinstall guidance. External AI settings/keys are
irrelevant to local inference; no cloud embedding fallback.

The in-process choice reduces process/protocol complexity but shares the native
runtime's crash/memory boundary with Go. Native cancellation, memory caps, package
size, tokenizer parity, offline startup and held-out quality are mandatory gates.
If they fail, revisit the single runtime design explicitly before implementation;
do not quietly ship two adapters, a dynamic downloader or an unrelated model worker.
No model/license security or distribution review is claimed complete by choosing a
candidate. Exact versions, hashes and measured budgets remain an implementation gate.

## 7. Shared retrieval/classification contracts (planned sketches)

The following are consumer-owned design signatures, **not compiling code or a
new arbitrary IPC surface**. Types are conceptual domain values, not SDK types.
Implement the smallest concrete methods needed; do not build a repository framework.

```go
// Internal Go adapter only; role/text originate in authorized Go use cases.
type Embedder interface {
    Descriptor() EmbeddingProfile
    Tokenize(context.Context, string) (Tokens, error)
    Embed(context.Context, []EmbeddingInput) ([]Vector, error)
}

// The same account/source-checked store serves every consumer.
type ChunkReader interface {
    Snapshot(context.Context, AccountID, EmailID, ProfileID) (ChunkSnapshot, error)
    ScanReady(context.Context, ChunkFilter, func(ChunkVector) error) error
}

// Search UI and AI evidence preparation call this same method.
type Retriever interface {
    Search(context.Context, SearchQuery) (SearchPage, error)
}

// Preserve the existing classification consumer shape where practical.
type ClassificationEngine interface {
    Name() string
    Version() string
    Classify(context.Context, domain.Email, []domain.EmailCategory) (domain.ClassificationRun, error)
}
```

`ChunkSnapshot` carries account/email/profile/generation/source hash, completeness
and ordered source passages/vectors. `ChunkFilter` always includes account/profile,
visible-current-source/active-generation conditions and bounded metadata filters;
there is no unscoped scan. `SearchQuery` includes authorized account, literal user
query, bounded filters/page/cursor and desired available mode, not user-supplied
vectors, model paths, SQL or hidden source text. `SearchPage` returns message IDs,
source revisions, ranked chunk evidence, mode/coverage and a generation-bound cursor.
The visitor scan is bounded/streamed and keeps SQLite access inside Go.

The shared semantic classifier is constructed with this `ChunkReader`, compatible
category/example access and the existing rules; it calls no embedder for email
bodies. Missing/stale chunks return an explicit queued/unavailable semantic state
or rule fallback, not an implicit mailbox-wide rebuild. The index coordinator owns
all durable embedding work, including category prototypes, and publishes the
current snapshot before dependent classification can use it.

Use one tested `Cosine`/finite-vector validation implementation for exact ranking
and category comparisons. Search embeds its query once, scans matching ready chunks,
retains bounded top-K, groups by email and deduplicates overlaps/quoted copies.
Hybrid mode merges lexical/semantic ranks rather than adding incompatible scores.
Classification compares one email's same stored vectors to category prototypes and
confirmed example vectors; do not search top-K across the entire mailbox and treat
unreviewed neighbors as category labels.

Aggregate a bounded number of nonoverlapping section-weighted matches per category
rather than the unbounded best score from every chunk. User primary corrections
win, useful decisive rules constrain/fallback, explicit rule conflicts abstain, and
semantic decisions require a measured positive score threshold and runner-up margin. At most
one primary suggestion is returned, with supporting chunk IDs and category/example
provenance. Score kind and uncertainty are explicit; the 0.60 rule gate must never
be reused as a cosine probability threshold.

## 8. Existing classification and journal incompatibilities

This is not a drop-in classifier swap. Resolve these boundaries before exposing
semantic suggestions as actionable label candidates:

| Existing boundary                                                           | Required compatibility treatment                                                                                                                                                                                                                                                                                                                                                                                                                                                               |
| --------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `classification.InputHash` and SQLite `Replace`/`Get`                       | They recompute one rule-specific hash from mail/categories. Add version-aware evidence validation that includes profile/generation, source text, new header inputs, definitions/examples/overrides and rule/aggregation versions. Retain the legacy branch/codecs for stored runs.                                                                                                                                                                                                             |
| `validClassificationRun`, `ClassificationResult.Confidence`, v1 review DTOs | Candidate codecs already preserve signed cosine in [-1,1]; accepted results require positive 0–1 values and historical cosine rows mirror the winning candidate in the legacy `confidence` column. Reuse these codecs with a measured positive acceptance gate; negative/weak candidates remain unclassified. Expose explicit score kind/value and richer evidence through coordinated typed DTOs, not a probability or clamped/remapped score. No duplicate score column is initially needed. |
| `Organization.prediction`, `matchesOrganizationInput`, current preview rows | They check current engine name/version and rule hash, and carry scalar confidence. Route new previews through versioned shared-assessment freshness/evidence checks; never bypass old checks with a fake rule hash. Old preview evidence retains its original meaning.                                                                                                                                                                                                                         |
| `domain.OrganizationPlan`/row serialization and stored digests              | A new evidence field can change marshaling/digests even when defaulted. Keep old serialization/digest paths exact; version new plans explicitly and verify old fixture round trips before adding a new-plan extension. Reconciliation and verified undo remain based on saved outcomes.                                                                                                                                                                                                        |
| `email_categories` multi-row assignments                                    | Preserve them and distinguish a newly confirmed primary preference. No automatic deletion/backfill choosing one arbitrary historical category.                                                                                                                                                                                                                                                                                                                                                 |
| Old 008/009/010/013 vectors                                                 | No reliable source passage/order/section/model-contract mapping exists for the new design. Do not convert or count them as ready chunks; build new derived sets from current authorized source only. Preserve old rows/schema and necessary cleanup tests.                                                                                                                                                                                                                                     |
| Current Summary/provider settings/014                                       | Keep model/provider/key slots and current behavior. New multi-source QA needs new typed consent/operation with fresh acknowledgment; old enablement/approval does not transfer.                                                                                                                                                                                                                                                                                                                |
| 011 draft journal and source FKs                                            | Historical draft recovery needs neither current embeddings nor AI keys. Preserve tombstones, immutable reviewed bodies/routing/digests and GET-only matching; never recreate/resend/delete drafts or revive a preview.                                                                                                                                                                                                                                                                         |

Semantic result persistence and organization integration must be delivered together
before those new results become selectable for Gmail writes. During staged rollout,
keep the rule-only write path or disable semantic label selection visibly. A rebuilt
index or changed category/model never grants a pending old preview new authority.
Existing uncertain label writes reconcile before any retry; undo removes only
verified MailPilot-owned additions and preserves unrelated changes. No Gmail
network call is made inside a database transaction.

## 9. Privacy, security, consent and IPC

All queries, source loads, chunks, examples, category decisions and write previews
are account-scoped with composite FKs and Go authorization. The UI can request IDs,
filters and explicit questions, not arbitrary source bodies/vector data or storage
paths. Keep 64 KiB private pipe frames; return bounded results/jobs/status instead
of streaming giant vectors to React or monopolizing the sequential dispatcher.
New retrieval/index/classification/evidence operations require coordinated Go/Rust/
TypeScript fixtures/allowlists and a deliberate protocol/version decision. Old
Ask/semantic/draft fixtures remain rejected, even if their names resemble new work.

Email/MIME/HTML/headers, vectors and model output are untrusted. Keep escaped current
reading; rich HTML remains a separate sanitized/capability-free renderer task. No
attachment execution/download, remote images, link following, automatic mark-as-read
or external model calls from sync/index/classification. Similarity/categories do
not influence the independent phishing verdict or establish sender authenticity.

Local inference transmits no mail. Optional external QA calls the **same retriever**
locally, then Go reconstructs a selected immutable evidence bundle from approved
source revisions. Show exact passages/question/metadata/policy/provider/model and
expiry before separate single-use approval. Bind approval to account/client/session,
payload bytes/order, source/profile evidence, settings/key/provider/model identity;
revalidate before dispatch and publication. No hidden thread expansion, background
AI, tool list, generic agent, model-controlled retrieval or arbitrary endpoint.
Validate citation IDs/quotes and render escaped output; provenance does not prove
truth. Cancellation cannot recall already disclosed text. Fresh approval is needed
for retry; previews/status/settings transmit no email externally.

Preserve multiple-provider architecture and OS-only OAuth tokens/API keys, failing
closed with no file/SQL/browser/environment/argument fallback. Developer OAuth app
configuration is distinct from user secrets. Clear submitted key inputs, sensitive
UI/evidence buffers and cancelled-session results; log safe operational codes only.
SQLite/source/chunks/FTS/vectors/prototypes/examples/WAL/backups remain sensitive
unencrypted local data. Explain retained journal evidence; no forensic deletion
claim, telemetry, hosted backend or cloud vector/embedding service.

## 10. Rollout and verification gates

These are technical increments under the existing product milestones, not permission
to start work. Do not allocate migration numbers until the implementing slice sees
the actual maximum; preserve prior dirty edits and keep intermediate builds usable.

| Increment                                       | Reuse/addition                                                                                                                            | Required gate                                                                                                                                                                                    |
| ----------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| 0 — Baseline and native feasibility             | Record current hashes/fixtures and qualify the proposed model, tokenizer, ONNX/CGO ABI and signed/bundled resource path on Apple Silicon. | License/artifact manifest, tokenizer/vector parity, native memory/cancellation and offline package feasibility; revise the single runtime decision if needed. No live mail/paid AI.              |
| 1 — Shared source/chunk contract                | Extend `internal/mail`; preserve existing `CleanBody` consumers, reader and risk.                                                         | Forwarded-only evidence, quoted/signature sections, MIME alternatives, UTF-8/source offsets, deterministic hashes, model token budget and partial/oversized source tests.                        |
| 2 — Additive shared store and local coordinator | Required core schema only, source/checkpoint hooks, staged publication, bundled adapter and durable bounded batches.                      | Migration checksums/upgrade/FKs, unchanged reimport, stale worker, cancellation/purge/restart, dimension/finite-vector and zero-network native inference tests.                                  |
| 3 — Keyword plus exact/hybrid search            | Shared source IDs/excerpts, FTS lifecycle, one retriever and typed UI.                                                                    | Account/filter/isolation, partial coverage, source revision/cursor changes, exact-identifier fallback and held-out Recall@10/nDCG@10 versus lexical/vector baselines.                            |
| 4 — Shared semantic category consumer           | Add category/example/primary metadata only now; adapt existing review/storage and versioned organization evidence.                        | One-primary/abstention, legacy assignments, rule conflicts, example/source/model changes, category precision/coverage and old-plan recovery/undo fixture compatibility. No automatic labels.     |
| 5 — Optional evidence QA                        | Reuse secure settings/consent/transport; one typed answer workflow using the same retriever; later separately tested Summary transition.  | Zero hidden disclosure/replay, unsupported citation/refusal/insufficient evidence, prompt injection, no-key/disabled-provider, source-change/cancel/shutdown and second synthetic adapter tests. |
| 6 — Qualification                               | Integrate local-data controls, safe offline access claims, coordinated package/contracts and accessible reader/search/review flows.       | Full relevant language checks plus real native startup/resources/shutdown, upgrades, measurements, privacy disclosure and no retired-operation reactivation.                                     |

Required future checks: Go tests/vet/race; frontend typecheck/lint/tests/build;
Rust fmt/clippy/tests; native smoke for packaging, runtime loading and lifecycle.
Use synthetic mail, temporary SQLite, fake providers/keyrings and retained journal
fixtures. Live Gmail writes or paid provider calls require separate authorization.

At about 10,000 messages, report actual chunk count, warm/cold latency, indexing
throughput, peak RSS, disk and installer size. Proposed warm-search target remains
p95 below 500 ms on a named 50,000-chunk fixture; this is not a measured result or
coverage cap. Report category precision/recall with abstention/denominators, source
citation support and no-answer cases. Do not tune on held-out threads/templates.
A model that fails quality stays disabled/reselected rather than forcing categories.

Cold-start cache authorization remains a separate gap: current restore refreshes
OAuth/profile online. Do not weaken credential checks to advertise offline mode.
Existing session-local reading/search is distinct from secure offline restart.

## 11. This task's limits and incompatibility report

No application edits, new migrations/DDL, dependency/artifact downloads, data
inspection/deletion, credentials changes, commits or pushes are performed here.
Documentation checks and non-Markdown content hashes verify preservation; they do
not claim model parity, benchmarks, native ONNX packaging or runtime tests passed.

The important incompatibilities are: historical vectors cannot become the new
chunk store by renaming; old rule hash/score DTOs cannot carry shared semantic
freshness without versioned extensions; old plan digests must remain exact; multiple
legacy assignments cannot be collapsed automatically; current CGO-disabled build
cannot import the proposed native binding unchanged; current Summary consent cannot
authorize retrieved multi-message evidence. All are handled additively/gated above,
without deleting data, historical migrations or recovery evidence.
