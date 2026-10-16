# MailPilot engineering contract

Applies throughout this repository. Follow explicit user instructions and implement
only the requested work. This contract defines active scope; older code/docs may
describe retired behavior. Read [the architecture](docs/ARCHITECTURE.md),
[focused product plan](docs/PRODUCT_PLAN.md) and
[contraction plan](docs/CONTRACTION_PLAN.md) before changing relevant boundaries.
Contract the existing system; do not rewrite it or prebuild future features.

## PRODUCT

MailPilot is a local-first Gmail organizer, phishing-warning tool, and bounded
single-email summarizer. Risk findings are versioned explanations, never guarantees
of safety or permission for automatic action.

## ACTIVE PRODUCT SCOPE

- One Gmail account; Gmail OAuth and OS credential storage.
- SQLite cached mail and bounded initial/incremental Gmail sync.
- Cached message reader and local keyword search.
- Editable categories and deterministic, explainable category suggestions.
- Reviewed MailPilot label additions through dry run, preview/confirmation,
  journaling, reconciliation and best-effort undo.
- Deterministic phishing-risk explanations.
- Optional AI summary for one explicitly selected cached email.
- Multiple AI providers supporting the same bounded summary use case.
- Future Chrome side-panel companion using the existing running desktop core.

Scope is not implementation status or authorization to implement every feature.

## AI SCOPE

Multiple AI providers share one provider abstraction and exactly one product workflow:
summarize one explicitly selected cached email, extract explicit requests, and extract
stated deadlines. Do not invent requests or deadlines. Isolate provider-specific
SDKs/types inside provider adapters.

Providers must not expose product capabilities for reply generation, thread
summarization, mailbox-wide Q&A, RAG, semantic search, classification, autonomous
actions, Gmail draft creation or tool use. No arbitrary prompts/chat interface.
The app must work normally with AI disabled or without a provider/key.

## NON-GOALS

Do not add embeddings/vector databases, MiniLM/model workers, semantic classification,
semantic search, RAG/chat over the mailbox, AI replies, Gmail draft creation,
autonomous mailbox actions, background AI, automatic classification writes,
sending/deleting/archiving/mark-as-read, multiple email accounts/providers, or a
hosted backend. Multiple **AI** providers do not imply multiple email providers.

## ARCHITECTURAL RULES

- Go owns business logic, SQLite, credentials and network adapters. Rust/Tauri
  stays thin; React with strict TypeScript handles presentation and typed IPC.
- Chrome must later use the same running Go core. Keep one database owner, fixed
  packaged sidecars and private communication; no generic arbitrary RPC, shell,
  SQL, filesystem or fetch bridge to JavaScript.
- Validate IPC and enforce authorization/account ownership in Go. Scope every
  mailbox query, search, preview and mutation by account; preserve composite FKs.
- Require explicit preview/confirmation before Gmail writes and journal before
  dispatch. Reconcile uncertain outcomes before retrying. Undo only verified changes
  owned by MailPilot; preserve unrelated changes and report partial/conflicting results.
- Never rewrite historical migrations; use numbered, checksummed additions.
  Enable SQLite foreign keys on every connection. Keep indexes consistent with
  updates/purges and sync checkpoints atomic with their data; never hold a database
  transaction across a network call or infer deletion from incomplete listings.
- Treat email, MIME/HTML, attachments and model output as untrusted. Render escaped
  text; never execute attachments or load remote email content automatically.
- AI receives only the explicitly approved selected-email payload. Bind approval
  to account, source revision, provider/model and expiry; invalidate on changes.
  AI never receives mailbox tools, Gmail permissions or action privileges.
- Never request Gmail passwords. Store OAuth tokens/AI keys only in OS credential
  storage; fail closed if unavailable. No file, database, browser-storage, argument
  or environment fallback. Clear submitted key inputs and sensitive session caches.
- Use system-browser OAuth with PKCE, state validation, bounded loopback callbacks
  and serialized refresh. Keep mail local except intended Gmail traffic and approved
  AI payloads; no default telemetry. Do not claim SQLite encryption or forensic deletion.

## IMPLEMENTATION RULES

- Inspect existing code, contracts, migrations and tests before modifying them.
  Prefer deletion/simplification over replacement abstractions; retain no dead
  abstractions for hypothetical flexibility. Preserve unrelated uncommitted work.
- Add/update tests for changed behavior, especially isolation, consent, stale
  previews, partial writes, recovery and disabled AI. Use synthetic fixtures; never
  mutate a live mailbox without explicit authorization for that action.
- Keep remote writes explicit, bounded, cancellable and recoverable. Use Go contexts,
  input limits, timeouts and capped retries; cancellation after dispatch can be uncertain.
- Never log tokens, keys or email contents or commit secrets, mail databases,
  attachments, local logs or generated build outputs.
- Prefer existing dependencies/standard libraries. Justify and review additions;
  pin compatible versions and preserve lockfiles/checksums.
- Run relevant Go tests/vet/race, frontend typecheck/lint/tests/build and Rust
  fmt/clippy/tests; use native smoke checks for lifecycle/packaging changes.
  Do not claim completion while required tests/builds fail. Report blocked or unrun checks.
- Preserve keyboard/focus access, readable errors, progress/cancellation and truthful
  offline/cache/partial-result states. Update architecture/contracts for boundary changes.
- For documentation-only tasks, check consistency, paths and examples without changing
  application code or installing dependencies. Finish with changes, verification and
  limitations; distinguish implemented behavior from plans.

## LEGACY RULE

Historical journals/migrations may require narrow compatibility code. Preserve
journal evidence, immutable plan formats and necessary label/draft recovery; never
expose retired behavior merely because recovery code remains. Draft recovery may
read Gmail and record outcomes, but must not create, resend or delete drafts.
