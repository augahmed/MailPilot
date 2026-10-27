# MailPilot quality and security evaluation

The [2026-10-26 security follow-up](#security-follow-up-2026-10-26) below supersedes
the dependency dispositions from the original run. Historical quality/performance
measurements and test counts remain dated evidence, not current release approval.

Evaluated 2026-10-21 against baseline
`3c839493e440e71c6d654b2fe003837f82aa18b2` and the measured search-allocation changes
described in [PERFORMANCE.md](PERFORMANCE.md). All mail is synthetic. No live Gmail
data was changed, paid provider request made, or artifact published.

## Scope and methods

The evaluation combines the generated 10,000-message corpus, real offline MiniLM
inference, frozen held-out retrieval/classification fixtures, existing Go/Rust/
frontend regressions, source review and signed packaged native smoke tests.
[PERFORMANCE.md](PERFORMANCE.md) records hardware, reproducible commands, corpus
hash, lengths, thread/category distributions and timing definitions.

Safety claims apply to tested application boundaries, not resistance to a fully
compromised OS/user account or arbitrary malicious model behavior. Paid-model
factual/adversarial evaluation, production-mailbox judgments, power-loss testing
and distribution signing/notarization are outside this run.
Dependency manifests/checksums were preserved; no replacement dependency was added.

## Dependency advisory audit

This subsection records the **2026-10-21** results. See the follow-up for current
versions, new findings and patch verification.

`npm audit --json` reports zero known advisories across its 273-dependency inventory
on the evaluation date. The official Go `govulncheck` v1.9.0 source/symbol scan
reports one distinct reachable advisory:
[GO-2026-6809 / CVE-2026-111943](https://pkg.go.dev/vuln/GO-2026-6809), concerning
compromised intermediate certificate distrust in `crypto/x509`. The pinned build
uses Go 1.27.2; the advisory lists Go 1.27.3 as fixed on that release branch.
The advisory was published on 2026-10-21; the scanned database timestamp is
2026-10-21T00:27:29Z. Its module/package/symbol records are three reports of the
same issue, not three vulnerabilities. The source scan establishes potential
reachability, not exploitation on this Mac's particular certificate trust store.

This remains an open build-security finding. A separately reviewed toolchain patch
and rebuilt/revalidated artifacts are needed before treating this build as clear
of the reported Go advisory. The evaluation does not alter the pinned toolchain,
module checksums or model/preprocessing identity. Runtime indexing itself remains
offline and does not exercise TLS.

RustSec `cargo-audit` 0.22.2 scans all 445 lockfile dependencies and reports zero
vulnerability entries, four unmaintained notices (`proc-macro-error` 1.0.4,
`zbus_names` 4.3.4, `zvariant` 5.15.0 and `zvariant_derive` 5.15.0), and one unsoundness
warning: [`glib` 0.18.5 / RUSTSEC-2024-0429](https://rustsec.org/advisories/RUSTSEC-2024-0429.html).
The latter concerns `VariantStrIter` and can cause undefined behavior/crashes.
The all-target dependency tree places it under the GTK/WebKit Linux path; it is
absent from the `aarch64-apple-darwin` tree. This limits its relevance to this native
run, not to hypothetical Linux builds. These five warnings are retained, not
ignored or represented as a completely clear audit. The advisory database commit
is `4b587f00996ebe5c05a73eaef105ef241e876414` (updated 2026-10-21).

Audit tools are pinned temporary evaluation tools under ignored
`.cache/evaluation/tools`; they do not become application dependencies. They fetch
public advisory/package metadata without sending mail or credentials. Reproduce:

```sh
npm audit --json
GOBIN="$PWD/.cache/evaluation/tools/bin" go install golang.org/x/vuln/cmd/govulncheck@v1.9.0
cd backend
../.cache/evaluation/tools/bin/govulncheck -json ./...
../.cache/evaluation/tools/bin/govulncheck -json ./cmd/mailpilot-core
cd ..
cargo install --locked --root .cache/evaluation/tools --version 0.22.2 --jobs 2 cargo-audit
.cache/evaluation/tools/bin/cargo-audit audit --file src-tauri/Cargo.lock \
  --json --db .cache/evaluation/rustsec
```

Advisory databases are time-dependent, so later runs may differ. These ecosystem
scanners do not establish the absence of vulnerabilities in the bundled native
ONNX library, system frameworks or application logic.

## Retrieval quality

The existing frozen
[`hybrid-heldout-v1.json`](../backend/internal/retrieval/testdata/hybrid-heldout-v1.json)
contains 73 messages: 72 searched messages plus one foreign-account duplicate,
145 indexed chunks, 24 queries, 22 answerable and two unrelated. Judgments and
policies were not edited. Macro Recall@10 averages the fraction of relevant emails
retrieved per answerable query; nDCG uses binary judgments and logarithmic discount;
MRR uses the first relevant email. Unrelated queries are excluded from those means
and reported separately. Each query has five warm timing repetitions.

| Mode     | Recall@10 | nDCG@10 | MRR@10 | Exact top-one misses / eight | Empty unrelated results / two |
| -------- | --------: | ------: | -----: | ---------------------------: | ----------------------------: |
| Keyword  |    0.8182 |  0.7631 | 0.7470 |                            0 |                             1 |
| Semantic |    0.9091 |  0.8755 | 0.8636 |                            4 |                             0 |
| Hybrid   |    0.9545 |  0.8872 | 0.8652 |                            0 |                             0 |

Hybrid improves recall and exact identifiers on this small synthetic fixture, but
still returns candidates for unrelated queries. Similarity/rank is not a calibrated
relevance probability, an answerability guarantee or exhaustive mailbox evidence.
The 10,000-message corpus separately measures twelve thread-reference queries,
three repetitions per mode. Those deliberately test identifier/thread recovery;
they do not substitute for semantic paraphrase or real-mailbox quality.

## Category precision, recall and abstention

The frozen
[`semantic-eval-v1.json`](../backend/internal/classification/testdata/semantic-eval-v1.json)
has six confirmed examples, 45 original tuning cases and 45 original held-out cases.
Forwarded wrappers derive another 45 cases from each split: reported split counts
of 90 therefore include paired wrappers, not 90 independent held-out messages.
Neither thresholds nor category descriptions were tuned during this evaluation.

Per-suggestion precision is correct suggested labels divided by all suggested
labels. Recall is correct expected labels divided by labeled support; abstention
is messages with no suggestions divided by all messages, including the eight
unclassified held-out cases. Multiple suggestions can include a correct label and
still incur false-positive labels. A confusion matrix is preserved in the ignored
qualification JSON. Semantic-only and combined-rule variants had identical metrics
on these original held-out cases.

| Original held-out scenario | Correct labels / suggestions | Micro precision | Micro recall over 37 labeled cases | Abstention / 45 |
| -------------------------- | ---------------------------: | --------------: | ---------------------------------: | --------------: |
| Definitions only           |                      34 / 56 |          60.71% |                             91.89% |               0 |
| Six confirmed examples     |                      35 / 53 |          66.04% |                             94.59% |               0 |

| Category    | Support | Definition precision / recall | Confirmed-example precision / recall |
| ----------- | ------: | ----------------------------: | -----------------------------------: |
| Jobs        |       6 |                 66.67% / 100% |                      44.44% / 66.67% |
| School      |       6 |                 42.86% / 100% |                           60% / 100% |
| Receipts    |       6 |                    60% / 100% |                        85.71% / 100% |
| Finance     |       6 |               83.33% / 83.33% |                           75% / 100% |
| Newsletters |       6 |               62.50% / 83.33% |                        66.67% / 100% |
| Personal    |       7 |               66.67% / 85.71% |                           70% / 100% |

Forwarded-definition precision is 57.63%; forwarded-confirmed precision is 61.02%,
both with zero abstention. The proposed 90% precision target is unmet. In particular,
the classifier does not abstain on the eight negative original held-out cases.
Confirmed examples can worsen an individual category, as Jobs demonstrates.
These results support local reviewable suggestions, not automatic assignment or
Gmail writes. Classification uses existing chunk vectors; the native evaluation
observes zero extra mail embeddings during classification.

The generated 10,000-message template corpus reports its own confusion matrix,
per-category precision/recall and abstention in `corpus-classification.json`.
Its per-suggestion precision is **76.41%** (8,998 correct / 11,776 suggestions),
micro recall is 100% over 8,998 labeled messages, and abstention is **0%**. All
1,002 expected-unclassified messages receive suggestions. There are 1,684 messages
with multiple suggestions and 7,325 with exactly one correct suggestion.

| Generated-corpus category | Support | Precision | Recall | False-positive suggestions |
| ------------------------- | ------: | --------: | -----: | -------------------------: |
| Jobs                      |     999 |      100% |   100% |                          0 |
| School                    |   1,003 |    37.44% |   100% |                      1,676 |
| Receipts                  |   2,004 |    64.69% |   100% |                      1,094 |
| Finance                   |     998 |      100% |   100% |                          0 |
| Newsletters               |   1,998 |      100% |   100% |                          0 |
| Personal                  |   1,996 |    99.60% |   100% |                          8 |

Most School false positives are Personal messages; every unclassified case is
suggested as Receipts. Even this simpler template corpus misses the 90% precision
target and shows inadequate abstention. Its repeated wording makes it a scale
regression, not evidence that the harder held-out problem is solved. The 12
large-corpus thread-reference queries score Recall@10 100% for keyword/hybrid and
0% for semantic-only; [PERFORMANCE.md](PERFORMANCE.md) records the actual rankings'
metric definitions and latency. These identifier judgments do not replace the
independent paraphrase fixture above.

## Security and lifecycle verification

| Requirement                                    | Evidence and practical limit                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      |
| ---------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Source-to-vector consistency                   | Full-corpus audit rebuilds every deterministic snapshot and compares persisted passage/context, source revision, offsets and preprocessing version, then validates every 384-dimensional saved vector. Existing source-change, model/preprocessing ABA, tombstone, purge and stale-worker tests verify invalidation. This verifies provenance/compatibility, not semantic correctness of each vector.                                                                                                                                                                                                             |
| Account isolation                              | A foreign duplicate remains unindexed during the corpus run; unauthorized account controls reject. Native held-out searches reject foreign results. Repository, reader, category, consent and IPC suites exercise wrong-account and generation fences.                                                                                                                                                                                                                                                                                                                                                            |
| No external transmission during local indexing | Native index/search/category/classification/answer fixtures and the corpus run execute with OS-level `deny network*`. Indexing has no Gmail, credential or generative-provider instance. The provider explicitly disables ORT telemetry; no downloader or external embedding fallback is in its runtime path. Successful offline runs prove local operation; the installed application is not globally network-denied because explicit Gmail/approved AI features require network.                                                                                                                                |
| No AI request without approval                 | `TestExactSingleUseApproval`, `TestSummaryOnlySelectedCachedEmailAndSingleUseConsent`, `TestAnswerExactSelectedEvidenceAndOneUseConsent` and invalidation tests count zero fake-provider calls before approval, reject wrong account/payload/replay/expiry and revoke on source, session, key and settings changes. Selected-email Summary consent cannot approve evidence answers. Native answer smoke makes one explicitly approved synthetic-adapter call and zero paid calls.                                                                                                                                 |
| Prompt injection containment                   | `TestEvidenceAnswerValidationAndPromptInjectionBoundary`, strict IPC tests and OpenAI wire tests preserve malicious evidence as untrusted quoted JSON, supply the fixed policy separately, expose no tools/Gmail/search/action privileges and reject invented source IDs/quotes, uncited output and extra output fields. React escapes evidence/citations; CSP excludes arbitrary network/script/frame actions. No real generative model was exercised. A provider can still emit a false or manipulated statement alongside a valid quote: provenance is not entailment or complete prompt-injection resistance. |
| Gmail journaling/recovery                      | `TestOrganizationDryRunSelectionAndPersistence` inspects durable in-flight journal state from the fake provider's write callback. Uncertain-dispatch, cancellation-after-dispatch, fresh-instance reconciliation, pre-existing labels and undo tests verify journal-before-dispatch, no blind retry and preservation of unrelated labels. Historical recovery tests prohibit draft creation/sending/deletion. Only synthetic providers/loopback servers are used.                                                                                                                                                 |
| Unavailable model fallback                     | `TestLocalIndexUnavailableAndInvalidVectorFailClosed`, search cancellation/unavailability tests, hybrid and answer fixtures verify safe pending/error states, keyword retrieval and disclosed `semantic_unavailable` fallback. Rule fallback and ordinary reading remain usable; no cloud inference fallback or automatic indexing is introduced.                                                                                                                                                                                                                                                                 |
| Correct packaged model                         | Pin/hash/tamper/signature tests plus real batch/single parity, unit norms, relevance and maximum-input inference. Signed app and copied debug/release sidecars run offline from fixed resources. Missing/corrupt/unsupported resources fail closed. macOS 13.4+ Apple Silicon only; local ad-hoc signing is not distribution qualification.                                                                                                                                                                                                                                                                       |
| Credentials and transport                      | Existing keyring scope/failure/redaction, OAuth PKCE/state/loopback/refresh/cancellation, bounded frames/strict allowlists and adapter timeout/redirect/error tests pass. Native keyring smoke creates/removes one synthetic temporary-profile credential. Reader/HTML extraction remains bounded escaped text without remote content or attachment execution.                                                                                                                                                                                                                                                    |

SQLite/WAL and backups remain unencrypted sensitive local files. Interrupted source
generations may retain private reuse-cache vectors until replacement; no forensic
deletion claim follows. Cancellation after an approved external request or Gmail
dispatch can remain uncertain; synthetic tests require reconciliation rather than
claiming the remote request was undone. Inference construction is not preemptible.

## Verification commands and outcomes

Run from the repository root unless indicated. Use cached dependencies/resources;
Go tests use synthetic transports/loopback servers, and native fixture scripts deny
network. The native keyring test is deliberately isolated from mock-keyring tests.

```sh
cd backend
go test -count=1 ./...
go vet ./...
GOMAXPROCS=2 go test -race -p 1 -count=1 ./...
GOEXPERIMENT=cgocheck2 go test -race -count=1 -tags embedding_native ./internal/embedding/...
go test -count=1 -tags keyring_integration -run '^TestNativeKeyring$' ./internal/credentials
cd ..
npm run typecheck
npm run lint
npm test
npm run build:frontend
npm run test:embedding:assets
PATH="$PATH:$HOME/.cargo/bin" cargo fmt --manifest-path src-tauri/Cargo.toml -- --check
PATH="$PATH:$HOME/.cargo/bin" cargo clippy --offline --manifest-path src-tauri/Cargo.toml --all-targets -- -D warnings
PATH="$PATH:$HOME/.cargo/bin" cargo test --offline --manifest-path src-tauri/Cargo.toml
npm run test:embedding:package
npm run test:desktop
npm run test:indexing:native
npm run test:search:native
npm run test:search:hybrid
npm run test:categories:native
npm run test:classification:native
npm run test:answers:native
npm run format:check
```

Initial restricted-sandbox attempts could not access Go's build cache, bind test
listeners, reach the window server or nest `sandbox-exec`; those checks were rerun
with the necessary local test permissions. Parallel and serialized race attempts
under competing index/build/audit load exceeded an existing 12-second index-test
wait. A final quiet run with serialized packages and `GOMAXPROCS=2` passed the
entire final-source race suite, including the expanded 35-message cancellation
regression. No data race was reported and no test/application timeout or safety
bound was relaxed. Initial failure logs remain under `.cache/evaluation/`.

| Check                                                          | Final outcome                                                                                                        |
| -------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------- |
| Full Go tests                                                  | Passed: 19 tested packages, 726 passing test/subtest events, no skips/failures                                       |
| Go vet, including the native harness                           | Passed                                                                                                               |
| Full Go race suite, quiet serialized run                       | Passed                                                                                                               |
| Real native embedding race/CGO checks                          | Passed                                                                                                               |
| CGO-disabled embedding/application suites                      | Passed                                                                                                               |
| Isolated native OS-keyring round trip/cleanup                  | Passed                                                                                                               |
| Frontend typecheck/lint/test/build                             | Passed: 101 tests across 25 files                                                                                    |
| Rust fmt/clippy with warnings denied/test                      | Passed: 21 tests                                                                                                     |
| Node embedding artifact tests                                  | Passed: two tests                                                                                                    |
| Final app build/ad-hoc signature/offline packaged model        | Passed, including copied debug/release layouts                                                                       |
| All eight existing native smoke scripts                        | Passed on final packaged/source artifacts                                                                            |
| Full synthetic corpus with explicit checkpoint resume          | Passed: 10,000 messages, 39,517 audited vectors, 10,000 assessments, no re-embedding on replay/restart               |
| Numeric source-to-vector regeneration sample                   | Passed: 512/512 match, maximum absolute difference zero                                                              |
| Reproducible corpus under a different timezone                 | Passed: frozen UTC JSONL hash and 10,000-message normalization parity                                                |
| Formatting, syntax, local documentation paths, diff whitespace | Passed                                                                                                               |
| Advisory scans                                                 | Go finding remains open; npm has zero advisories; RustSec has zero vulnerability entries plus five retained warnings |
| Quality/performance targets                                    | Category precision/abstention and dense semantic/hybrid latency remain below qualification targets                   |

The baseline corpus run was intentionally stopped after the measured assessment
bottleneck was identified. Its real vectors were retained with SQLite's consistent
backup and the optimized evaluation explicitly resumed them, completing all
audits/quality/replay/restart checks. It is not recorded as an uninterrupted fresh
final-source run; timing denominators are explicit in [PERFORMANCE.md](PERFORMANCE.md).
No functional regression check remains failed or blocked. The Go advisory and
published quality/latency shortfalls remain open findings, not test passes.
Paid-model answer quality, live Gmail behavior, non-Apple-Silicon inference and
release distribution remain unqualified.

## Security follow-up 2026-10-26

Portfolio prompt 2 reviewed the current source and the earlier findings. All mail,
key stores, provider transports and OAuth flows used for regression testing were
synthetic/mocked. Public registry/advisory and repository-settings metadata were
queried; no live Gmail account, real OS credential item or paid AI service was
used. This follow-up does not repeat the 10,000-message performance evaluation or
change its published quality/latency shortfalls.

### Application boundary review

The concrete gap was Summary's saved-key binding: settings changes through the
app already revoked previews, but replacement outside that path could leave an
old approval usable with a newly loaded OS key. Summary now stores a fingerprint
with the immutable backend-only request, checks settings/key/source before
approval and execution, rechecks before result publication and status display,
and verifies the actual loaded key at the adapter's HTTP dispatch boundary.
Generic request formatting/JSON are redacted. The existing evidence-answer path
keeps its separate consent and key checks; no IPC fields or database migrations
were added. Regression tests reject replacement/missing bindings without a
transport call, suppress results after replacement during/after execution, and
allow a new preview to use the replacement key.

Reviewed existing controls remain in place:

- SQLite directory/file permission repair, regular-file/link rejection, live
  WAL/SHM permissions, per-connection foreign keys, memory temporaries and
  `secure_delete` are covered by the storage regressions. The format and migrations
  are unchanged; these are not encryption or forensic-deletion guarantees.
- OAuth PKCE/state, temporary loopback callbacks, serialized refresh, disconnect
  invalidation, OS-only credential storage, fail-closed errors and safe diagnostic
  codes retain their synthetic regression coverage.
- API-key inputs clear after submission; failed deletion remains retryable while
  AI is disabled. Provider keys remain separate from the Gmail connection.
- Mail/model text is escaped; remote images and attachments are not executed or
  loaded. Typed IPC rejects extra fields and retired commands. Summary and answers
  require separate exact-request approval. Local features remain available with
  external AI disabled; no live provider call was used to establish this.

### Dependency dispositions

Only the following patch versions and their lockfile/checksum entries changed:

| Component                      | Before → after  | Finding and disposition                                                                                                                                                                         |
| ------------------------------ | --------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Go minimum/build               | 1.27.2 → 1.27.3 | GO-2026-6809, `crypto/x509`: initial source scan emitted the finding; final source and rebuilt packaged executable scans emitted no findings. The binary's build metadata identifies Go 1.27.3. |
| Zod, production validation     | 4.6.5 → 4.6.6   | GHSA-mrhx-c7xc-m3w5: oversized collection validation can consume excessive memory. Patched before relying on reachability arguments; IPC frame/schema bounds remain defense in depth.           |
| Undici, dev-only through jsdom | 7.30.0 → 7.30.1 | GHSA-4qv8-2p9j-2685, GHSA-f72v-6w45-q472 and GHSA-xjj6-f33q-929c. Not shipped as a Node runtime in the desktop app; patched for development/test tooling too.                                   |
| Tokio through Tauri            | 1.53.2 → 1.53.3 | RUSTSEC-2026-0386, unsound `AsyncFd` constructors: present in the Apple Silicon dependency tree; patched rather than assuming the affected call path is unused.                                 |

Patch evidence: [Go's 1.27.3 release history](https://go.dev/doc/devel/release#go1.27.3),
[Zod advisory/fixed version](https://github.com/advisories/GHSA-mrhx-c7xc-m3w5) and
[upstream fix](https://github.com/colinhacks/zod/commit/a0ebf970f2d752e5f58527cd2942ff48be4f46bd),
[Undici's upstream advisory](https://github.com/nodejs/undici/security/advisories/GHSA-xjj6-f33q-929c),
and [RustSec's Tokio advisory](https://rustsec.org/advisories/RUSTSEC-2026-0386.html).
No new direct dependency, general upgrade, model/tokenizer change or advisory
ignore rule was introduced. Go module checksums were unchanged.

The final npm scan has zero known advisories across the 273-dependency inventory.
Go source and packaged-binary scans used govulncheck 1.9.0 and the served database
timestamp `2026-10-21T00:27:29Z`. Read JSON `finding` messages, not only exit status:
the initial JSON-mode scan exited zero while reporting GO-2026-6809.

Cargo-audit 0.22.2 against RustSec commit
`a78282a1414d6c28cc228c4eae3570451affb0dc` (updated 2026-10-26) reports zero
vulnerability entries **and five retained warnings**: unmaintained
`proc-macro-error` 1.0.4 (RUSTSEC-2024-0370), `zbus_names` 4.3.4
(RUSTSEC-2026-0371), `zvariant` 5.15.0 (RUSTSEC-2026-0370), `zvariant_derive`
5.15.0 (RUSTSEC-2026-0372), and unsound `glib` 0.18.5 (RUSTSEC-2024-0429).
The GTK/WebKit Linux dependency path still includes glib; the locked
`aarch64-apple-darwin` tree does not. These are not silently ignored or resolved by
this Apple Silicon run. Linux remains unqualified; a compatible upstream stack
review is required before recommending that target. The unmaintained notices
remain maintenance risks. Scans do not cover all ONNX native code or OS frameworks
and do not prove absence of vulnerabilities.

### Repository material and reporting

The working tree was clean before this task. All 431 tracked files were inspected
with credential signatures, artifact-path checks and an address-domain inventory;
no suspected live credentials, private mail, databases, build/model artifacts or
screenshots were identified. The only tracked visual was the source SVG icon.
There were no staged files. This was a current-tree/index review, not a Git-history
or exhaustive secret-detection audit. No credential was rotated or history changed.
Machine-readable audit summaries remain in ignored
`.cache/security-review/2026-10-26/`; they are not application dependencies.

GitHub's read-only repository API confirmed Issues enabled and private vulnerability
reporting disabled. [SECURITY.md](../SECURITY.md) therefore gives a public
contact-request-only issue route, warns against posting sensitive details, and
does not invent a private email address or response promise. Repository settings
were not changed. [PRIVACY.md](PRIVACY.md) consolidates actual data handling and
retains unencrypted-cache, same-user/admin, disconnect-retention, separate AI-key,
backup/snapshot and approved-external-transmission limits.

### Verification and limits

Frontend typecheck/lint and production build passed with **173 tests**. Rust
formatting, Clippy with warnings denied and **25 tests** passed using the final
locked dependency graph. The Apple Silicon app was rebuilt with Go 1.27.3 and
Tokio 1.53.3, locally ad-hoc signed and verified. Real network-denied packaged
MiniLM inference passed for the signed app and copied debug/release layouts;
the two artifact-integrity tests, native CGO/race checks and native WebView/
Tauri/Go/SQLite authorization/lifecycle smoke test passed. The model descriptor,
tokenizer/preprocessing identity and pinned resource hashes remain unchanged.

The initial sandboxed full Go run stalled in synthetic loopback OAuth tests and
was interrupted; it was rerun with local-network permission. Final Go vet,
ordinary tests and the complete serialized race suite passed. Changed-file
formatting, local documentation paths/anchors and diff whitespace checks passed.
No required check remains failed or blocked for this prompt's scope.

No live-mailbox/paid-model evaluation, native keyring integration, Windows/Linux
runtime qualification, Developer ID signing, notarization or public OAuth
verification was run. No artifact was published. The remaining warnings and
earlier quality/latency findings prevent treating these checks as public-release
certification. Any unresolved reachable high-risk finding blocks a real-mail
release recommendation, even when the functional checks pass.

## Personal build qualification 2026-10-26

Portfolio prompt 3 added `dev:personal` and `build:personal` with an explicit
external Google Desktop JSON file. Node validates a bounded 32 KiB regular file,
the `installed` shape and required fields; wrong clients, token/key records,
unknown fields and conflicting endpoint metadata fail with fixed safe errors.
Only client ID/secret enter a generated, ignored mode-0600 Go build input. Go
validates the minimal packaged default again. No token store, endpoint, scope,
PKCE/state, IPC DTO, database schema, dependency or model identity changed.

Standard sidecar builds explicitly pass an empty build-tag selection, overriding
inherited `GOFLAGS` tags. Personal builds select `personal_google` and move the
finished macOS bundle intact to `.local/personal/MailPilot.app`. The configuration
is present before signing. Direct launch permits only atomic environment-pair
overrides; an explicit empty pair disables OAuth and partial pairs fail closed.
The keyring namespace remains client-ID/data-directory based.

Six Node tests passed, covering minimal extraction, bad/missing/malformed/
oversized/wrong-client input, endpoint enforcement, outside-repository paths,
symlink-to-repository rejection, CLI rejection before any build, cleanup after child
failure and preservation of another personal run's input. Go tests cover
packaged/default/environment precedence, malformed packaged input, safe errors,
redacted diagnostics and loading with no environment. Full Go tests and vet passed;
race checks passed for changed configuration/startup packages. Frontend
typecheck/lint/production build and 173 tests passed. Rust fmt, Clippy with warnings
denied and all 25 tests passed. Native embedding CGO/race and both artifact tests
passed. The affected-package race run does not replace or claim a new full Go
race run; the preceding security follow-up records the last full run.

`npm run test:personal:package` passed on Apple Silicon macOS. It built a personal
app with fixed synthetic canaries, verified the intended client fields in Go and
their absence from frontend assets/discarded project metadata, and launched with
both Google environment variables absent. A fresh temporary profile reached the
configured/disconnected landing page with Connect enabled; every protected
feature still rejected access. It read only that fresh synthetic profile's
nonexistent keyring slot, never a real account/token. Both personal and subsequent
standard apps passed ad-hoc signature verification, real network-denied MiniLM
inference (including copied debug/release layouts), and WebView/IPC/lifecycle smoke
checks. The standard rebuild deliberately retained the generated input and an
inherited personal tag; its executable contained neither client canary and showed
setup required. Temporary inputs and the synthetic personal bundle were removed.

The first personal desktop smoke failed because its navigation assertion required
the unconfigured landing page; the app had correctly loaded its default. The
corrected harness checks the configured/disconnected page and preserves all
authorization assertions. Initial and final local logs are retained under ignored
`.cache/security-review/personal-package*.log`. No unresolved check remains.

The real `dev:personal` command also started Vite, the native window and Go/SQLite
with an external synthetic client and temporary profile. Ctrl+C stopped the core
and dev server, and the generated input was removed. The shared sidecar was then
rebuilt in standard mode. No real Google sign-in, live mailbox change, paid AI
request, public artifact upload, Developer ID signing or notarization occurred.
Windows/Linux personal packaging remains unqualified. The earlier security and
quality warnings remain applicable; these helpers provide owner-only builds,
not Google verification or a new production API key. See [setup](GMAIL_SETUP.md).

## Synthetic demo qualification 2026-10-27

Portfolio prompt 4 adds `dev:demo`, `build:demo` and `demo:reset`. The separate
MailPilot Demo application identifier forces trusted demo startup; an ordinary
bundle can opt in with `--demo`. Neither IPC nor JavaScript selects a mode or data
path. Go derives one fixed `.demo-v1` profile, requires its marker, validates
synthetic-only account metadata and rejects demo data during ordinary startup.
The demo session has no Gmail-token authority. No credential-store, Gmail/sync,
external-generation or remote-recovery adapter is constructed in that branch.

Twelve fixed fictional messages reuse the current repositories, original cached
sources, `mail.Sections`/chunker, pinned WordPiece tokenizer and passage FTS.
Existing account triggers supply the current starter categories. Repeated startup
does not duplicate messages or overwrite local edits. Sample restoration only
reinstates known message content/metadata; category edits, compatible indexes and
immutable preview/recovery records remain intact. It is not a cache-purge feature.

Local indexing and category/classification work use the existing coordinator and
native provider. The store admits synthetic indexing only after successful demo
profile validation and only for the exact fixture account; arbitrary synthetic
accounts retain the prior rejection. Demo query policy requires a durable explicit
indexing run before semantic inference can initialize. Before that, Hybrid uses
the genuine keyword path with an unavailable warning, while Semantic reports
unavailable. No fixture contains vectors, similarity scores, inference timings or
generated answers. Normal search policy remains unchanged.

Organization reuses its existing preview/journal logic with a cached-snapshot
adapter whose write capabilities are false. The demo-only preview policy allows
local proposals but rejects apply, undo and reconciliation; adapter mutation
methods independently return unsupported. The additive auth `demo:true` response
and `demo_mode` rejection code drive the persistent banner and explicit disabled
external-service presentation. Existing production authentication checks remain.

Validation used synthetic data only. Go composition tests install a failing
credential-store mock and a transport that panics on network use. They cover three
starts including restoration, fixed-profile/account fences, foreign reads/search,
keyword retrieval without model resources, local previews, blocked external
actions and preservation of the previous immutable preview ID/digest. Additional
tests prove that unvalidated/unrelated synthetic accounts cannot index, failed
profile validation revokes admission, and demo queries cannot call the embedding
fake until explicit indexing. Frontend tests cover the banner and disabled
controls across navigation. Full Go tests/vet, affected-package race checks,
frontend typecheck/lint/build with **174 tests**, Rust fmt/Clippy with warnings
denied and **25 tests**, native CGO/race and both artifact tests were run.

The signed demo package passed actual startup with network access and ordinary
app-data/keychain paths denied, keyword search, real MiniLM indexing of all 12
messages, hybrid flight retrieval, local label preview and rejected writes/AI/
foreign reads. Its native WebView smoke checked the visible synthetic banner,
offline mailbox, navigation, absent API-key entry and rejected Google sign-in.
The source sample-restore command started Tauri/Vite/Go with the correct app
arguments and stopped cleanly with Ctrl+C. The ordinary bundle was also rebuilt
and its protected native startup and signed/offline model resources checked.

The first offline indexing check exposed the existing Gmail-only admission rule;
the fix is scoped to the validated demo profile and covered by negative tests.
The first source launch exposed Tauri's two runner/application argument separators;
the documented command was corrected and exercised successfully. Initial local
logs remain under ignored `.cache/demo-*.log`. Final checks are recorded there;
no required failure is treated as a pass.

After the demo checks passed, a genuine window-only synthetic dashboard capture
was saved to ignored `.cache/demo-dashboard.png` and visually reviewed. It includes
the synthetic identity and no real mail, keys or OS account information. The
walkthrough in [DEMO.md](DEMO.md) records reproducible reader/search/privacy states;
the professional README and committed visual assets remain prompt 5 work.

No schema/migration, dependency/lockfile, model/runtime hash, tokenizer identity,
preprocessing version or historical journal format changed. No live-mailbox or
paid-provider request, public upload, Developer ID signing or notarization was
performed. Previous security/quality limits remain; this is a local synthetic
portfolio route, not public OAuth verification or release certification.
