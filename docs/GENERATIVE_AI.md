# Optional AI: approved email evidence and current Summary

Updated 2026-10-20. [PRODUCT_PLAN.md](PRODUCT_PLAN.md) is the scope authority.
Optional AI now supports selected-email Summary and separately approved evidence
questions. Both preserve selected provider/model, OS-only key storage and bounded
normalized provider contracts. Neither workflow grants mailbox/action tools.

## Implemented retrieval-augmented questions

[EMAIL_QUESTIONS.md](EMAIL_QUESTIONS.md) defines the implemented `email.answer` /
`email_answer` workflow: local shared hybrid retrieval, at most eight bounded source
excerpts, source selection, exact payload/policy preview, expiring one-use approval,
one selected-provider generation and validated source-linked citations. Questions,
evidence and output remain memory-only. Missing/conflicting evidence, nonexhaustive
retrieval, cancellation and stale source/session/settings/key invalidation are explicit.
No hidden context, external/model-controlled search, Gmail tools, SQL or actions.

The normalized `AIProvider.AnswerWithEvidence` capability is separate from Summary;
Summary enablement or tokens never authorize a question. The user must approve each
exact question/evidence request. Empty retrieval and common mailbox-wide aggregate
requests make no paid call. Citation provenance cannot establish factual correctness.
See the question contract for exact limits, adversarial tests and native validation.

## Implemented baseline: selected-email summaries

The retained Summary workflow summarizes one explicitly selected cached email,
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
    AnswerWithEvidence(context.Context, AnswerRequest) (AnswerResult, error)
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
Gmail `CreateDraft`, and the old reply editor are deleted. The retired Ask endpoint,
reply generation, thread summaries, arbitrary chat/tools and AI classification
remain unreachable through the UI, IPC, provider interface or indirect recovery.
The new bounded evidence-answer endpoint is documented separately above.

The separately implemented typed evidence-question operation does not revive the
old generic `AnswerWithContext` or Ask contract. No generic AI methods remain for compatibility. Only historical draft plan
values/digests/validation, account-scoped journal reads, recovery transitions and
Gmail `FindDraft` remain. Recovery has no AI/key/settings dependency and cannot
resume previews or create/resend/delete drafts. Journal bodies/source references
remain immutable evidence in SQLite. See [draft recovery](REPLY_DRAFTING.md).

Wire endpoints are `ai.settings`, `email.summarize` and `email.answer`, exposed by
narrow native commands `ai_settings`, `email_summarize` and `email_answer`. `reply.manage` only handles historical
status/reconcile/resolve/cancel. [Contracts and synthetic examples](../contracts/README.md)
define the operation-specific fields; unknown prompt/thread/tool fields reject.

Validation uses synthetic mail, fake adapters, mocked OS credentials and local
HTTP transports. It makes no paid AI call or personal-mailbox mutation. Native
smoke checks exercise authorization and lifecycle with Gmail/AI unconfigured;
these are not real-provider quality or accuracy measurements.
