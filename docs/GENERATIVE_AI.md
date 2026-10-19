# Optional AI: approved email evidence and current Summary

Updated 2026-10-19. [PRODUCT_PLAN.md](PRODUCT_PLAN.md) is the scope authority and
[ARCHITECTURE.md](ARCHITECTURE.md#8-optional-retrieval-augmented-answers-planned)
defines the intended application boundary. This feature specification distinguishes
planned answers from the currently implemented selected-email Summary. The reset
changes no command, provider method, key, credential, approval or runtime behavior.

## Intended workflow: retrieval-augmented questions (planned)

The immediate AI use case is answering bounded questions about information in
cached email. Go retrieves through the shared local semantic/hybrid pipeline;
users inspect/select and separately approve the exact passages, question, metadata,
policy and provider/model before disclosure. No hidden thread expansion, attachments,
prior conversation, model-controlled search or action tools. Answers cite approved
source emails and preserve insufficient/conflicting evidence and uncertainty.

Keep multiple providers behind one narrow evidence-answer abstraction, with
provider-specific wire/SDK types in adapters and provider-scoped OS keys. Local
embedding inference is independent and uploads no mail. No generative provider or
key is needed for local search/classification. Composition/replies, sending, new
Gmail drafts, unrestricted chat/agents and background external AI are outside scope.

Approval must be expiring/single-use and bound to account/client, exact payload,
source revisions, policy and provider/model/settings/key identity. Recheck before
dispatch/display, revoke on relevant changes and suppress late results after
cancellation. Validate source IDs/quotes, bounds and escaped output; citation
provenance cannot prove semantic correctness. Keep previews/answers memory-only
initially. Set exact RAG payload limits and typed contracts during implementation;
the current Summary's limits below are not permission for broader disclosure.

Plan a later tested Summary transition, reusing safe settings/keyring/redaction/
transport foundations. Existing Summary enablement or approval never authorizes
RAG. Historical draft recovery stays independent. No replacement is implemented
by this documentation task.

## Implemented baseline: selected-email summaries

The current runtime supports one AI workflow: summarize one explicitly selected cached email,
extract explicit requests, and extract stated deadlines. The reader provides an
exact payload preview and a separate approval step. Ordinary mail reading, sync,
categories, organization and phishing analysis work with AI disabled.

### Providers and settings

**OpenAI is the only installed functional provider adapter.** It retains its existing
bounded HTTPS Responses API transport, OS key loading per dispatch, timeout,
no automatic retry, redirect rejection and environment-proxy rejection. There are
no external SDK dependencies. No unimplemented provider is offered in Settings.

The shared boundary supports multiple adapters for the same workflow. Trusted Go
composition registers each adapter with its own constructor and provider-scoped
OS key store. Desktop **Optional AI settings** lists registered providers; the user
chooses provider/model. Tests register a second synthetic adapter to verify
substitution, selection and separate key handling; it is not a shipped provider.

Migration 014 adds the selected provider to the existing non-secret
`reply_ai_settings` record. Existing records default to `openai`; saved model,
enabled state and existing profile/account/OpenAI keyring slot remain valid.
Migrations 001–013 are unchanged. Keys remain in OS credential storage, never SQL,
files, environment, returned DTOs or logs. The input clears immediately after
submission. Blank input uses only the selected provider's saved key. Changing
providers leaves other providers' keys alone. Disable turns AI off before removing
the selected provider's key; OS-store failures are reported with no plaintext fallback.

### Provider contract

```go
type AIProvider interface {
    SummarizeEmail(context.Context, SummaryRequest) (SummaryResult, error)
}

type SummaryRequest struct {
    EmailID  domain.EmailID // application identity; not forwarded to OpenAI
    Revision string         // cached content hash; not forwarded to OpenAI
    Email    Text           // exact approved bounded email payload
}

type SummaryResult struct {
    Summary   Text
    Requests  []Text
    Deadlines []Text
}
```

`Text` redacts standard formatting, JSON and structured logs. The application
explicitly reveals only approved input for preview/provider transport and validated
output for presentation. SDK/wire types remain inside each provider adapter.

Adapters return this normalized result, never arbitrary chat text or tool calls.
The OpenAI adapter decodes strict JSON with only `summary`, `requests`, `deadlines`.
The summary is limited to 4,000 UTF-8 bytes. Each extraction array has at most ten
verbatim excerpts of at most 500 bytes, with no duplicates within the array.
Every extracted excerpt must occur in the approved payload. This verifies text
provenance, not semantic correctness; summaries and extraction can still be wrong.
Absent requests/deadlines are empty arrays. No date/time-zone inference is requested.

### Selection, consent and lifecycle

Go loads only the selected account/email ID from SQLite. It has no thread-query,
retrieval, Gmail or attachment reader in the summary path. The payload includes
bounded subject/sender/received time and locally cleaned current body text, with
quote/signature removal and HTML-to-text fallback. A complete readable cached body
is required. Input is capped at **6,000 UTF-8 bytes**, with truncation disclosed in
the preview. This is the implemented limit, superseding larger proposed plan limits.

The preview shows the exact email payload, fixed versioned policy, destination,
model and five-minute expiry. Approval binds to that immutable snapshot, account,
provider/model and one-use token. Go rechecks the source before dispatch and before
publishing output. Changing selection, cached content, provider/model/key, disabling
AI, disconnecting, cancelling or leaving the reader revokes consent or hides stale
results. No external AI request occurs during settings, status, sync or preview.

Execution is one bounded background job per core, polled through the summary
endpoint. Provider calls have a one-minute limit; the surrounding job has a
75-second deadline. Cancellation revokes local permission and suppresses late output;
a dispatched external request may already have reached the provider. Failed calls
consume approval and require a fresh preview to retry. Shutdown cancels and joins
work before SQLite closes. Summary previews/output are memory-only. Wire responses
are bounded to 48 KiB after JSON escaping.

The provider receives the fixed summary policy and exactly the approved payload.
There are no tools, Gmail permissions, previous-response IDs, conversation state,
arbitrary user prompts, hidden mail expansion, background AI or action authority.
Email/model output is untrusted and rendered as escaped text with inactive links.

### Removed capabilities and compatibility

`GenerateDraft`, `AnswerWithContext`, `SummarizeThread`, generic operation dispatch,
reply payload construction, thread-context SQL, new draft-plan insertion/dispatch,
Gmail `CreateDraft`, and the old reply editor are deleted. Ask, mailbox Q&A/RAG,
reply generation, thread summaries, arbitrary chat/prompts and AI classification
cannot be reached through the UI, IPC, provider interface or indirect recovery.

No generic AI methods remain for compatibility. Only historical draft plan
values/digests/validation, account-scoped journal reads, recovery transitions and
Gmail `FindDraft` remain. Recovery has no AI/key/settings dependency and cannot
resume previews or create/resend/delete drafts. Journal bodies/source references
remain immutable evidence in SQLite. See [draft recovery](REPLY_DRAFTING.md).

Wire endpoints are `ai.settings` and `email.summarize`, exposed by narrow native
commands `ai_settings` and `email_summarize`. `reply.manage` only handles historical
status/reconcile/resolve/cancel. [Contracts and synthetic examples](../contracts/README.md)
define the operation-specific fields; unknown prompt/thread/tool fields reject.

Validation uses synthetic mail, fake adapters, mocked OS credentials and local
HTTP transports. It makes no paid AI call or personal-mailbox mutation. Native
smoke checks exercise authorization and lifecycle with Gmail/AI unconfigured;
these are not real-provider quality or accuracy measurements.
