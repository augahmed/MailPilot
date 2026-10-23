# Shared email chunk-and-vector architecture

Reviewed 2026-10-21 against current composition and callers. The historical filename
is retained for links; this document describes implemented components and concrete
compatibility constraints. It contains no speculative schema, reserved framework or
implementation milestone. [PRODUCT_PLAN.md](PRODUCT_PLAN.md) records product scope,
[ARCHITECTURE.md](ARCHITECTURE.md) records runtime boundaries and remaining debt,
and [AGENTS.md](../AGENTS.md) governs changes.

## Runtime and reuse map

```text
Gmail history/snapshot → original SQLite mail → mail.Sections / Chunker
  → email_chunks → explicit LocalIndex → one embedding.Provider
  → chunk_embeddings → shared retrieval + semantic classification
  → separately approved AI evidence answers
```

| Owner                                                   | Implemented responsibility                                                                                                                                        |
| ------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `backend/cmd/mailpilot-core/main.go`                    | Composes one Go database owner, Gmail/auth, the local provider/coordinator/retriever/classifier, AI and historical recovery; joins workers before storage closes. |
| `internal/provider/gmail`, `sync`                       | OAuth/network translation, bounded manual snapshot/history import, incomplete-body retries and atomic source/checkpoint updates.                                  |
| `internal/mail`                                         | Shared cleaned sections, source mapping, deterministic chunks, pinned offline WordPiece tokenizer and canonical `ModelInput`.                                     |
| `internal/embedding`                                    | One lazy bounded CPU MiniLM provider, model identity and native resource lifecycle; no mailbox, SQLite or cloud service.                                          |
| `internal/repository`, `store/sqlite`                   | One shared chunk/vector/prototype/assessment/FTS store with account-qualified SQL and immutable migration/journal history.                                        |
| `app.LocalIndex`                                        | Sole durable coordinator: chunks/vectors, category prototypes, then dependent assessments; explicit start/control, no background auto-enablement.                 |
| `internal/retrieval`, `app.LocalSearch`                 | Shared cosine kernel and keyword/semantic/hybrid ranking; a transient explicit query worker, no separate index job.                                               |
| `internal/classification`                               | One active semantic classifier over existing shared vectors with deterministic rule fallback, abstention and explicit preferences.                                |
| `internal/risk`                                         | Independent deterministic phishing extraction/scoring.                                                                                                            |
| `app.AIService`, `internal/ai`                          | One provider-neutral Summary/evidence-answer boundary and transient work/consent lifecycle; no model tools/actions or second vector store.                        |
| `app.ReplyService`, `internal/reply`, Gmail `FindDraft` | Historical GET-only draft recovery, immutable codecs and local outcome transitions; no generation/creation API.                                                   |
| React/strict TypeScript, Rust/Tauri                     | Presentation, strict private IPC and fixed packaged lifecycle; no generic SQL/fetch/filesystem/shell bridge.                                                      |

Manual sync projects bounded local cached sources and never infers. The coordinator
scans the whole live cache and embeds only missing compatible inputs. Search/AI
retrieval and classification consume the same chunks/model space. Distinct transient
search and approved-generation jobs have no durable indexing queue of their own.

## Implemented persistence

The executable schema is migrations **001–020**, unchanged byte-for-byte. There is
one SQLite database with one pooled Go connection, foreign keys on every connection,
WAL, FULL synchronization, busy timeout and checksummed transactional migrations.

Migrations 015/016 own shared chunks/provenance; 017 owns durable job/model identities;
018 owns category prototypes and confirmed examples; 019 owns semantic assessment
and primary-preference revisions; 020 owns external-content FTS5 over shared passages.
See [migration responsibilities](ARCHITECTURE.md#implemented-persistence-and-immutable-history).
Original cached text/HTML, headers, completeness and provider identities remain
separate from derived text; no migration guesses or reinterprets old vectors.

### Implemented minimal shared index records

The explicit persistence request chose separate chunks, model-versioned embeddings
and per-email state. This supersedes the earlier proposal for vectors inline in
chunk rows plus profile/chunk-set/work tables. There is one shared vector store;
`chunk_embeddings` is its sole embedding table, not a second pipeline or cache.

| Table              | Implemented fields and ownership                                                                                                                                                                                                                                                                                                                                                                                                                     |
| ------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `email_chunks`     | Opaque `id`; `account_id`, `email_id`, contiguous zero-based `chunk_index`; `section_type`, exact `text`, `token_count`; `source_revision`, half-open UTF-8 byte `start_offset`/`end_offset`, text SHA-256 `content_hash`, `preprocessing_version`; 016 adds `source_kind`, `section_index`, `section_start_offset`, `section_end_offset`, `context_text`. Composite email FK with cascade, unique account/email/ordinal and account/chunk identity. |
| `chunk_embeddings` | `account_id`, `chunk_id`, `model_id`, `model_version`, `dimension`, nullable `vector_data`, `status` (`pending`, `ready`, `failed`). Composite account/chunk FK with cascade; primary key account/chunk/model/version. Ready BLOB byte length must equal dimension × 4; other statuses carry no vector.                                                                                                                                              |
| `index_state`      | Account/email primary key and cascading composite email FK; source/preprocessing/index versions; active model/version/dimension; opaque `generation` work token; source `body_state` and projected `content_status`; index `status` and nullable `last_indexed_at`. No scheduler, lease, retry or profile framework.                                                                                                                                 |

SQL-free contract: [repository/chunks.go](../backend/internal/repository/chunks.go).
Concrete implementation: [sqlite/chunks.go](../backend/internal/store/sqlite/chunks.go).
`Store.Chunks()` provides `Replace`, `State`, paged `List`, `SaveEmbeddings` and
`Embedding`. No new public command, inference, network calls or historical-vector fallback
is added. `app.CachedChunker` now supplies versioned chunks to this store during
manual sync; stored text remains untrusted. Upstream body completeness is retained.

`Replace` validates and atomically replaces a complete chunk snapshot. Shuffled
input is sorted and must have contiguous ordinals. Identical snapshots retain IDs,
generation and timestamps. Unchanged exact text/context/section/token-count/preprocessing
inputs reuse vectors, including chunks moved to new ordinals. Matching prefers
original ordinals for duplicates; reuse is bounded to the same account and email.
Removed/changed inputs cascade their vectors; preprocessing changes require new
chunks. Model changes retain compatible chunk identities and versioned vectors;
switching back can reuse the old recipe. Model version identifies the complete
artifact/tokenizer/pooling/input-policy recipe; incompatible dimensions require a
new version. There is no cross-message/global vector deduplication.

Source revision is the existing cached `EmailContentHash`, not provider
`content_revision`, `updated_at` or the chunk hash. Source hash recipe v2 omits
opaque attachment retrieval handles while retaining all other attachment metadata.
Affected old hashes transition only on normal source upserts, invalidating old
derived state through the existing source fence; immutable journals are unchanged.
No tokenizer, cleaned text or model input changes, so their recipes stay pinned.
Migration triggers participate
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
when all chunks have valid vectors and cached/projected body coverage is complete, `unavailable` for
zero chunks, and `stale` after invalidation. Individual failures stay in embedding
status. `last_indexed_at` is set when every stored chunk has a valid vector, including
partial-body coverage; consumers must also inspect body/index status. It is cleared
on invalidation or incomplete vectors. A partial body never becomes complete because
its available chunks were embedded. Paged storage reads can expose pending/partial
chunk metadata; semantic retrieval additionally requires compatible ready vectors
and report incomplete coverage.

Bounds: 4,096 chunks and 4 MiB aggregate text per snapshot, 64 KiB per chunk,
100 embedding writes per atomic batch, dimension 1–8,192; paged chunk reads use the
existing 1–100 row limit. Offsets measure bytes into a caller-defined normalized
section and must span exactly the stored text. The chunker now produces/reproduces these named projection mappings. The store
validates spans; it does not silently truncate or promise coverage for rejected
oversized inputs. Legacy chunks retain unknown mapping fields. No transaction crosses inference.

Existing migrations 001–014, original emails, category assignments and label/draft
journals are unchanged. Old noncanonical source hashes remain readable; indexing
requires a current 64-character canonical hash, obtained through normal import, not
an automatic migration rewrite. Semantic rules/consent/recovery formats are unchanged.
Migration itself does not backfill. A later manual sync performs bounded cached
chunk preparation without inference. The bundled model is executed only through
the separately started index job.
Further category/consumer metadata must extend this shared store only as
needed and preserve its account, revision and vector-validation guarantees.

### Implemented deterministic chunk production

Source files: `internal/mail/sections.go`, `chunks.go`, `wordpiece/tokenizer.go`,
`internal/app/chunks.go`. One pure-Go tokenizer uses the selected MiniLM vocabulary
at revision `1110a243fdf4706b3f48f1d95db1a4f5529b4d41`; vocabulary SHA-256 is
`07eced375cec144d27c900241f3e339478dec958f92fddbc551f295c992038a3`.
Text assets, configuration, license and notice are embedded in the Go binary.
There is no native tokenizer dependency, weight download, Python requirement or
inference. 167 frozen parity cases come from Hugging Face tokenizers 0.21.4 and the
pinned official tokenizer.json, with padding/truncation disabled. This qualifies
these tokenizer cases, not embedding quality/native runtime compatibility.

`mail.Sections` accepts already MIME/charset-decoded cached mail, preserves the cache,
normalizes line endings/NULs into named `subject`, `body_text`, `body_html` projections,
and performs bounded text-only HTML extraction plus shared `readable-v4` cleanup
of transport placeholders/tracking clutter. Useful image descriptions, original
quotes/forwards and unique alternative text are retained; the reader uses those
same sections. See [clean text contract](CLEAN_TEXT.md). Source offsets are half-open UTF-8
byte offsets in those projections; section index and full section span are stored.
They are not offsets into raw HTML/MIME. Re-running the same preprocessing version
on the unchanged cached source reproduces them. Existing rule/Summary `CleanBody` is unchanged; reading now uses this cleaned
projection and binds page revisions to its version plus source hash.

Sections distinguish `subject`, `authored`, `quoted`, `forwarded`, `signature` using
conventional reply/forward/signature markers and HTML blockquote/class hints.
Wrapped reply attributions are recognized. These are structural heuristics, never
authentication. No recognized section is stripped; forwarded headers/content remain
as evidence. Attached messages remain attachment metadata, not hidden body fetches.
MIME alternatives are selected without concatenating duplicate HTML alternatives.
When plain and HTML represent the same text, use one projection with HTML hints;
otherwise cleaned visible HTML is primary and unique plain-text evidence is
retained by section. Whitespace-normalized comparisons prevent duplicate alternatives; this is
not semantic deduplication and cannot reconstruct omitted MIME parts.

Chunk indices are contiguous and deterministic. Each section is chunked separately
with a 256-token total model-input ceiling, including CLS/SEP plus bounded context.
Prefer paragraph/sentence ends in the latter part of the window; otherwise split at
whole-word WordPiece boundaries. Passage overlap starts at about 32 tokens and can
expand to a whole word; no overlap crosses a section. `TokenCount` is the exact
`ModelInput` count including specials, without padding. The stored passage remains
an exact projection substring; `context_text` contains its small subject/section
prefix. Only about 22 subject tokens plus `Subject:` (at most 256 subject bytes)
repeat; the complete subject is also chunked once. Arbitrary headers and recipient
lists are never repeated as model input. A changed context invalidates vector reuse.

`ContentStatus` distinguishes complete/partial/unavailable projected body coverage
from the cached `BodyState`. Subject-only evidence never claims a complete body.
The manual-sync chunking workflow computes no vectors; fresh rows remain pending
with NULL data. The separate explicitly started `app.LocalIndex` uses the native
provider to populate them.
Model recipe: shared `embedding/model.Version`
(`minilm-1110a243-6fd5d72f-f32-mean-l2-s24-v1-u17.0.0-nfd17.0.0`), dimension 384;
preprocessing: `email-sections-readable-v4-wp-07eced375cec-256-32-u17.0.0-nfd17.0.0`. Both include the
Go Unicode and NFD table versions so toolchain upgrades cannot silently reuse
incompatible vectors. Any algorithm/tokenizer/input
policy change requires an explicit recipe/preprocessing version and rebuild.

Bounds additionally include 1 MiB per cached body representation, 32 KiB subject,
32,768 plain-text newlines, 100,000 HTML tokens/128 nesting levels and 262,144 raw
WordPiece tokens per input. Excessive source, section/chunk counts or giant
single passages return a content error; they are not silently truncated. Context
cancellation is checked during parsing/tokenization/chunking and persistence.

`CachedChunker.Prepare` reads one committed cached message and atomically replaces
its derived chunks; concurrent source changes reject publication. Manual sync also
calls `PrepareCached` to scan at most 10,000 summaries and attempt at most its sync
limit of missing/stale recipes; current recipes skip body reads/tokenization. No
background scheduler or wider Gmail fetch is added. Source/checkpoints commit first;
chunking failures leave cached mail readable, report `chunking_failed`, and are
retried in later manual cache passes. New source/model/preprocessing revisions
invalidate old work through the existing shared store. The separately authorized
provider now enables CGO on Apple Silicon and packages a verified native runtime.
No embedding scheduling or automatic vector publication is added to manual sync.

### Implemented resumable local indexing

[LOCAL_INDEXING.md](LOCAL_INDEXING.md) is the authoritative coordinator contract.
`app.LocalIndex` selects all live account-scoped cached mail in bounded candidate
pages, reuses current chunks/vectors, and persists at most four new vectors per
transaction. Migration 017 adds one `local_index_jobs` row per account,
`local_index_models` identity metadata, and attempt/failure fields in `index_state`;
there is no duplicate vector store or generic queue. Every background replacement
and vector publication verifies both job and source/chunk generations. Pause/cancel,
restart and cache removal revoke old work. Startup never automatically infers;
Resume is explicit. Rebuild reprojects all cached sources and reuses compatible
vectors. The narrow additive `index.local` contract/UI exposes only controls and
coverage counts, not raw inference/model selection. Failed/partial/pending mail
is never presented as complete; finalization rechecks durable work transactionally.
Synthetic offline native performance is recorded in the indexing contract; target
10,000-message and retrieval quality qualification remain open.

## Bundled local inference

### Implemented single provider

The previously selected **`sentence-transformers/all-MiniLM-L6-v2`** remains the
model. Its pinned FP32 ONNX export matches the existing tokenizer and passes real
synthetic relevance, batch/single parity, maximum-batch, normalization and native
cancellation/reuse tests. No replacement model or second tokenizer was needed.

The [local embedding contract](LOCAL_EMBEDDING.md) is the authoritative implementation
and qualification record for this slice. `internal/embedding` exposes lazy
`EmbedDocument`, `EmbedQuery`, descriptor and context-aware `Close`; CPU ONNX Runtime
1.23.2 and `onnxruntime_go v1.23.0` are pinned. Masked-mean pooling/L2 normalization
produce validated 384-dimensional vectors. Queries use the same symmetric encoder;
no query instructions from another model are invented. The provider imports neither
SQLite nor product services. `embedding/model` owns the pure-Go artifact/encoding
identity used by chunk metadata.

### Native packaging and remaining limits

`scripts/prepare-embedding.mjs` acquires checksummed fixed public artifacts only at
build time. `build-sidecar` now enables CGO for Apple Silicon and Tauri bundles the
90,405,214-byte model, 35,138,784-byte native library and required notices in fixed
resources. Tokenizer/configuration/notices remain embedded in the Go core. Installed
first use performs no download and requires no Python. Weights/runtime binaries are
ignored by Git; the manifest and module sums are committed source inputs.

Runtime resource lookup derives only from the fixed sidecar location. Model SHA-256
and signature-independent Mach-O code fingerprints reject altered artifacts; OS
signature verification and native re-signing/offline package checks are described
in the implementation contract. No arbitrary path, executable, plugin provider,
network endpoint or cloud fallback is exposed. Telemetry is explicitly disabled.

The official arm64 dylib requires **macOS 13.4+**. Ordinary app features retain the
13.0 baseline, with the provider unavailable before 13.4 or on unsupported targets.
CPU-only use avoids another GPU/runtime dependency, though the upstream library
links system Apple frameworks. Batches are capped at four 256-token inputs with
bounded contexts and native ORT run termination. Session construction itself cannot
be preempted; callers can cancel waiting and cleanup joins it after it returns.
There is no hard process-memory cap.

Local ad-hoc packaging/offline qualification does not claim Developer ID notarization,
Gatekeeper distribution approval, other-platform inference, a mailbox quality
benchmark or 10,000-message throughput. Measured memory/latency, test commands and
remaining packaging limits are recorded in [LOCAL_EMBEDDING.md](LOCAL_EMBEDDING.md).
Indexing, retrieval and classification reuse this one adapter, model identity,
tokenizer and shared chunk store.

## Shared category and classification consumers

Migration 018 and `CategoryVectorRepository` retain account/category/profile-bound
prototypes and explicit examples referencing shared email chunks. Category definition
inputs use the same tokenizer/encoder. Ready/partial/stale prototypes include model,
fingerprint, recipe, source/definition hashes and finite vectors. Editing definitions,
examples or their sources invalidates dependent prototypes and assessments. Category
work runs after email vectors in `LocalIndex`; there is no feature-specific queue.
See [category contract](CATEGORY_VECTORS.md).

Migration 019 and `SemanticRepository` bind assessments to source, chunk generation,
category/prototype/example inputs, per-email preferences/labels and classifier/model
profile. `classification.SemanticClassifier` reads existing vectors; it never embeds
mail. `SemanticRules` supplies deterministic fallback through the same assessment
boundary. The rule engine remains necessary; it is not an obsolete semantic stack.
Suggestions include categories above the 0.20 floor and within 0.03 of the best score.
Scores are not probabilities. Explicit primary preferences and abstention remain
separate from Gmail labels and confirmed training examples. Preserve legacy multiple
assignments, classifier hashes/candidate decoding and immutable organization evidence.
See [classification contract/evaluation](SEMANTIC_CLASSIFICATION.md).

## Search and approved evidence

`retrieval.VectorSearch` streams complete compatible account-scoped vectors using
exact cosine. Shared external-content FTS5 also retrieves prepared partial text
without embeddings. Hybrid fuses email ranks, prioritizes literal identifiers and
can disclose keyword-only fallback if inference is unavailable. Both branches check
source/generation/filter membership before publication and completed-result access.
No active consumer reads historical one-vector-per-email or passage caches.
See [search](VECTOR_SEARCH.md) and [hybrid contract](HYBRID_SEARCH.md).

`AIService.Answer` uses that same retriever. Go constructs bounded current excerpts,
question, policy and exact provider/model payload; separate one-use expiring approval
binds account/session, source generations, payload, settings and actual key identity.
There is no model-controlled expansion, mailbox/search tool, autonomous action,
background external generation or arbitrary endpoint. Validate approved source aliases
and exact quoted substrings; render escaped citations. Provenance is not factual
entailment. Summary retains a separate one-message approval and cannot authorize RAG.
The shared provider boundary supports substitution and provider-scoped OS keys;
OpenAI is the only shipped adapter, with a second synthetic adapter used in tests.
See [evidence-question contract](EMAIL_QUESTIONS.md).

## Source, worker and coverage fences

[SYNC_LIFECYCLE.md](SYNC_LIFECYCLE.md) records the complete source-to-consumer behavior.
A source/checkpoint transaction immediately makes outdated chunks ineligible; local
preparation may follow without risking stale search visibility. Source/preprocessing/
model/job generations fence every computed write. Exact unchanged inputs, including
moved chunks, can retain vectors. Label-only changes retain text vectors while
refreshing consumed classifier/filter evidence. Repeated identical observations do
not invalidate current assessments. Confirmed deletion clears active shared indexes
and revokes publication while retaining journal-required source identity.

`IndexRepository.Coverage` reads job, mail/vector coverage, category readiness and
assessment counts in one SQLite transaction. Polling never infers. Committed batches
survive pause/cancel/restart; startup revokes interrupted workers and requires Resume.
Source changes during an authorized run are picked up before completion; later changes
require another explicit run. Partial/unavailable content cannot claim full coverage.

## Retired runtime and retained compatibility

The old embedding service/Python worker/downloader, message-vector search paths,
separate indexing jobs, older semantic/hybrid classifiers, Ask implementation,
reply editor, generic generation/thread-context methods and Gmail draft-creation
capability were already removed. They remain absent. Old `ask.manage` and
`search.semantic` commands are rejected at IPC; negative fixtures are retained.
All current frontend controls have active consumers; Summary and historical recovery
are live. No unused direct dependency or runtime model/interpreter/provider path was
found. No replacement dependency/framework was introduced.

The 2026-10-21 cleanup removed unused generic mailbox-plan creation from the runtime
`MailboxActionRepository`. Its producer remains solely in synthetic compatibility
fixtures; legacy reads/recovery outcomes, original digest shapes and shared journal
validators remain. Current reviewed plans use `OrganizationRepository.Create`.
Redundant recovery forwarding and a test-only production busy accessor were removed;
the historical recovery progress message no longer describes removed AI settings.

Historical `email_embeddings`, `category_embeddings`, `email_passage_vectors` and
`email_retrieval_indexes` remain inert. Their migration definitions, invalidation
behavior and upgrade tests remain. Original draft plans, routing/body/source evidence,
label journals and old classifier score/candidate codecs remain decodable; none
re-enables generation, sending or draft dispatch. Preserve original numbered migrations
and old digest meanings. See [historical record](archive/LEGACY_FEATURES.md),
[current architecture/debt](ARCHITECTURE.md#legacy-retirement-audit-and-remaining-debt)
and [draft recovery](REPLY_DRAFTING.md).

## Validation and remaining qualification limits

Synthetic fixtures cover account/model/source isolation, invalidation and stale
workers/results/consent, partial imports/vectors, bounded retries, cancellation,
restart, missing/disabled inference, citation validation and reviewed label recovery.
Run Go tests/vet/race, frontend typecheck/lint/tests/build, Rust fmt/clippy/tests and
native lifecycle/resource checks. Native qualification uses fixed offline packaged
resources, synthetic mail and strict CGO checks; tests do not mutate a live mailbox
or call a paid provider.

Recorded measurements and limitations are in the linked embedding, search,
classification and question contracts. Semantic precision and dense-scan latency
remain below their published targets; real-mailbox quality, additional shipped AI
adapters, rich HTML/attachment support, cold-start offline authorization,
other-platform native inference and Developer ID notarization are not implemented
or qualified by those synthetic results. Future changes require explicit scope;
these gaps are not permission to add another runtime, index or generic framework.
