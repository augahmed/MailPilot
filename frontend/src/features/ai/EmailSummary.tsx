import { useEffect, useRef, useState } from "react";
import { summarizeEmail, type EmailSummary as Summary } from "../../lib/ipc/ai";
const errors: Record<string, string> = {
  ai_disabled: "AI is disabled. Configure a provider in Optional AI settings.",
  context_unavailable: "A complete readable cached body is required.",
  stale_preview: "The email or AI settings changed. Review a new preview.",
  unauthorized: "Reconnect Gmail to use this account’s cached email.",
  cancelled: "Summary cancelled.",
};
function SummarySession({
  accountId,
  emailId,
}: {
  accountId: string;
  emailId: string;
}) {
  const [view, setView] = useState<Summary | null>(null);
  const [approved, setApproved] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const live = useRef(false);
  const sequence = useRef(0);
  const sending = useRef(false);
  const accept = (next: Summary) => {
    setView(next);
    if (next.state !== "preview") setApproved(false);
    if (next.code)
      setError(errors[next.code] || `Summary unavailable (${next.code}).`);
  };
  const acceptRef = useRef(accept);
  useEffect(() => {
    acceptRef.current = accept;
  });
  useEffect(() => {
    live.current = true;
    let active = true;
    const requests = sequence;
    let timer: ReturnType<typeof setTimeout>;
    const poll = async () => {
      const revision = sequence.current;
      try {
        if (sending.current) return;
        const next = await summarizeEmail({
          operation: "status",
          accountId,
          emailId,
        });
        if (active && revision === sequence.current) acceptRef.current(next);
      } catch {
        if (active && revision === sequence.current) {
          setView(null);
          setApproved(false);
          setError("Cannot read summary status.");
        }
      } finally {
        if (active)
          timer = setTimeout(() => {
            void poll();
          }, 1000);
      }
    };
    void poll();
    return () => {
      active = false;
      live.current = false;
      requests.current++;
      clearTimeout(timer);
      void summarizeEmail({ operation: "clear", accountId, emailId }).catch(
        () => {},
      );
    };
  }, [accountId, emailId]);
  const request = async (operation: "preview" | "approve" | "cancel") => {
    const revision = ++sequence.current;
    sending.current = true;
    setBusy(true);
    setError("");
    setApproved(false);
    try {
      const next = await summarizeEmail({
        operation,
        accountId,
        emailId,
        ...(operation === "approve"
          ? { previewId: view?.previewId || "", approved: true }
          : {}),
        ...(operation === "cancel" ? { jobId: view?.jobId || "" } : {}),
      });
      if (live.current && revision === sequence.current)
        acceptRef.current(next);
    } catch {
      if (live.current && revision === sequence.current) {
        setView(null);
        setError("The summary request failed. Preview again to retry.");
      }
    } finally {
      if (revision === sequence.current) {
        sending.current = false;
        if (live.current) setBusy(false);
      }
    }
  };
  return (
    <section aria-labelledby="email-summary-heading">
      <h3 id="email-summary-heading">Optional email summary</h3>
      <p>
        Summarizes this selected cached email and extracts explicit requests and
        stated deadlines. AI output can be wrong; verify it against the email.
      </p>
      <button
        disabled={busy || view?.state === "running"}
        onClick={() => {
          void request("preview");
        }}
      >
        Preview email summary
      </button>
      {view?.state === "preview" && (
        <div>
          <p>
            {view.external
              ? "This email will be sent to"
              : "This email will be processed by"}{" "}
            {view.provider}, model {view.model}. Approval expires at{" "}
            {new Date(view.expiresAt).toLocaleTimeString()}.
          </p>
          {view.truncated && (
            <p role="status">
              The email exceeds the limit. Only the excerpt below will be
              summarized.
            </p>
          )}
          <h4>Exact email payload</h4>
          <pre style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>
            {view.email}
          </pre>
          <details>
            <summary>Summary policy</summary>
            <p>{view.policy}</p>
          </details>
          <label>
            <input
              type="checkbox"
              checked={approved}
              disabled={busy}
              onChange={(e) => setApproved(e.target.checked)}
            />
            I approve processing this email with this provider
          </label>
          <button
            disabled={busy || !approved}
            onClick={() => {
              void request("approve");
            }}
          >
            Summarize selected email
          </button>
        </div>
      )}
      {view?.state === "running" && (
        <>
          <p role="status">Summarizing selected email…</p>
          <button
            disabled={busy}
            onClick={() => {
              void request("cancel");
            }}
          >
            Cancel summary
          </button>
        </>
      )}
      {view?.result && (
        <div>
          <p>{view.result.summary}</p>
          <h4>Explicit requests</h4>
          {view.result.requests.length ? (
            <ul>
              {view.result.requests.map((v, i) => (
                <li key={i}>{v}</li>
              ))}
            </ul>
          ) : (
            <p>No explicit requests found.</p>
          )}
          <h4>Stated deadlines</h4>
          {view.result.deadlines.length ? (
            <ul>
              {view.result.deadlines.map((v, i) => (
                <li key={i}>{v}</li>
              ))}
            </ul>
          ) : (
            <p>No stated deadlines found.</p>
          )}
        </div>
      )}
      {error && <p role="alert">{error}</p>}
    </section>
  );
}

export function EmailSummary(props: { accountId: string; emailId: string }) {
  return (
    <SummarySession key={`${props.accountId}/${props.emailId}`} {...props} />
  );
}
