# Frontend usability review

Reviewed 2026-10-21. This review covers presentation, navigation and existing
workflows. Organization batches now accept 25 emails. Gmail operation types,
credential storage, consent requirements and dependency versions remain unchanged.

## Corrections

- The reader now uses the shared typography, borders, button variants and risk
  badges. Metadata and long subjects/addresses wrap inside the content width.
- Opening a message aligns the reader after its body renders, keeping its return
  action visible. Returning restores list scroll position and keyboard focus.
- Explicitly reloading a stale search result reads the latest cached original.
  The previous excerpt remains hidden unless its revision matches that original.
  Continuation reads still use the current reader revision.
- Successful retries clear old reader-pagination, AI settings and Summary errors.
- Empty status elements no longer leave blank colored bars. Feature card headings
  distinguish the page title from the section purpose; result counts use singular
  and plural wording appropriately.
- Native selects match text-input heights. Filter legends retain their readable
  width in WebKit. Short windows can scroll the account panel, and long modal
  content retains its confirmation actions.
- Email tables are keyboard-focusable scroll regions. Positioned screen-reader
  text stays inside their scroll container, fixing page overflow in WebKit.

## Coverage

All listed screens were inspected with synthetic mail at 360×520, 760×640 and
1280×800 CSS pixels. The supplemental native WebKit review loaded the production
JavaScript/CSS bundle in an isolated WKWebView with fixture-only IPC responses.
It exercised connect/disconnect navigation, feature navigation, reader return
focus/scroll, category expansion, search results, question previews and native
confirmation cancellation. It did not contact Gmail or an AI provider.

| Screen                      | Review focus                                                                        |
| --------------------------- | ----------------------------------------------------------------------------------- |
| Gmail landing               | Loading/error copy, connection gate, branding and narrow layout                     |
| Dashboard                   | Card layout, long sender/subject text, cached list and textual risk levels/scores   |
| Reader                      | Escaped body, wrapping metadata, return action, stale reload and paging recovery    |
| Local Email Index           | Status, coverage counts, partial progress and explicit controls                     |
| Email Search                | Input/filter layout, all category options, results and original-source navigation   |
| Ask About Email             | Provider settings, question/filters, selected sources and separate approval         |
| Categories & Classification | Accordions, local editing/actions, review cards and evidence                        |
| Gmail Organization          | Selection, previews, expiry, confirmation, uncertain outcomes and recovery          |
| Account dropdown            | Position, long address wrapping, short-window scrolling and disconnect confirmation |

The final fixture review found no page-level horizontal overflow. Wide tables
remain locally scrollable. Primary text, muted text, buttons and the three risk
badge text/background pairs were checked from their CSS colors; the checked
small-text pairs meet at least 4.5:1 contrast. This is not a full accessibility
audit. The existing light-only theme remains in place.

Local synthetic screenshots and layout measurements are ignored artifacts in
`.cache/ui-review/`. The fixture harness was session-local; it does not ship in the
application or bypass production authentication. The separate packaged Tauri
smoke test uses the real WebView → Rust → Go → SQLite path with an isolated,
unconfigured profile. It checks startup/shutdown, the landing gate and existing
unauthorized feature boundaries, rather than live connected-account workflows.

## Classification and organization follow-up

- The classification reset is **Reset To Default**. It is disabled and lightly
  blurred when no category is selected or assigned. A successful reset also
  clears the category selection in the form.
- Organization displays up to 25 emails per page and accepts up to 25 selected
  emails per confirmed batch. Go, Rust, TypeScript and SQLite plan validators
  agree on 25 emails and up to 50 operations. No migrations or historical
  journal/digest formats were changed.
- The category filter loads every page of the actual enabled category source,
  includes Unclassified, and scans later cached-email pages for matching mail.
  Filtered pagination retains a raw-list cursor so matches are not skipped.
  Sorting applies to the current display page. Small Select all and Reset
  controls appear directly below the filters. Select all selects eligible emails
  on this page; Reset clears selection, category filtering and sorting.
- **Preview label changes** now prepares a saved preview directly and shows only
  selected emails in **3. Confirm and apply changes**. Applying uses the existing
  exact saved action/digest, confirmation checkbox and native confirmation dialog.
  Optional **Dry Run only** remains available as a separate read-only check.
- Organization requests normalize undefined optional values before native IPC
  serialization so required Rust fields remain present. Restored Dry Runs recover
  their selected IDs, and expired previews allow a fresh selection.
- Results separately show current cached Gmail labels after verified writes.
  Before-preview label snapshots remain immutable. Cache-refresh failures remain
  visible; optional label-display enrichment cannot hide a valid recovery record
  by overflowing the IPC frame.
- Stable table columns keep category and label text readable. The list scrolls
  locally in both directions, and the selection toolbar remains within reach.
- A synthetic HTTP integration test exercises the real Gmail adapter, SQLite
  cache, saved preview, 25 verified additions and 25 separately confirmed undo
  removals for both rule suggestions and manual primary categories. It preserves Inbox membership and makes no live Gmail requests.

## Validation

- Frontend typecheck, lint and production build passed; all 150 frontend tests
  across 28 files passed, including request serialization and UI recovery cases.
- Native WebKit fixture review passed all 42 screen/size cases at the three
  viewport sizes above, including dialog cancellation and reader scroll/focus.
  The follow-up organization fixture also checks a 25-email list, category
  filtering/sorting, selection reset, selected-only confirmation and required
  request fields at each size.
- The macOS Tauri application built successfully, was locally ad-hoc signed and
  passed strict signature verification. Its isolated packaged desktop smoke test
  passed the real IPC/authorization checks and shut down the Go core cleanly.
- Rust formatting and strict offline Clippy passed; all 22 Rust tests passed.
- Relevant Go application, IPC, SQLite and Gmail provider tests, vet and race
  checks passed, including the synthetic HTTP integration.
- Changed-file formatting and `git diff --check` passed. No dependency, lockfile, migration or Gmail operation types were changed.
  The explicitly requested backend batch bound is now 25; existing write safety
  checks remain in place.

## Final manual testing

The UI is ready for final manual testing. No unresolved presentation issues were
found in the reviewed synthetic cases.
Remaining checks are live system-browser OAuth/keyring prompts, a user's display
scaling and input preferences, VoiceOver reading order/announcements, and
connected-account workflows in the packaged app. No live Gmail writes or paid AI
requests were performed during this review. Cold-start offline authorization
remains the existing backend limitation documented in [ARCHITECTURE.md](ARCHITECTURE.md).

Use the rebuilt app to check:

1. Tab through the landing page and account controls; verify visible focus and
   Escape/cancel behavior. Restore a saved session and check connection errors.
2. Resize the desktop window, scroll the dashboard and open a long email. Return
   to the same list position and verify local table scrolling with the keyboard.
3. Visit each feature and return without losing inputs or selections. Check the
   category accordions and review evidence using the keyboard and VoiceOver.
4. Inspect an AI preview and a Gmail label preview, then cancel. Verify that the
   exact selected evidence/labels and approval requirements remain clear.
5. Review partial or uncertain recorded outcomes and recovery controls. Actual
   Gmail apply/undo or external AI dispatch requires separately authorized manual
   testing; a UI inspection alone does not authorize those operations.
