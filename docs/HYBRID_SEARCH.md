# Shared keyword, semantic and hybrid retrieval

Implemented 2026-10-20. `retrieval.VectorSearch.SearchMode` extends the existing
shared exact cosine search. Everything stays in the existing Go-owned SQLite
database. No model/tokenizer/runtime/dependency changes, cloud service, generative
AI calls, Gmail writes or additional coordinator are involved.

## Ranking and evidence

- `keyword`: literal tokens are quoted by Go and joined with OR for FTS5 BM25.
  User input cannot select raw MATCH grammar. Unicode61 tokenization splits email
  addresses and hyphenated codes; a separate literal boundary check prioritizes
  full queries, embedded addresses/alphanumeric codes/numeric codes of four or more
  characters, and quoted phrases. Search a company name directly, or quote it in
  a longer query. This is deterministic literal matching, not named entity inference.
  Sender name/address literal matches supplement FTS passage matches. Sender
  substring SQL case folding remains ASCII, as in the existing filter contract.
- `semantic`: retains exact cosine retrieval and original stable ties, using the
  pinned local provider/query recipe and compatible complete vector snapshots.
- `hybrid`: each branch first groups chunks into distinct emails; retain at most
  100 candidate emails per branch, then sum `1 / (60 + rank)` for each available
  branch. Missing branches contribute zero. Equal weights and the conventional
  smoothing constant avoid adding incomparable BM25 and cosine scores. Exact
  literal matches form a priority tier before fusion score, then email ID breaks
  ties. Long emails and duplicate chunks receive no extra fusion votes. The fixed
  defaults were selected before measuring the held-out fixture; no held-out tuning.

Return at most 20 distinct emails and three distinct nonoverlapping passages per
email. Keyword evidence precedes additional semantic evidence in a fused result.
Separate replies with conflicting information remain separate source messages.
Keyword excerpts center on a literal/token occurrence when the passage exceeds
600 UTF-8 bytes; source text remains unchanged. Substrings are copied from source,
never generated. Sender-only matches also show the original sender metadata.

`rankScore` is negated SQLite BM25 in keyword mode, best cosine in semantic mode,
and reciprocal-rank sum in hybrid mode. It is not a probability. Email metadata
also carries `keywordRank`, `semanticRank` (zero means absent), `exactMatch` and
`partialContent`. Evidence `scoreKind` distinguishes `cosine` from `keyword`;
only cosine evidence has a meaningful `score`, while keyword evidence uses zero.
Reader/source revisions, chunk/email IDs and original sender/subject/date bind
escaped excerpts to the existing cached reader. No raw vectors leave Go.

## Shared storage, filters and availability

Migration 020 adds external-content `chunk_fts` over `email_chunks.text` and
`context_text`. It stores postings rather than another passage/vector store.
Insert/update/delete triggers maintain FTS in the same transaction as shared
chunk publication. Upgrade rebuilds postings for existing chunks without rewriting
migrations 001–019. Subject chunks and context participate in FTS. There was no
active keyword handler/index before this slice, only an FTS5 capability probe.

Keyword visibility joins current account-owned mail/chunks/index state and checks
source revisions, preprocessing/index versions and nonstale state. It requires
neither embedding rows nor a registered/loaded model. Partial available text can
qualify; `partialContent` discloses incomplete bodies/projections. Changed raw mail
immediately fails the source/state gate; bounded manual sync cache preparation
replaces the shared passages and postings without inference. Confirmed remote
deletion and local purge delete chunks and their FTS postings transactionally,
including cascades that preserve necessary historical journal evidence.

All modes apply the same literal sender substring, inclusive after / exclusive
before UTC timestamps and assigned local category filters **before ranking**.
Account scope is mandatory. Suggestions do not count as assigned categories.
Coverage counts describe the whole authorized cached account, independent of
query filters: `keywordMessages` counts current prepared messages with passages;
`searchableMessages` retains its complete compatible semantic coverage meaning;
`eligibleMessages` counts live eligible cached messages. Neither top-K nor partial
coverage represents exhaustive mailbox history or attachment search.

Only explicit semantic/hybrid search can run local query inference. Keyword-only
never calls the provider. Hybrid returns keyword-only results with
`warning: semantic_unavailable` when the provider is missing/closed/fails or rejects
the query's token budget. Semantic-only reports its existing safe error. Storage,
source-freshness and cancellation failures fail closed rather than being hidden
as fallback. No indexing starts automatically. Status/coverage polling never infers.

Both branches are cancellable streams retaining bounded email candidates. Original
source/generation/filter membership is checked after ranking and on completed-result
polls. Fusion retains branch source fences for selected emails even when overlap
suppression hides a contributing passage. Results remain memory-only, expire in
five minutes and clear on session changes. Existing 30-second query deadlines,
48 KiB escaped-response bounds and authorization/lifecycle rules remain in force.

## IPC and UI

The existing protocol-1 `search.local` / `vector_search` request adds optional
start-only `mode: keyword | semantic | hybrid`. Omission preserves semantic behavior
for existing callers. Status/cancel omit mode. Responses add actual `mode`,
`requestedMode`, `warning`, `keywordMessages` and the rank/evidence metadata above.
Empty mode values apply to idle/error views without results. Packaged Go/Rust/TS
components must ship together because strict responses now require these fields.
See [semantic fixture](../contracts/vector-search-v1.json),
[hybrid fallback fixture](../contracts/hybrid-search-v1.json) and [IPC contract](../contracts/README.md).
Retired `search.semantic` and `ask.manage` remain rejected.

Email search defaults to hybrid with explicit keyword-only and semantic-only
options. It displays actual mode/fallback, separate keyword/semantic coverage,
partial-index notices, per-email ranks/exact matches/partial content and typed
excerpt scores. Existing filters, cancellation, escaped rendering, source navigation,
keyboard focus restoration and reader revision validation remain intact.

## Fixed held-out evaluation

[hybrid-heldout-v1.json](../backend/internal/retrieval/testdata/hybrid-heldout-v1.json)
contains 73 synthetic English messages: 72 in the searched account, one private
duplicate in another account; 24 fixed queries, 22 answerable and two unrelated.
Eight queries cover exact addresses, company names and codes; the others cover
paraphrases, mixed terms, conflicting dates and quoted evidence. Related templates
remain together. The earlier `evaluation.json` is development evidence and is not
relabelled held-out. No user mail or private judgments are used. This small synthetic
held-out corpus is not a production-mailbox quality benchmark.

```sh
npm run test:search:hybrid
cd backend
go test ./internal/store/sqlite -run '^$' -bench '^BenchmarkSearchModes50K$' -benchtime=10x -count=1
```

The native script uses the existing offline assets, strict CGO pointer checks,
temporary packaged resources/SQLite and a macOS sandbox denying all network. It
indexes through the existing coordinator and uses a fresh query provider. Every
mode runs the same queries and five warm repeats each (120 samples per mode).
Recall@10 is macro email recall over the 22 answerable queries; nDCG@10 uses binary
email judgments and ideal ordering; MRR@10 uses the first relevant email. Unrelated
queries are excluded from those denominators and reported separately. Timings
include ranking, query embedding when applicable and source rechecks, excluding
IPC, coverage polling and UI rendering. The ignored report is
`.cache/hybrid-search-qualification.json`, including all per-query rankings.

Measured on Apple M2 arm64, macOS 13.4, pinned MiniLM FP32 CPU provider, with
strict CGO checks and networking denied. Final measurement runs were sequential,
without concurrent builds/tests from this task. The account has **145 shared chunks**.

| Mode     | Recall@10 | nDCG@10 | MRR@10 | Exact top-1 misses / 8 | Warm p50 / p95     |
| -------- | --------- | ------- | ------ | ---------------------- | ------------------ |
| Keyword  | 0.9091    | 0.8755  | 0.8636 | 0 / 8                  | 2.145 / 2.769 ms   |
| Semantic | 0.9091    | 0.8755  | 0.8636 | 4 / 8                  | 6.397 / 12.056 ms  |
| Hybrid   | 0.9545    | 0.9267  | 0.9182 | 0 / 8                  | 24.054 / 30.496 ms |

First query timings: keyword 1.012 ms; semantic 204.257 ms including fresh model
initialization; hybrid 21.323 ms with the provider already warm. These are not
cold-disk timings: fixture setup/indexing warms SQLite/filesystem caches.

Hybrid improves macro Recall@10 by **4.55 percentage points** over either baseline
and nDCG@10 by 0.0511 on this fixture. It is not uniformly better: the university-fee
query misses tuition in the fused top ten even though semantic-only finds it.
Keyword-only misses two paraphrases; semantic-only misses the exact hotel code
and sender address in its top ten. Neither the candidate union nor RRF guarantees
that every relevant candidate survives top-K.

For the two unrelated queries, keyword returns zero results for one; semantic and
hybrid both return candidates for both. There is no calibrated answerability
threshold. These are ranked source candidates, not factual answers or exhaustive
search results. All evaluated modes return zero cross-account messages.

## 10,000-message / 50,000-chunk latency

`BenchmarkSearchModes50K` uses the same fixed synthetic scale fixture for all three
modes: 10,000 emails, five passages each, 50,000 finite 384-dimensional SQLite
vectors. Query vectors are fixed, so these measurements **exclude native query
inference, IPC, coverage polling and rendering**. All passages match a broad keyword
term, deliberately exercising dense candidate scans and stable ties. Repeated
fixture text/vectors make this a storage/ranking cost test, not relevance evidence.
Each mode has ten timed runs; setup warms the database and filesystem caches.

| Mode     | Retrieval p50 | Retrieval p95 | Mean        | Total allocations / scan |
| -------- | ------------- | ------------- | ----------- | ------------------------ |
| Keyword  | 513.4 ms      | 783.0 ms      | 525.66 ms   | 181,904,967 bytes        |
| Semantic | 776.3 ms      | 925.3 ms      | 730.99 ms   | 332,235,230 bytes        |
| Hybrid   | 1,141 ms      | 1,502 ms      | 1,186.48 ms | 516,843,577 bytes        |

SQLite page total including FTS and vectors: **134,332,416 bytes (128.11 MiB)**.
Allocations are cumulative bytes per scan, not retained memory or peak RSS.
The approximately 500 ms proposed p95 target remains unmet at this scale. No
large-corpus native end-to-end, cold-storage, production mailbox relevance or
peak-RSS guarantee is claimed. Profile query/row decoding, candidate aggregation
and source rechecks before considering a different index; no ANN/hosted service
was introduced. The current `.app` build is about 144.74 MiB; no new public installer
or release signing/notarization qualification is claimed.

## Verification record

Passed on this implementation host:

- Full Go tests, vet and race suite; affected app/IPC/retrieval checks repeated after
  the final coverage-preservation change. Synthetic SQLite checks cover migration
  019-to-020 backfill, account/filter isolation, unavailable-provider fallback,
  no inference in keyword/status, literal boundaries/FTS grammar escaping, source
  and category invalidation, partial content, cancellation, deletion and purge.
- Frontend strict typecheck, lint, 92 tests and production build; mode selection,
  fallback/partial coverage and original source navigation are covered.
- Rust fmt, clippy with warnings denied and 20 tests, including both shared strict
  search fixtures and rejection of unsupported modes/extra capabilities.
- Fresh desktop `.app` build and native WebView-to-Rust-to-Go authorization/lifecycle
  smoke; final sequential network-denied native held-out evaluation and scale run.
- Changed code/fixture formatting, `git diff --check` and changed-document link paths.

Initial sandbox runs could not use the default Go cache, local test listeners or
nested `sandbox-exec`; reruns used a temporary cache and test permissions. The
interrupted restricted full-suite run was superseded by passing full tests/vet/race.
Rust was accessed through the existing per-user toolchain because cargo was absent
from the initial shell PATH. No dependency installation, live mailbox mutation,
external generative AI call or secret/database/model artifact was added to source.
Signed/offline embedding package requalification was not rerun: inference,
resource hashes and packaging code are unchanged; the native evaluation itself
uses the pinned packaged resources with networking denied.
