# Optional generative AI provider layer

Active status: provider/key settings and the shared AI/consent foundation remain.
Reply generation, thread summaries, Ask and arbitrary prompts have no active entry
point. Selected-email summary UI/IPC and additional provider adapters are future
work; the older operation descriptions below document retained implementation history.

The Go backend now supports `AIProvider.GenerateDraft`, `SummarizeThread`, and
`AnswerWithContext`. All three accept a context, a provider-neutral `ai.Request`
(user instruction/question, selected plain-text context, output-token limit), and
return untrusted plain text. Generating a draft does not create a Gmail draft,
send mail, or change mailbox state.

The desktop now exposes optional OpenAI settings and [reviewed reply drafting](REPLY_DRAFTING.md). AI remains disabled until configured; reading, sync, rules, risk scoring and local semantic search remain independent. [Ask MailPilot](ASK_MAILPILOT.md) now adds reviewed retrieval context and cited answers using the same settings and consent boundary. Summary UI remains unimplemented.

## Composition and configuration

A backend caller can compose the implementation as follows:

```go
// Disabled is a supported configuration and never touches a credential store.
service, err := ai.NewService(nil, ai.Identity{})

// For an explicitly configured provider, use an application-owned profile path
// and an explicit model ID supported by the user's OpenAI project.
keys := credentials.NewAPIKeyStore(privateDataDir, "openai")
provider, err := openai.New(keys, modelID)
// Check err before using provider.
service, err = ai.NewService(provider, provider.Identity())
```

These snippets describe composition, not a new command-line or IPC API. Provider
and model identifiers are non-secret configuration. No model default is silently
selected. `NewService(nil, ...)` returns `Enabled() == false`; preparing/executing
returns `ai_disabled`. A configured adapter with a missing, locked, corrupt or
unavailable key fails with `ai_credentials_unavailable` before any HTTP request.
Constructors perform no network or keyring operation. Provider instances and their
identity must remain immutable while a service is in use.

`credentials.SecretStore` exposes `Load`, `Save`, and `Delete` for one scoped secret.
The concrete `APIKeyStore` uses the existing OS keyring library in a namespace
separate from Gmail OAuth. Slots are isolated by application-data profile and
provider; the desktop reply use case additionally scopes by account. There is no file, SQLite, environment-variable or command-argument key
fallback. Save accepts the existing redacted `credentials.Secret` wrapper. The settings form submits briefly held input through the narrow typed reply command, clears it immediately, and never returns a saved key through a read API. OS keyring calls
are synchronous; context is checked before and after the OS operation, but cannot
interrupt an OS credential-store prompt already in progress.

Anthropic and local/Ollama adapters can implement the same three-method interface
without changing consumers. They are not implemented or routed through OpenAI.
Local adapters can disclose `External: false`. They must preserve the approval,
redaction, limits and cancellation contract; no arbitrary endpoint supplied by
mailbox content is allowed. No provider SDK types appear in neutral inputs or
results. No dependencies were added.

## Approval and privacy boundary

`ai.Service.Prepare` creates an immutable, account-bound preview containing the
provider/model/external identity, operation, full versioned policy instructions,
user instruction, selected context, and token limit. The adapter sends those texts
as policy plus two user messages; the context message has the fixed prefix
`Untrusted email context:` followed by a newline. There are no hidden email
headers, attachments, retrieved sources, prior conversations, or tools.

The reply application use case authorizes the current account/session before
both `Prepare` and `Execute`, fetches only account-owned sources, shows every preview
field (using `Text.Reveal()` only for this explicit presentation), and obtains a
separate user confirmation. The service is not an authentication mechanism. The UI uses a validated, fixed reply.manage command rather than direct provider access.

Approvals expire after five minutes, are bound to one service configuration and
account, and are consumed before dispatch, even if the request fails. Only one
pending preview and one active call are supported; new previews replace old
ones. Modifying a returned preview cannot modify the stored request. Execution
accepts only the preview ID, authorized account and explicit confirmation, not a
replacement payload. Clear the service on disconnect/configuration changes to
revoke previews, release pending input, cancel work, and suppress late output.
Cancellation cannot recall data already received by an external provider. Retrying
requires a new preview and confirmation.

AI requests and unreviewed results stay in memory. Once the user requests a Gmail draft preview, the reviewed body and routing are persisted in the local action journal for safe recovery. `ai.Text` and `credentials.Secret` redact
ordinary formatting, structured logs and generic JSON serialization. The adapter
has no prompt/body logging, tracing or log opt-in. It reports fixed neutral error
codes, never raw HTTP, keyring or provider error details. Explicitly extracting
text into a string is sensitive and must not be logged; only explicitly reviewed draft plans are persisted by this workflow.
Prompts do not confer action rights; model responses have no tools or mailbox
mutation access and are rendered as escaped text for human review.

## OpenAI adapter

The adapter uses Go's standard HTTP/JSON libraries and a fixed HTTPS Responses
API endpoint. It loads the key immediately before each dispatch, so replacement
or removal is respected on the next call. It refuses redirects and ignores
proxy environment variables. One request runs at a time per adapter; waiting and
network work share a one-minute context deadline. There are no automatic retries,
streaming, conversation IDs, tools or remote files.

Input is capped at 32 KiB total and must be valid UTF-8; output-token requests are
1–4096. Thread summaries and contextual answers require nonempty context. HTTP
response bodies are capped at 256 KiB; generated text is capped at 32 KiB. Refusals,
incomplete responses, invalid output, authentication failures, and rate limits
have distinct safe errors. Failed or partial model text is not returned as success.

The adapter explicitly sends `store: false`. This disables stored Responses
application state; it is **not** a promise of zero retention by the external
provider. OpenAI's other retention/data-control policies still apply. The wire
implementation was checked against official [text-generation documentation](https://developers.openai.com/api/docs/guides/text)
and [data controls](https://developers.openai.com/api/docs/guides/your-data).

## Verification

`FakeAIProvider` exercises all operations and the consent service with synthetic
inputs. OpenAI tests use an in-memory HTTP transport and synthetic SecretStore;
OS-store unit tests use the existing keyring mock. Tests never need a paid API
call or a real key. Coverage includes disabled behavior, approval expiry/replay,
account isolation, immutable previews, clear/cancellation races, provider errors,
key rotation/failure, redirects, request/response limits, refusals, incomplete
output, concurrency, and log/serialization redaction.

No live OpenAI test was performed. Model availability and generation quality
remain dependent on the selected model and the user's project. Reply workflow tests additionally cover IPC validation, both approvals, source/account isolation, editing, session cancellation, journal recovery and the native unauthorized-command boundary.
