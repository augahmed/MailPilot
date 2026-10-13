# Local semantic mailbox search

**Search your mailbox** retrieves related downloaded email using local embeddings and cosine similarity. It uses the same optional all-MiniLM-L6-v2 provider as semantic classification. No query or mailbox content is sent to an external inference service. Search has no Gmail adapter, remote endpoint, LLM, or mailbox write capability. Gmail keyword syntax is not interpreted, and provider keyword search remains a separate, unimplemented feature.

## Use

Configure the existing local model using [the setup guide](SEMANTIC_CLASSIFICATION.md), connect the account and sync its Inbox. In **Search your mailbox**, choose **Index cached mail**, then enter a description of what you want and choose **Search locally**. Search uses a separate versioned subject/passage index. After upgrading, run **Index cached mail** even if classification vectors already exist; indexing computes only missing normalized passage vectors for complete cached messages. Saved vectors survive restart. Sync/import never starts embedding inference automatically.

Each result shows sender, subject, received date, cosine similarity and the best matching subject or body passage copied from cleaned cached text. Results are sorted by descending similarity, then descending receive date and ascending local email ID for ties. Request top-K from 1 to 20 (default UI 10). Search returns up to K matches from the indexed candidate set, including negative/distant similarities when fewer strong matches exist; scores are not probabilities or guarantees of relevance.

Optional filters:

| Filter     | Behavior                                                                                                                                                                                                                                                                                                                    |
| ---------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Date range | Core `from` is inclusive and `until` is exclusive, in UTC epoch milliseconds. UI date inputs include the whole selected “Through” date in UTC. Result timestamps display in the user's local timezone.                                                                                                                      |
| Sender     | Literal substring of the cached sender name/address; ASCII case-insensitive. `%` and `_` are literal characters, not SQL wildcards or Gmail syntax.                                                                                                                                                                         |
| Category   | Same-account local category assignments **or** the currently saved chosen classification. Predictions must have the active engine/version and current email/category fingerprint. Stale predictions do not match. Disabled categories can still filter explicit local assignments; stale/disabled predictions do not match. |

Category filter options use the existing paginated category API; **Load more category filters** exposes additional definitions. Search does not classify emails, assign a category or change Gmail labels.

Counts distinguish all cached messages, messages with complete cached bodies, and messages indexed for the current model. Unknown/unavailable bodies are excluded explicitly. This cache is a downloaded historical subset, not a complete or live Gmail mailbox. Sync new mail and index it manually to expand coverage. Saved results reflect the cache at the last search; rerun after sync or category changes.

## Index and retrieval

`internal/search` owns exact ranking over subject and body representations. Migration [013_retrieval_passages.sql](../backend/internal/store/sqlite/migrations/013_retrieval_passages.sql) adds `email_passage_vectors` and complete-document manifests in `email_retrieval_indexes`. The old message vector cache remains available to classification and the reproducible baseline; production search uses preprocessing version `email-passages-v1`. All cache keys include account, email, model identity, dimensions, preprocessing version and normalized SHA-256 input hash.

Cleanup is deterministic and local: normalize whitespace/control characters; remove quote-prefixed lines and conventional reply headers; stop at reliable RFC signature delimiters, trailing mobile signatures and Outlook header blocks. Keep ordinary closings and unquoted inline replies. HTML-only mail removes conventional quote/signature containers using the existing parser. If cached plain text exactly matches extraction of the HTML alternative, use that structural HTML cleanup too. No HTML is rendered, and no links or attachments are fetched. These remain heuristics; unusual/multilingual quoting and forwarded context can be misinterpreted.

Subjects (with repeated Re/Fw prefixes removed) and bodies have separate representations. Each passage is at most **240 UTF-8 bytes**, a conservative bound below MiniLM's 256-wordpiece input limit including special tokens. Splitting prefers nearby sentence/word boundaries with a small, at-most-32-byte overlap. Every portion of a valid long body is represented; there is no 16-KiB retrieval cutoff. The existing 1-MiB source limits and a maximum of 8,192 passages bound work; invalid/oversized inputs report indexing errors rather than silently marking a partial index complete. No thread-wide merged embedding is added.

An explicit indexing job computes only missing passage hashes in batches of 32 using the existing local inference gate. Repeated text within an email shares a vector. Source edits discard the complete-document manifest and obsolete vectors atomically, preserving unchanged passage vectors for reindexing. Quote/signature-only changes with unchanged normalized text preserve the manifest as well. Vectors survive restart. Account isolation, source-hash rechecks, complete writes and remote-deletion cleanup apply to both tables. No plaintext passage copy is stored in the index; passages are reconstructed deterministically from the cache.

The exact scan reads at most 32 email candidates per page and keeps the best K source emails. Score is the maximum cosine across that email's subject/body passages; the corresponding passage becomes its excerpt. No passage inference is performed at query time. Identical winning passages within the same provider thread collapse to one result, with deterministic score/date/ID selection. Distinct matching passages can still return different emails in the same thread. Cross-thread messages are not merged by subject alone. This conservative deduplication does not claim to detect every paraphrased duplicate.

Date/sender/category filters remain local and account-scoped. Corrupt or missing passage vectors invalidate the complete manifest, skip that source, and report a partial result until explicit reindexing repairs it. Returned rows optionally include `passageKind` and zero-based document `passageIndex`; excerpts are at most 240 Unicode characters (plus a possible display ellipsis). The UI identifies whether the match is a subject or body passage, and opens the same source email through the cached reader.

Before excerpts are returned, sources, current vector hashes and filter membership are reread. Changed/deleted results are omitted and reported as partial instead of returning stale scored text. Concurrent changes after the final read remain possible; this is snapshot retrieval, not a globally locked mailbox view. No SQL transaction spans model execution.

Search returns persisted winning passages rather than selecting snippets after message ranking. The text is escaped and remains untrusted; it contains no generated explanation, rendered HTML or fetched content. The old single-vector/excerpt path remains only for reproducible before/after evaluation and existing classification-cache regressions.

Short conservative passages can lose surrounding context, and maximum cosine may favor a broad subject or generic advice over a specific answer. Quote/signature trimming is heuristic. Relevance calibration, hybrid FTS ranking and broader natural-mailbox evaluation remain future work.

## Jobs, bounds and privacy

`semantic_search` is a main-window allowlisted native command mapping only to `search.semantic` protocol v1. Its operations are `status`, `index`, `query`, `cancel`; requests and responses are validated in Go, Rust and strict TypeScript against [the synthetic fixture](../contracts/search-semantic-v1.json). JavaScript cannot supply a provider/model path, endpoint, SQL, executable or arbitrary backend method.

Index/query requests start a single local job and return immediately. Status polling exposes progress without holding the private bridge through inference. An index run considers at most 10,000 missing messages under a ten-minute deadline; later explicit runs skip saved vectors and can continue. Query jobs have a one-minute deadline and examine at most 10,000 filtered indexed rows. Capped scans expose partial results, so top-K is scoped to the examined candidates, not a claim of global best matches beyond the cap. Refine filters to reduce the candidate set. Each model call has the existing 30-second limit and 32-text batch bound. Partial indexing persists successful batches, reports failed inputs and requires an explicit retry for failures. No automatic network retry, upload or scheduled indexing occurs.

Cancellation targets the returned job ID. Old job IDs cannot cancel newer jobs. Disconnect/session replacement cancels work, clears in-memory results and suppresses late responses; shutdown cancels and joins workers before closing SQLite. Authorized cached sessions work offline under the existing account lifecycle; disconnected/expired sessions must reconnect to access retained data. Unconfigured embeddings show a readable message and leave ordinary sync/category/risk features available.

Queries are capped at 1,000 UTF-8 bytes, sender filters at 256 bytes and top-K at 20. Responses are capped at 48 KiB before private framing, with explicit display-size errors. React renders text, supports form submission/keyboard controls, exposes live progress and a **Cancel local job** button, and keeps sensitive data out of browser storage. Query strings, vectors and mail are not logged. Stored vectors, cache/WAL and backups remain sensitive local data; SQLite is not encrypted and purging is not forensic erasure.

## Retrieval quality and validation

Fake-provider tests verify relative ranking, top-K, category/date/sender filters, account isolation, unchanged-vector reuse, restart, invalidation, purge, malformed-vector handling, cancellation and source freshness. UI/contract tests verify filtered requests, scores/excerpts, escaped hostile text, unavailable models, progress/cancellation and disconnect races. Fake embeddings validate mechanics, not real semantic quality.

The real opt-in MiniLM retrieval test uses four synthetic cached messages (interview, receipt, school and personal) and four queries, including paraphrases. The relevant message ranked first and above unrelated messages in **4/4** cases:

| Query                                      | First result | Cosine |
| ------------------------------------------ | ------------ | ------ |
| Scheduling a technical job interview       | Interview    | 0.595  |
| Proof that I paid for a purchase           | Receipt      | 0.453  |
| When to submit my university homework      | School       | 0.498  |
| Getting together with relatives for a meal | Personal     | 0.465  |

This tiny authored corpus is a development regression test, not a representative mailbox benchmark. It does not establish long-message, multilingual or adversarial retrieval quality. The test uses disposable SQLite and an already installed verified model; no Gmail account, external inference or mail upload is involved.

After model setup, reproduce from `backend/`:

```sh
MAILPILOT_TEST_EMBEDDING_PYTHON="$PWD/../.local/minilm-py312/bin/python" \
MAILPILOT_TEST_EMBEDDING_MODEL_DIR="$PWD/../.local/models/all-MiniLM-L6-v2" \
  go test -count=1 -v ./internal/store/sqlite -run TestOptInMiniLMRetrievalQuality
```

The new native smoke check exercises the real WebView → allowlisted search command → Go authorization response in a disposable, unconfigured Gmail profile. It verifies lifecycle/IPC, not ranking on a real mailbox. Packaging of the optional Python/ONNX runtime and qualification beyond the tested macOS host remain the existing release limitations.

## Expanded retrieval evaluation and scan profile

The synthetic [retrieval corpus](../backend/internal/search/testdata/retrieval.json)
contains **40 messages and 22 queries**, including confusing near-neighbors, eight
long administrative messages with actionable content beyond both the old token
limit and 16-KiB cutoff, inline replies, signatures, subject-only clues and repeated
thread copies. It was authored before measuring either ranking path; no thresholds
or query-specific rules were fitted to its results. Both paths use the same pinned
local MiniLM model and corpus. This is a development evaluation, not a held-out
production mailbox estimate.

| Metric                                          | Previous single-vector search | Subject/passage search |
| ----------------------------------------------- | ----------------------------: | ---------------------: |
| Top-1 accuracy                                  |                 13/22 (59.1%) |          18/22 (81.8%) |
| Top-3 recall                                    |                 14/22 (63.6%) |           22/22 (100%) |
| Mean query wall time                            |                        485 ms |                 230 ms |
| Mean local loading/ranking, excluding inference |                        7.4 ms |                13.1 ms |

Top-1 means the first result is an annotated relevant source. Top-3 recall here is
query-level hit rate: at least one acceptable source among three results. Exact
copies in the lift thread are equivalent relevant sources, not separate recall
targets. No no-answer queries or automatic abstention calibration are included.
Times are one local run on Apple M2; they include real MiniLM worker startup and
are not throughput guarantees. Indexing is more expensive because more content is
represented; cached queries no longer need repeated excerpt embeddings.

Representative remaining top-1 failures (all correct sources are second):

- A camera-refund timing question ranks generic return policy above the actual refund confirmation.
- A dental appointment timing question ranks a dental newsletter above the rescheduled appointment.
- A timeline-review query ranks a short reply's matching subject above the original substantive request.
- A financial-aid document question ranks scholarship advice above specific bursary requirements deep in the email.

These expose the limits of maximum passage cosine, short context and subject-only
matches. The implementation does not contain fixture-specific boosts to hide them.

`BenchmarkPassageExactScan1000` measures SQLite hydration, reconstruction and exact
cosine ranking over 1,000 synthetic messages with 384-dimensional vectors, excluding
model inference. Three iterations averaged **162.6 ms/query**, **46.7 MB allocated/query**
(total transient allocations, not retained memory), and 313,729 allocations/query
on Apple M2. This does not justify an ANN or network vector database at the current
scale. Exact scanning and the existing 10,000-email partial-result cap remain.

Reproduce the expanded evaluation and profile from `backend/`:

```sh
MAILPILOT_TEST_EMBEDDING_PYTHON="$PWD/../.local/minilm-py312/bin/python" \
MAILPILOT_TEST_EMBEDDING_MODEL_DIR="$PWD/../.local/models/all-MiniLM-L6-v2" \
  go test -count=1 -timeout 15m -v ./internal/store/sqlite -run TestOptInPassageRetrievalEvaluation

go test -run '^$' -bench BenchmarkPassageExactScan1000 -benchtime=3x -benchmem ./internal/store/sqlite
```
