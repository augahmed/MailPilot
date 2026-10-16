# Semantic evaluation (historical)

The synthetic semantic evaluation suites, corpora and model benchmarks were retired with their implementation. Earlier measurements are available in Git history; they do not measure the current deterministic classifier. Current checks cover rule evidence, abstention, text limits, isolation and stale historical assessments.

MailPilot setup requires no Python, MiniLM, ONNX, model download or model configuration.
No user-installed interpreter, downloaded model or cached message is deleted.

All 13 historical migrations remain immutable. Historical vector tables and
classification metadata may remain in existing SQLite databases as inert data.
Content updates invalidate affected derived rows transactionally without running
inference. Journal and draft recovery evidence remains intact. See
[the architecture](ARCHITECTURE.md), [setup](../README.md) and
[the contraction plan](CONTRACTION_PLAN.md) for the active boundary.
