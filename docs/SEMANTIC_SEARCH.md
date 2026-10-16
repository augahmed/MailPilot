# Semantic search (retired)

The semantic query/index services, vector/passage adapters, UI and IPC exposure have been removed. No index rebuild or replacement ML service is provided. Local keyword search remains planned and is not implemented by this contraction.

MailPilot setup requires no Python, MiniLM, ONNX, model download or model configuration.
No user-installed interpreter, downloaded model or cached message is deleted.

All 13 historical migrations remain immutable. Historical vector tables and
classification metadata may remain in existing SQLite databases as inert data.
Content updates invalidate affected derived rows transactionally without running
inference. Journal and draft recovery evidence remains intact. See
[the architecture](ARCHITECTURE.md), [setup](../README.md) and
[the contraction plan](CONTRACTION_PLAN.md) for the active boundary.
