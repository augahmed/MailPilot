# Shared semantic email classification

Implemented 2026-10-20. The classifier reads current account-scoped shared email
chunk vectors and enabled category prototypes. It has no embedder, external AI,
mailbox writer, separate vector cache or background queue. Start/Resume on the
existing Local Email Index coordinator builds vectors/prototypes and then assesses
cached emails. Startup and polling never start work. The existing five-email
**Classify this page** action computes assessments from saved vectors without
inference. Missing/incompatible/incomplete vectors abstain or use decisive rules.

## Scoring and decisions

The versioned algorithm is `semantic-v3-close-category-suggestions`. It removes equal
passages and overlapping chunks within the same source section. Every remaining
passage contributes; the maximum chunk score alone never decides the category.
Within each structural group, average all its chunk-to-prototype cosine scores.
Normalize fixed total group weights over present groups: authored 1.0, subject
0.6, forwarded 0.25, quoted 0.05; signatures contribute zero. Additional chunks
share their group's fixed weight rather than adding votes. When there is no authored
body, forwarded content supplies the primary body evidence with weight 1.0 rather
than 0.25. With an authored reply present, forwarded history retains weight 0.25.
At least two distinct passages, including authored or forwarded content, are
required for a semantic suggestion. Subject-only, quoted-only and single-passage
messages still abstain. Forwarding without a covering note no longer discards all
semantic scores. The authored-word policy gate applies to the primary body group.
Subject context already follows the shared `mail.ModelInput` recipe.

Rank by aggregate cosine, then category ID for deterministic ties. Every category
with a score at least 0.20 and within 0.03 (inclusive) of the best score becomes a
suggestion. A lead greater than 0.03 produces one suggestion; a closer runner-up
produces multiple suggestions rather than an ambiguity abstention. Each suggested
category must independently clear the score floor. Comparisons tolerate only
1e-12 floating-point error at the boundary. The existing 100-category bound applies.
Missing/insufficient evidence, weak scores, explicit user abstention and conflicting
deterministic rules can still leave an email Unclassified. Cosine is never a
calibrated probability. When no scores were computed, review points to the abstention
reason and omits the score explanation. Previous algorithm assessments become stale
and are recomputed by Start/Resume or Classify this page; existing vectors are reused.

The policy is in [semantic_policy.json](../backend/internal/classification/semantic_policy.json).
Its dataset, model, tokenizer/preprocessing fingerprint and classifier identity are
pinned. A changed model/preprocessing profile cannot reuse old semantic thresholds;
it abstains until qualified. Classifier versions also bind the active profile and
rule configuration. The implementation reuses `retrieval.Cosine`.

Confirmed category examples contribute through the existing prototype recipe;
predictions and ordinary assignments never become examples. Stale/incomplete
example contributions are excluded by that recipe, with partial prototypes marked.
The optional rule-engine dependency can be omitted for semantic-only operation.
The packaged app retains decisive, unambiguous rules at the tuning-selected 0.60
rule-strength gate. Rule conflicts abstain. A rule score and a cosine score retain
different types; neither is presented as probability. The new classifier maps the
two historical job-rule names to the combined Jobs default without altering the
legacy rule engine, rule hashes or historical journals.

## Assignments and review

Review shows **Suggested**, **Assigned** and **Unclassified** separately. It displays
the three leading category scores plus all suggested categories (up to 100),
model/classifier versions and up to four supporting
chunk IDs/passages per category, with their cosine and normalized contribution
weight. All category scores and evidence IDs remain stored; visible excerpts are
read from shared chunks. Evidence opens the source-revision-bound cached reader.

Choose a category and **Assign primary category**, **Leave unclassified**, or
**Reset To Default** to change only the local primary preference.
Preferences override both rules and semantic suggestions. Existing single manual
assignments are honored; multiple legacy assignments remain preserved and do not
produce an arbitrary primary prediction. Clearing the new preference restores the
existing assignments rather than deleting them. These actions never confirm an
example or change Gmail. Manual choices work without embedding inference.

## Storage, freshness and Gmail compatibility

Migration 019 adds a versioned `semantic_json` column to existing classification
runs, explicit account/email primary preferences, account and email revision fences,
and the active classifier version on the existing index job. It creates no vector
store. Semantic JSON stores signed category scores, prototype hashes/status,
weighted evidence chunk IDs, source/generation/profile identity and policy gates.
Assignments remain in their assignment/preference records; typed user-assignment
results are reconstructed for display, not inserted as model predictions.

The semantic snapshot hash is a separate versioned branch from `classification.InputHash`.
Publication and every read recheck source/generation, account/category/prototype
revision, email/label/preference revision, model and preprocessing identity. Category,
example, prototype and index changes conservatively invalidate account assessments;
mail/label/manual-choice changes invalidate the affected email. This preserves a
multi-email Gmail batch when an earlier email's label cache changes. Work is copied
in short transactions and computed outside SQLite transactions. Context cancellation,
Pause/Cancel/Recover and purge fence late background publication. Confirmed remote
deletion clears active preferences and assessments while retaining action journals.

New Gmail preview rows carry an optional `semantic-organization-v1` evidence record
with exact assessment hash, original rule-input/source hashes, model/classifier
versions and score kind. Preview/apply validate both live mail and the current saved
assessment. Edits revoke stale previews. The optional field is omitted from legacy
rows so old serialized plans/digests retain their meaning. Explicit preview, separate
confirmation, journal-before-dispatch, reconciliation, and verified undo remain the
same. Undo uses saved verified outcomes even if current classification is stale.
Emails with multiple suggestions display `multiple_suggestions` in organization
review and require an explicit primary choice before a label preview. Suggestions
never silently choose the first tied category for Gmail. Existing preview/confirmation/
journaling/undo remains unchanged. There are no automatic Gmail label writes.

## Evaluation and limitations

The [frozen synthetic dataset](../backend/internal/classification/testdata/semantic-eval-v1.json)
contains 96 messages: six separately confirmed examples, 45 tuning messages and
45 held-out messages. Each evaluation split contains six positives per category,
one extra long quoted-chain Personal case, and eight ambiguous/out-of-scope cases.
Confirmed examples never come from tuning or held-out messages. The definition-only
and confirmed-example cohorts reuse the same held-out messages; they are comparisons,
not 90 independent held-out messages. Held-out paraphrases share topics with tuning
but use distinct texts. These are small synthetic fixtures, not production accuracy.

The original single-category policy selected section weights and gates using the
tuning split. This v3 policy changes the lead to 0.03 and returns close categories
at the user's explicit request. It is a user-selected behavior change, not a new
evaluation-selected optimum. Other weights and the 0.20 evidence floor stay fixed.
No retuning used the already inspected held-out texts. The new classifier version
invalidates older assessments, but preserves manual preferences, vectors and journals.

The deterministic `forwarded-wrapper-v1` regression also wraps every tuning and
held-out message in a forwarding envelope with no authored note. These 90 variants
reuse the existing texts and labels; they are not independent held-out examples.
All complete forwarded variants produce semantic scores.

With multiple predictions, precision counts **correct category suggestions / all
category suggestions** (micro precision). Per-category recall counts messages where
the expected category appears among the suggestions. Coverage/abstention count
messages, not suggestions. `Correct` in the report is exact single-label correctness;
`CorrectLabels` and `SuggestedLabels` define micro precision. Confusion matrices count
each proposed category, so a row may contain more predictions than source messages.
This penalizes extra false category suggestions instead of counting a mixed result
as entirely correct. Previous single-category accepted precision is not the same
metric. Scores remain evidence rather than probabilities.

| Held-out cohort              | Correct / all suggestions | Precision | Coverage | Abstention | Multiple-category emails |
| ---------------------------- | ------------------------- | --------- | -------- | ---------- | ------------------------ |
| Definitions only             | 34 / 56                   | 60.7%     | 100.0%   | 0.0%       | 9 / 45                   |
| Confirmed examples           | 35 / 53                   | 66.0%     | 100.0%   | 0.0%       | 8 / 45                   |
| Forwarded definitions        | 34 / 59                   | 57.6%     | 100.0%   | 0.0%       | 8 / 45                   |
| Forwarded confirmed examples | 36 / 59                   | 61.0%     | 100.0%   | 0.0%       | 11 / 45                  |

### Definitions only per-category results

| Category    | Precision | Recall |
| ----------- | --------- | ------ |
| Jobs        | 66.7%     | 100.0% |
| School      | 42.9%     | 100.0% |
| Receipts    | 60.0%     | 100.0% |
| Finance     | 83.3%     | 83.3%  |
| Newsletters | 62.5%     | 83.3%  |
| Personal    | 66.7%     | 85.7%  |

### Confirmed examples per-category results

| Category    | Precision | Recall |
| ----------- | --------- | ------ |
| Jobs        | 44.4%     | 66.7%  |
| School      | 60.0%     | 100.0% |
| Receipts    | 85.7%     | 100.0% |
| Finance     | 75.0%     | 100.0% |
| Newsletters | 66.7%     | 100.0% |
| Personal    | 70.0%     | 100.0% |

### Forwarded definitions per-category results

| Category    | Precision | Recall |
| ----------- | --------- | ------ |
| Jobs        | 75.0%     | 100.0% |
| School      | 42.9%     | 100.0% |
| Receipts    | 50.0%     | 100.0% |
| Finance     | 62.5%     | 83.3%  |
| Newsletters | 50.0%     | 83.3%  |
| Personal    | 85.7%     | 85.7%  |

### Forwarded confirmed examples per-category results

| Category    | Precision | Recall |
| ----------- | --------- | ------ |
| Jobs        | 46.2%     | 100.0% |
| School      | 66.7%     | 100.0% |
| Receipts    | 54.5%     | 100.0% |
| Finance     | 54.5%     | 100.0% |
| Newsletters | 71.4%     | 83.3%  |
| Personal    | 87.5%     | 100.0% |

All cohorts miss the 90% precision target. Removing ambiguity abstention produces
suggestions for all eight deliberately out-of-scope/ambiguous held-out cases too.
The higher coverage is not a production accuracy improvement. Full per-category
counts, confusion matrices, tuning-split metrics, rule-on/off comparisons and
individual suggestion sets are saved to `.cache/semantic-classification-qualification.json`.
Rule-on/off results in this dataset do not establish a general rule improvement.

`npm run test:classification:native` replays the policy with the packaged local model
under network denial and strict CGO checks. Classification generates zero embeddings.
Warm scoring-only latency was 0.018 ms median / 0.026 ms p95. These times exclude SQLite reads/writes, indexing and model loading, and are not
a 10,000-message performance claim.

Go tests cover multi-category persistence and complete suggestion sets, inclusive
lead boundaries, minimum score floors, deterministic ties, normalized aggregation,
length/overlap invariance, account isolation, manual overrides, stale workers and
previews, restart/purge, missing vectors and verified undo. Shared Go/Rust/TypeScript
fixtures cover five simultaneous suggestions and score identities. UI tests verify
all suggestions display with one set of evidence/override controls and that label
selection requires an explicit primary preference for multiple suggestions. Review
pages remain bounded to five emails and 48 KiB; oversized evidence stays persisted
and reports the existing display-limit error instead of silently dropping categories.
