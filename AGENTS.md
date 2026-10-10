# MailPilot engineering rules

These rules apply throughout this repository. Read `docs/ARCHITECTURE.md` before changing architecture or implementation. It is the proposed baseline; preserve explicit user decisions and record material design changes there. Do not claim a proposal is user-approved unless it is. The initial task is documentation only: do not scaffold or implement the application without a subsequent implementation request.

## Product invariants

- Use Go for application behavior, React + strict TypeScript for presentation, a thin Tauri Rust host, SQLite for local storage, and Gmail API + OAuth as the first provider.
- Build one local desktop product. No microservices, hosted application backend, unnecessary frameworks, or parallel Rust business logic.
- Never ask for or store a Gmail password. Never persist OAuth tokens or AI keys in files, SQLite, source code, logs, browser storage, arguments, or environment variables. Use OS credential storage before real-account access. If unavailable, fail closed; use in-memory fake credentials only in tests.
- Never automatically delete suspected phishing emails or automatically send AI-generated email. MVP provider interfaces expose neither sending nor destructive operations.
- Risk detection supplies a versioned score and explanations, never a guarantee of safety. No automatic remote mutation follows a risk finding.
- Keep AI optional. Reading, sync, organization, risk analysis and keyword search must work without an AI provider or key.
- Email content stays local except intended provider traffic and an explicit user-approved external AI payload. No background embedding uploads, remote image loads, tracking, or telemetry by default.
- Every mailbox change must use the preview/confirm/journal/reconcile/undo path. No shortcuts for tests, background jobs, AI output or UI convenience. Report partial results and undo conflicts truthfully.

## Boundaries and scope

- Follow the phased plan. Implement only the requested phase/features; do not prebuild future AI/vector/provider systems.
- Keep Gmail types and semantics in its adapter. Use opaque provider IDs and capability checks. Do not pretend other providers have Gmail's label semantics.
- Go owns the database, network adapters, credentials, risk rules and use cases. React uses typed IPC. Rust owns native lifecycle and narrow bridge validation.
- Use fixed packaged sidecars and private pipes. Do not expose generic shell, SQL, filesystem, fetch, or arbitrary backend invocation commands to JavaScript.
- Validate all IPC inputs and enforce authorization/account ownership in Go. Define schema/DTO changes with versioned examples and compatibility checks.
- Prefer standard libraries, explicit SQL, concrete types and small consumer-owned interfaces. Add abstractions only at real boundaries or demonstrated test seams.
- Do not add placeholder directories, dependency frameworks, generalized queues, repositories or plugins merely for possible future needs.

## Data integrity and security

- Scope every mailbox query, search result and mutation by account. Preserve composite foreign keys; test isolation even before multi-account UI exists.
- Enable SQLite foreign keys on every connection. Use bounded transactions and explicit numbered migrations with checksums. Never edit already-released migrations; add new ones.
- Keep FTS and later vector data consistent with updates and purges. Treat derived indexes, WAL files and backups as sensitive mail content.
- Do not claim SQLite encryption or forensic deletion. Use private app-data permissions and document backup/retention behavior.
- Never hold a database transaction during network calls. Commit sync checkpoints only with the data they represent. Use idempotent imports and recover expired cursors without inferring deletions from incomplete listings.
- Journal before remote writes. Reconcile uncertain outcomes before retrying. Undo only actual changed membership and preserve unrelated changes. Do not promise exactly-once behavior or perfect restoration.
- Use `context.Context`, bounded concurrency, timeouts, cancellation, capped retry/backoff and input limits for external operations. Treat cancellation after dispatch as potentially uncertain.
- Treat email, HTML, MIME, attachments and model output as hostile input. Render escaped text for MVP; do not inject HTML, execute attachments, fetch message links, or grant model output action privileges.
- Keep secrets and email content out of diagnostics, error messages, fixtures and screenshots. Use synthetic fixtures and dedicated opt-in test accounts. Never run live personal-mailbox mutations as a routine test.
- Require PKCE, state validation, system-browser login and a bounded loopback callback for desktop OAuth. Keep refresh tokens in the OS store and serialize refresh operations.
- A submitted AI key may exist briefly in a UI input, but must never be returned by a read API or retained in frontend state after submission. Clear sensitive UI caches on disconnect.

## Dependencies, quality and delivery

- Before adding a dependency, state its concrete purpose, verify official documentation, maintenance, license, supported targets and advisories, and prefer an existing dependency or standard library when sufficient.
- Pin compatible dependencies and commit Go module checksums, frontend lockfile and Cargo lockfile when those manifests exist. Do not install dependencies during documentation-only work.
- Test behavior and failure boundaries proportionately. Prioritize migration integrity, account isolation, malformed MIME/IPC, OAuth failure/refresh, sync recovery, partial label writes, stale previews, undo, AI consent and disabled-AI behavior.
- Once configured, run relevant Go tests/vet/race checks, frontend typecheck/lint/tests/build, and Rust fmt/clippy/tests for affected code. Run packaging/native smoke tests for lifecycle changes; a mocked browser test does not validate Tauri.
- For documentation-only work, check consistency, links/paths and any documented schema examples; do not invent passing application tests or build results.
- Keep interfaces accessible: keyboard navigation, focus, readable errors, progress, cancellation, offline/cache status and risk explanations. Do not hide missing cached content or partial provider results.
- Never commit credentials, mail databases, attachments, local logs, generated sidecar binaries or build outputs. Review diffs before completion; preserve unrelated user work.
- Update architecture docs when changing boundaries, storage, privacy promises, scopes or the MVP. Explain material tradeoffs and deviations clearly.
- Complete authorized reversible work without unnecessary permission questions. Ask when genuinely missing information blocks a decision or when an action exceeds the user's authorization. Never send messages or mutate a live mailbox without explicit authorization for that action.
- Finish with what changed, why, verification performed and material limitations. Distinguish implemented behavior from proposed behavior. Do not call the project production-ready based on scaffolding or documentation alone.
