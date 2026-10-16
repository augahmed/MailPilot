# Ask MailPilot (retired)

Mailbox-wide retrieval Q&A, its evidence builder, application service, frontend state and commands have been removed. The shared AI provider/settings/consent foundation and OpenAI adapter remain for future explicitly approved single-email summaries. There is no active summary or chat endpoint yet.

MailPilot setup requires no Python, MiniLM, ONNX, model download or model configuration.
No user-installed interpreter, downloaded model or cached message is deleted.

All 13 historical migrations remain immutable. Historical vector tables and
classification metadata may remain in existing SQLite databases as inert data.
Content updates invalidate affected derived rows transactionally without running
inference. Journal and draft recovery evidence remains intact. See
[the architecture](ARCHITECTURE.md), [setup](../README.md) and
[the contraction plan](CONTRACTION_PLAN.md) for the active boundary.
