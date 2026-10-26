# MailPilot portfolio and developer-release prompt plan

Prepared 2026-10-26 from the current working tree. This is a sequence of future
implementation prompts, not a record of completed work. Creating this plan does
not authorize running its implementation steps. Paste one prompt at a time into
a coding session after the preceding step meets its acceptance checks.

The objective is a professional, easy-to-demonstrate local desktop project with
optional user-owned Gmail and OpenAI configuration. Public Google verification,
Cloudflare hosting, automatic updates and additional platforms are separate work.

## Outcomes and execution order

| Prompt | Outcome                                                          | Dependencies                                     |
| ------ | ---------------------------------------------------------------- | ------------------------------------------------ |
| 1      | A buildable repository and a short documentation entry point     | None                                             |
| 2      | Verified security baseline and honest privacy documentation      | 1                                                |
| 3      | Simple local Google setup and personal app builds                | 1, 2                                             |
| 4      | A synthetic demo that needs no credentials                       | 1, 2                                             |
| 5      | A professional README, setup instructions and real visual assets | 3, 4                                             |
| 6      | Encrypted local storage and carefully scoped cache removal       | 1, 2; include for the stronger real-data release |
| 7      | A reproducible, accurately described portfolio release candidate | 1–5; also 6 for the stronger real-data release   |

For an early résumé demonstration, execute **1 → 2 → 3 → 4 → 5 → 7** and lead
with synthetic data. Before recommending the build for sensitive real mail,
include **6**, refresh the documentation from **5**, and repeat **7**. Security
findings from prompt 2 may require fixes before any real-mail recommendation.
Neither path grants Google verification or establishes blanket security/compliance.

## Baseline recorded before executing the prompts

This is the starting-tree inventory, not a claim that later fixes remain undone.
Recheck source and [repository consolidation notes](archive/README.md) when running
each prompt. Preserve the architecture and existing work described here.

- Go owns Gmail, credentials, business rules and one SQLite database. React is
  presentation; Rust owns lifecycle and narrow private IPC. Reuse the current
  chunker, tokenizer, coordinator, vector store and provider boundaries.
- The repository already has uncommitted storage hardening, privacy disclosures
  and AI-key-removal retry changes. Preserve them and unrelated work.
- Duplicate ` 2` source files and iCloud placeholders currently interfere with
  ordinary builds. Prior checks used an isolated dependency installation and a
  temporary Go overlay. Future final checks must work without that overlay.
- The actual migration maximum is **021**, while some documentation still says
  017 or 020. Confirm executable source before correcting claims; do not rewrite
  migrations, historical codecs, fixtures required by tests, or recovery evidence.
- Current Google configuration uses environment variables. The app does not yet
  import Google's downloaded JSON or contain packaged client configuration.
- OpenAI already has an in-app settings flow and OS-only user-key storage.
  Preserve separate exact-request approvals for Summary and evidence answers.
- The mail cache remains unencrypted. Disconnect retains local data and does not
  remove AI keys. There is no cache-removal UI. Existing hardening is not encryption.
- The recorded evaluation includes an unresolved Go toolchain advisory and
  platform-specific dependency warnings. Revalidate those records against current
  authoritative advisories rather than treating old audit results as current.
- Apple Silicon macOS is the native target; inference requires macOS 13.4+.
  Developer ad-hoc signing is not Developer ID signing/notarization. Native model
  assets are build-time, checksummed resources, never Git source or runtime downloads.

The authority documents are [AGENTS.md](../AGENTS.md),
[product scope](PRODUCT_PLAN.md), [architecture](ARCHITECTURE.md),
[vector implementation](VECTOR_ARCHITECTURE_PLAN.md) and
[IPC contracts](../contracts/README.md). Additional useful starting points are
[Gmail setup](GMAIL_SETUP.md), [privacy](PRIVACY.md),
[external AI](GENERATIVE_AI.md), [embedding packaging](LOCAL_EMBEDDING.md) and
[evaluation](EVALUATION.md). Re-read the affected boundaries before editing them.

## Prompt 1 — Stabilize the repository and simplify documentation navigation

```text
Prepare MailPilot's repository for a professional developer/portfolio release.
Read AGENTS.md, the current product/architecture/vector plans, and relevant IPC
contracts. Inspect git status first and preserve unrelated uncommitted work,
including the recent privacy hardening.

Audit duplicate " 2" files, iCloud placeholders, stale documents and incomplete
generated dependencies. Compare contents and references before changing anything.
Consolidate duplicate source only after preserving unique intended behavior and
tests. Do not blindly delete every duplicate-looking filename. Restore generated
dependencies from the existing lockfiles without opportunistic upgrades.

Create docs/README.md as the documentation index. The public reading path should
highlight setup, architecture, privacy/security, and measured evaluation. Put
specialist contracts behind a separate technical-reference section. Keep historical
material under docs/archive with an explicit historical status; it must not look
like a current feature or implementation instruction.

Keep AGENTS.md, the authoritative product/vector plans, active contracts, test
fixtures, numbered migrations, licenses and immutable recovery formats. "Only
important documentation" means clear navigation and removal of verified redundancy,
not destroying engineering evidence. Prefer leaving authoritative file paths stable;
if a document moves, repair all source, script, test and documentation references.
Keep the portfolio prompt plan reachable only as an engineering planning reference.

Check schema/version and feature-status claims against source. Correct stale current
claims without rewriting clearly identified historical measurements or fixtures.
Do not revive legacy Ask/search/drafting operations. Do not change application
features beyond consolidating the build-blocking duplicates.

Acceptance: ordinary source commands compile without a duplicate-file overlay;
the docs index has a concise reader path; local links resolve; active vs historical
features are unambiguous; no unique work or immutable artifacts are lost. Run the
checks appropriate to source changes, and report pre-existing or remaining blockers.
```

## Prompt 2 — Close the immediate security gaps and document the threat model

```text
Audit and harden MailPilot for a developer demonstration using the current
architecture. Start with AGENTS.md and the privacy, AI, OAuth, storage and IPC
contracts. This step preserves the selected storage-hardening approach; it does
not silently replace SQLite or claim that the database is encrypted.

Verify the existing private directory/database/WAL/SHM/journal handling and memory
temporary settings. Check OS-only tokens/AI keys, credential-store failure behavior,
safe diagnostics, secret input clearing, session invalidation, escaped mail/model
rendering, remote-content blocking and separate AI disclosure approvals. Correct
concrete gaps found within those boundaries. Preserve the retry for failed AI-key
removal while disabled. Do not disable functioning Summary or evidence answers as
a substitute for enforcing their existing consent rules.

Re-run supported dependency/toolchain advisory checks, starting with the recorded
findings in docs/EVALUATION.md. Verify reachable advisories and fixed versions using
official sources, then apply narrowly justified compatible patches. Preserve and
update checksums/lockfiles intentionally; do not perform a general dependency
upgrade. Separate target-platform findings from unsupported-platform findings.

Inspect tracked/staged material for credentials, mail, databases, build artifacts
and screenshots containing private information. Do not print discovered secrets.
Document any incident and required rotation privately; do not rewrite Git history
or rotate live credentials without explicit authorization.

Add a concise root SECURITY.md covering the threat model, supported security
boundaries, reporting instructions that actually work for this repository, and
known limitations. Consolidate current data-handling details in docs/PRIVACY.md.
Retain prominent statements that the cache is unencrypted, same-user/admin access
is outside permission hardening, disconnect retains data, AI keys are separate,
and backups/snapshots are not erased. Do not promise "all data stays local" when
optional approved AI can transmit content. Do not invent audit or compliance badges.

Acceptance: meaningful synthetic regression tests cover any security fixes; keys
and mail cannot escape through errors/IPC/logs; local features work with external
AI disabled; findings have accurate evidence and dispositions. Unresolved reachable
high-risk findings block real-mail release recommendations and must not disappear
from the README or evaluation when docs are simplified.
```

## Prompt 3 — Make personal Google configuration and app packaging simple

```text
Implement a small developer-owned credential setup/build workflow for MailPilot.
This is a personal/developer build flow, not a shared verified public OAuth app.
Read the Google setup, configuration, credentials, lifecycle and packaging contracts.

Provide one documented way to supply Google's downloaded Desktop app OAuth JSON
from an explicit local file outside the repository. Validate a bounded file, the
"installed" client type and required client fields. Reject web/service-account
credentials and token/API-key records. Keep OAuth endpoints and loopback/PKCE
behavior fixed in trusted Go code; never adopt arbitrary endpoints from JSON.
Never print the JSON, client secret, token values or full child environment.

Add small Node helpers using fixed executables and argument arrays, not shell
evaluation or a generic launcher framework. Suggested command names are
"npm run dev:personal -- --google-client-json /absolute/path/client.json" and
"npm run build:personal -- --google-client-json /absolute/path/client.json".
These names are proposed here: implement and verify them before documenting them
as available. The first starts development without two manual environment exports;
the second creates a personal bundle that can be opened from Finder with a clean
environment. Use existing build scripts and their pinned embedding resources.

Package only the minimal Google desktop client configuration in a fixed
backend-owned build resource/default. Keep it out of React/VITE variables, tracked
source and public release artifacts. Standard builds must not inherit stale personal
configuration from an earlier build. Add configuration before signing; do not modify
a signed app in place. Validate packaged configuration in Go and preserve fail-closed
authentication. Keep profile/client credential namespaces stable and isolated.

Explain that a desktop client ID and desktop client_secret are extractable public-
client configuration; PKCE and user authorization remain essential. Never package
refresh/access tokens, an OpenAI key, a Gmail password or signing credentials. The
downloaded JSON remains the user's Google configuration file, not MailPilot's token
store. A personalized bundle is for its owner, not a verification bypass to advertise
as a general consumer release.

Keep OpenAI setup in the existing in-app Optional AI settings. OpenAI user keys
must remain in the OS credential store, never in Google JSON, environment variables,
build resources, shell commands, SQLite or screenshots. No paid validation request
may occur just because settings are saved or the app starts.

Acceptance: synthetic tests cover missing/oversized/malformed/wrong-client JSON,
fixed endpoint enforcement, config precedence and clean standard builds. A fresh
personal app launches without environment exports. Artifacts contain only intended
desktop client configuration, never user secrets. Test real native plus signed/
offline model packaging as required by the packaging contract. Use synthetic client
canaries for automated checks; real Google sign-in needs an authorized test account.
```

## Prompt 4 — Add a credential-free synthetic demonstration

```text
Make MailPilot easy for a recruiter to demonstrate without Google or OpenAI setup.
Add a clearly marked synthetic demo mode with a small deterministic mailbox and
useful examples for reading, keyword/hybrid search, categories and label previews.

Keep the demo selection in trusted process startup, with a dedicated isolated data
directory, fixture account/session and an always-visible "Synthetic demo" indicator.
Do not weaken production authentication or let JavaScript select arbitrary account,
filesystem, provider or executable paths. Production mode must not read demo records,
and demo mode must never load real credentials, open Google sign-in, call Gmail,
dispatch label changes or contact an AI provider. Stop those operations in Go, not
only by hiding buttons. Do not create a second application database owner or index.

Use the existing schema, deterministic mail.Sections/ModelInput/tokenizer pipeline,
store and retrieval/classification services inside the isolated demo profile.
Reuse the current local embedding provider only after explicit Start/Resume.
Keyword search must work when embeddings are unavailable. Do not fabricate vectors,
inference timings, similarity scores or model answers for screenshots. Explain
unavailable/partial features instead. Optional external generation stays disabled
for the credential-free demo; local retrieval/evidence preview can still be shown.

Use obviously synthetic senders and content, such as example.test addresses. Keep
all fixtures separate from real app data. Seed idempotently and offer reset only
for this known demo profile; a demo reset must never target a user-selected path
or real mailbox, keys, cache or historical journal. Avoid a generic mock/plugin
framework. Existing test fakes are evidence, not a reason to bypass live boundaries.

Acceptance: the documented demo command and packaged demo route are actually
implemented; startup works without credentials or network; repeat startup does not
duplicate fixtures; reset is profile-fenced; attempted network/Gmail writes and
cross-profile reads are rejected by backend tests. Capture reproducible screen
states for the README only after these checks pass.
```

## Prompt 5 — Write the professional visual README and simple setup guides

```text
Rewrite MailPilot's README for a developer, recruiter and prospective technical
user. Describe the verified implementation after prompts 1–4. Read the authoritative
scope/architecture/contracts and current evaluation before making capability claims.
Use clear prose, a table of contents and progressive detail: quick demonstration
first, setup next, architecture/security/evaluation afterward.

Include: concise project purpose; actual feature/status table; prominent demo and
personal-use routes; supported OS/architecture; clean prerequisites; exact tested
commands; expected output and troubleshooting; design tradeoffs; repository layout;
measured evaluation with dates, denominators and limitations; security/retention
summary; current release/signing status; and a short documentation map. Do not use
invented badges, download links, benchmark numbers, production-readiness claims,
or unchosen project-license declarations. Keep detailed contract text in its source
documents and link it rather than duplicating multiple competing explanations.

Create native, source-controlled visual assets under docs/assets: a runtime/trust-
boundary diagram and a shared mail-to-chunks-to-index-to-search/approved-AI diagram.
Use maintainable Mermaid and/or SVG source, with locally rendered image exports
where useful for GitHub readability. Add at least three genuine app screenshots
from the isolated synthetic demo: dashboard/mail reader, search/classification,
and a privacy or evidence-approval screen. Include descriptive alt text, captions
and reproduction instructions. Do not generate fake UI images or use real mail,
credential values, OS account information or a logged-in developer console. Do not
commit model weights, native libraries, screen recordings or bulk build artifacts.

Make Gmail setup a short numbered walkthrough in docs/GMAIL_SETUP.md, with a concise
version in the README: create a Google Cloud project, enable Gmail API, configure
External/Testing plus the owner's test account, declare the actual gmail.modify
scope with its reason, create a Desktop app client, download its JSON outside Git,
run the implemented personal launcher/build helper, and Connect Gmail in the system
browser. Explain normal consent, loopback callbacks, testing-mode token expiry and
reconnection. No purchased domain/public callback is needed for this desktop test
flow. Do not suggest bypassing unknown app warnings or claim user-owned credentials
remove Google's policies or public-distribution verification obligations.

Add docs/OPENAI_SETUP.md and a concise README walkthrough: create an OpenAI Platform
project/API key; check API billing and model access; open Optional AI settings;
choose OpenAI and an adapter-compatible accessible model; enter the user's key;
save; inspect and separately approve a selected-email Summary or evidence request
only if desired. Recheck current official OpenAI documentation for account/billing
guidance and supported request features. Saving settings must not make a paid call.
Explain charges, key removal/retry, retention and cancellation limits. The app uses
OS key storage; do not copy generic SDK environment-key instructions into this app.
No API key is needed for ordinary local features or the synthetic demo.

Acceptance: all commands and links correspond to actual current files/features;
diagrams match ownership and consent boundaries; all screenshots are real and
synthetic; local links and alt text pass review; a reader can follow each route
without relying on this chat. Keep the top-level docs list short and route advanced
details through docs/README.md. Documentation-only edits need consistency/path/
example checks, not fabricated runtime-test claims.
```

## Prompt 6 — Add encrypted storage and honest cache-management controls

This is a separate, substantial real-data security slice. The earlier choice to
start with hardening did not implement it. Synthetic portfolio demonstrations can
proceed with accurate limitations while it remains unfinished.

```text
Implement stronger local-data protection for MailPilot as a scoped storage/security
change. Read the storage, source/index lifecycle, recovery, AI and IPC contracts.
First document the concrete storage design, threat model, dependencies and migration
failure cases, then implement that design within this task's scope.

Keep one Go-owned SQLite database, the current repositories/schema, and the shared
FTS/vector pipeline. Select a maintained encrypted-SQLite solution supported by
the target platform after verifying its primary documentation, license, FTS5,
foreign-key/WAL behavior and packaging requirements. Pin and justify any necessary
driver/toolchain dependency change. Do not invent cryptography/a VFS or encrypt
only selected fields while leaving mail, indexes, journals or sidecars plaintext.
Do not change the embedding runtime or add an unrelated service/database.

Generate a random per-profile database key and store it only in OS credential
storage, with no file/SQL/argument/environment/frontend/log fallback. Missing,
denied, wrong or corrupt keys must fail closed without replacing an existing
database or silently creating a fresh profile. Define genuine first-use behavior
separately. Account tokens and AI keys remain separate credentials. Explain that
at-rest encryption does not protect against an unlocked compromised user process.

Implement a bounded recoverable migration of existing plaintext caches using
synthetic fixtures first. Preserve source mail, checksummed migration history,
account foreign keys, original journal bodies/codecs and all current derived-data
compatibility. Handle interruption, disk-full, wrong-key and restart cases. Do not
rewrite numbered migrations, hold transactions over keyring calls, or run migration
against a user's real cache during automated validation. Account for leftover old
plaintext files/backups explicitly; do not promise automatic forensic erasure or
delete user copies without an explicit user action.

Add a narrowly typed, account-scoped cache-clear preview/confirmation if it can
preserve recovery contracts. Cancel/join workers, revoke previews/AI approvals,
fence stale publication, and transactionally invalidate/remove authorized cached
mail and all dependent chunks/vectors/FTS/examples/assessments/checkpoints as
appropriate. Prevent late workers from recreating cleared data. Preserve immutable
recovery evidence and disclose what it retains. Do not label this "delete all data"
when journals or keys remain. It must not delete Gmail mail or silently remove keys.
A full-profile deletion policy that destroys journal evidence is outside this
prompt and needs an explicit change to the repository's retention contract.

Acceptance: real target-native tests verify encrypted main/WAL/recovery files,
successful reopen/search/indexing, key failure without data loss, interrupted
migration, cache-clear cancellation/publication fences and account isolation.
Package/sign/offline tests must exercise the actual native runtime and encryption
driver, not an unavailable stub. Update privacy documentation only to protections
demonstrated by those tests. Report retained plaintext history and unresolved risks.
```

## Prompt 7 — Verify and prepare the résumé-ready release candidate

```text
Prepare a reviewable MailPilot portfolio release candidate from the completed
earlier prompts. This does not authorize publishing, uploading artifacts, creating
tags/releases or contacting anyone. Preserve unrelated work and do not change
product scope to make checks pass.

Run the relevant Go tests/vet/race, frontend typecheck/lint/tests/build, Rust
fmt/clippy/tests and target-native lifecycle/rendering/package checks using the
normal repository commands. Run localhost fixtures in an environment that permits
them. Isolate resource-heavy tests when justified, explain retries, and never
conceal failures with a temporary source-exclusion overlay. Restore dependencies
from lockfiles rather than relying on a one-off hidden validation installation.

Verify from a clean supported environment: credential-free demo; documented Gmail
JSON development/personal-build routes; Finder launch without shell exports; no
OpenAI key needed for local features; optional AI key entry/clearing/removal; bounded
source consent; unavailable embeddings; cancellation/restart; and retained-data
disclosures. Use synthetic client/key canaries and fake providers for automated
checks. Real Gmail sign-in, mailbox writes and paid AI calls need their own explicit
authorization. Do not publish a credential-personalized bundle as a general download.

For embedding or packaging changes, exercise the real pinned native resources,
their notices/integrity checks, signed/offline inference and installed resource
paths. A CGO-disabled smoke pass does not qualify native inference. Distinguish
ad-hoc developer signing from Developer ID distribution/notarization, and synthetic
evaluation from actual-mailbox/model quality. Do not tell users to disable platform
security protections to compensate for missing distribution qualification.

Create a short docs/RELEASE_CHECKLIST.md with exact reproduction commands, tested
revision/environment, passed/failed/blocked/unrun results and remaining release
gates. Review README screenshots and all tracked/staged files for sensitive data.
Check license/third-party notice completeness and identify any unresolved project
license choice; do not select a license for the owner without authorization.

Finish with a concise demonstration script and a truthful résumé bullet grounded
in implemented features and measured results. The demonstration should take only
a few minutes and require no real credentials. Do not claim Google approval,
consumer-release readiness, encrypted storage, accuracy or speed not established
by this candidate's evidence. Leave publishing as a separate explicit action.
```

## Authoritative setup references

These references were checked when preparing this plan. Recheck them when writing
the actual setup guides; console labels, billing and access can change.

- [Google desktop OAuth](https://developers.google.com/identity/protocols/oauth2/native-app):
  desktop public-client credentials, system-browser authorization, PKCE and loopback.
- [Google testing audience](https://support.google.com/cloud/answer/15549945?hl=en):
  test-user enrollment and testing-mode authorization/refresh-token expiry.
- [OpenAI developer quickstart](https://developers.openai.com/api/docs/quickstart):
  API account/key setup; adapt the generic instructions to MailPilot's OS key store.
- [OpenAI API authentication](https://developers.openai.com/api/reference/overview):
  API-key confidentiality; never ship a developer's shared key in a desktop app.
- [OpenAI API pricing](https://developers.openai.com/api/docs/pricing):
  current usage charges; do not hard-code prices or promise model access/free credit.

## Definition of done

The résumé-ready result has a short credential-free demo, professional real images
and diagrams, accurate detailed documentation, a repository that builds normally,
and simple user-owned Google/OpenAI instructions. Real-mail protection is judged
against the explicit security findings and completed controls, not the appearance
of the README. Personal client-config packaging never includes user tokens or AI
keys and never substitutes for Google's public-distribution requirements.
