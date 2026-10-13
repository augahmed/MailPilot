# Synthetic retrieval development corpus

`retrieval.json` contains 40 authored messages and 22 paraphrased queries. It is
synthetic, with no private messages. Eight long reports place the requested
information beyond the previous 16-KiB/model input cutoff. Distractors share
vocabulary but differ in intent (policy versus confirmation, marketing versus
appointment, interview advice versus hiring, parcel versus childcare pickup).
Other cases cover subject clues, inline replies, signatures and duplicate threads.

The corpus was fixed before measuring old and new retrieval. It is a development
set, not a held-out natural mailbox sample. Relevant IDs for the lift query are
interchangeable copies; top-3 recall is query-level success, not recall of every
copy. The evaluator logs all top-1 failures and measures both implementations
without query-specific ranking rules. See `docs/SEMANTIC_SEARCH.md` from the
repository root for results and commands.
