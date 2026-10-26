# Shared local vector search

Implemented 2026-10-20 over the existing migrations 015–017. No schema, model,
tokenizer, dependency, external AI authority or Gmail write behavior changed.
This document records the original semantic slice and its measurements. Keyword/hybrid retrieval now extends it through migration 020; see [current hybrid contract and held-out evaluation](HYBRID_SEARCH.md). Semantic classification is also implemented separately.

## Service and compatible sources

`internal/retrieval.VectorSearch` is independent of AI generation and classification.
`SearchChunks(ctx, account, query, k, filter)` embeds a natural-language query with
the existing `embedding.Provider.EmbedQuery`, then returns the exact top-K chunk
neighbors using cosine similarity. It retains a bounded worst-first heap. Results
include full chunk text/provenance, original subject/sender/date, source email and
chunk IDs, source revision and score. Vectors never leave Go.

`Search` provides the message-oriented view: top-K distinct emails ordered by their
maximum chunk cosine, retaining up to three distinct best passages per email.
The SQLite stream is ordered by email/ordinal/ID, so only one message and bounded
winning groups are retained. Duplicate text and overlapping offsets in the same
source projection/section are suppressed, always keeping the higher-scoring
passage. Separate email replies/forwards remain separate results, preserving
conflicting statements; repeated threads do not receive a score boost. Ties use
email ID, chunk ordinal and chunk ID, all ascending. Raw `SearchChunks` deliberately
preserves overlaps for consumers that need the exact chunk-level ranking.

The store streams one consistent SQLite read snapshot with account-qualified
joins across `email_chunks`, `chunk_embeddings`, `index_state` and original mail.
Only `ready`, complete-body/complete-projection snapshots with a non-null completion
time qualify. Source hashes, preprocessing/index versions, model ID/version/dimension
and the existing registered model fingerprint must all match. Partial/pending/stale,
failed/unavailable, deleted, unregistered and incompatible generations are excluded.
A partially completed account indexing run can still supply its fully ready messages.
The scan validates passage hashes/mapping and finite nonzero float32 vectors, uses
float64 dot/norm accumulation, and never assumes unit length. It loads one vector
at a time, reusing its buffer, rather than caching the corpus in memory.

Filters are applied before scoring: literal ASCII case-insensitive sender name or
address substring (SQL metacharacters are literal); inclusive UTC received-after,
exclusive UTC received-before epoch milliseconds; and assigned local category ID
(account-scoped, including retained disabled-category assignments). A category
suggestion is not an assignment. Category filters never run classification.
SQLite's built-in case folding is ASCII; Unicode sender substring matching is not
advertised as case-insensitive. Dates refer to original received metadata.

All calls accept contexts. Queries are bounded to 2,048 UTF-8 bytes and the shared
254 WordPiece-token input budget, with no silent truncation. K is 1–20. Stored
source and generation identities are rechecked after ranking and before every
completed-result poll. Source deletion/replacement, rebuilding and filter membership
changes invalidate retained evidence. A changed corpus can add new neighbors after
an earlier search; rerun search to rank those. Coverage is always current, not a
claim that a previous top-K is exhaustive.

## Application, UI and privacy

`app.LocalSearch` derives the account from the active authorized session, requires
an explicit Search action for query inference, and shares the one provider with
indexing. Its single memory-only query worker has a 30-second deadline. Narrow
`search.local` / Tauri `vector_search` start/status/cancel operations return promptly
through serialized IPC. Cancellation suppresses late results. Session replacement
clears sensitive results and joins the revoked worker before accepting another;
shutdown joins search before closing indexing/provider and SQLite. Results expire
after five minutes, with expiry enforced on access. Nothing is written to browser
storage or to a second index; no Gmail fetch, AI provider, key or AI consent is used.
Status polling never runs inference. Ordinary reading works without embeddings.

The original Semantic search form exposed query, sender, UTC dates, assigned categories and
5/10/20 maximum emails. It shows searchable/eligible coverage, cancellation, errors,
cosine scores and escaped excerpts (up to 600 UTF-8 bytes per chunk). Metadata is
bounded to 240 bytes per field and encoded responses to 48 KiB. An oversized result
fails with `too_large`, permitting a smaller follow-up query. Nearest neighbors are
candidates, not confidence, factual answers or safety judgments. Unrelated queries
can return irrelevant candidates: no arbitrary relevance threshold is calibrated.

Opening a result loads the original cached message and displays matching excerpts
inside the reader only after the original source and projection revision match.
Search carries the existing reader cursor computed by shared `mail.ReaderRevision`;
a source change returns stale rather than displaying old evidence with new mail.
Reader authorization/revision polling also clears excerpts. Back navigation retains
query/filter/result state and restores keyboard focus to the originating button
(or the workspace if that result was invalidated while reading).
No mailbox read-state change is performed.

See the strict [wire fixture](../contracts/vector-search-v1.json) and
[IPC contract](../contracts/README.md#local-search-additive-protocol-v1-extension).
The retired `search.semantic` and `ask.manage` operations remain rejected.

## Synthetic relevance evaluation

The fixed [evaluation fixture](../backend/internal/retrieval/testdata/evaluation.json)
contains 30 English messages (29 in the searched account, 1 private duplicate in a
second account), 59 searchable chunks and 12 queries. Ten have explicit relevant
email IDs; two unrelated queries have none. Judgments are frozen before measurement.
Macro Recall@K is the mean fraction of judged relevant **emails** found in the first
K grouped results for each answerable query. No-relevant queries are excluded from
that denominator and reported separately. This small synthetic smoke corpus is not
a held-out mailbox benchmark, relevance precision, or an exact-identifier guarantee.

Run on Apple Silicon with the existing offline model assets:

```sh
npm run prepare:embedding -- --offline
npm run test:search:native
```

The fixed script builds the native test in a temporary packaged resource layout,
uses only synthetic SQLite and `sandbox-exec` with all network denied, and enables
strict CGO pointer checks. It indexes both accounts through the shared pipeline,
closes the indexing provider, then initializes a fresh query provider for cold
measurement. Warm timing includes query embedding, SQL scan/cosine, aggregation
and source recheck; it excludes IPC, React rendering and coverage polling. Report
output is ignored `.cache/vector-search-qualification.json` with per-query rankings.
The following measurements are on Apple M2, arm64, using the pinned MiniLM FP32 CPU
provider; no user mail, credentials, paid AI or network is used.

| Measurement                            | Observed                                   |
| -------------------------------------- | ------------------------------------------ |
| Answerable queries / unrelated queries | 10 / 2                                     |
| Macro Recall@5 / Recall@10             | 1.000 / 1.000                              |
| Cold model + first search              | 163.936 ms                                 |
| Warm search p50 / p95, 60 samples      | 5.700 / 6.655 ms                           |
| Paraphrases / names / identifiers      | All judged relevant emails within top five |
| Cross-account results                  | 0                                          |

Important observed limits: `INV-2048-AX` ranked the neighboring invoice first and
the exact invoice second; `Q7M4ZP` ranked its correct itinerary third. Both unrelated
queries returned ten candidates (top scores 0.191 and 0.256). Conflicting budget
updates and the quoted earlier proposal remained independently discoverable. These
results motivated the now-implemented [keyword/hybrid extension](HYBRID_SEARCH.md).
This historical evaluation does not claim full M3 qualification.

## Exact-scan scale benchmark

```sh
cd backend
go test ./internal/store/sqlite -run '^$' -bench BenchmarkVectorSearch50K -benchtime=10x -count=1
```

`BenchmarkVectorSearch50K` creates 10,000 synthetic original cached emails, five
chunks each, and **50,000 SQLite vectors of 384 float32 dimensions**. It exercises
the real compatible-source SQL, cosine scan, deduplication, bounded message ranking
and source recheck. A fixed query vector isolates retrieval from native inference;
its numbers must not be described as end-to-end query latency or semantic quality.
The vectors repeat for stable ties and the benchmark does not measure indexing.

| Apple M2 arm64, ten measured scans | Observed                                            |
| ---------------------------------- | --------------------------------------------------- |
| Retrieval p50 / p95                | 533.8 / 558.8 ms                                    |
| Mean scan                          | 535.39 ms                                           |
| SQLite page size total             | 131,997,696 bytes (125.88 MiB)                      |
| Total allocated bytes / scan       | 319,094,587 bytes (not retained memory or peak RSS) |

Database writes during fixture setup warm filesystem caches; no OS cache eviction
or production cold-storage percentile is claimed. The measured p95 exceeds the
plan's proposed 500 ms target. Exact search remains the baseline; no ANN or SQLite
vector extension is introduced. Profile row decoding and allocation costs with a
representative mailbox before choosing another index/dependency. A production
held-out evaluation, large-corpus native end-to-end latency, peak RSS and release
signing/notarization remain separate qualification work.

## Validation record

Validation passed on the implementation host: full Go tests, vet and race suite;
frontend strict typecheck, lint, tests and production build; Rust fmt, clippy with
warnings denied and tests; packaged native WebView authorization/lifecycle smoke;
real native search evaluation with networking denied; and ad-hoc signed offline
embedding package checks (including copied development layouts). The local `.app`
bundle is approximately 144.33 MiB; no public installer or release signature is
claimed. Existing real native indexing qualification is also run against synthetic
mail. No live mailbox or external AI call is used.

Initial restricted-sandbox checks failed because Go's default build cache, localhost
test listeners, the macOS window server and nested `sandbox-exec` were unavailable.
Reruns used a temporary Go cache and the required test permissions. The default
bundle needed the existing documented ad-hoc signing step before strict signature
verification; the signed rerun passed. These were environment/qualification setup
failures, not silently skipped tests. Release notarization, cross-platform native
qualification and held-out mailbox quality are not claimed by this validation.

The repository-wide `npm run format:check` still reports two **unchanged pinned
upstream tokenizer artifacts**: `special_tokens_map.json` and `tokenizer_config.json`
under `backend/internal/mail/wordpiece/assets/`. Reformatting those bytes would
change the pinned tokenizer fingerprint, so they were preserved. Formatting of
all new/changed search files passes; no formatter exclusion was added to hide the
pre-existing warnings. This outstanding repository-wide style check does not
represent a runtime test pass.
