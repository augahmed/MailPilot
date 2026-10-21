import { useEffect, useRef, useState } from "react";
import {
  manageReplies,
  type ReplyRequest,
  type ReplyResponse,
} from "../../lib/ipc/client";

// Historical journal recovery has no AI provider or new-draft capability.
export function AIMaintenance() {
  const [view, setView] = useState<ReplyResponse | null>(null);
  const [approved, setApproved] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const mounted = useRef(false);
  const sequence = useRef(0);
  const inFlight = useRef(false);
  const identity = useRef("");
  const accept = (r: ReplyResponse) => {
    const next = `${r.accountId}/${r.actionId}`;
    if (identity.current !== next || r.code === "unauthorized") {
      setApproved(false);
      identity.current = next;
    }
    setView(r.code === "unauthorized" ? null : r);
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
        if (inFlight.current) return;
        const r = await manageReplies();
        if (active && current === sequence.current) acceptRef.current(r);
      } catch {
        if (active && current === sequence.current) {
          setView(null);
          setApproved(false);
          identity.current = "";
          setError("Cannot reach historical draft recovery.");
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
      mounted.current = false;
      clearTimeout(timer);
    };
  }, []);
  const request = async (p: Partial<ReplyRequest>) => {
    const current = ++sequence.current;
    inFlight.current = true;
    setPending(true);
    setError("");
    try {
      const r = await manageReplies({ accountId: view?.accountId ?? "", ...p });
      if (mounted.current && current === sequence.current) acceptRef.current(r);
    } catch {
      if (mounted.current && current === sequence.current) {
        setView(null);
        setApproved(false);
        setError("The request failed. No draft creation will be retried.");
      }
    } finally {
      if (current === sequence.current) {
        inFlight.current = false;
        if (mounted.current) setPending(false);
      }
    }
  };
  const busy = pending || view?.state === "reconciling";
  const unresolved = view?.state === "uncertain" || view?.state === "conflict";
  return (
    <section className="status-card" aria-labelledby="ai-maintenance-heading">
      <h2 id="ai-maintenance-heading">Historical draft recovery</h2>
      <p>
        AI is optional. Reply generation and new Gmail draft creation are
        retired.
      </p>
      {!view?.accountId ? (
        <p>Connect Gmail to recover historical actions.</p>
      ) : (
        <>
          {view.actionId && (
            <div>
              <h3>Historical Gmail draft action</h3>
              <p>
                Recorded recipient: {view.to}. Subject: {view.subject}.
              </p>
              {view.state === "draft_preview" ? (
                <p>
                  This historical preview was not submitted. Creating it is
                  disabled.
                </p>
              ) : (
                <p>Recorded outcome: {view.state}.</p>
              )}
              <p>
                Undo is unavailable. Review or remove the draft manually in
                Gmail. MailPilot only checks the existing outcome.
              </p>
              {unresolved && (
                <>
                  <p role="alert">
                    The outcome is uncertain or conflicting. It will never be
                    automatically retried.
                  </p>
                  <button
                    disabled={busy}
                    onClick={() => {
                      setApproved(false);
                      void request({
                        operation: "reconcile",
                        actionId: view.actionId,
                      });
                    }}
                  >
                    Reconcile historical draft
                  </button>
                  {view.reconciled && (
                    <>
                      <label>
                        <input
                          type="checkbox"
                          checked={approved}
                          disabled={busy}
                          onChange={(e) => setApproved(e.target.checked)}
                        />
                        I checked Gmail and want to close this action without
                        retrying it
                      </label>
                      <button
                        disabled={busy || !approved}
                        onClick={() => {
                          setApproved(false);
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
                </>
              )}
            </div>
          )}
          {busy && (
            <p role="status">
              {view.state === "reconciling"
                ? "Checking the historical Gmail draft…"
                : "Updating historical action…"}
            </p>
          )}
          {view.jobId && busy && (
            <button
              disabled={pending}
              onClick={() => {
                void request({ operation: "cancel", jobId: view.jobId });
              }}
            >
              Cancel maintenance operation
            </button>
          )}
          {view.code && (
            <p role="alert">
              Operation status: {view.code}. No new draft will be created.
            </p>
          )}
        </>
      )}
      {error && <p role="alert">{error}</p>}
    </section>
  );
}
