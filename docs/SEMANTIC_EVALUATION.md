# Synthetic semantic classification evaluation

Measured with the real local `all-MiniLM-L6-v2` ONNX adapter, pinned model revision `10dbd2f06a8baf40ad285b037edda610c1b9a57c`, ONNX Runtime 1.23.2, NumPy 2.5.4 and Tokenizers 0.23.3 on macOS 13 ARM64 / Python 3.12. The test uses a disposable SQLite database and synthetic sender addresses; it makes no Gmail requests.

The checked-in [dataset](../backend/internal/classification/testdata/labeled_emails.json) contains 18 manually labeled synthetic messages: two for each of seven default categories, two deliberately ambiguous messages, one out-of-scope incident alert and one empty message. A null category means expected abstention. Category descriptions are the unchanged production defaults. The test runs the complete hybrid → embedding caches → cosine ranking → persisted assessment pipeline. Strong-rule precedence is tested separately using fake vectors and deliberately high-confidence rule configuration.

Default policy: deterministic threshold 0.85; minimum cosine 0.45; ambiguity margin 0.05; top three candidates. Starter rule strengths are below the rule threshold, so this corpus measures the semantic branch. The thresholds were not tuned to improve this report. This small authored corpus is a development check, not a held-out benchmark or evidence of production accuracy.

| Metric                                                                   | Result        |
| ------------------------------------------------------------------------ | ------------- |
| Overall accuracy, including correct abstentions                          | 10/18 = 55.6% |
| Precision among selected categories                                      | 7/8 = 87.5%   |
| Coverage (any category selected)                                         | 8/18 = 44.4%  |
| Recall on labeled categories                                             | 7/14 = 50.0%  |
| Leading-candidate accuracy on the 14 labeled messages, before abstention | 14/14 = 100%  |

Precision counts selecting a category on an expected-abstention message as a false positive. Overall accuracy counts abstention on a labeled message as an error. Coverage and recall prevent high precision from hiding excessive abstention. Leading-candidate accuracy excludes the four null-label messages and does not measure the decision policy.

| Category         | True positives | False positives | False negatives |
| ---------------- | -------------- | --------------- | --------------- |
| Job Applications | 2              | 0               | 0               |
| Job Interviews   | 2              | 0               | 0               |
| School           | 1              | 0               | 1               |
| Finance          | 0              | 0               | 2               |
| Receipts         | 0              | 1               | 2               |
| Newsletters      | 2              | 0               | 0               |
| Personal         | 0              | 0               | 2               |

Seven correctly ranked labeled messages were left unclassified because their best scores were below 0.45. The ambiguous banking/receipt message was incorrectly assigned Receipts (cosine 0.472); the other ambiguous hiring message was correctly left unclassified by the margin rule. The incident alert and empty message were correctly rejected by the minimum score. These failures show that a universal threshold and short category descriptions need broader validation; a clear cosine lead does not establish the correct label.

The real-model evaluation test logs each outcome and per-category counts. Reproduce it with the opt-in command in [development setup](SEMANTIC_CLASSIFICATION.md). Unit tests also cover rule precedence, semantic fallback, low scores, ties, disabled categories, stale/cross-account writes, invalid vectors, cancellation and cache reuse without downloading a model. Model similarity scores are not calibrated confidence probabilities. This evaluation does not establish behavior on multilingual mail, long messages truncated at 256 wordpieces, real mailbox distributions, adversarial text or category definitions different from the defaults.
