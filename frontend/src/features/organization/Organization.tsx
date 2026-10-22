import { useCallback, useEffect, useRef, useState } from "react";
import { ConfirmDialog } from "../../components/ConfirmDialog";
import {
  organize,
  type OrganizationRequest,
  type OrganizationResponse,
} from "../../lib/ipc/client";

const messages: Record<string, string> = {
  unauthorized: "Connect Gmail to organize this account’s mail.",
  stale_preview:
    "Mail, categories, predictions, or labels changed. Generate a new preview.",
  reconciliation_required:
    "An interrupted batch needs reconciliation before another batch can run.",
  not_found: "There is no eligible batch or prediction to review.",
  cancelled: "Work stopped. Review recorded outcomes before continuing.",
  reauth_required: "Reconnect Gmail before continuing.",
  unsupported: "This provider does not support the required label operations.",
  operation_failed:
    "The operation failed. Refresh recorded outcomes before continuing.",
  validation: "This selection exceeds supported limits or is invalid.",
  busy: "An earlier account operation is still stopping.",
};
export function Organization() {
  const [source, setSource] = useState<"" | "phishing">("");
  const [page, setPage] = useState<OrganizationResponse | null>(null);
  const [batch, setBatch] = useState<OrganizationResponse | null>(null);
  const [selected, setSelected] = useState<string[]>([]);
  const [offset, setOffset] = useState(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [confirmed, setConfirmed] = useState(false);
  const [confirmation, setConfirmation] = useState<{
    id: string;
    digest: string;
  } | null>(null);
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, []);
  const mounted = useRef(false);
  const pending = useRef(false);
  const revision = useRef(0);
  const account = useRef("");
  const clearAccount = useCallback(() => {
    account.current = "";
    setPage(null);
    setBatch(null);
    setSelected([]);
    setConfirmed(false);
    setConfirmation(null);
  }, []);
  useEffect(() => {
    let active = true;
    const current = ++revision.current;
    mounted.current = true;
    async function load() {
      try {
        const result = await organize({
          operation: "list",
          offset,
          source: source || undefined,
        });
        if (!active || pending.current || current !== revision.current) return;
        if (account.current && account.current !== result.accountId) {
          setBatch(null);
          setSelected([]);
          setConfirmed(false);
          setConfirmation(null);
        }
        account.current = result.accountId;
        setPage(result);
        setError(messages[result.code] ?? "");
        if (!result.code) {
          const saved = await organize({
            operation: "status",
            accountId: result.accountId,
          });
          if (active && !pending.current && current === revision.current) {
            if (
              saved.code === "unauthorized" ||
              (saved.accountId && saved.accountId !== result.accountId)
            ) {
              clearAccount();
              setError(messages.unauthorized ?? "");
            } else {
              setBatch(saved);
              setError(messages[saved.code] ?? "");
            }
          }
        }
      } catch {
        if (active && !pending.current && current === revision.current)
          setError(
            "Cannot reach Gmail organization. Retry or restart MailPilot.",
          );
      }
    }
    void load();
    return () => {
      active = false;
      mounted.current = false;
    };
  }, [offset, source, clearAccount]);
  useEffect(() => {
    let active = true;
    let timer: ReturnType<typeof setTimeout>;
    async function poll() {
      const current = revision.current;
      const expected = account.current;
      try {
        if (pending.current || !expected) return;
        const result = await organize({
          operation: "status",
          accountId: expected,
        });
        if (
          !active ||
          pending.current ||
          current !== revision.current ||
          expected !== account.current
        )
          return;
        if (
          result.code === "unauthorized" ||
          (result.accountId && result.accountId !== expected)
        ) {
          clearAccount();
          setError(messages.unauthorized ?? "");
        } else if (
          result.state === "uncertain" ||
          result.state === "applying"
        ) {
          setBatch(result);
          setConfirmed(false);
          setConfirmation(null);
        }
      } catch {
        if (
          active &&
          !pending.current &&
          current === revision.current &&
          expected === account.current
        )
          setError(
            "Cannot reach Gmail organization. Refresh recorded outcomes before continuing.",
          );
      } finally {
        if (active)
          timer = setTimeout(() => {
            void poll();
          }, 5000);
      }
    }
    timer = setTimeout(() => {
      void poll();
    }, 5000);
    return () => {
      active = false;
      clearTimeout(timer);
    };
  }, [clearAccount]);
  async function act(operation: OrganizationRequest["operation"]) {
    if (pending.current) return;
    revision.current++;
    pending.current = true;
    setBusy(true);
    setError("");
    setConfirmed(false);
    setConfirmation(null);
    const expectedAccount = account.current;
    try {
      let result = await organize({
        operation,
        source:
          operation === "list" ||
          operation === "preview" ||
          operation === "dry_run"
            ? source || undefined
            : undefined,
        accountId: expectedAccount,
        offset,
        emailIds:
          operation === "preview" || operation === "dry_run" ? selected : [],
        actionId: operation === "apply" ? batch?.id : undefined,
        digest: operation === "apply" ? batch?.digest : undefined,
      });
      while (mounted.current && result.state === "running") {
        await new Promise((resolve) => setTimeout(resolve, 250));
        if (!mounted.current) return;
        result = await organize({
          operation: "status",
          accountId: expectedAccount,
        });
      }
      if (!mounted.current || account.current !== expectedAccount) return;
      if (
        result.code === "unauthorized" ||
        (expectedAccount &&
          result.accountId &&
          result.accountId !== expectedAccount)
      ) {
        clearAccount();
        setError(
          messages[result.code] ?? "Account changed. Refresh organization.",
        );
        return;
      }
      setError(messages[result.code] ?? "");
      if (operation === "list") {
        account.current = result.accountId;
        setPage(result);
        setSelected([]);
        setBatch(null);
      } else setBatch(result);
    } catch {
      if (mounted.current)
        setError(
          "Cannot reach Gmail organization. Refresh recorded outcomes before continuing.",
        );
    } finally {
      pending.current = false;
      if (mounted.current) setBusy(false);
    }
  }
  const preview = batch?.state === "preview" && batch.id !== "" && !batch.code;
  const rows = batch && batch.rows.length > 0 ? batch.rows : (page?.rows ?? []);
  const planned = batch?.state === "preview" || batch?.state === "dry_run";
  const unresolved =
    batch?.state === "uncertain" ||
    batch?.state === "applying" ||
    batch?.steps.some((step) => step.state === "uncertain") ||
    batch?.code === "reconciliation_required";
  const canApply =
    preview && batch.expiresAt > now && confirmed && !busy && !unresolved;
  const results =
    !!batch &&
    [
      "applied",
      "partial",
      "failed",
      "cancelled",
      "uncertain",
      "applying",
      "error",
    ].includes(batch.state);
  const step = results ? 4 : preview ? 3 : batch?.state === "dry_run" ? 2 : 1;
  const stateLabels: Record<string, string> = {
    applied: "Changes applied",
    partial: "Some changes need attention",
    failed: "Label changes failed",
    cancelled: "Stopped — review saved outcomes",
    uncertain: "Some Gmail outcomes are uncertain",
    applying: "Applying confirmed changes…",
    preview: "Preview ready",
    dry_run: "Read-only preview ready",
    expired: "Preview expired",
    error: "Organization needs attention",
  };
  const confirmationOpen =
    !!confirmation &&
    !!canApply &&
    confirmation.id === batch?.id &&
    confirmation.digest === batch.digest;
  const emailTable = (
    <>
      {rows.length > 0 && (
        <div className="mail-table">
          <table>
            <caption>
              {planned ? "Live change preview" : "Email label review"}
            </caption>
            <thead>
              <tr>
                <th scope="col">Select</th>
                <th scope="col">Email</th>
                <th scope="col">Current labels</th>
                <th scope="col">Proposed label</th>
                <th scope="col">Evidence</th>
                <th scope="col">Change</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((row) => (
                <tr key={row.emailId}>
                  <td>
                    {!batch?.rows.length && row.change === "candidate" && (
                      <input
                        type="checkbox"
                        aria-label={`Select ${row.subject || "email"}`}
                        checked={selected.includes(row.emailId)}
                        disabled={busy || unresolved}
                        onChange={(event) => {
                          setSelected((current) =>
                            event.target.checked
                              ? [...current, row.emailId]
                              : current.filter((id) => id !== row.emailId),
                          );
                          setConfirmed(false);
                          setConfirmation(null);
                        }}
                      />
                    )}
                  </td>
                  <td>
                    <strong>{row.subject || "(No subject)"}</strong>
                    <small>{row.sender}</small>
                  </td>
                  <td>
                    {row.currentLabels.length
                      ? row.currentLabels.join(", ")
                      : "No labels"}
                  </td>
                  <td>
                    {row.proposedLabel ||
                      (source === "phishing"
                        ? "No HIGH-risk proposal"
                        : "Unclassified")}
                  </td>
                  <td>
                    {row.phishing ? (
                      <>
                        <strong>{row.phishing.score} HIGH</strong>
                        <p>HIGH threshold: {row.phishing.threshold}</p>
                        <ol aria-label="Phishing risk reasons">
                          {row.phishing.reasons.map((reason) => (
                            <li key={reason.code}>
                              {reason.points > 0 ? "+" : ""}
                              {reason.points} {reason.explanation}
                            </li>
                          ))}
                        </ol>
                      </>
                    ) : row.confidence ? (
                      row.scoreKind === "user_assignment" ? (
                        "User assigned"
                      ) : (
                        `${row.scoreKind === "cosine_similarity" ? "Cosine score" : row.scoreKind === "rule_confidence" ? "Rule strength" : "Evidence score"} ${row.confidence.toFixed(3)}`
                      )
                    ) : (
                      "—"
                    )}
                  </td>
                  <td>
                    {row.change === "none"
                      ? "No change"
                      : row.change === "multiple_suggestions"
                        ? "Multiple suggestions — choose a primary category in category review"
                        : row.change === "below_threshold"
                          ? "Below HIGH threshold"
                          : row.change === "risk_unavailable"
                            ? "Risk analysis unavailable; refresh to retry"
                            : row.change === "candidate"
                              ? "Ready to preview"
                              : row.change === "add"
                                ? "Add label"
                                : row.change === "remove"
                                  ? "Remove label"
                                  : row.change}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {!rows.length && page && (
        <p>
          No cached suggestions available. Sync mail, then review phishing risk
          or classify messages.
        </p>
      )}
    </>
  );
  return (
    <section
      className="status-card feature-panel organization-panel"
      aria-labelledby="organization-heading"
      aria-busy={busy}
    >
      <h2 id="organization-heading">Organize Gmail labels</h2>
      <p>
        Choose emails, inspect the proposed labels, then confirm before anything
        changes in Gmail. Messages are never deleted, archived, or moved to
        Spam.
      </p>
      <ol className="workflow-steps" aria-label="Organization progress">
        {[
          "Select emails",
          "Preview labels",
          "Confirm changes",
          "Results & undo",
        ].map((label, index) => (
          <li
            key={label}
            aria-current={step === index + 1 ? "step" : undefined}
          >
            <span>{index + 1}</span>
            {label}
          </li>
        ))}
      </ol>
      {error && <p role="alert">{error}</p>}
      <div className="feature-status" role="status" aria-live="polite">
        {busy
          ? "Reading Gmail or applying confirmed changes…"
          : batch?.state && batch.state !== "idle"
            ? (stateLabels[batch.state] ?? batch.state.replaceAll("_", " "))
            : !page
              ? "Loading suggested emails…"
              : "Select up to five suggested emails to begin."}
      </div>
      {unresolved && (
        <div className="attention-panel" role="alert">
          <h3>Check interrupted changes before continuing</h3>
          <p>
            Some Gmail outcomes are uncertain. Check recorded outcomes and
            reconcile them before starting another selection. Changes will not
            be retried automatically.
          </p>
          <button disabled={busy} onClick={() => void act("reconcile")}>
            Reconcile interrupted batch
          </button>
        </div>
      )}
      <div
        className="workflow-section"
        aria-labelledby="organization-select-heading"
      >
        <h3 id="organization-select-heading">1. Select suggested emails</h3>
        <p className="help">
          The list uses cached labels. Preview reads current labels from Gmail.
          Classification and phishing scores describe evidence; they are not
          probabilities or safety guarantees.
        </p>
        <label>
          Review suggestions
          <select
            value={source}
            disabled={busy || unresolved}
            onChange={(event) => {
              revision.current++;
              setSource(event.target.value === "phishing" ? "phishing" : "");
              setOffset(0);
              setSelected([]);
              setPage(null);
              setBatch(null);
              setConfirmed(false);
              setConfirmation(null);
            }}
          >
            <option value="">Classification labels</option>
            <option value="phishing">Potential Phishing</option>
          </select>
        </label>

        <button
          className="secondary-button"
          disabled={busy || unresolved}
          onClick={() => void act("list")}
        >
          Refresh organization
        </button>
        {!planned && !results && (
          <>
            {emailTable}
            <div className="account-actions">
              <button
                disabled={busy || unresolved || offset === 0}
                onClick={() => {
                  setBatch(null);
                  setSelected([]);
                  setOffset((value) => Math.max(0, value - 5));
                }}
              >
                Previous organization page
              </button>
              <button
                disabled={busy || unresolved || !page?.hasMore}
                onClick={() => {
                  setBatch(null);
                  setSelected([]);
                  setOffset((value) => value + 5);
                }}
              >
                Next organization page
              </button>
            </div>
          </>
        )}
      </div>
      <div
        className="workflow-section"
        aria-labelledby="organization-preview-heading"
      >
        <h3 id="organization-preview-heading">
          2. Preview proposed Gmail label changes
        </h3>
        <p className="help">
          Both previews check live Gmail labels. Neither applies label changes.
          A Dry Run also avoids creating an action journal entry.
        </p>
        {!batch?.rows.length && (
          <div className="account-actions">
            <button
              disabled={busy || unresolved || selected.length === 0}
              onClick={() => void act("dry_run")}
            >
              Preview label changes (Dry Run)
            </button>
            <button
              className="secondary-button"
              disabled={busy || unresolved || selected.length === 0}
              onClick={() => void act("preview")}
            >
              Prepare apply preview
            </button>
          </div>
        )}
        {batch?.rows.length && planned ? (
          <>
            {emailTable}

            {batch.state === "dry_run" && (
              <>
                <p>
                  Dry Run complete. No Gmail or action-journal changes were
                  made. Local risk assessments may be refreshed. Prepare a fresh
                  apply preview to continue.
                </p>
                <button
                  disabled={busy || selected.length === 0}
                  onClick={() => void act("preview")}
                >
                  Prepare apply preview
                </button>
              </>
            )}
          </>
        ) : null}
        {!!batch?.rows.length && !results && (
          <button
            className="secondary-button"
            disabled={busy || unresolved}
            onClick={() => {
              setBatch(null);
              setConfirmed(false);
              setConfirmation(null);
            }}
          >
            Back to selection
          </button>
        )}
        {planned && batch?.steps.length ? (
          <ul className="operation-list" aria-label="Planned operations">
            {batch.steps.map((operation, index) => (
              <li key={index}>
                {operation.kind === "create_label"
                  ? "Create label"
                  : operation.kind === "add"
                    ? "Add label to email"
                    : "Remove label from email"}
                : {operation.name} — {operation.state}
                {operation.code
                  ? ` (${operation.code.replaceAll("_", " ")})`
                  : ""}
              </li>
            ))}
          </ul>
        ) : null}
      </div>
      <div
        className="workflow-section"
        aria-labelledby="organization-confirm-heading"
      >
        <h3 id="organization-confirm-heading">3. Confirm and apply changes</h3>
        {preview ? (
          <>
            <p>
              Preview expires {new Date(batch.expiresAt).toLocaleTimeString()}.{" "}
              {batch.kind === "undo"
                ? "Only recorded additions will be removed. Created labels remain in Gmail."
                : "Missing labels shown above will be created."}
            </p>
            {batch.expiresAt <= now && (
              <p role="alert">
                This preview has expired. Prepare a fresh preview before
                applying changes.
              </p>
            )}
            <label className="approval-check">
              <input
                type="checkbox"
                checked={confirmed}
                disabled={busy || batch.expiresAt <= now}
                onChange={(event) => {
                  setConfirmed(event.target.checked);
                  setConfirmation(null);
                }}
              />{" "}
              I confirm these {batch.kind === "undo" ? "undo" : "Gmail label"}{" "}
              changes.
            </label>
            <button
              disabled={!canApply}
              onClick={() =>
                setConfirmation({ id: batch.id, digest: batch.digest })
              }
            >
              {batch.kind === "undo" ? "Confirm undo" : "Apply selected labels"}
            </button>
          </>
        ) : (
          <p className="help">
            Prepare and review a fresh apply preview to enable confirmation.
          </p>
        )}
      </div>
      <div
        className="workflow-section"
        aria-labelledby="organization-results-heading"
      >
        <h3 id="organization-results-heading">
          4. Review results and undo eligible changes
        </h3>
        {results && (
          <>
            <p>
              {stateLabels[batch.state]}. Review each operation below; uncertain
              changes must be reconciled.
            </p>
            {!!batch.rows.length && (
              <ul className="result-email-list">
                {batch.rows.map((row) => (
                  <li key={row.emailId}>
                    <strong>{row.subject || "(No subject)"}</strong> ·{" "}
                    {row.sender}
                    <p>
                      Labels: {row.currentLabels.join(", ") || "No labels"} ·
                      Proposed: {row.proposedLabel || "No change"}
                    </p>
                  </li>
                ))}
              </ul>
            )}
            <ul className="operation-list" aria-label="Recorded outcomes">
              {batch.steps.map((operation, index) => (
                <li key={index} className={`outcome-${operation.state}`}>
                  <strong>
                    {operation.kind === "create_label"
                      ? "Create label"
                      : operation.kind === "add"
                        ? "Add label to email"
                        : "Remove label from email"}
                    : {operation.name}
                  </strong>
                  <span>
                    {operation.state.replaceAll("_", " ")}
                    {operation.code
                      ? ` · ${operation.code.replaceAll("_", " ")}`
                      : ""}
                  </span>
                </li>
              ))}
            </ul>
          </>
        )}
        <p className="help">
          Undo checks ownership and removes only verified MailPilot additions
          from the last batch. It preserves unrelated labels and does not delete
          labels created by MailPilot.
        </p>
        <div className="account-actions">
          <button
            className="secondary-button"
            disabled={busy || !page?.accountId || unresolved}
            onClick={() => void act("undo_preview")}
          >
            Preview undo last batch
          </button>
          <button
            className="secondary-button"
            disabled={busy || !page?.accountId}
            onClick={() => void act("status")}
          >
            Refresh batch status
          </button>
        </div>
        {results && !unresolved && (
          <button
            className="secondary-button"
            disabled={busy}
            onClick={() => void act("list")}
          >
            Start another selection
          </button>
        )}
        <details className="technical-details">
          <summary>Recovery details</summary>
          <p>
            Operations are recorded before dispatch. An interrupted or uncertain
            request must be reconciled before it can be retried. Preview
            validation, batch limits, and undo ownership are checked by
            MailPilot.
          </p>
          {batch?.id && <p>Recorded action: {batch.id}</p>}
        </details>
      </div>
      {busy && (
        <button
          onClick={() => {
            const expected = account.current;
            void organize({ operation: "cancel", accountId: expected }).catch(
              () => {
                if (mounted.current && expected === account.current)
                  setError(
                    "Could not stop organization. Refresh batch status to check recorded outcomes.",
                  );
              },
            );
          }}
        >
          Stop organization
        </button>
      )}

      <ConfirmDialog
        open={confirmationOpen}
        title={
          batch?.kind === "undo"
            ? "Undo these Gmail label additions?"
            : "Apply these Gmail label changes?"
        }
        confirmLabel={
          batch?.kind === "undo" ? "Confirm and undo" : "Confirm and apply"
        }
        onCancel={() => setConfirmation(null)}
        onConfirm={() => {
          if (!confirmationOpen) return;
          setConfirmation(null);
          void act("apply");
        }}
      >
        <p>
          {batch?.kind === "undo"
            ? "Only verified MailPilot-owned label additions in this preview will be removed."
            : "Only the label changes in your reviewed preview will be applied. No messages will be deleted or archived."}
        </p>
        <ul>
          {batch?.steps.map((operation, index) => (
            <li key={index}>
              {operation.kind === "create_label"
                ? "Create label"
                : operation.kind === "add"
                  ? "Add label to email"
                  : "Remove label from email"}
              : {operation.name}
            </li>
          ))}
        </ul>
      </ConfirmDialog>
    </section>
  );
}
