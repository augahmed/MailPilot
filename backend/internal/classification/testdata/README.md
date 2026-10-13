# Synthetic classification corpus

All examples are authored synthetic text; no personal mail, real sender addresses or model-generated classifications are used. The test supplies the same synthetic sender for every message. IDs, splits, case names and expected categories never enter classifier input. The original `labeled_emails.json` remains unchanged; `expanded_emails.json` adds 84 cases and a preassigned development/validation split.

Annotation policy:

- Assign the one dominant communication intent under the existing default descriptions: employment application progress, actual hiring interviews, school communication, financial account matters, purchase/fulfillment events, recurring editorial/promotional mail, or private friends/family correspondence.
- Topic alone does not determine intent. A finance newsletter is Newsletters; discussing a receipt parser is an operational software message.
- Deliberately mixed independent intents, empty/incomplete content, quoted demonstrations and clearly out-of-scope mail have null labels. Null means the conservative expected decision is abstention, not that a human could never choose a category.
- Mock/media/research interviews are hard negatives for hiring interviews. Failed purchases, estimates and templates are not proof of a completed transaction. Negated events test the intended conservative abstention policy; real users may organize such status messages differently.
- Positives include paraphrases, follow-ups, fulfillment/refunds and categories without any rule support. Do not rewrite their labels or descriptions to improve reported metrics.

Each added category has seven positive examples, with three in development and four in validation. The 35 additional expected-abstention cases split 15/20. An empty-message control is repeated from the original corpus; all nonempty messages are distinct. The author and language overlap across splits, so reserved validation is a development guard, not an independent benchmark. Broader human-reviewed mailbox evaluation is needed before fitting category thresholds or interpreting scores as probabilities.
