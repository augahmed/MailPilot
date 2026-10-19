# Documentation reset audit — 2026-10-19

Scope: documentation and planning only, following the user's Project Direction
and Documentation Reset request. No application behavior, dependencies, migrations,
credentials, databases, user data or executable fixtures are changed. No commit/push.
Source-level findings include pre-existing uncommitted/untracked work and are not
new runtime test results.

## Authoritative structure

| Document                                                 | Authority                                                                                                                                                                       |
| -------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| [AGENTS.md](../AGENTS.md)                                | Concise engineering contract: purpose/status, scope, ownership/simplicity, shared indexing/SQLite, security/Gmail safeguards, AI restrictions, non-goals/legacy and validation. |
| [PRODUCT_PLAN.md](PRODUCT_PLAN.md)                       | The only active product scope, release assumptions, user workflows, milestones and quality/dependency decisions.                                                                |
| [ARCHITECTURE.md](ARCHITECTURE.md)                       | Source-verified implementation status and intended shared pipeline, package ownership, chunk metadata, lifecycle and trust boundaries.                                          |
| [README.md](../README.md)                                | Current setup/operation, honest runtime status and navigation.                                                                                                                  |
| [contracts/README.md](../contracts/README.md)            | Actual wire behavior and existing synthetic fixtures; no future endpoint authorization.                                                                                         |
| [GMAIL_SETUP.md](GMAIL_SETUP.md)                         | Existing OAuth/keyring setup and troubleshooting; preserved without content changes.                                                                                            |
| [GENERATIVE_AI.md](GENERATIVE_AI.md)                     | Planned approved-evidence questions plus precise current Summary/provider/consent specification.                                                                                |
| [PHISHING_FEATURES.md](PHISHING_FEATURES.md)             | Existing independent risk feature/provenance/limit specification.                                                                                                               |
| [REPLY_DRAFTING.md](REPLY_DRAFTING.md)                   | Current GET-only historical draft recovery compatibility.                                                                                                                       |
| [archive/LEGACY_FEATURES.md](archive/LEGACY_FEATURES.md) | Consolidated retired-feature, migration and superseded-plan notes; not instructions.                                                                                            |

No active implementation prompt plan remains. The old numbered prompts included
composition/Chrome and no longer define work. Future scoped tasks should be derived
from the single current product plan and inspected code.

## Disposition of inspected documents

| Previous file                     | Action and reason                                                                                                                                                                                                       |
| --------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `AGENTS.md`                       | Rewritten: removes the summary-only/embeddings-prohibited target, makes shared retrieval planned scope and preserves runtime safeguards.                                                                                |
| `README.md`                       | Updated only for direction/status, schema overview, documentation links and demonstrated stale risk wording; setup remains usable today. Formatting verification now lists Go formatting instead of rewriting the tree. |
| `docs/ARCHITECTURE.md`            | Consolidated/reorganized: current source status first, intended shared source/chunk/model/retrieval architecture next; obsolete hypothetical package/interface sketches removed.                                        |
| `docs/PRODUCT_PLAN.md`            | Replaced overlapping summary/composition/Chrome release direction with the sole retrieval/search/classification/evidence-question plan.                                                                                 |
| `docs/GENERATIVE_AI.md`           | Updated target to approved-evidence answers only; retains implemented Summary/settings/keyring/payload/timeout specifications.                                                                                          |
| `docs/REPLY_DRAFTING.md`          | Removed the future composition pointer; preserves current journal/GET-only recovery details.                                                                                                                            |
| `docs/PHISHING_FEATURES.md`       | Preserved technical content, corrected the architecture reference.                                                                                                                                                      |
| `contracts/README.md`             | Repointed scope references, corrected documented 30/32-second classification deadlines and current schema-14 health example. No JSON fixture or handler changed.                                                        |
| `docs/GMAIL_SETUP.md`             | Preserved unchanged: accurate existing OAuth/credential setup and verification boundaries.                                                                                                                              |
| `docs/CONTRACTION_PLAN.md`        | Moved to [archive/CONTRACTION_2026-10-16.md](archive/CONTRACTION_2026-10-16.md): important file disposition, migration/journal evidence and recovery couplings retained, with archival status and rebased links.        |
| `docs/ENGINEERING_REVIEW.md`      | Moved to [archive/ENGINEERING_REVIEW.md](archive/ENGINEERING_REVIEW.md): earlier maintainability observations retained but marked historical.                                                                           |
| `docs/RETRIEVAL_READER_PLAN.md`   | Removed after consolidating useful chunk/source/lifecycle/security/reader details into architecture/product plan and legacy notes; composition/Chrome no longer shape the core.                                         |
| `docs/IMPLEMENTATION_PROMPTS.md`  | Removed: its unique compatible decisions survive in the product plan, but its broad numbered implementation sequence is superseded.                                                                                     |
| `docs/ASK_MAILPILOT.md`           | Removed short redundant retirement stub; compatibility preserved in legacy notes/contracts.                                                                                                                             |
| `docs/SEMANTIC_SEARCH.md`         | Removed short retirement stub; current lack of runtime and planned new retrieval are explicit in architecture; historical schemas/commands preserved in legacy notes.                                                   |
| `docs/SEMANTIC_CLASSIFICATION.md` | Removed short retirement stub; rule gate/stale historical engine behavior and codec constraints preserved in current architecture/legacy notes.                                                                         |
| `docs/SEMANTIC_EVALUATION.md`     | Removed short historical stub; legacy notes preserve the fact that old corpora/measurements do not validate the future model.                                                                                           |

New documents: this audit, the consolidated legacy notes and the two explicitly
archived records at their new paths. No historical migrations, journal formats,
contracts JSON, setup scripts or application source files are removed. Link labels
in historical inventories may mention old paths as snapshots; live links target
retained documents.

## Conflicts resolved

The old contract made embeddings/RAG categorically non-goals while newer plans
proposed them. Several documents simultaneously treated Summary as the product's
only allowed AI operation and proposed answers/composition. Three overlapping plans
and retired-feature stubs supplied conflicting scope. Chrome and composition were
still present as implementation phases. The maintainability review described removed
dependencies/runtime as though current. These conflicts are resolved by the authority
structure above and explicit implemented/planned/archive labels.

Compatible previously confirmed choices remain: English first, approximately 10,000
cached messages, one primary category with abstention and a bundled model/tokenizer.
The new request removes composition/Chrome from immediate work and keeps the shared
pipeline central. Supporting formatted reading remains planned independently; it
is not a reason to delay the core source/chunk contract or a claim of Gmail parity.

## Source findings and technical gaps

- `backend/cmd/mailpilot-core/main.go` composes rules directly; it has no chunker,
  embedding/index/retrieval service. `backend/internal/app/app.go` has no search
  service. Old Ask/search commands are absent from the IPC switch.
- Migration 013 passage rows have input-hash/vector identity but no full chunk text,
  ordinal/section/source offsets. Historical 008–010/013 data is inert. The new chunk
  store requires an explicit additive schema decision; do not merely flip a flag.
- `mail.CleanBody` trims quotes/signatures and stops at forward markers, which can
  discard unique retrieval evidence. `HTMLText` is not an HTML sanitizer. New shared
  source preparation must preserve reading/source evidence and map passage offsets.
- Current classification fingerprints exclude recipients/most headers. A new
  classifier must fingerprint every new feature and category/example revision;
  content/label-only changes need correct invalidation without needless re-embedding.
- `AIProvider` exposes only `SummarizeEmail`; it cannot answer from retrieved mail
  today. Only OpenAI is installed. Preserve provider/key settings and use new typed
  answer/consent boundaries rather than treating old Summary approval as RAG consent.
- FTS5 is probed in tests, but there is no product keyword index/search endpoint.
  A probe or retained vector index is not a search implementation.
- Current cold-start account restoration refreshes OAuth and reads the Gmail profile
  online. Cache-local reads within a session do not establish offline restart support.
- Current reader is escaped paged text, not rich HTML. New sanitation/native resource
  isolation is a separate supporting change, and the existing CSP blocks frames.
- The README risk section incorrectly said scoring/persistence/UI were absent;
  source already composes/persists/displays a separate risk engine. IPC prose also
  incorrectly described the classification timeout as five seconds; source uses
  30 seconds in Go and 32 in Rust. Documentation now matches those boundaries.
- The working tree has substantial earlier AI/contraction changes. This reset does
  not reconcile or commit them, alter their behavior, or certify release readiness.

## Recommended first implementation task

First resolve the bounded model/tokenizer/native-bundling/license decision using
synthetic evidence (M0). Then request M1: add loss-preserving versioned source
sections and the one shared chunk contract/chunker under the existing Go mail
boundary, with original passage text, account/source revision, order/section,
offsets/mapping and content hashes. Test forwards, quotes, alternatives, Unicode,
incomplete/oversized input and deterministic boundaries. Preserve current reading,
risk and rule-cleanup behavior.

This yields the concrete representation needed for minimal additive chunk/vector
storage and invalidation, then local indexing/search. Do not begin with external
AI routes, revive retired Ask, create a composition editor or rewrite the app.
No M0 experiment, dependency installation or M1 code was started during this reset.

## Verification boundaries

Documentation validation checks local paths/anchors, fenced examples, Markdown
formatting, active scope consistency and reference removal. Compare non-Markdown
source/config/fixture content hashes and migration checksums against the reset's
starting working state to prove unrelated application work is preserved.

Application tests/builds, native smoke, model benchmarks and provider/mailbox calls
are not rerun for this documentation-only task. Historical validation prose is
preserved as history, never reported as a new pass. New targets remain unmeasured.
