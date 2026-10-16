import { useEffect, useRef, useState } from "react";
import {
  manageReplies,
  type ReplyRequest,
  type ReplyResponse,
} from "../../lib/ipc/client";

// Settings remain reusable for a future selected-email summary. No generation
// or new draft operation is reachable from this component.
export function AIMaintenance() {
  const [view, setView] = useState<ReplyResponse | null>(null);
  const [model, setModel] = useState("");
  const [approved, setApproved] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const key = useRef<HTMLInputElement>(null);
  const mounted = useRef(false);
  const sequence = useRef(0);
  const inFlight = useRef(false);
  const identity = useRef("");
  const accept = (r: ReplyResponse) => {
    const next = `${r.accountId}/${r.actionId}`;
    if (identity.current !== next || r.code === "unauthorized") {
      setApproved(false);
      setModel("");
      if (key.current) key.current.value = "";
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
          setModel("");
          identity.current = "";
          if (key.current) key.current.value = "";
          setError("Cannot reach AI settings or historical draft recovery.");
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
        if (key.current) key.current.value = "";
        setError("The request failed. No draft creation will be retried.");
      }
    } finally {
      if (current === sequence.current) {
        inFlight.current = false;
        if (mounted.current) setPending(false);
      }
    }
  };
  const busy =
    pending || view?.state === "configuring" || view?.state === "reconciling";
  const unresolved = view?.state === "uncertain" || view?.state === "conflict";
  return (
    <section className="status-card" aria-labelledby="ai-maintenance-heading">
      <h2 id="ai-maintenance-heading">
        AI settings and historical draft recovery
      </h2>
      <p>
        AI is optional. Reply generation and new Gmail draft creation are
        retired.
      </p>
      {!view?.accountId ? (
        <p>Connect Gmail to manage settings or recover historical actions.</p>
      ) : (
        <>
          <details>
            <summary>Optional AI settings</summary>
            <p>
              OpenAI is the currently installed adapter. Settings are retained
              for the future single-email summary workflow; saving them does not
              upload email or enable generation.
            </p>
            <form
              onSubmit={(e) => {
                e.preventDefault();
                const apiKey = key.current?.value ?? "";
                if (key.current) key.current.value = "";
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
                />
              </label>
              <label>
                OpenAI API key{" "}
                <input
                  ref={key}
                  type="password"
                  autoComplete="off"
                  spellCheck={false}
                  maxLength={2048}
                  disabled={busy}
                />
              </label>
              <p>
                Keys use OS credential storage only. Leave blank to keep the
                saved key. Configured model:{" "}
                {view.configured ? view.model : "None"}.
              </p>
              <button type="submit" disabled={busy}>
                Save AI settings
              </button>
              <button
                type="button"
                disabled={busy || !view.configured}
                onClick={() => {
                  void request({ operation: "disable" });
                }}
              >
                Disable AI and remove key
              </button>
            </form>
          </details>
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
                : "Updating settings…"}
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
