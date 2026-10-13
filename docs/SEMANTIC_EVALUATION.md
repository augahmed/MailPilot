# Classification quality evaluation

Measured on 2026-10-12 with the real local `all-MiniLM-L6-v2` ONNX adapter, pinned revision `10dbd2f06a8baf40ad285b037edda610c1b9a57c`, on Apple Silicon macOS. Inference uses the installed Python 3.12 environment and pinned runtime dependencies. Temporary SQLite databases exercise preprocessing, vector caches, classification, account-scoped assessment persistence and version metadata. No Gmail traffic, external inference or LLM classification is involved.

## Corpus and experimental protocol

The original [18-message corpus](../backend/internal/classification/testdata/labeled_emails.json) is unchanged and reported separately. The [84-message expansion](../backend/internal/classification/testdata/expanded_emails.json) adds seven positives per category and 35 expected-abstention cases. The combined 102 examples contain **63 positives (nine per category) and 39 expected abstentions**.

The expansion was authored and split before running the revised policy: 36 development examples (21 positive, 15 negative) and 48 reserved validation examples (28 positive, 20 negative). Development runs excluded validation outcomes. No score sweep, fitted calibration, default-description change or per-category threshold tuning was performed. The only outcome-driven development correction was a general immediate-negation bug in positive rule matching. The final validation run was not used to tune the policy. One empty-message control is repeated in the original and validation sets (102 cases, 101 distinct subject/body pairs). This is an authored synthetic split, not independent real-mailbox validation; similar vocabulary and author assumptions can occur across splits.

Hard negatives cover negated events, software applications versus employment applications, mock/research/media interviews, estimates and failed payments, templates and quoted examples, newsletter design, account alerts containing unsubscribe text, mixed intents, incomplete bodies, empty messages and unrelated operational/legal/health/travel mail. Positives include topic overlap (a finance newsletter), paraphrases, follow-ups, refunds and shipping updates, rather than just exact rule templates. See the [annotation policy](../backend/internal/classification/testdata/README.md).

**Before** reproduces the former shipped hybrid: starter rule scores never reached 0.85, so decisions were exactly the semantic classifier with cosine minimum 0.45 and margin 0.05. The evaluation freezes those values and uses unchanged category descriptions, text preprocessing and model artifacts for both policies. **After** uses the evidence-aware hybrid described in [classification policy](SEMANTIC_CLASSIFICATION.md). **Rules-only** uses that new hybrid with no model configured. Model scores and rule strengths are not calibrated probabilities.

## Before / after

Coverage = assigned / all; precision = correctly assigned / assigned; accuracy = all correct decisions / all, including correct abstentions. Abstaining on a positive is an error. Assigning any category to an expected-abstention example is a false positive. Null predictions are not silently removed from denominators.

| Corpus                 | Policy           | Assigned | Correct assignments | Correct decisions | Coverage | Assigned precision | Accuracy |
| ---------------------- | ---------------- | -------- | ------------------- | ----------------- | -------- | ------------------ | -------- |
| Original 18            | Before           | 8        | 7                   | 10                | 44.4%    | 87.5%              | 55.6%    |
| Original 18            | After            | 9        | 8                   | 11                | 50.0%    | 88.9%              | 61.1%    |
| Development 36         | Before           | 5        | 4                   | 18                | 13.9%    | 80.0%              | 50.0%    |
| Development 36         | After            | 7        | 7                   | 22                | 19.4%    | 100.0%             | 61.1%    |
| Reserved validation 48 | Before           | 12       | 8                   | 24                | 25.0%    | 66.7%              | 50.0%    |
| Reserved validation 48 | After            | 12       | 10                  | 28                | 25.0%    | 83.3%              | 58.3%    |
| All 102                | Before           | 25       | 19                  | 52                | 24.5%    | 76.0%              | 51.0%    |
| All 102                | After            | 28       | 25                  | 61                | 27.5%    | 89.3%              | 59.8%    |
| All 102                | Rules-only after | 15       | 15                  | 54                | 14.7%    | 100.0%             | 52.9%    |

The old no-model hybrid selected zero messages. The new no-model path provides useful conservative matches without assigning invented confidence probabilities. Its 15/15 precision is a small synthetic observation, not a guaranteed error rate.

On the full corpus, recall on positives increases from **19/63 (30.2%) to 25/63 (39.7%)**. Negative false positives decrease from **6/39 to 3/39**. Validation recall increases from **8/28 to 10/28**, while negative false positives fall from **4/20 to 2/20**. Validation coverage does not improve: two correct receipt decisions replace two false-positive decisions. Comparing the original 44.4% to the expanded 27.5% directly would confuse a policy change with a harder, differently composed dataset.

## Per-category results

Full 102-message corpus; each category has nine positives. A dash means precision is undefined because nothing was assigned, not zero measured precision.

| Category         | Before precision | Before recall | After TP / FP / FN | After precision | After recall |
| ---------------- | ---------------- | ------------- | ------------------ | --------------- | ------------ |
| Job Applications | 100.0%           | 44.4%         | 4 / 0 / 5          | 100.0%          | 44.4%        |
| Job Interviews   | 63.6%            | 77.8%         | 7 / 1 / 2          | 87.5%           | 77.8%        |
| School           | 100.0%           | 11.1%         | 1 / 0 / 8          | 100.0%          | 11.1%        |
| Finance          | —                | 0.0%          | 0 / 0 / 9          | —               | 0.0%         |
| Receipts         | 0.0%             | 0.0%          | 5 / 2 / 4          | 71.4%           | 55.6%        |
| Newsletters      | 100.0%           | 77.8%         | 8 / 0 / 1          | 100.0%          | 88.9%        |
| Personal         | —                | 0.0%          | 0 / 0 / 9          | —               | 0.0%         |

Reserved validation has four positives per category. After precision/recall: Applications 100%/50%; Interviews 75%/75%; Receipts 66.7%/50%; Newsletters 100%/75%. School, Finance and Personal have no assignments. These small denominators prohibit reliable per-category calibration claims.

## Diagnosis and calibration limits

Before the change, 42 of 63 positives abstained because of the absolute cosine minimum and another two because of the ambiguity margin. The original corpus alone had seven below-minimum positives. The old hybrid also discarded all sub-0.85 rules, rule conflicts and exclusion evidence before semantic fallback.

The category descriptions are broad lists of related intents, represented by one vector each. Narrow messages can be the closest match to the right category without reaching 0.45; conversely a hard negative can have a high cosine. Finance and Personal have no starter rules to supply independent evidence. Short category descriptions, overlapping taxonomy (applications/interviews and finance/receipts), one-vector text representation, sender framing and 256-wordpiece truncation limit relevance. The experiment does not isolate each representation factor causally.

After the change, 35 positives still miss the minimum and three abstain on semantic ambiguity. The remaining false positives are the original mixed banking/receipt example, the reserved mixed banking/order message, and a media interview described in the body rather than excluded in the subject. These outcomes remain in the report; no validation-specific exception was added. The system does not recognize every ambiguous intent.

Calibration diagnostics separate score kinds rather than mixing their numerical scales:

| After decision score bin                  | Correct / assigned | Observed precision |
| ----------------------------------------- | ------------------ | ------------------ |
| Cosine below 0.45, corroborated by a rule | 4 / 4              | 100.0%             |
| Cosine 0.45–0.60                          | 19 / 22            | 86.4%              |
| Direct rule strength at least 0.60        | 2 / 2              | 100.0%             |

Before the change, the only cosine above 0.60 was a false positive. Neither cosine nor the legacy field named `confidence` is a probability. There is insufficient independent labeled evidence to fit a probability calibration or justify category-specific cutoffs; those are deliberately deferred instead of fitting seven thresholds to nine positives each. Existing defaults and user category definitions are unchanged.

## Reproduce

From the repository root, after the local model setup in [SEMANTIC_CLASSIFICATION.md](SEMANTIC_CLASSIFICATION.md):

```sh
MAILPILOT_TEST_EMBEDDING_PYTHON="$PWD/.local/minilm-py312/bin/python" \
MAILPILOT_TEST_EMBEDDING_MODEL_DIR="$PWD/.local/models/all-MiniLM-L6-v2" \
  go -C backend test -count=1 -v ./internal/store/sqlite -run TestOptInMiniLMEvaluation
```

Set `MAILPILOT_EVALUATION_DEVELOPMENT_ONLY=1` to omit reserved validation outcomes. The test prints per-example decisions, named candidates, aggregate/per-split metrics, TP/FP/FN counts and score-bin diagnostics; it verifies persisted evidence and metadata. Zero-denominator numeric diagnostics use zero; interpret precision with zero assignments as undefined. Ordinary tests check corpus integrity and use fake vectors for deterministic decision boundaries, without requiring model files.

The model is not evaluated on real personal mail, multilingual distributions, long-message corpora or custom category descriptions. No production accuracy or latency claim follows from these results.

## Verification of this revision

The full Go suite passed with a fresh `-count=1 -race` run, followed by `go vet` and `go build`. Frontend strict typecheck, lint, production build and all 56 tests passed. Rust fmt, Clippy and all 12 tests passed. Repository formatting and diff whitespace checks passed. The Apple Silicon macOS app bundle built and passed the real native WebView/bridge/database smoke check with a disposable profile and Gmail disabled. Real MiniLM smoke, search retrieval and comparative classification evaluation passed. Corpus-integrity and metric-denominator regressions also run without a model.

The first restricted Go run could not bind loopback fixture listeners and was stopped; the full suite passed outside that restriction with a 120-second per-package timeout. Native smoke also ran outside the sandbox. No live mailbox mutations, paid model calls or native-keyring integration were part of this verification.
