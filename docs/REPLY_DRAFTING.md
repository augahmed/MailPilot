# Historical draft recovery only

Status reviewed 2026-10-21. This is a current compatibility specification, not a
reply/composition roadmap. Those features are outside the immediate
[product scope](PRODUCT_PLAN.md). Preserve recovery without adding any new remote
creation or mutation capability.

Reply generation and new Gmail draft creation are retired. The reply editor,
generic AI draft methods, thread-context builder, draft-plan insertion API,
dispatch transitions, provider `DraftCreator` contract and Gmail `CreateDraft`
implementation have been deleted. There is no creation endpoint or automatic retry.

The desktop **Historical draft recovery** section can inspect an existing journal
action, reconcile uncertain/conflicting outcomes with Gmail GET requests, and
record explicit local acknowledgment after reconciliation. Recovery needs no AI
provider or key. No historical preview becomes an editor or a resumable creation.
No match does not establish non-creation; edited/duplicate drafts remain conflicts.
There is no remote undo; users handle drafts manually in Gmail.

Compatibility is limited to `reply.Plan`, source/reference values, its exact JSON
shape and digest validation; SQLite journal reads/recovery transitions; and Gmail
`FindDraft` matching the historical Message-ID and reviewed routing/body. Plan
bodies, source foreign keys, journal rows and migrations 001–013 remain intact.
The active response exposes only recipient/subject, action/outcome identifiers,
reconciliation state and safe error code; historical body/prompt/context never
becomes a frontend editing session.

`reply.manage` / `reply_manage` permits only status, reconcile, resolve and cancel.
It rejects settings, generation, prompts, thread fields, body changes and creation.
AI settings now use their own narrow endpoint. Active AI workflows are [selected-email Summary](GENERATIVE_AI.md) and
[separately approved evidence questions](EMAIL_QUESTIONS.md), both independent of
this recovery boundary.
