# Semantic classification (retired)

Future design (documentation only): [header/semantic category assessment](RETRIEVAL_READER_PLAN.md#7-categorizing-every-cached-email)
proposes full-cache local assessment with rules, semantic evidence, corrections
and abstention. The old implementation remains retired as described below.

The semantic and hybrid classifiers, embedding provider abstraction, MiniLM adapter and Python/ONNX worker have been removed. Deterministic classification uses bounded local text cleanup and the existing 0.60 direct-rule gate; weak or conflicting evidence abstains. Old engine identities require explicit re-review before new organization previews.

MailPilot setup requires no Python, MiniLM, ONNX, model download or model configuration.
No user-installed interpreter, downloaded model or cached message is deleted.

All 13 historical migrations remain immutable. Historical vector tables and
classification metadata may remain in existing SQLite databases as inert data.
Content updates invalidate affected derived rows transactionally without running
inference. Journal and draft recovery evidence remains intact. See
[the architecture](ARCHITECTURE.md), [setup](../README.md) and
[the contraction plan](CONTRACTION_PLAN.md) for the active boundary.
