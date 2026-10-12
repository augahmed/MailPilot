# Local semantic mailbox search

**Search your mailbox** retrieves related downloaded email using local embeddings and cosine similarity. It uses the same optional all-MiniLM-L6-v2 provider as semantic classification. No query or mailbox content is sent to an external inference service. Search has no Gmail adapter, remote endpoint, LLM, or mailbox write capability. Gmail keyword syntax is not interpreted, and provider keyword search remains a separate, unimplemented feature.

## Use

Configure the existing local model using [the setup guide](SEMANTIC_CLASSIFICATION.md), connect the account and sync its Inbox. In **Search your mailbox**, choose **Index cached mail**, then enter a description of what you want and choose **Search locally**. Embeddings already saved by classification are immediately searchable; indexing computes only missing vectors for complete cached messages. Saved vectors survive restart. Sync/import never starts embedding inference automatically.

Each result shows sender, subject, received date, cosine similarity and a short excerpt copied from cleaned cached text. Results are sorted by descending similarity, then descending receive date and ascending local email ID for ties. Request top-K from 1 to 20 (default UI 10). Search returns up to K matches from the indexed candidate set, including negative/distant similarities when fewer strong matches exist; scores are not probabilities or guarantees of relevance.

Optional filters:

| Filter     | Behavior                                                                                                                                                                                                                                                                                                                    |
| ---------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Date range | Core `from` is inclusive and `until` is exclusive, in UTC epoch milliseconds. UI date inputs include the whole selected “Through” date in UTC. Result timestamps display in the user's local timezone.                                                                                                                      |
| Sender     | Literal substring of the cached sender name/address; ASCII case-insensitive. `%` and `_` are literal characters, not SQL wildcards or Gmail syntax.                                                                                                                                                                         |
| Category   | Same-account local category assignments **or** the currently saved chosen classification. Predictions must have the active engine/version and current email/category fingerprint. Stale predictions do not match. Disabled categories can still filter explicit local assignments; stale/disabled predictions do not match. |

Category filter options use the existing paginated category API; **Load more category filters** exposes additional definitions. Search does not classify emails, assign a category or change Gmail labels.

Counts distinguish all cached messages, messages with complete cached bodies, and messages indexed for the current model. Unknown/unavailable bodies are excluded explicitly. This cache is a downloaded historical subset, not a complete or live Gmail mailbox. Sync new mail and index it manually to expand coverage. Saved results reflect the cache at the last search; rerun after sync or category changes.

## Index and retrieval

`internal/search` owns ranking and passage selection. Its small repository boundary scopes SQLite candidates, missing vectors, counts and source rechecks by account. Migration [010_semantic_search.sql](../backend/internal/store/sqlite/migrations/010_semantic_search.sql) adds an index over the existing `email_embeddings` account/model/dimension/preprocessing/email-ID columns. Existing composite foreign keys, checksum migrations and transactional content invalidation remain unchanged. No new vector database, ANN engine or dependency was added.

The exact scan reads at most 32 candidates at a time and keeps only the best K messages. Date/sender and preliminary category predicates execute in parameterized SQLite queries before cosine ranking; current chosen-category fingerprints are rechecked in the same bounded read transaction. The engine validates vector dimensions and finite values, prepared-text hashes and account ownership. Changed email content invalidates its vector during the existing upsert transaction; label-only or excluded-signature changes can reuse the same embedding. Email deletion cascades vectors. Malformed vectors are reported as skipped errors and discarded so an explicit later indexing run can rebuild them.

Before excerpts are returned, sources, current vector hashes and filter membership are reread. Changed/deleted results are omitted and reported as partial instead of returning stale scored text. Concurrent changes after the final read remain possible; this is snapshot retrieval, not a globally locked mailbox view. No SQL transaction spans model execution.

Passage selection takes up to eight short sentences/lines per winning message from the existing cleaned embedding text, prioritizes exact query-term overlap, and compares their local embeddings with the query. The best passage is returned verbatim as escaped text, capped at 161 Unicode characters including a possible ellipsis. No generated excerpt, HTML rendering, URL fetch or attachment content is used. Passage vectors and query text/vectors remain in memory only. If passage inference fails, results retain a deterministic text-match fallback and expose `excerptFallback` rather than hiding that failure.

The underlying email vector still represents one prepared text, truncated by MiniLM to 256 wordpieces. Long-message retrieval can miss topics later in a body; this phase does not introduce a chunk index. Passage candidates come from bounded cleaned text and may omit a relevant passage. Large-mailbox ANN selection, hybrid FTS ranking and relevance calibration remain future work.

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
