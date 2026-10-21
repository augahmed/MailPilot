# Local email indexing

Implemented 2026-10-19. One explicitly started local coordinator connects the
existing cached email/chunk repository to the bundled MiniLM CPU provider and
writes vectors to the same SQLite database. [Shared exact search](VECTOR_SEARCH.md) now consumes completed compatible snapshots through the same provider/store. Enabled category prototypes now share this coordinator/provider ([contract](CATEGORY_VECTORS.md)); separate category counts report current readiness. Semantic classification now reads these same vectors and stores assessments after prototypes ([contract/evaluation](SEMANTIC_CLASSIFICATION.md)); bounded evidence questions now use the shared retriever ([contract](EMAIL_QUESTIONS.md)). No Gmail or external AI
adapter is called by indexing.

## User controls and coverage

The **Local Email Index** card exposes Start, Pause, Resume, Cancel and Rebuild.
Status polling performs no inference. Starting or resuming authorizes one local
run, not persistent automatic enablement. It processes all live cached messages
for the authorized Gmail account, independent of Inbox labels, visible pages or
the manual-sync backfill's 10,000-row scan cap. It does not expand Gmail sync scope,
fetch missing bodies or attachments, or infer that the cache is the whole mailbox.
Changes arriving during a run are picked up before completion; later changes show
pending coverage and require another Start. Resume after app restart is explicit.

Eligible means every non-tombstoned cached message. Indexed counts messages whose
available, compatible chunks all have valid saved vectors, including partial-body
messages. Message coverage is separately reported as mutually exclusive complete,
partial, failed, pending and unavailable counts. Partial includes intermediate
vector coverage and successfully embedded incomplete bodies; a missing body never
becomes complete because a subject was embedded. Unavailable means a processed
message produced no usable chunks or retains a historical noncanonical source
hash. Those historical cached records remain readable and untouched; a normal
Gmail reimport supplies current source identity before they can be embedded. Failed denotes a current input with a recorded
processing/inference failure. Provider unavailability stops the job and leaves
unfinished messages pending rather than labeling all source mail invalid.

Chunk and embedding counts describe current compatible persisted rows, not
lifetime inference calls. Stale/historical vectors are not counted. Progress
measures messages with all available chunks embedded, not a claim that every
original body is complete. If cached content changes after completion, the card
shows changes waiting instead of claiming a complete index.

Pause/cancel preserves committed batches and retains the loaded native session for
fast Resume; native resources are released on shutdown. Both revoke publication; Resume retries
unfinished/failed inputs and reuses good vectors. Rebuild reprojects every cached
message but deliberately retains/reuses unchanged compatible vectors; it is not a
force-recompute or data-deletion command. Cache removal pauses/revokes the account
job; resuming processes only the remaining cache. There is no cache-purge UI added
by this task.

## Ownership, persistence and recovery

- `internal/app/indexing.go`: authorization, one worker, chunk preparation,
  validation, inference and control/status DTOs. Reuses `mail.Chunker`, canonical
  `mail.ModelInput` and the existing provider; no second preprocessing pipeline.
- `repository/indexing.go` / `store/sqlite/indexing.go`: durable job state,
  candidates/counts and guarded shared-store writes. No inference in SQL.
- Migration **017** adds `local_index_jobs` (one account row),
  `local_index_models` (fingerprint/dimension pinning per account/model version),
  and `index_state.attempt_generation` / `failure_code`. The model registry is
  identity metadata, not another vector cache or indexing queue. Migrations
  001–016, historical vector tables and recovery journals are unchanged.
- Candidates are selected in bounded pages of 32 from live emails and existing
  index state, not a duplicated per-feature queue. An invalid unchanged input is
  attempted once per run. Failed inputs become retryable on the next explicit run
  or source change. There is no persistent cursor that can skip earlier changed mail.
- At most one message is prepared at a time; chunk reads are paged by 100 and
  inference/publication uses at most four chunks. No DB transaction spans inference.
  Pending chunks and committed four-vector batches are the durable checkpoints.
- Every background chunk/vector transaction verifies the running account job's
  opaque generation, model/dimension/fingerprint/profile and the current source
  and chunk generation. Pause, new runs/model switches, restart recovery and cache
  removal reject late publications. Completed batches survive interruption; an
  interrupted batch is computed again. Only all-compatible-vector, complete-body
  message snapshots become `ready`. Finishing a job transaction rechecks remaining
  work and coverage before reporting complete/partial.
- Source hashes plus section/context/preprocessing and model identity govern reuse
  through the existing shared store. A changed passage invalidates its vectors;
  matching passages can keep their IDs/vectors even when their order changes.
  A model version change creates a new job generation, retains versioned rows and
  excludes incompatible vectors from coverage. A cleanup/preprocessing change requires explicit Resume to replace incompatible
  chunks/vectors; `preprocessing_changed` reports that case separately from model
  changes. See [shared readable text](CLEAN_TEXT.md). A fingerprint/dimension change
  under an already registered version fails closed; bump the recipe version.
- Startup marks abandoned running jobs interrupted and revokes their generation.
  It never automatically starts model inference. Shutdown and session changes cancel
  the worker; shutdown joins it before SQLite closes and closes the native provider.
  Ordinary mailbox/category/summary features do not require a local index.

## Narrow IPC and privacy

`index.local` / Tauri `local_index` is an additive protocol-v1 command. Requests
contain only an operation and account ID. All mutations require that ID to match
the current Go-authorized session; status can omit it to discover that session's
account. No paths, text, email selectors, models, prompts, vectors, endpoints or
execution parameters cross the boundary. Go, Rust and Zod validate the shared
[fixture](../contracts/local-index-v1.json). Commands return promptly; the UI polls
bounded metadata while one cancellable background worker runs. Retired Ask/search
and drafting commands remain rejected.

Mail/chunks/vectors remain sensitive unencrypted local data; this is not forensic
removal. No new credentials, telemetry, model download or external AI consent is
introduced. Existing app cold-start Gmail session restoration still performs online
OAuth/profile verification; this task does not replace that access policy. Once
an account session is authorized, indexing itself needs no network or AI key.
The provider's macOS 13.4+/Apple Silicon, memory, signing and non-preemptible native
initialization limitations remain documented in [LOCAL_EMBEDDING.md](LOCAL_EMBEDDING.md).

## Verification and synthetic performance

Fake-model coordinator tests cover explicit opt-in, more than one UI/work page,
account isolation, idempotency/rebuild, partial/missing/malformed sources, committed
batch pause/cancel/resume, in-flight source replacement, restart and model changes,
and unavailable/invalid outputs. Storage tests cover revoked writes, deletion/purge,
atomic mixed-validity batches, false completion, fingerprint reuse and a real
version-16 upgrade retaining vectors and uncertain historical draft journals.
A 10,001-row storage fixture confirms selection beyond the manual-sync scan cap.
Frontend tests cover opt-in, pause/resume, interruption and stale poll rejection;
Go/Rust/Zod tests share the strict wire fixture. Desktop smoke checks the unauthorized
index path through the actual WebView/host/core without a Gmail connection.

Run the fixed offline real-model pipeline qualification after preparing assets:

```sh
npm run prepare:embedding -- --offline
npm run test:indexing:native
```

The script compiles the native integration test into a temporary fixed app-resource
layout, copies only bundled assets, and runs under macOS sandbox-exec with **all
network access denied** and strict CGO pointer checks. It seeds its own synthetic
SQLite database, indexes 200 messages (40 longer messages), then repeats the run
and verifies zero regenerated embeddings. A second synthetic newsletter test checks
cleaned inputs, real inference, saved vectors and cached reading without changing
the original source. It never opens the user's database,
credentials or mailbox. Machine-readable results go to ignored
`.cache/local-index-qualification.json`; timing is not a correctness assertion.

Final qualification on Apple Silicon arm64/macOS 13.4, native CPU provider with
strict CGO checks and all networking denied:

| Measurement                          | Observed                              |
| ------------------------------------ | ------------------------------------- |
| Synthetic messages / chunks          | 200 / 560                             |
| Saved embeddings / inference batches | 560 / 240                             |
| Fresh-process index run              | 13,002.780 ms (15.38 messages/second) |
| Unchanged replay                     | 53.249 ms; **0** new embeddings       |
| Peak process RSS                     | 448,364,544 bytes (427.59 MiB)        |
| Synthetic database plus WAL          | 6,700,824 bytes (6.39 MiB)            |

The fresh-process timing includes chunk preparation, lazy model initialization,
inference, transactional persistence and progress polling. Copying/verifying assets
has warmed filesystem caches. This is one synthetic host/run, not a percentile or
10,000-message throughput/semantic-quality benchmark. Peak RSS is a high-water mark
including the Go test harness, native allocations and loading buffers, not a
steady-state allocation or hard memory limit. Database plus WAL size includes the
original synthetic mail and journal overhead. No user's database or mailbox is used.

Current status also reports `assessedMessages` and `pendingAssessments`. The job,
message, category and assessment counts are read in one SQLite transaction. The same explicit Start/Resume/Pause/Cancel lifecycle controls dependent semantic assessment work. No new inference occurs for classification.
