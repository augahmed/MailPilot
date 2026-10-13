# AI-assisted reply drafting

Reply drafting is optional. Reading, sync, classification, risk analysis and local
search work without an AI provider. The current adapter is OpenAI; no sending
operation exists.

## Use the desktop workflow

1. Connect Gmail and sync the messages you need. In **Draft a reply**, open
   **Optional AI settings**, enter a model ID available to your OpenAI project and
   an API key. The key goes through SecretStore into the OS credential store;
   it is never returned by status, stored in SQLite or logged. The input clears
   immediately on submission. Leaving the key blank preserves the saved key.
2. Select an email. Optionally include earlier cached messages from its thread.
   Choose Professional, Friendly, Concise or Formal and optionally add an instruction.
3. Select **Preview AI context**. Review the provider/model, exact policy,
   instruction and context. The external-provider notice explains that these
   texts will leave the device. Approve this exact request, then generate.
4. Review and edit the proposed plain-text reply. Select **Preview Gmail draft**,
   review recipient, subject and edited body, and separately approve creation.
   Changing the body invalidates the approval. Creating a draft never sends it.

Use **Disable AI and remove key** to disable generation and delete the scoped
key. A keyring failure is reported; there is no insecure fallback. Disconnect
cancels work and clears displayed private content, but retains local settings,
cached mail and journal records for reconnection.

## Context and privacy

Only the selected account's selected message and, when requested, up to four
earlier cached messages in the same thread are considered. The selected message
is last. The cache may be incomplete; later messages are excluded. Incomplete
cached bodies block generation rather than silently pretending to be complete.
Deterministic preprocessing removes quoted history and signatures. The selected
text is capped at 1,800 UTF-8 bytes and earlier text at 1,000 bytes each, with a
visible truncation indicator. This heuristic may omit useful text: review the
disclosed context before approval.

No attachments, unrelated mailbox messages, search results, HTML rendering,
remote images or hidden retrieved context are sent. Recipient routing uses a
single valid Reply-To header, otherwise the selected sender; it is shown for
review. Reply-all and attachment drafts are not implemented. Invalid or missing
RFC Message-ID/thread metadata blocks drafting.

The exact external request uses the displayed policy and instruction plus
`Untrusted email context:` followed by a newline and the displayed context.
Generation is limited to 1,200 output tokens; replies over 6,000 UTF-8 bytes are
rejected. Model output is untrusted escaped text and has no mailbox privileges.
Approval expires after five minutes and is consumed once. Cancellation cannot
recall content already received externally. OpenAI receives `store: false`,
which is not a guarantee of zero external retention; see
[provider privacy details](GENERATIVE_AI.md).

AI prompts and unreviewed output are memory-only. Requesting a Gmail draft
preview persists its reviewed body, recipient, subject, source hashes and
recovery metadata locally. These journal records, SQLite WAL and backups are
sensitive mail data; SQLite encryption and forensic deletion are not provided.
There is no automatic journal-retention purge.

## Gmail creation and recovery

The core rechecks cached source hashes and fetches the selected message before
dispatch. It journals the immutable plan before one creation attempt. Gmail
receives a plain-text RFC message with the authenticated sender, reviewed
recipient/body, thread ID, In-Reply-To, References and an app-generated Message-ID.
The existing gmail.modify permission supports
[draft creation](https://developers.google.com/workspace/gmail/api/reference/rest/v1/users.drafts/create);
no additional OAuth scope is introduced. Reply association follows Gmail's
[thread requirements](https://developers.google.com/workspace/gmail/api/guides/threads).

A failed/cancelled creation or interrupted process may still have created the
draft. MailPilot reports an uncertain outcome and never retries it automatically.
**Reconcile draft** performs only reads, looking up the app Message-ID and
checking the returned thread, headers, recipient and body. Missing search results
do not prove non-creation; edited or duplicate matches remain conflicts. Inspect
Gmail before explicitly marking an uncertain action resolved. This permits a new
separately reviewed action; it does not replay the old action.

Undo is unavailable because the provider exposes no draft deletion operation.
Remove unwanted drafts manually in Gmail. MailPilot never claims removal or
restoration it did not perform.

## Validation

Synthetic FakeAIProvider, in-memory secrets and fake Gmail transports cover
approval separation/replay, context boundaries, all tones, editing, account
isolation, source changes, cancellation, journal failure/restart and reconciliation.
UI tests cover external disclosure, escaped content, approval invalidation,
immediate key clearing and late-result suppression. The shared Go/Rust/TypeScript
contract rejects unknown operations, including sending. Native smoke tests check
the real WebView command denies unauthorized mailbox access.

No live Gmail mutation or paid model request is part of routine validation.
