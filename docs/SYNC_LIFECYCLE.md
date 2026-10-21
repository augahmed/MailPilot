# Gmail source and local index lifecycle

The source lifecycle is shared by manual Gmail sync, chunk preparation, local
indexing, keyword/vector search and semantic classification. The synthetic
integration suite is [lifecycle_test.go](../backend/internal/provider/gmail/lifecycle_test.go).
It uses the real Gmail HTTP/history adapter with a GET-only local fixture server,
a temporary SQLite database and deterministic fake embeddings. It does not
measure model quality or use a live mailbox.

| Observation                                 | Transactional source effects                                                                | Derived effects                                                                                                                                                                                                                     |
| ------------------------------------------- | ------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| New cached message                          | Save original mail, relations and the sync checkpoint together.                             | Prepare current chunks after commit; embeddings wait for explicit indexing.                                                                                                                                                         |
| Changed content or completeness             | Save the new source and rotate/invalidate the old chunk generation in the same transaction. | Old passages become ineligible immediately, including before chunk preparation succeeds. Reproject; reuse exact compatible inputs and embed only missing inputs. Invalidate dependent assessments and category examples/prototypes. |
| Label-only change                           | Update actual label differences, retaining original source/hash and text.                   | Keep chunks/vectors. Invalidate classification inputs and reviewed-write freshness independently.                                                                                                                                   |
| Repeated identical observation              | Advance the checkpoint without rewriting unchanged mail/relations.                          | Preserve current chunk/vector generations and assessments. Identical chunk/vector publication also avoids invalidation updates.                                                                                                     |
| Confirmed deletion or authoritative GET 404 | Tombstone/clear source content and active derived rows with the checkpoint.                 | Remove chunks, vectors and FTS entries; revoke the account index job before late publication. Keep immutable Gmail journals and retained source identity.                                                                           |
| Failed source/checkpoint write              | Roll back the whole observation.                                                            | Preserve prior source, checkpoint and derived state; retry the unfinished observation.                                                                                                                                              |

Gmail received-message content is immutable under its provider ID. Minimal GETs
reuse complete cached bodies when the adapter's content revision matches; drafts
include the history ID in that revision. Partial cached bodies always require a
full GET when observed. No broader Inbox import, attachment download, background
sync or new Gmail mutation path is introduced.

After history has drained, manual sync spends only its remaining observation
budget retrying incomplete cached bodies. This closes the gap where a temporary
partial response recovered without producing a new Gmail history event. The
additive `SyncCheckpoint.RetryAfter` field rotates these retries across cached
provider IDs and persists with each source observation. Old checkpoints decode
with an empty retry cursor; no migration is needed. A failed/cancelled retry keeps
its position. Successfully observed but still incomplete bodies advance the
rotation, so later identities receive attempts on later manual runs. The body-retry
phase skips messages already observed through history in that run; distinct
history records may still refresh the same message. History work has priority
and sustained history backlog can defer body retries.

A body that remains unavailable reports `body_unavailable`, partial/unavailable
index coverage and `more`. Some bodies remain unavailable indefinitely, including
ones requiring unsupported attachment fetching. Retrying does not promise eventual
complete content. Absence from a bounded listing never implies deletion.

Source/checkpoint commits precede bounded local chunk preparation. Preparation
failure or cancellation cannot discard a committed source observation. Stale work
remains visible through current coverage and is recoverable by the next manual
cache preparation or explicit index run. Preparation never infers. Indexing uses
the existing shared store/provider and Start/Pause/Resume/Cancel/Rebuild controls;
there is no additional queue or vector store.

Every embedding publication rechecks live source, chunk generation, model/profile
and running job generation. Unchanged inputs retain vectors even when chunks move;
changed context, preprocessing or model identity requires compatible replacements.
Partial saved vector batches survive pause/cancel/restart, but semantic search
requires a complete compatible message snapshot. Keyword search can use current
partial text independently. Retained search results are rechecked on access.
Classification binds source, email inputs, category/prototype inputs, chunk
generation and classifier profile; stale predictions are hidden. The combined job, message, category and
assessment status now comes from one SQLite transaction during concurrent sync.
Historical sources without a current content identity remain pending assessment
until a Gmail refresh supplies that identity; they are never claimed as assessed.

A source change during an authorized index run is picked up before completion.
A later change reports pending work and requires another explicit run. Confirmed
cache removal revokes/pauses the job; Resume processes remaining live cache.
Startup revokes abandoned running generations and reports interruption without
starting inference. Gmail label writes retain their independent preview,
confirmation, journal, reconciliation and undo boundaries.

Run the integration regressions with:

```sh
cd backend
go test ./internal/provider/gmail -run '^TestGmailLifecycle' -count=1
go test ./internal/sync -run 'Test(IncompleteRetries|HistoryPrioritizes)' -count=1
```

The tests cover partial initial imports and service restart, incomplete-body
recovery without history, bounded retry rotation, unchanged observations,
label-only changes, source replacement during inference, rejected stale workers,
retained-result revocation, post-commit projection failure and repair,
concurrent-sync coverage, deletion/journal preservation, account isolation,
cancellation before checkpoint publication and during post-commit preparation,
partial vector batches and explicit
resume after database/process restart. Existing store, coordinator and consumer
suites additionally cover rollback, expired-history reconciliation, multiple
changes within one history record, disabled/unavailable inference, model changes,
purge, FTS invalidation and reviewed Gmail apply/reconcile/undo behavior.

## Validation for this change

Passed full Go tests, vet and race checks, plus strict-CGO real native embedding
race tests. Frontend typecheck, lint, all 101 tests and production build passed;
Rust fmt, clippy with warnings denied and all 21 tests passed. Network-denied
native indexing, semantic search, categories, classification, hybrid search and
email-answer qualifications passed against the final application implementation.
A fresh macOS app build, ad-hoc signature verification, offline inference from
packaged/development layouts and native WebView-to-Go lifecycle smoke passed.
Repository formatting, Go formatting, changed-document links and diff whitespace
checks passed. Dependencies, model resources and numbered migrations are unchanged.

The initial restricted Gmail HTTP tests could not bind localhost; the permitted
rerun passed. The concurrent coverage fixture's initial ten-second sync and index
waits expired under the race detector; bounded waits and polling cadence were
adjusted and the full Go suite then passed. No validation failure remains.
All mailbox data was synthetic. No live-mailbox or paid-provider checks were run;
release signing/notarization and production-mailbox quality are not claimed.
