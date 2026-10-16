import { useEffect, useRef, useState } from "react";
import {
  getMailbox,
  manageReplies,
  type MailboxPage,
  type ReplyRequest,
  type ReplyResponse,
} from "../../lib/ipc/client";
const running = new Set([
  "configuring",
  "generating",
  "creating",
  "reconciling",
]);
const messages: Partial<Record<ReplyResponse["code"], string>> = {
  unauthorized: "Connect Gmail to draft a reply.",
  ai_disabled: "Configure an optional AI provider to generate a reply.",
  ai_credentials_unavailable:
    "The API key is missing or the OS credential store is unavailable. Save the key again or unlock your credential store.",
  context_unavailable:
    "The selected context needs complete cached bodies and valid reply metadata. Sync again or choose another message.",
  stale_preview:
    "This preview expired or its source changed. Prepare a new preview before continuing.",
  outcome_unknown:
    "Gmail may have created the draft. Reconcile before doing anything else; creation will not be retried.",
  journal_unavailable:
    "The draft outcome could not be recorded. Reconcile this action before creating another draft.",
  draft_changed:
    "The matching Gmail draft differs or multiple matches exist. Review it in Gmail; MailPilot will not overwrite it.",
  undo_unavailable:
    "MailPilot cannot remove Gmail drafts. Review or delete the draft manually in Gmail.",
  manually_resolved:
    "The uncertain action was closed after your review. MailPilot did not retry it or change Gmail.",
  ai_rate_limited:
    "The AI provider rate-limited this request. A new preview and approval are required to retry.",
  ai_authentication_failed:
    "The AI provider rejected the configured key or access.",
  ai_refused: "The AI provider declined this request.",
  ai_incomplete:
    "The AI response was incomplete. Try a shorter request with a new preview.",
  response_too_large:
    "The content exceeds the review limit. Use a shorter instruction or selected-message context.",
  cancelled:
    "Work stopped. A dispatched Gmail request may still need reconciliation.",
};
export function ReplyDrafts() {
  const [view, setView] = useState<ReplyResponse | null>(null);
  const [page, setPage] = useState<MailboxPage | null>(null);
  const [offset, setOffset] = useState(0);
  const [refresh, setRefresh] = useState(0);
  const [emailId, setEmailId] = useState("");
  const [thread, setThread] = useState(false);
  const [instruction, setInstruction] = useState("");
  const [tone, setTone] = useState<ReplyRequest["tone"]>("Professional");
  const [model, setModel] = useState("");
  const [body, setBody] = useState("");
  const [aiApproval, setAIApproval] = useState(false);
  const [draftApproval, setDraftApproval] = useState(false);
  const [resolvedApproval, setResolvedApproval] = useState(false);
  const [invalidPreview, setInvalidPreview] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const keyInput = useRef<HTMLInputElement>(null);
  const mounted = useRef(false);
  const sequence = useRef(0);
  const requestInFlight = useRef(false);
  const activeAccount = useRef("");
  const accountId = view?.accountId ?? "";
  const loadedReview = useRef("");
  const loadedAction = useRef("");
  const editor = useRef<HTMLTextAreaElement>(null);
  const clearPrivate = () => {
    setPage(null);
    setEmailId("");
    setInstruction("");
    setBody("");
    setAIApproval(false);
    setDraftApproval(false);
    setResolvedApproval(false);
    loadedReview.current = "";
    loadedAction.current = "";
    if (keyInput.current) keyInput.current.value = "";
  };
  const accept = (r: ReplyResponse) => {
    if (r.code === "unauthorized") {
      activeAccount.current = "";
      clearPrivate();
      setView(r);
      return;
    }
    if (r.code && !r.accountId) {
      setError(
        messages[r.code] ??
          "The operation failed. Review your settings or prepare a new preview.",
      );
      return;
    }
    if (activeAccount.current !== r.accountId) {
      clearPrivate();
      setOffset(0);
      setModel("");
      activeAccount.current = r.accountId;
    }
    setView(r);
    if (
      (r.reviewId && loadedReview.current !== r.reviewId) ||
      (r.actionId && loadedAction.current !== r.actionId)
    ) {
      setBody(r.body);
      setDraftApproval(false);
      loadedReview.current = r.reviewId;
      loadedAction.current = r.actionId;
      if (r.state === "editing") setTimeout(() => editor.current?.focus(), 0);
    }
  };
  const acceptRef = useRef(accept);
  useEffect(() => {
    acceptRef.current = accept;
  });
  useEffect(() => {
    mounted.current = true;
    let active = true;
    let timer: ReturnType<typeof setTimeout>;
    const poll = async () => {
      const current = sequence.current;
      try {
        if (requestInFlight.current) return;
        const r = await manageReplies();
        if (active && mounted.current && current === sequence.current)
          acceptRef.current(r);
      } catch {
        if (active && mounted.current && current === sequence.current)
          setError(
            "Cannot reach reply drafting. Open the desktop app or retry.",
          );
      } finally {
        if (active && mounted.current)
          timer = setTimeout(() => {
            void poll();
          }, 1000);
      }
    };
    void poll();
    return () => {
      mounted.current = false;
      active = false;
      clearTimeout(timer);
    };
  }, []);
  useEffect(() => {
    if (!accountId) return;
    let active = true;
    void getMailbox(offset)
      .then((r) => {
        if (
          active &&
          activeAccount.current === accountId &&
          r.accountId === accountId
        )
          setPage(r);
      })
      .catch(() => {
        if (active) setPage(null);
      });
    return () => {
      active = false;
    };
  }, [offset, refresh, accountId]);
  const request = async (p: Partial<ReplyRequest>) => {
    const current = ++sequence.current;
    requestInFlight.current = true;
    setPending(true);
    setError("");
    try {
      const r = await manageReplies({
        accountId: view?.accountId ?? page?.accountId ?? "",
        ...p,
      });
      if (mounted.current && current === sequence.current) {
        acceptRef.current(r);
        if (p.operation === "prepare" && !r.code) {
          setInvalidPreview(false);
          setAIApproval(false);
        }
      }
    } catch {
      if (mounted.current && current === sequence.current)
        setError(
          "The request failed. Nothing will be sent automatically. Refresh status before retrying.",
        );
    } finally {
      if (current === sequence.current) requestInFlight.current = false;
      if (mounted.current && current === sequence.current) setPending(false);
    }
  };
  const changed = () => {
    setInvalidPreview(true);
    setAIApproval(false);
  };
  const busy = pending || running.has(view?.state ?? "");
  const unresolved = ["uncertain", "conflict"].includes(view?.state ?? "");
  const canEdit = view && ["editing", "draft_preview"].includes(view.state);
  return (
    <section
      className="status-card reply-drafts"
      aria-labelledby="reply-heading"
    >
      <h2 id="reply-heading">Draft a reply</h2>
      <p>
        Generate reply text, review and edit it, then separately approve saving
        a Gmail draft. MailPilot never sends email.
      </p>
      {!view?.accountId ? (
        <p>Connect Gmail to draft a reply.</p>
      ) : (
        <>
          <details>
            <summary>Optional AI settings</summary>
            <p>
              OpenAI is an external provider. Your API key is saved only in the
              OS credential store. Enabling AI does not authorize uploading
              email.
            </p>
            <form
              onSubmit={(e) => {
                e.preventDefault();
                const apiKey = keyInput.current?.value ?? "";
                if (keyInput.current) keyInput.current.value = "";
                changed();
                void request({ operation: "configure", model, apiKey });
              }}
            >
              <label>
                OpenAI model{" "}
                <input
                  value={model}
                  onChange={(e) => setModel(e.target.value)}
                  maxLength={128}
                  required
                  disabled={busy}
                  placeholder="Model ID available to your project"
                />
              </label>
              <label>
                OpenAI API key{" "}
                <input
                  ref={keyInput}
                  type="password"
                  autoComplete="off"
                  spellCheck={false}
                  maxLength={2048}
                  disabled={busy}
                />
              </label>
              <p>
                Leave the key blank to keep the saved key. Configured model:{" "}
                {view.configured ? view.model : "None"}.
              </p>
              <button disabled={busy} type="submit">
                Save AI settings
              </button>
              <button
                disabled={busy || !view.configured}
                type="button"
                onClick={() => {
                  changed();
                  void request({ operation: "disable" });
                }}
              >
                Disable AI and remove key
              </button>
            </form>
          </details>
          {!view.configured && (
            <p>
              AI generation is disabled. Configure an optional provider above;
              other mailbox features remain available.
            </p>
          )}
          <fieldset disabled={busy || unresolved}>
            <legend>Select reply context</legend>
            <button
              type="button"
              onClick={() => setRefresh((value) => value + 1)}
            >
              Refresh cached emails
            </button>
            <label>
              Email{" "}
              <select
                value={emailId}
                onChange={(e) => {
                  setEmailId(e.target.value);
                  changed();
                }}
              >
                <option value="">Choose a cached email</option>
                {(page?.accountId === accountId ? page.rows : []).map((m) => (
                  <option key={m.id} value={m.id}>
                    {m.sender} — {m.subject || "(No subject)"}
                  </option>
                ))}
              </select>
            </label>
            <button
              type="button"
              disabled={offset === 0}
              onClick={() => {
                setOffset(Math.max(0, offset - 10));
                setEmailId("");
                changed();
              }}
            >
              Previous emails
            </button>
            <button
              type="button"
              disabled={!page?.hasMore}
              onClick={() => {
                setOffset(offset + 10);
                setEmailId("");
                changed();
              }}
            >
              Next emails
            </button>
            <label>
              <input
                type="checkbox"
                checked={thread}
                onChange={(e) => {
                  setThread(e.target.checked);
                  changed();
                }}
              />
              Include earlier cached messages from this thread
            </label>
            <p>
              Uses up to five cached messages through the selected email, with
              quoted history and signatures removed. The cache may not contain
              the whole thread. Attachments and unrelated messages are excluded.
            </p>
            <label>
              Tone{" "}
              <select
                value={tone}
                onChange={(e) => {
                  setTone(e.target.value as ReplyRequest["tone"]);
                  changed();
                }}
              >
                {["Professional", "Friendly", "Concise", "Formal"].map((t) => (
                  <option key={t}>{t}</option>
                ))}
              </select>
            </label>
            <label>
              Optional instruction{" "}
              <textarea
                value={instruction}
                onChange={(e) => {
                  setInstruction(e.target.value);
                  changed();
                }}
                maxLength={250}
              />
            </label>
            <button
              type="button"
              disabled={!emailId || !view.configured}
              onClick={() => {
                void request({
                  operation: "prepare",
                  emailId,
                  includeThread: thread,
                  tone,
                  instruction,
                });
              }}
            >
              Preview AI context
            </button>
          </fieldset>
          {view.state === "ai_preview" && !invalidPreview && (
            <div>
              <h3>Approve AI disclosure</h3>
              <p>
                {view.external
                  ? `The email content below will be sent to external provider ${view.provider}, model ${view.model}.`
                  : "The content below will be processed by the configured local model."}{" "}
                No content is sent until you approve.
              </p>
              {view.external && (
                <p>
                  External provider retention policies apply. MailPilot requests
                  that the response is not stored for later API retrieval.
                </p>
              )}
              <p>
                {view.sourceCount} selected message(s).{" "}
                {view.truncated && "Context was shortened to the review limit."}
              </p>
              <p>
                Draft recipient: {view.to}. Subject: {view.subject}.
              </p>
              <h4>Policy instructions</h4>
              <pre>{view.policy}</pre>
              <h4>Your request</h4>
              <pre>{view.instruction}</pre>
              <h4>Exact email context</h4>
              <pre>{"Untrusted email context:\n" + view.context}</pre>
              <p>
                Maximum output: 1,200 tokens. Approval expires at{" "}
                {new Date(view.expiresAt).toLocaleTimeString()}.
              </p>
              <label>
                <input
                  type="checkbox"
                  checked={aiApproval}
                  onChange={(e) => setAIApproval(e.target.checked)}
                  disabled={busy}
                />
                I approve this exact AI request
              </label>
              <button
                disabled={busy || !aiApproval}
                onClick={() => {
                  setAIApproval(false);
                  void request({
                    operation: "generate",
                    previewId: view.previewId,
                    approved: true,
                  });
                }}
              >
                Generate proposed reply
              </button>
            </div>
          )}
          {canEdit && (
            <div>
              <h3>Review and edit proposed reply</h3>
              <p>To: {view.to}</p>
              <p>Subject: {view.subject}</p>
              <label>
                Reply body{" "}
                <textarea
                  ref={editor}
                  value={body}
                  rows={10}
                  maxLength={6000}
                  disabled={busy}
                  onChange={(e) => {
                    setBody(e.target.value);
                    setDraftApproval(false);
                  }}
                />
              </label>
              <p>
                Check names, facts and commitments. Edits stay local until you
                confirm Gmail draft creation.
              </p>
              <button
                disabled={busy || !body.trim()}
                onClick={() => {
                  setDraftApproval(false);
                  void request({
                    operation: "preview_draft",
                    previewId: view.reviewId,
                    body,
                  });
                }}
              >
                Preview Gmail draft
              </button>
            </div>
          )}
          {view.state === "draft_preview" && body === view.body && (
            <div>
              <h3>Confirm Gmail draft</h3>
              <p>To: {view.to}</p>
              <p>Subject: {view.subject}</p>
              <pre>{view.body}</pre>
              <p>
                This saves a draft in Gmail. It does not send it. Draft removal
                is manual in Gmail; MailPilot has no draft deletion capability.
              </p>
              <label>
                <input
                  type="checkbox"
                  checked={draftApproval}
                  onChange={(e) => setDraftApproval(e.target.checked)}
                  disabled={busy}
                />
                I approve creating this exact Gmail draft
              </label>
              <button
                disabled={busy || !draftApproval}
                onClick={() => {
                  setDraftApproval(false);
                  void request({
                    operation: "create",
                    actionId: view.actionId,
                    previewId: view.previewId,
                    approved: true,
                  });
                }}
              >
                Create Gmail draft
              </button>
            </div>
          )}
          {running.has(view.state) && (
            <div role="status">
              <p>
                {view.state === "generating"
                  ? "Generating proposed reply…"
                  : view.state === "creating"
                    ? "Creating Gmail draft…"
                    : view.state === "reconciling"
                      ? "Checking Gmail for this draft…"
                      : "Updating AI settings…"}
              </p>
              <button
                disabled={pending}
                onClick={() => {
                  void request({ operation: "cancel", jobId: view.jobId });
                }}
              >
                Cancel reply operation
              </button>
            </div>
          )}
          {unresolved && (
            <div>
              <p role="alert">
                This draft has an uncertain or conflicting outcome. No automatic
                retry will occur.
              </p>
              <button
                disabled={busy}
                onClick={() => {
                  void request({
                    operation: "reconcile",
                    actionId: view.actionId,
                  });
                }}
              >
                Reconcile draft
              </button>
              {view.reconciled && (
                <>
                  <label>
                    <input
                      type="checkbox"
                      checked={resolvedApproval}
                      onChange={(e) => setResolvedApproval(e.target.checked)}
                    />
                    I checked Gmail and want to close this action without
                    retrying it
                  </label>
                  <button
                    disabled={busy || !resolvedApproval}
                    onClick={() => {
                      setResolvedApproval(false);
                      void request({
                        operation: "resolve",
                        actionId: view.actionId,
                        approved: true,
                      });
                    }}
                  >
                    Close reviewed action
                  </button>
                </>
              )}
            </div>
          )}
          {view.state === "created" && (
            <div role="status">
              <p>
                Gmail draft created. Nothing was sent. Review and edit the draft
                in Gmail.
              </p>
              <p>
                Undo is unavailable in MailPilot. You can delete the draft
                manually in Gmail.
              </p>
            </div>
          )}
        </>
      )}
      {view?.code && view.code !== "unauthorized" && (
        <p role="alert">
          {messages[view.code] ??
            "The operation could not be completed. Review the current state and prepare a new preview if needed."}
        </p>
      )}
      {error && <p role="alert">{error}</p>}
    </section>
  );
}
