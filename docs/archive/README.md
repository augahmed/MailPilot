# Historical MailPilot records

These documents preserve earlier decisions and test evidence. They are not current
feature descriptions or implementation instructions. Use the
[documentation index](../README.md), [product scope](../PRODUCT_PLAN.md) and
[architecture](../ARCHITECTURE.md) for the current app.

| Record                                                                     | Historical context                                                                                     |
| -------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------ |
| [Legacy features and compatibility](LEGACY_FEATURES.md)                    | Consolidated retirement history; retained formats and negative-test fixtures                           |
| [2026-10-16 contraction plan](CONTRACTION_2026-10-16.md)                   | Earlier removal of the original Ask/search/drafting systems                                            |
| [Retired semantic-classification note](SEMANTIC_CLASSIFICATION_RETIRED.md) | Superseded pre-shared-pipeline description; not the current native classifier                          |
| [2026-10-21 UI review](UI_REVIEW_2026-10-21.md)                            | Earlier screen behavior and dated measurements, superseded by the [2026-10-22 review](../UI_REVIEW.md) |

The contraction snapshot contains names and paths for files subsequently removed.
Those recorded paths are historical evidence, not promises of current files or
APIs. Existing migrations, journal formats and compatibility fixtures remain intact.

## Repository consolidation on 2026-10-26

Eight ` 2` files were byte-identical duplicates of canonical Go/TypeScript source
or JSON fixtures. Two older test copies repeated coverage retained by their
canonical files: the Gmail flow keeps both original rule/manual cases plus newer
source/label/freshness cases, and the organization IPC test keeps the original
assertions plus conflict/suggestion coverage. Only the redundant copies were
removed. No canonical production implementation or fixture payload was changed.

The two superseded documentation copies were moved here rather than discarded.
Their dates, measurements and limitations remain historical. Six tracked iCloud
placeholder files were already absent at cleanup start; their hydrated source
copies were compared with canonical files before consolidation. Generated npm
dependencies were restored from the existing lockfile, without version upgrades.
Missing fixed model/runtime build resources were restored through the existing
checksummed preparation script and remain ignored generated artifacts.

Validation on Apple Silicon macOS used the ordinary working tree, with no temporary
source overlay or separate dependency installation:

- Go build/vet and `go test -race -p=1 -timeout=8m ./...` passed.
- Frontend typecheck, lint, all 173 tests and the production build passed.
- Rust fmt, locked strict Clippy and all 25 tests passed.
- `npm run build -- --bundles app` produced the desktop application. The documented
  local ad-hoc signing step then passed strict signature verification and the
  network-denied native inference package check, including copied debug/release
  resource layouts. The desktop WebView/IPC lifecycle smoke check passed.
- The two embedding artifact tests, documentation path/heading checks,
  changed-document formatting and `git diff --check` passed.

An initial offline resource check failed because model bytes were missing; the
existing connected preparation step restored and verified them. An initial package
check failed before local ad-hoc signing; it passed after the documented signing
step. Developer ID signing/notarization, Windows/Linux qualification, live mailbox
operations and paid AI calls were not run. Prior security findings remain recorded
in [the evaluation](../EVALUATION.md); this consolidation is not a security audit
or public-release approval.
