import { useEffect, useRef, useState } from "react";
import {
  answerEmail,
  answerRequest,
  type AnswerRequest,
  type AnswerResponse,
  type AnswerEvidence,
} from "../../lib/ipc/answers";
import { emptyFilter } from "../../lib/ipc/search";
import type { OpenMail } from "../../lib/ipc/reader";

const codes: Record<string, string> = {
  unauthorized: "Connect Gmail to ask about cached email.",
  ai_disabled:
    "Enable a provider in Optional AI settings, then retrieve evidence again.",
  ai_credentials_unavailable:
    "The selected provider's OS-stored key is unavailable.",
  stale_preview:
    "The approved context changed or expired. Retrieve and review fresh evidence.",
  stale: "Cached evidence changed. Retrieve fresh evidence.",
  cancelled:
    "Email question cancelled. Already dispatched content may have reached the provider.",
  busy: "The previous local or AI job is stopping. Try again shortly.",
  validation: "Use a question up to 2,048 UTF-8 bytes and valid filters.",
  ai_invalid_response:
    "The provider returned invalid citations or unsupported output. Review a fresh preview before retrying.",
  ai_rate_limited:
    "The provider rate-limited this request. No automatic retry was made.",
  ai_authentication_failed:
    "The provider rejected its key. Update Optional AI settings.",
  ai_refused: "The provider declined this request.",
  ai_incomplete: "The provider returned incomplete output.",
  non_exhaustive:
    "Top-K retrieval cannot establish mailbox-wide totals or completeness. Ask a question about specific source information.",
  response_too_large:
    "The evidence or answer exceeded the safe response limit. Use a narrower question.",
  timeout: "Local retrieval timed out. Narrow the question or filters.",
  operation_failed:
    "The request failed. Retrieve and approve fresh evidence to try again.",
  storage_error: "Local evidence storage is unavailable.",
  unsupported: "Email questions are unavailable.",
  model_unavailable:
    "The local model is unavailable. Try again using the keyword fallback.",
  inference_failed: "Local retrieval failed.",
  too_large: "The evidence exceeds the safe response limit.",
};

export function EmailQuestion({
  onOpen,
  onEvidence,
}: {
  onOpen: OpenMail;
  onEvidence: (e: { text: string; revision: string }) => void;
}) {
  const [view, setView] = useState<AnswerResponse | null>(null);
  const [account, setAccount] = useState("");
  const [question, setQuestion] = useState("");
  const [sender, setSender] = useState("");
  const [after, setAfter] = useState("");
  const [before, setBefore] = useState("");
  const [approved, setApproved] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const mounted = useRef(false),
    pending = useRef(false),
    epoch = useRef(0);
  const current = useRef<AnswerResponse | null>(null),
    scope = useRef("");
  const accept = (r: AnswerResponse) => {
    if (r.previewId !== current.current?.previewId || r.state !== "preview")
      setApproved(false);
    if (r.code === "unauthorized") {
      scope.current = "";
      setAccount("");
    } else if (r.accountId) {
      scope.current = r.accountId;
      setAccount(r.accountId);
    }
    current.current = r;
    setView(r);
    setError("");
  };
  useEffect(() => {
    mounted.current = true;
    epoch.current++;
    let active = true;
    let timer: ReturnType<typeof setTimeout>;
    const poll = async () => {
      const revision = epoch.current;
      try {
        const r = await answerEmail(answerRequest("status", scope.current));
        if (active && revision === epoch.current && !pending.current) accept(r);
      } catch {
        if (active && revision === epoch.current && !pending.current) {
          current.current = null;
          setView(null);
          setError("Could not read email question status.");
        }
      } finally {
        if (active)
          timer = setTimeout(
            () => {
              void poll();
            },
            ["retrieving", "running"].includes(current.current?.state ?? "")
              ? 350
              : 5000,
          );
      }
    };
    void poll();
    return () => {
      active = false;
      mounted.current = false;
      clearTimeout(timer);
      if (scope.current)
        void answerEmail(answerRequest("clear", scope.current)).catch(() => {});
    };
  }, []);
  const act = async (request: AnswerRequest) => {
    if (pending.current) return;
    pending.current = true;
    const revision = ++epoch.current;
    setBusy(true);
    setApproved(false);
    setError("");
    try {
      const r = await answerEmail(request);
      if (mounted.current && revision === epoch.current) accept(r);
    } catch {
      if (mounted.current && revision === epoch.current) {
        current.current = null;
        setView(null);
        setError(
          "The request could not complete. Review fresh evidence before trying again.",
        );
      }
    } finally {
      pending.current = false;
      epoch.current++;
      if (mounted.current) setBusy(false);
    }
  };
  const invalidate = () => {
    setApproved(false);
    if (
      current.current &&
      ["preview", "completed", "retrieving", "running"].includes(
        current.current.state,
      ) &&
      !pending.current &&
      scope.current
    ) {
      current.current = null;
      setView(null);
      void act(answerRequest("clear", scope.current));
    }
  };
  const running = view?.state === "retrieving" || view?.state === "running";
  const preview = view?.state === "preview";
  const open = (e: AnswerEvidence, button: HTMLButtonElement) => {
    if (!view?.accountId) return;
    onEvidence({ text: e.excerpt, revision: e.readerRevision });
    onOpen(view.accountId, e.emailId, button);
  };
  return (
    <section
      className="status-card feature-panel email-question"
      aria-labelledby="email-question-heading"
      aria-busy={busy || running}
    >
      <h2 id="email-question-heading">Ask about email</h2>
      <p>
        Retrieve source excerpts locally, inspect the exact material, then
        separately approve one AI request. The provider can answer from selected
        excerpts; it has no mailbox tools or action permissions.
      </p>
      <form
        onSubmit={(event) => {
          event.preventDefault();
          if (account)
            void act(
              answerRequest("retrieve", account, {
                question,
                filter: {
                  ...emptyFilter,
                  sender,
                  after: after ? Date.parse(`${after}T00:00:00Z`) : 0,
                  before: before ? Date.parse(`${before}T00:00:00Z`) : 0,
                },
              }),
            );
        }}
      >
        <label className="question-field">
          Email question
          <textarea
            required
            placeholder="What would you like to find out from your cached email?"
            maxLength={2048}
            value={question}
            onChange={(e) => {
              invalidate();
              setQuestion(e.target.value);
            }}
            disabled={busy}
          />
        </label>
        <fieldset className="filter-grid">
          <legend>Narrow the evidence (optional)</legend>
          <label>
            Evidence sender contains
            <input
              value={sender}
              maxLength={200}
              onChange={(e) => {
                invalidate();
                setSender(e.target.value);
              }}
              disabled={busy}
            />
          </label>
          <label>
            Evidence received on or after (UTC)
            <input
              type="date"
              value={after}
              onChange={(e) => {
                invalidate();
                setAfter(e.target.value);
              }}
              disabled={busy}
            />
          </label>
          <label>
            Evidence received before (UTC)
            <input
              type="date"
              value={before}
              onChange={(e) => {
                invalidate();
                setBefore(e.target.value);
              }}
              disabled={busy}
            />
          </label>
        </fieldset>
        <button disabled={busy || running || !account || !question.trim()}>
          Retrieve evidence locally
        </button>
      </form>
      <p className="feature-status" role="status">
        {error ||
          (view?.code
            ? (codes[view.code] ??
              "Email questions are unavailable. Retrieve fresh evidence before retrying.")
            : view?.state === "retrieving"
              ? "Retrieving cached excerpts locally…"
              : view?.state === "running"
                ? "Generating from the approved evidence…"
                : preview
                  ? "Inspect the selected sources and exact request before approving."
                  : view?.state === "completed"
                    ? "Email question completed."
                    : "No question or email is sent externally until you approve a preview.")}
      </p>
      {running && (
        <button
          disabled={busy}
          onClick={() => {
            if (view?.jobId)
              void act(answerRequest("cancel", account, { jobId: view.jobId }));
          }}
        >
          Cancel email question
        </button>
      )}
      {view?.accountId && (preview || view.state === "completed") && (
        <p>
          Keyword coverage: {view.coverage.keyword} of {view.coverage.eligible}.
          Semantic coverage: {view.coverage.semantic} of{" "}
          {view.coverage.eligible}.{" "}
          {view.coverage.semantic < view.coverage.eligible &&
            "Semantic index coverage is partial. "}
          Search returns a limited selection even when the cached index is
          complete.{" "}
          {view.warning === "semantic_unavailable" &&
            "Semantic retrieval unavailable; keyword evidence was used."}
        </p>
      )}
      {(preview || view?.state === "completed") && view && (
        <>
          <p>Question: {view.question}</p>
          <p>
            Provider: {view.provider} · Model: {view.model} ·{" "}
            {view.external ? "External generation" : "Local generation"}
          </p>
          {view.aggregate && (
            <p role="alert">
              This question may require a mailbox-wide aggregate or exhaustive
              search. Top-K evidence cannot establish that; generation is
              blocked. Ask a more specific question.
            </p>
          )}
          <h3>{preview ? "Choose source excerpts" : "Source excerpts"}</h3>
          <ol className="email-results source-results">
            {view.evidence.map((source) => (
              <li key={source.sourceId}>
                {preview && (
                  <label>
                    <input
                      type="checkbox"
                      checked={view.selectedIds.includes(source.sourceId)}
                      disabled={
                        busy ||
                        (view.selectedIds.length === 1 &&
                          view.selectedIds.includes(source.sourceId))
                      }
                      onChange={(e) => {
                        const ids = e.target.checked
                          ? [...view.selectedIds, source.sourceId]
                          : view.selectedIds.filter(
                              (id) => id !== source.sourceId,
                            );
                        void act(
                          answerRequest("select", account, {
                            previewId: view.previewId,
                            sourceIds: ids,
                          }),
                        );
                      }}
                    />
                    Include {source.sourceId}:{" "}
                    {source.subject || "(No subject)"}
                  </label>
                )}
                <button onClick={(e) => open(source, e.currentTarget)}>
                  Open source {source.sourceId}
                </button>
                <p>
                  {source.sender} ·{" "}
                  {new Date(source.receivedAt).toLocaleString()}{" "}
                  {source.partialContent && "· Partial cached content"}
                </p>
                <blockquote>{source.excerpt}</blockquote>
                <details>
                  <summary>Source identifiers and revisions</summary>
                  <p>
                    Email: {source.emailId} · Chunk: {source.chunkId}
                  </p>
                  <p>Source revision: {source.sourceRevision}</p>
                  <p>Reader revision: {source.readerRevision}</p>
                </details>
              </li>
            ))}
          </ol>
        </>
      )}
      {preview && view && (
        <>
          <p>
            Approval expires {new Date(view.expiresAt).toLocaleString()}.
            Changing the question, filters, sources, provider, key or cached
            content invalidates approval.
          </p>
          <details>
            <summary>Exact external request</summary>
            <p>
              The provider receives this fixed policy and this complete
              user-message JSON, using the provider/model above. No additional
              email context is included.
            </p>
            <h4>Policy</h4>
            <pre>{view.policy}</pre>
            <h4>Question and selected source material</h4>
            <pre>{view.payload}</pre>
          </details>
          <label>
            <input
              type="checkbox"
              checked={approved}
              disabled={busy || view.aggregate}
              onChange={(e) => setApproved(e.target.checked)}
            />
            I reviewed and approve sending this exact question, selected
            evidence and policy to {view.provider}/{view.model} for one request.
          </label>
          <button
            disabled={busy || !approved || view.aggregate}
            onClick={() =>
              void act(
                answerRequest("approve", account, {
                  previewId: view.previewId,
                  digest: view.digest,
                  approved: true,
                }),
              )
            }
          >
            Approve and generate cited answer
          </button>
          <p>
            Generation may incur provider charges. Requests are never
            automatically retried; cancellation cannot recall content already
            dispatched.
          </p>
        </>
      )}
      {view?.state === "completed" && view.result && (
        <div className="answer-card">
          <h3>
            {view.result.outcome === "insufficient"
              ? "Insufficient evidence"
              : view.result.outcome === "conflicting"
                ? "Conflicting evidence"
                : "Answer from selected excerpts"}
          </h3>
          {view.result.statements.map((statement, i) => (
            <div key={i}>
              <p>{statement.text}</p>
              <ul>
                {statement.citations.map((citation, j) => {
                  const source = view.evidence.find(
                    (e) => e.sourceId === citation.sourceId,
                  );
                  return (
                    <li key={j}>
                      <blockquote>{citation.quote}</blockquote>
                      {source && (
                        <button onClick={(e) => open(source, e.currentTarget)}>
                          Read citation {citation.sourceId}
                        </button>
                      )}
                    </li>
                  );
                })}
              </ul>
            </div>
          ))}
          <ul>
            {view.result.limitations.map((limitation, i) => (
              <li key={i}>{limitation}</li>
            ))}
          </ul>
        </div>
      )}
      {view && ["preview", "completed", "error"].includes(view.state) && (
        <button
          disabled={busy}
          onClick={() => {
            if (account) void act(answerRequest("clear", account));
          }}
        >
          Clear email question
        </button>
      )}
    </section>
  );
}
