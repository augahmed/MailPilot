# Repository maintainability review

This review examined composition, application jobs, provider/storage boundaries,
semantic retrieval, optional AI consent, IPC contracts, React async state, tests and
direct dependencies. It is a targeted source review, not a comprehensive security
audit or real-account qualification.

## Changes

- **Account isolation in reply UI:** mailbox data previously loaded independently
  of the account in reply status. A stale response could populate the new account's
  message selector. Loads now follow account identity, discard obsolete responses,
  and clear private form state on account changes. Regression tests cover mismatched
  and deliberately delayed responses. Existing Go account authorization remains the
  final enforcement boundary.
- **Error consistency:** settings and draft reads replaced cancellation with generic
  storage errors; one source lookup also misreported any SQL failure as “not found”.
  These paths now use the existing SQLite error sanitizer. Application AI workflows
  preserve the cancellation wire code. Tests reproduced the original cancellation
  failures and verify the corrected repository and application behavior.
- **Package ownership:** shared non-secret AI settings no longer live in the reply
  domain or require Ask to depend on reply types. Settings have their own SQLite
  adapter, while reply storage retains its source/journal responsibilities. Existing
  table names, migrations and saved configuration remain compatible; restart and
  account-isolation tests exercise the extracted adapter.
- **File responsibilities:** independent classification, organization and search
  contracts moved out of the large TypeScript IPC client, retaining explicit
  reexports. Durable Gmail draft creation/reconciliation moved out of the reply
  service file. Shared safe AI-workflow errors now have a clearly named owner. The
  extractions preserve the existing contract fixtures and action-journal tests.
- **Small cleanup and test quality:** removed the redundant reply-input error and an
  unused Ask job parameter. Reply UI tests now wait for the actual email option,
  rather than assuming the mailbox has loaded when the empty selector appears.

## Deliberately retained

- Small consumer-owned read interfaces and explicit job state machines remain.
  Although several jobs have similar lifecycle code, generation consent, indexing
  and uncertain remote writes have different guarantees. A generic job framework
  would add risk and indirection in this pass.
- The larger reply/organization React forms remain cohesive workflows. They were
  not split into many components requiring extensive state/approval prop passing.
- The older general mailbox-action repository is currently exercised by its tests,
  while production organization uses the specialized journal. It also contains
  transition helpers used by that journal. Removing its durable-state machinery
  and regression coverage requires a separate compatibility decision; it was not
  treated as safely deletable dead code.
- Direct Go, React/TypeScript, Rust and optional local inference dependencies have
  concrete uses. No unused direct dependency justified removal, and none was added.
- Exact vector scanning, classifier policies, provider semantics, encryption claims
  and external-AI privacy policy were not redesigned. Existing prompt-injection
  defenses cannot prove model interpretations correct; citation validation checks
  provenance, as documented in Ask MailPilot.

## Validation

Run the complete Go test/race/vet/build suite; frontend typecheck, lint, tests and
build; Rust fmt, tests and clippy; repository formatting/diff checks; and the macOS
bundle/native smoke test. Synthetic tests use no live mailbox changes or paid AI
calls. The native smoke check verifies real WebView/IPC authorization and lifecycle,
not successful live Gmail/AI integration. Optional real-MiniLM benchmarks are not
part of the default suite and are not needed for these unchanged inference paths.
