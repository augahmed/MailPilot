# MailPilot quality and security evaluation

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
