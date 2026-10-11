import { useCallback, useEffect, useRef, useState } from "react";
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
  const [page, setPage] = useState<OrganizationResponse | null>(null);
  const [batch, setBatch] = useState<OrganizationResponse | null>(null);
  const [selected, setSelected] = useState<string[]>([]);
  const [offset, setOffset] = useState(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [confirmed, setConfirmed] = useState(false);
  const [dryRun, setDryRun] = useState(true);
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
  }, []);
  useEffect(() => {
    let active = true;
    const current = ++revision.current;
    mounted.current = true;
    async function load() {
      try {
        const result = await organize({ operation: "list", offset });
        if (!active || pending.current || current !== revision.current) return;
        if (account.current && account.current !== result.accountId) {
          setBatch(null);
          setSelected([]);
          setConfirmed(false);
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
  }, [offset, clearAccount]);
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
    const expectedAccount = account.current;
    try {
      let result = await organize({
        operation,
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
  const canApply = preview && batch.expiresAt > now && confirmed && !busy;
  const rows = batch && batch.rows.length > 0 ? batch.rows : (page?.rows ?? []);
  const planned = batch?.state === "preview" || batch?.state === "dry_run";
  return (
    <section className="status-card" aria-labelledby="organization-heading">
      <h2 id="organization-heading">Gmail organization</h2>
      <p>
        Select predictions, then review live Gmail labels before applying.
        Confidence reflects rule strength. Only MailPilot user labels are added;
        messages stay in your Inbox.
      </p>
      <p className="help">
        The selection table uses cached labels. Dry Run and previews read live
        Gmail state. Undo removes verified additions from the last batch;
        created labels remain in Gmail.
      </p>
      {error && <p role="alert">{error}</p>}
      <div role="status" aria-live="polite">
        {busy
          ? "Reading Gmail or applying confirmed changes…"
          : batch?.state && batch.state !== "idle"
            ? `Batch: ${batch.state}`
            : ""}
      </div>
      <div className="account-actions">
        <button disabled={busy} onClick={() => void act("list")}>
          Refresh organization
        </button>
        <button
          disabled={busy || !page?.accountId}
          onClick={() => void act("undo_preview")}
        >
          Preview undo last batch
        </button>
        <button
          disabled={busy || !page?.accountId}
          onClick={() => void act("status")}
        >
          Refresh batch status
        </button>
        {(batch?.state === "uncertain" || batch?.state === "applying") && (
          <button disabled={busy} onClick={() => void act("reconcile")}>
            Reconcile interrupted batch
          </button>
        )}
      </div>
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
                <th scope="col">Confidence</th>
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
                        disabled={busy}
                        onChange={(event) => {
                          setSelected((current) =>
                            event.target.checked
                              ? [...current, row.emailId]
                              : current.filter((id) => id !== row.emailId),
                          );
                          setConfirmed(false);
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
                  <td>{row.proposedLabel || "Unclassified"}</td>
                  <td>
                    {row.confidence
                      ? `${Math.round(row.confidence * 100)}%`
                      : "—"}
                  </td>
                  <td>{row.change === "none" ? "No change" : row.change}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {!rows.length && (
        <p>No cached predictions available. Sync and classify mail first.</p>
      )}
      {!batch?.rows.length && (
        <>
          <label>
            <input
              type="checkbox"
              checked={dryRun}
              disabled={busy}
              onChange={(event) => setDryRun(event.target.checked)}
            />{" "}
            Dry Run (read only)
          </label>
          <button
            disabled={busy || selected.length === 0}
            onClick={() => void act(dryRun ? "dry_run" : "preview")}
          >
            {dryRun ? "Run Dry Run" : "Generate apply preview"}
          </button>
        </>
      )}
      {batch && batch.steps.length > 0 && (
        <ul aria-label="Planned operations">
          {batch.steps.map((step, index) => (
            <li key={index}>
              {step.kind === "create_label"
                ? "Create label"
                : step.kind === "add"
                  ? "Add label to email"
                  : "Remove label from email"}
              : {step.name} — {step.state}
              {step.code ? ` (${step.code.replaceAll("_", " ")})` : ""}
            </li>
          ))}
        </ul>
      )}
      {batch?.rows.length ? (
        <button
          disabled={busy}
          onClick={() => {
            setBatch(null);
            setConfirmed(false);
          }}
        >
          Back to selection
        </button>
      ) : null}
      {batch?.state === "dry_run" && (
        <p>
          Dry Run complete. No mailbox or journal changes were made. Turn off
          Dry Run and generate a fresh apply preview to continue.
        </p>
      )}
      {preview && (
        <>
          <p>
            Preview expires {new Date(batch.expiresAt).toLocaleTimeString()}.{" "}
            {batch.kind === "undo"
              ? "Only recorded additions will be removed."
              : "Missing labels shown above will be created."}
          </p>
          <label>
            <input
              type="checkbox"
              checked={confirmed}
              disabled={busy}
              onChange={(event) => setConfirmed(event.target.checked)}
            />{" "}
            I confirm these {batch.kind === "undo" ? "undo" : "Gmail label"}{" "}
            changes.
          </label>
          <button disabled={!canApply} onClick={() => void act("apply")}>
            {batch.kind === "undo" ? "Confirm undo" : "Apply selected labels"}
          </button>
        </>
      )}
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
      <div className="account-actions">
        <button
          disabled={busy || offset === 0}
          onClick={() => {
            setBatch(null);
            setSelected([]);
            setOffset((value) => Math.max(0, value - 5));
          }}
        >
          Previous organization page
        </button>
        <button
          disabled={busy || !page?.hasMore}
          onClick={() => {
            setBatch(null);
            setSelected([]);
            setOffset((value) => value + 5);
          }}
        >
          Next organization page
        </button>
      </div>
    </section>
  );
}
