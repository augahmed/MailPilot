# MailPilot performance evaluation

Evaluation date: 2026-10-21. This is a synthetic, single-host engineering evaluation,
not a production-mailbox or release qualification. See [EVALUATION.md](EVALUATION.md)
for quality, security, safety evidence and test outcomes.

## Reproduction

Baseline source: `3c839493e440e71c6d654b2fe003837f82aa18b2`, plus the evaluation
harness in this change. Host: Apple M2, Mac14,2, eight logical CPUs, 8 GiB RAM,
Apple Silicon macOS 13.4 (22F66). Toolchains: Go 1.27.2, Node 24.13.1,
Rust 1.99.0. Dependencies and model/runtime fingerprints remain pinned and unchanged.

Use already prepared resources and cached dependencies. The evaluator fails if
offline assets are absent; it does not download a replacement model.

```sh
node scripts/prepare-embedding.mjs --offline
node scripts/evaluate-performance.mjs --corpus-only
node scripts/evaluate-performance.mjs --safety-only
node scripts/evaluate-performance.mjs
node scripts/evaluate-performance.mjs --search-only
node scripts/evaluate-performance.mjs --throughput-only
```

The full run can take tens of minutes. It compiles a test binary into a temporary
fixed app-resource layout, enables CGO pointer checks, and executes under macOS
`sandbox-exec` with `(deny network*)`. It creates its own synthetic SQLite database
and no authentication, Gmail or generative-provider instance. It uses the production
`LocalIndex`, shared tokenizer/chunker, native provider, chunk/vector/FTS repositories,
category prototypes and semantic assessment coordinator. No fake vectors are used
in this end-to-end experiment. The temporary database is removed on completion;
a closed synthetic checkpoint is retained under `.cache/evaluation/cached-corpus/`
so `--search-only` can repeat real native queries against the final source without
regenerating document vectors. Corpus, checkpoint, metric JSON and logs are ignored
artifacts under `.cache/evaluation/`. Run full evaluations sequentially.

For distributions, p50 and p95 use the nearest-rank order statistic; p95 with ten
samples is the maximum. Corpus statistics also report arithmetic means. Timings
use a monotonic clock. Filesystem caches are warm from fixture/build/resource
verification; no reboot or OS cache eviction is claimed. Indexing overlaps
with regression checks, local builds, profiling and advisory-tool preparation;
these observations describe that host load, not isolated hardware capacity.
Allocations per operation are cumulative allocation volume,
not retained memory or peak RSS.

## Synthetic corpus

Recipe `mailpilot-synthetic-10k-v1-seed21` is frozen in
[`performance_native_test.go`](../backend/internal/app/performance_native_test.go).
It uses deterministic arithmetic, fixed UTC timestamps and reserved `example.test`
addresses. The test freezes the SHA-256 of the generated JSONL bytes:

```text
9fb8754eb91bdb6640d81f902914d2947522b127f54828ef7c52a3a7d0aa9e4c
```

| Property                               |                                                          Value |
| -------------------------------------- | -------------------------------------------------------------: |
| Cached messages in evaluated account   |                                                         10,000 |
| Threads                                |                                                          3,686 |
| Thread-length recipe                   | 1, 1, 2, 2, 3, 4, 6 messages, final thread truncated at 10,000 |
| Body words, mean / p50 / p95 / maximum |                                     263.63 / 169 / 776 / 1,856 |
| Body bytes, mean / p50 / p95 / maximum |                              1,661.86 / 1,051 / 5,089 / 11,782 |
| Jobs / School / Finance                |                                              999 / 1,003 / 998 |
| Receipts / Newsletters / Personal      |                                          2,004 / 1,998 / 1,996 |
| Explicitly unclassified truth          |                                                          1,002 |

Forty-five percent use one topical paragraph, 35% use three to six, 15% use eight
to fifteen, and 5% use 28 to 39. Replies retain attributed earlier text; every
eleventh message includes forwarded evidence; every message has a signature;
20% include an HTML alternative. A second account contains one duplicate canary,
excluded from all evaluated-account counts and indexing. Messages include thread
references and varied dates/senders. This approximates message length and thread
structure; its seven recurring topic templates are easier and less diverse than
real email. Category and identifier scores on this corpus must be read alongside
the independent held-out fixtures in [EVALUATION.md](EVALUATION.md).

The initial long run serialized dates with local offsets (JSONL SHA-256
`cbdb88fb61f8a825f764834ace58e3820834ca12161c818923f4a31c424839ea`).
The final generator explicitly uses UTC and reproduces identical bytes across
timezones. Every message's date instant and other field is unchanged;
`repository.EmailContentHash` already canonicalizes UTC, so stored source hashes,
model inputs and measured chunks/vectors are identical. Both raw corpus artifacts
are retained locally to make that normalization auditable.

## End-to-end measurements

The full-corpus results are in `.cache/evaluation/performance.json`, with exact
descriptor and coverage. All 10,000 messages, 39,517 vectors, six category
prototypes and 10,000 assessments completed; there were no partial, failed,
pending or unavailable messages. The audit rebuilt all source projections and
validated every saved vector in 8.859 seconds.

| Measurement                                                  |                       Observed |
| ------------------------------------------------------------ | -----------------------------: |
| Chunks / average chunks per message                          |                39,517 / 3.9517 |
| Original run: first observed complete source/vector coverage |                    27 min 21 s |
| Effective persisted-vector throughput through that phase     |           about 24.1 vectors/s |
| Optimized remaining 9,983 assessments                        |    140.826 s, 70.89 messages/s |
| Document/category vectors regenerated during resume          |                              0 |
| Checkpointed SQLite database, excluding WAL/SHM              | 232,808,448 bytes (222.02 MiB) |
| Complete-corpus audit/query process peak RSS                 | 419,807,232 bytes (400.36 MiB) |
| Sampled Go heap after that work, no forced GC                |   49,203,752 bytes (46.92 MiB) |
| Unchanged index replay, zero new vectors                     |                   2,191.555 ms |
| Populated database/provider/coordinator reopen, no inference |                       6.802 ms |
| Explicit restart Resume, zero new vectors                    |                   1,397.054 ms |

The original coordinator exposed the assessment-rescan bottleneck below. That run
was deliberately stopped after 1,229 assessments, preserving a consistent backup
with all real document/category vectors and 17 assessments. The optimized run
explicitly resumed that backup, completed the remaining assessments, and performed
all-source audits, queries, unchanged replay and restart fencing. Thus these are
measured phases across an intentional interruption, **not one fresh uninterrupted
final-source indexing run**. The source/vector phase plus optimized remaining
assessment phase is approximately 29 min 42 s; that sum excludes investigation,
backup/import overhead and the superseded assessment work. Five-second polling
also bounds the precision of completion observations. No fake vectors or alternate
store were substituted.

To repeat the checkpoint path after a complete run:

```sh
node scripts/evaluate-performance.mjs --resume-corpus
```

The fresh-run command now includes the batching optimization and reports its own
complete-job timing. Native provider-only throughput is measured separately by
`--throughput-only`: the first 512 canonical chunks, in their original per-message
batches of at most four, real inference with lazy initialization/tokenization
included in cumulative provider-call time, no SQLite publication. This is a
bounded representative sample, not another full-corpus embedding run.

That sample generated **512 vectors in 162 batches**, with **14.888 seconds** of
provider-call time including initialization: **34.39 chunks/second**. Its separate
process peak RSS was 468,746,240 bytes (447.03 MiB), including its synthetic fixture
and native allocations. These provider-call timings exclude SQLite reads/writes;
the 24.1 vectors/s figure above includes source preparation, publication, polling
and concurrent host load. The different denominators are intentional.

All 512 freshly generated vectors also matched the persisted source-bound vectors
numerically: maximum absolute component difference **0** (test tolerance 2e-5).
That numeric sample supplements the provenance/compatibility audit of every one
of the 39,517 vectors; it does not claim that all were re-embedded a second time.

## Native queries over the generated corpus

Twelve fixed thread-reference queries have three repetitions per mode. Timing
includes real query embedding, SQL, ranking and source revalidation; it excludes
IPC, rendering and coverage polling. One initial semantic sample includes lazy
model initialization. All branches use the same current 39,517 shared chunks.

| Mode     |        p50 |        p95 |    Maximum | Reference Recall@10 / nDCG@10 / MRR@10 |
| -------- | ---------: | ---------: | ---------: | -------------------------------------: |
| Keyword  | 163.610 ms | 178.440 ms | 201.537 ms |                              1 / 1 / 1 |
| Semantic | 482.529 ms | 515.001 ms | 970.770 ms |                              0 / 0 / 0 |
| Hybrid   | 739.508 ms | 852.804 ms | 865.028 ms |                              1 / 1 / 1 |

The repeated topic templates make adjacent numeric references particularly hard
for semantic-only search. Its zero recall here is an observed identifier failure,
not a paraphrase-recall estimate. Exact keyword/hybrid recovery is why both
retrieval paths are retained. Semantic and hybrid p95 exceed the proposed 500 ms
target even on this corpus; dense 50,000-chunk results are separate below.

## Dependent assessment rescans

At 10,000 indexed messages, sampling found the coordinator repeatedly inside
`IndexRepository.Next`/SQLite, and progress measured roughly 4.4–4.6 assessments/s.
It fetched 32 pending assessment IDs but used only the first before restarting a
full mailbox/prototype/candidate scan. The bounded existing page is now consumed
serially before that scan repeats. Every individual snapshot and save retains
source/category/model/job-generation checks, and cancellation is checked between
messages. No queue, parallel worker, schema or classifier-policy change was added.

The resumed full-corpus phase averaged 70.89 assessments/s, approximately 16 times
the observed baseline prefix rate, with no document or category inference. The
comparison uses a measured baseline prefix and a complete remaining cohort, not
two complete fresh jobs; the baseline was not extrapolated into a fabricated total.
The existing pause/resume regression now crosses a 35-message candidate-page
boundary and verifies that cancelled assessments do not publish and vectors are
reused after explicit Resume.

## Cancellation and restart

`--safety-only` interrupts the second real native batch of a long synthetic message,
then joins the worker, closes/reopens SQLite, simulates a durable running job and
explicitly resumes. Observed under concurrent evaluation load:

| Measurement                                    |    Observed |
| ---------------------------------------------- | ----------: |
| Cancel control response                        |    1.406 ms |
| Cancel through worker join                     |    3.301 ms |
| Committed vectors retained / late publications |       4 / 0 |
| Startup inference inputs                       |           0 |
| Restart state                                  | interrupted |
| Resume through completion                      | 1519.107 ms |
| Newly generated / final vectors                |      7 / 11 |

The full-corpus evaluator separately checks unchanged replay, durable restart,
generation rotation, rejected stale-worker publication and zero re-embedding on
resume. Existing race-tested fixtures cover cancellation before publication, source
changes during inference, pause/resume within a message, purge and model changes.
This is not power-loss or filesystem-corruption fault injection. ORT session
construction remains non-preemptible; these warm-run cancellation measurements
do not establish a hard shutdown bound during initialization.

## Search profiling and bounded optimization

```sh
cd backend
go test ./internal/store/sqlite -run '^$' -bench '^BenchmarkSearchModes50K$' \
  -benchtime=20x -benchmem
go test ./internal/store/sqlite -run '^$' -bench '^BenchmarkVectorSearch50K$' \
  -benchtime=10x -cpuprofile ../.cache/evaluation/search-cpu.pprof \
  -memprofile ../.cache/evaluation/search-memory.pprof
```

This existing benchmark uses 10,000 messages and exactly 50,000 stored 384-float32
vectors with a fixed query vector. It isolates SQL, ranking and source revalidation;
it measures neither real embedding nor semantic quality. Its SQLite page total was
134,332,416 bytes. It scans 50,000 candidates per keyword/semantic query and 100,000
branch visits for hybrid. Broad lexical matches deliberately stress the FTS branch.

Allocation profiling attributed 39.1% of allocation volume to per-chunk ranking
and 22.8% to `database/sql` cloning vector blobs. Changes are limited to those
measured costs: decode row-scoped `sql.RawBytes` directly into the existing vector
buffer; sort with typed comparators; compact the at-most-four-hit passage buffer
in place. Ranking, overlap rules, source/hash checks, finite-vector validation,
account joins and revalidation remain covered by existing tests. No model,
preprocessing, schema, cache size, concurrency or retrieval-policy change was made.

The CPU profile still attributes most sampled time to SQLite page access/syscalls.
It includes fixture construction and runs under competing load, so it does not
justify a new index or a wider SQLite cache. Dense exact/hybrid scans must still be
checked against the proposed 500 ms p95 target. Before/after allocation and final
latency measurements follow. The final comparison runs precompiled baseline and
optimized binaries sequentially with indexing, builds and other tests stopped,
20 scans per mode, networking denied. These are single-host observations with
warm filesystem caches, not statistically established latency guarantees.

| Mode     | Baseline p50 / p95 | Optimized p50 / p95 | Baseline / optimized allocated bytes per query |
| -------- | -----------------: | ------------------: | ---------------------------------------------: |
| Keyword  |   436.9 / 450.8 ms |    424.1 / 460.1 ms |                       188,142,633 / 89,341,264 |
| Semantic |   539.1 / 542.7 ms |    514.4 / 644.2 ms |                      332,232,240 / 156,631,834 |
| Hybrid   |   1,084 / 1,271 ms |    1,102 / 1,150 ms |                      523,080,626 / 248,679,678 |

The benefit is allocation volume, approximately 52–53%, with little change in
median latency. Semantic p95 increased in this small sample and remains an open
tail-latency concern; hybrid p95 improved but still misses the 500 ms target.
No general latency improvement is claimed. SQL page access remains the dominant
measured CPU cost, so these changes do not eliminate the exact-scan bottleneck.

The first 20-scan allocation comparison reduced keyword allocation volume from
188.14 MB to 89.34 MB, semantic from 332.23 MB to 156.63 MB, and hybrid from
523.08 MB to 248.68 MB per query (approximately 52–53%). These are decimal MB.
The native retrieval fixtures retain the same quality metrics, and all 72
held-out query/mode rankings match the pre-change results exactly. Timing from
these first runs is confounded by competing race tests/builds; the separate
sequential binary comparison is used for latency assessment.

## Startup, memory and packaging

```sh
CARGO_NET_OFFLINE=true GOPROXY=off npm run build -- --bundles app --no-sign
codesign --force --deep --sign - src-tauri/target/release/bundle/macos/MailPilot.app
npm run test:embedding:package
node scripts/evaluate-startup.mjs
```

The startup evaluator launches ten fresh packaged processes with fresh temporary
databases and OAuth disabled. It measures launch to the frontend's successful
WebView → Tauri → Go health event and launch through clean shutdown. This includes
first-database migrations and frontend startup, not signed-in Gmail restoration or
model initialization. The full-corpus database/coordinator reopen is measured
separately without inference. A signed-in cold offline startup remains outside
qualification because existing account restoration verifies Gmail online.

The rebuilt ad-hoc signed app is 144.94 MiB (Tauri bundle measurement). Ten
fresh-process observations against the final source give:

| Packaged lifecycle measurement |       p50 | p95 / maximum |
| ------------------------------ | --------: | ------------: |
| Launch to frontend health      | 298.65 ms |   1,617.62 ms |
| Launch through clean shutdown  | 330.75 ms |   1,672.50 ms |

Peak RSS uses macOS `getrusage(RUSAGE_SELF)` and includes native memory and the
evaluation harness's retained synthetic corpus, query and audit work. Go heap
allocation is sampled separately and excludes native allocations. The packaged
self-test reports baseline/loaded RSS for fixed inputs; neither is a hard memory
limit. Local ad-hoc signing and offline inference do not establish Developer ID,
notarization, Gatekeeper distribution or other-platform inference support.

These RSS measurements cover the Go pipeline/evaluation processes. A combined
desktop/renderer/WebKit process-tree peak was not measured.
