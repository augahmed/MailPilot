# MailPilot engineering contract

Updated 2026-10-19. Applies throughout this repository. Follow explicit user
instructions and change only the requested scope. Read [the product plan](docs/PRODUCT_PLAN.md),
[architecture](docs/ARCHITECTURE.md), [shared vector implementation design](docs/VECTOR_ARCHITECTURE_PLAN.md)
and relevant [IPC contracts](contracts/README.md) before changing a boundary.
The product plan defines scope; the vector plan supplies concrete reuse/schema/
compatibility decisions within that scope. Archives are evidence, not instructions.
Plans do not authorize implementation without an explicit implementation request.
The authorized persistence/chunking/indexing slices use migrations 015–017 and
`Store.Chunks()` (`repository/chunks.go`). Reuse `email_chunks`, `chunk_embeddings`
and `index_state`; do not add parallel stores. `internal/mail` owns deterministic
sections/chunks and the pinned offline `wordpiece` tokenizer; inference must
reuse that tokenizer and `mail.ModelInput` recipe or version/rebuild explicitly.
Manual sync performs bounded local cache preparation; it does no inference.
One native CPU provider is now implemented in `internal/embedding`; its shared
`model` identity is used by chunks. Reuse it and the pinned WordPiece tokenizer.
`app.LocalIndex` now connects that provider to the shared store through explicit
Start/Pause/Resume/Cancel/Rebuild controls and the narrow `index.local` IPC. Startup
marks interrupted work resumable; it never auto-starts inference. See
[local embedding contract](docs/LOCAL_EMBEDDING.md) for bounds, fixed resources,
cancellation, signing and macOS 13.4+ qualification, and
[local indexing contract](docs/LOCAL_INDEXING.md) for durable generations, current
coverage counts and purge/restart fences. Retrieval and semantic classification
remain unimplemented. Preserve unrelated uncommitted work.

## Product and scope

MailPilot is a local-first intelligent Gmail manager built around one shared local
email preprocessing, chunking, embedding and retrieval pipeline in Go and SQLite.

Preserve current Gmail OAuth/keyring, bounded sync/cache, cached reading, editable
categories, deterministic review and phishing explanations, reviewed label changes,
provider settings and historical recovery. Selected-email Summary currently exists;
it is a transition feature, not the product's defining workflow. This reset removes
no functionality. Source-level status is recorded in the architecture.

Planned core: natural-language semantic search with complementary keyword search;
one primary category suggestion from shared chunk/category vectors, useful rules
and confirmed examples, with abstention and user corrections; optional answers to
email-information questions using explicitly approved retrieved evidence and citations.
Support multiple AI providers behind a shared abstraction. Start with English and
approximately 10,000 cached messages. Bundle the local model/tokenizer; require no
user Python installation or post-install model download. Ordinary use must remain
possible with embeddings or external AI disabled.

## Ownership and simplicity

- Go owns business rules, Gmail/network adapters, credentials, SQLite, indexing,
  classification, retrieval and AI consent. React/strict TypeScript owns presentation;
  Rust/Tauri owns desktop lifecycle and narrow typed private communication.
- Keep one Go database owner and one SQLite database. Extend existing mail, sync,
  classification, AI and repository boundaries; do not rewrite functioning features.
- Prefer reusing the shared chunk/embedding store and retrieval methods before
  adding any component. Use one shared chunker/model/vector representation for
  semantic search, classification and AI evidence retrieval. Classification reads
  existing email chunk vectors; it must not re-embed mail or own another index.
  Category descriptions use the same adapter; confirmed examples reference shared
  chunks rather than storing duplicate vectors. No one-vector-per-email search cache.
  Keep provider-specific SDK/wire types inside adapters. Local embedding inference
  and optional external generation have separate settings and authority.
- Use fixed packaged executables and private IPC; no generic RPC, shell, SQL,
  filesystem or fetch bridge to JavaScript. Validate inputs/account ownership in Go.
- Prefer standard libraries/existing dependencies and small concrete components.
  No generic job/plugin framework, service locator, extra database, microservice,
  parallel processing framework or second embedding system for hypothetical needs.

## Source, indexing and SQLite

- Preserve original cached email separately from processed text. Chunks retain
  source account/email/revision, original passage text, ordering/section metadata,
  content hash and model/preprocessing identity with their vector. Do not destroy
  forwarded/quoted evidence through classifier-oriented cleanup.
- Begin with exact cosine retrieval over compatible account-scoped vectors. Add
  ANN/vector extensions only when measured scale/latency justifies their complexity.
  Similarity is neither a calibrated category probability nor evidence of safety.
- Historical vector tables remain inert and are not a complete chunk store. Do not
  rename/reinterpret them or revive disabled Ask/search/draft commands. Reuse current
  compatibility tests and add schema only for the authorized implementation slice.
- Never rewrite numbered checksummed migrations or historical journal formats.
  Add migrations after the actual current maximum. Preserve composite account FKs,
  foreign keys on every connection and transactional source/checkpoint updates.
- Atomically invalidate derived data and queue local work when sources change.
  Never hold a transaction across inference/network calls. Publish computed results
  only after rechecking source/model/preprocessing revisions; reject stale workers.
- Keep model generations separate; do not mix incompatible query/category/chunk
  vectors. Category/example edits invalidate dependent assessments. Confirmed remote
  deletion and authorized purge must update all active indexes and sensitive caches.
  Never infer deletion from incomplete provider listings or delete journal evidence.
- Use bounded cancellable local jobs with explicit enablement, progress, partial/error
  states and restart recovery. One local coordinator owns durable indexing and
  dependent assessment work; no parallel feature-specific queues/frameworks.
  Preserve keyword/rule fallback and truthful coverage; keyword search is planned,
  not already implemented by the existing FTS capability test.
- Version new semantic assessment hashes/scores and organization evidence explicitly.
  Existing rule hashes, confidence DTOs and immutable plan digests are not a drop-in
  semantic contract. Preserve old decoding, reconciliation and undo independently
  of the current model; never collapse multiple legacy assignments automatically.

## Security, privacy and Gmail writes

- Use system-browser OAuth with PKCE/state, bounded loopback callbacks and serialized
  refresh. Never request Gmail passwords. OAuth tokens and AI keys use OS credential
  storage only; fail closed with no file/SQL/browser/argument/environment fallback.
  Developer OAuth client configuration is distinct from user tokens and keys.
- Treat email, MIME/HTML, headers, attachments and model output as untrusted. Current
  reader uses escaped text; any future HTML reader requires reviewed sanitation and
  capability-free isolation. Never execute attachments or load remote content by default.
- Keep mail local except intended Gmail traffic and explicitly approved AI evidence.
  No cloud embeddings, default telemetry or logs containing credentials/email content.
  Clear submitted keys and sensitive session caches; make no SQLite encryption or
  forensic-deletion claim. Do not commit secrets, databases, logs or build/model outputs.
- Require bounded preview and explicit confirmation before Gmail label additions;
  journal before dispatch and reconcile uncertainty before retry. Undo only verified
  MailPilot-owned changes, preserving unrelated labels and reporting partial/conflicts.
  Local category suggestions/corrections never authorize Gmail writes by themselves.

## AI boundary

- The planned external workflow answers bounded email-information questions from
  retrieved evidence. Go retrieves and constructs the payload; disclose exact selected
  excerpts, question, policy and provider/model before separate approval.
- Bind one-use expiring approval to account/session, source revisions, exact payload,
  provider/model/settings/key identity. Revoke on relevant changes; check before
  dispatch/display. Existing Summary enablement/consent does not authorize RAG.
- Give providers no Gmail permissions, mailbox/search tools, action privileges,
  hidden source expansion, arbitrary endpoint or general-purpose agent/chat interface.
  No background external AI. Output is untrusted; validate source IDs/quotes and
  render escaped citations. Provenance validation does not prove factual correctness.
- Keep multiple providers behind the same bounded application contract; only functional
  adapters appear in settings. No key/provider is required for ordinary local features.

## Non-goals and legacy

No AI replies/composition in the immediate scope; do not let a hypothetical editor
shape the vector design. No sending, new Gmail drafts, autonomous agents, unconfirmed
mailbox changes, deletion/archive/mark-as-read, Chrome integration, additional email
providers/accounts, hosted backend/cloud vectors, attachments/OCR or unrelated AI workflows.
Historical draft recovery may read Gmail and record local outcomes, but must never
create, resend, delete or revive drafts. Preserve immutable recovery evidence.

## Verification

Inspect affected code/contracts/tests first. Use synthetic fixtures; never mutate a
live mailbox without explicit authorization for that action. Test isolation, source
invalidation, stale previews/workers/consent, partial writes, recovery, abstention,
missing/disabled embeddings and disabled AI. Use contexts, limits, timeouts and capped
retries; cancellation after remote dispatch may remain uncertain.

For implementation, run relevant Go tests/vet/race, frontend typecheck/lint/tests/build
and Rust fmt/clippy/tests; use native smoke checks for lifecycle/packaging/rendering.
The native MiniLM/ONNX slice enables CGO on Apple Silicon builds and packages
verified fixed resources; other builds retain an unavailable native adapter. Keep
model/runtime/binding hashes pinned, never commit weights or native libraries, and
run real native plus signed/offline package tests for inference/packaging changes.
No post-install download, cloud fallback or second runtime is authorized.
Pin and justify dependency changes, preserving lockfiles/checksums. Report failed,
blocked and unrun checks; never claim completion with required checks failing.
Preserve keyboard/focus access, readable errors, cancellation and offline/partial states.
For documentation-only work, check consistency, paths and examples without modifying
application code, installing dependencies or claiming runtime tests were run.
