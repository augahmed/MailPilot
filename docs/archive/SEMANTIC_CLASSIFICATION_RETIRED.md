# Semantic classification (retired)

Historical note, archived 2026-10-26 from `docs/SEMANTIC_CLASSIFICATION 2.md`.
This describes an earlier contraction, not today's shared native pipeline. See
[current semantic classification](../SEMANTIC_CLASSIFICATION.md). The original
`RETRIEVAL_READER_PLAN.md` and `CONTRACTION_PLAN.md` names are no longer current
paths; [the archived contraction](CONTRACTION_2026-10-16.md) preserves that context.

Future design (documentation only at the time): header/semantic category assessment
proposes full-cache local assessment with rules, semantic evidence, corrections
and abstention. The old implementation remains retired as described below.

The semantic and hybrid classifiers, embedding provider abstraction, MiniLM adapter and Python/ONNX worker have been removed. Deterministic classification uses bounded local text cleanup and the existing 0.60 direct-rule gate; weak or conflicting evidence abstains. Old engine identities require explicit re-review before new organization previews.

MailPilot setup requires no Python, MiniLM, ONNX, model download or model configuration.
No user-installed interpreter, downloaded model or cached message is deleted.

All 13 historical migrations remain immutable. Historical vector tables and
classification metadata may remain in existing SQLite databases as inert data.
Content updates invalidate affected derived rows transactionally without running
inference. Journal and draft recovery evidence remains intact. See
[the current architecture](../ARCHITECTURE.md), [setup](../../README.md) and
[the historical contraction plan](CONTRACTION_2026-10-16.md). The earlier active
boundary described above has since been superseded.
