# Optional OpenAI setup

MailPilot's Gmail reading, local indexing, keyword/semantic/hybrid search,
classification, phishing explanations and reviewed label organization work without
an OpenAI account or key. The synthetic demo disables all external AI and has no
key-entry flow. Use this guide only for the optional selected-email **Summary**
or **Ask About Email** workflow in your connected personal app.

## Set up in five steps

1. Sign in to [OpenAI Platform](https://platform.openai.com/) and create/select a
   project you control. Create a project API key through the dashboard's API-key
   page. Keep it private. The [official quickstart](https://developers.openai.com/api/docs/quickstart)
   explains account/key creation; MailPilot uses its own OS-keyring workflow,
   so no SDK installation or environment-key export is needed.
2. Check the project's **API billing/credits**, usage limits and model permissions.
   Do not assume a ChatGPT subscription supplies API credit. Review the current
   [API pricing](https://developers.openai.com/api/docs/pricing) and your billing
   dashboard before approving a request. Rates and account access can change;
   this guide promises no free allowance or spending cap.
3. In connected MailPilot, open the account menu → **Optional AI settings**.
   Select `openai`. Enter an accessible Responses API text model ID, for example
   `gpt-4.1-mini` if it is available to your project. The official
   [model page](https://developers.openai.com/api/docs/models/gpt-4.1-mini) lists
   Responses support and usage-tier limits; this is a compatibility example,
   not a paid-model quality qualification by MailPilot.
4. Paste the key into **Provider API key** and choose **Save AI settings**.
   The input clears immediately, including on failure. Successful saving shows
   `AI: openai / <model>`. The key is stored only in the OS credential store.
   **Saving makes no paid validation request and uploads no mail**; it does not
   prove the key, billing or model access will work. Blank key input reuses that
   provider's saved key, if present.
5. Only if desired, prepare a request. In a selected email, use **Summary** and
   review its exact payload/policy disclosure before approval. In **Ask About
   Email**, enter a bounded question, retrieve local evidence, select excerpts,
   inspect the exact question/excerpts/policy/provider/model, then separately
   approve that request. Approval for one workflow never approves the other.

Never put the key in Google client JSON, source, `.env`, shell arguments, build
resources, SQLite, screenshots or issues. The packaged app must never contain
someone's AI key. Unlock/authorize your OS credential store when prompted;
MailPilot fails closed rather than writing a plaintext fallback.

## Model and request compatibility

The [shipped adapter](../backend/internal/ai/openai/provider.go) calls the fixed
`https://api.openai.com/v1/responses` endpoint. It sends text instructions and one
bounded user input, `max_output_tokens`, `store:false` and `stream:false`.
It supplies no tools, conversation history, Gmail authority, arbitrary endpoints
or background mode. It expects completed assistant text containing the workflow's
small JSON result, then strictly validates fields, sizes and source quotes.
There is no API-level Structured Outputs schema in this adapter; JSON is requested
in instructions and checked by Go afterward.

Use a model that supports foreground Responses text output and these request
fields, and that your API project is allowed to use. A syntactically accepted model
ID is not a guarantee of compatibility. Refusals, incomplete output, invalid JSON,
invalid citations, output-budget exhaustion and unsupported model features can
cause a safe failure instead of a displayed answer. No live paid-model test was
performed for this guide. See the [AI contract](GENERATIVE_AI.md) and
[evidence-answer contract](EMAIL_QUESTIONS.md) for bounds and validation.

## Charges, retention and cancellation

An approved dispatch can incur provider charges even if MailPilot later rejects
its output, times out or cancels. The adapter has a one-minute timeout and no
automatic retry. Cancellation stops local work and suppresses stale results; it
cannot recall a submitted payload, promise that server processing stopped, or
reverse charges. A retry requires preparing/approving a fresh request.

MailPilot requests `store:false`, but **this is not zero retention**. OpenAI's
[data controls](https://developers.openai.com/api/docs/guides/your-data) state that
API content is not used for training by default unless you opt in. Default abuse
monitoring can retain content for up to 30 days, with policy/legal exceptions;
model-specific prompt caching and account controls also apply. MailPilot does
not grant Zero Data Retention status or delete provider-side copies. Check the
current provider policy and your project settings before sending sensitive mail.
Official guidance checked **2026-10-27**.

Go binds approval to the exact payload, account/session, source revisions,
provider/model/settings/key identity and expiry. Relevant changes revoke pending
approval and suppress stale output. Citation/source validation checks provenance,
not factual correctness. MailPilot keeps no persistent answer history; transient
workflow state is cleared on relevant lifecycle changes. This is not physical
erasure of memory, swap or provider data.

## Disable and remove the key

Before disconnecting Gmail, open **Optional AI settings → Disable AI and remove
selected provider key**. This disables AI and attempts to delete the **saved**
provider's key, preserving other providers' slots. If deletion fails, AI remains
disabled: unlock the OS store and retry the same action, which stays available
while disabled. An unsaved dropdown change does not retarget removal.

Disconnecting Gmail does **not** remove AI keys or cached mail. Revoke an exposed
key through OpenAI Platform as well; deleting the local key cannot revoke it at
the provider. See [Privacy](PRIVACY.md) for retention and OS-store limits.

## Troubleshooting

| Symptom                              | Check                                                                                                                                            |
| ------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------ |
| No AI settings/key entry             | Connect your personal Gmail session; external services are intentionally disabled in the synthetic demo                                          |
| Saved successfully, request rejected | Check API billing, key/project permissions and model availability; saving makes no paid access check                                             |
| Credential-store error               | Unlock/allow the OS store and retry; do not move the key to a file or environment variable                                                       |
| Approval expired or request changed  | Rebuild the exact preview and approve it again after reviewing the disclosure                                                                    |
| No usable evidence                   | Sync relevant cached mail, inspect search coverage and explicitly Start/Resume local indexing if needed; missing evidence does not prove absence |
| Invalid/incomplete answer            | Review model compatibility; do not treat rejected output as evidence or automatically retry a paid call                                          |
| Key removal failed                   | Keep AI disabled, unlock the OS store and retry removal before disconnecting                                                                     |
