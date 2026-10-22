import type { OpenMail } from "../../lib/ipc/reader";
import { useEffect, useRef, useState } from "react";
import {
  getMailbox,
  syncCommand,
  type MailboxPage,
  type SyncProgress,
} from "../../lib/ipc/client";

export function Mailbox({
  connectionRevision,
  onOpen,
}: {
  connectionRevision: number;
  onOpen?: OpenMail;
}) {
  const [progress, setProgress] = useState<SyncProgress | null>(null);
  const [page, setPage] = useState<MailboxPage | null>(null);
  const [offset, setOffset] = useState(0);
  const [error, setError] = useState(false);
  const [starting, setStarting] = useState(false);
  const revision = useRef(0);
  useEffect(() => {
    let active = true;
    let timer: ReturnType<typeof setTimeout>;
    const current = ++revision.current;
    const poll = async () => {
      try {
        const status = await syncCommand();
        const mailbox = await getMailbox(offset);
        if (active && current === revision.current) {
          setProgress(status);
          setPage(mailbox);
          setError(false);
        }
      } catch {
        if (active && current === revision.current) {
          setError(true);
          setPage(null);
        }
      } finally {
        if (active)
          timer = setTimeout(() => {
            void poll();
          }, 1500);
      }
    };
    void poll();
    return () => {
      active = false;
      clearTimeout(timer);
    };
  }, [offset, connectionRevision]);
  const start = async () => {
    setStarting(true);
    try {
      await syncCommand(true);
    } catch {
      setError(true);
    } finally {
      setStarting(false);
    }
  };
  return (
    <section className="status-card mailbox" aria-labelledby="sync-heading">
      <div className="mailbox-header">
        <div>
          <p className="eyebrow">CACHED GMAIL</p>
          <h2 id="sync-heading">Your Emails</h2>
        </div>
        <div className="account-actions sync-actions">
          <button
            disabled={
              starting || progress?.state === "running" || !page?.accountId
            }
            onClick={() => {
              void start();
            }}
          >
            {starting || progress?.state === "running"
              ? "Syncing Gmail…"
              : "Sync Gmail"}
          </button>
          {progress?.state === "running" && (
            <button
              className="secondary-button"
              onClick={() => {
                void syncCommand("cancel").catch(() => setError(true));
              }}
            >
              Stop sync
            </button>
          )}
        </div>
      </div>
      <p className="mailbox-caption">
        Your local cached subset. Sync to check for Gmail changes.
      </p>
      <details className="sync-details" open={progress?.state === "running"}>
        <summary aria-live="polite">
          {progress?.state === "running"
            ? "Syncing inbox…"
            : progress?.state === "completed"
              ? "Sync finished"
              : progress?.state === "cancelled"
                ? "Sync stopped"
                : progress?.state === "error"
                  ? "Sync failed"
                  : "Sync details"}
        </summary>
        <p>
          Imports a bounded Inbox snapshot, then checks mailbox changes. Up to{" "}
          {progress?.limit ?? "…"} message changes per run.
        </p>
        <div role="status" aria-live="polite">
          {progress?.mode && (
            <p>
              Sync mode:{" "}
              {progress.mode === "initial"
                ? "Initial import"
                : progress.mode === "recovery"
                  ? "Checkpoint recovery"
                  : "Incremental changes"}
            </p>
          )}
          {progress?.more && (
            <p>
              More synchronization work remains. Run sync again to continue from
              saved progress.
            </p>
          )}
          {progress?.snapshotLimited && (
            <p>
              The initial snapshot is a limited Inbox subset; older uncached
              mail has not been imported.
            </p>
          )}
          <dl className="sync-counts">
            <div>
              <dt>Messages discovered</dt>
              <dd>{progress?.discovered ?? 0}</dd>
            </div>
            <div>
              <dt>Messages imported</dt>
              <dd>{progress?.imported ?? 0}</dd>
            </div>
            <div>
              <dt>Messages updated</dt>
              <dd>{progress?.updated ?? 0}</dd>
            </div>
            <div>
              <dt>Removed from cache</dt>
              <dd>{progress?.removed ?? 0}</dd>
            </div>
            <div>
              <dt>Errors</dt>
              <dd>{progress?.errors ?? 0}</dd>
            </div>
          </dl>
        </div>
      </details>
      {progress?.capped && (
        <p className="help">
          The configured limit was reached. More messages may remain in Gmail.
        </p>
      )}
      {progress?.code && (
        <p className="help">
          {progress.code === "reauth_required"
            ? "Reconnect Gmail before syncing."
            : progress.code === "body_unavailable"
              ? "Some bodies are stored separately by Gmail and were not downloaded. Their metadata is cached."
              : progress.code === "chunking_failed"
                ? "Some cached messages could not be processed. Their original content is saved. Sync again to retry."
                : "Some changes could not be synchronized. Successful changes are saved; run sync again to retry unfinished work."}
        </p>
      )}
      {error && (
        <p role="alert">
          Cannot reach the desktop mailbox. Retry or restart MailPilot.
        </p>
      )}
      {page?.code && <p role="alert">Could not read the local mailbox.</p>}
      <p className="risk-note">
        Phishing risk is a local heuristic, not a probability or a guarantee of
        safety. Scores are limited to 0–100; missing content may hide risk.
      </p>
      {!page ? (
        <p role="status">
          {error
            ? "Cached emails are temporarily unavailable. MailPilot will retry automatically."
            : "Loading cached emails…"}
        </p>
      ) : page.code ? (
        <p>
          Cached emails could not be loaded. MailPilot will retry automatically.
        </p>
      ) : !page.accountId ? (
        <p>Connect Gmail to view this account’s cache.</p>
      ) : page.rows.length === 0 ? (
        <div className="mailbox-empty">
          <h3>No cached messages on this page</h3>
          <p>
            Use Sync Gmail to import a bounded Inbox snapshot, or return to the
            previous page.
          </p>
        </div>
      ) : (
        <div
          className="mail-table"
          tabIndex={0}
          role="region"
          aria-label="Cached email list"
        >
          <table>
            <thead>
              <tr>
                <th scope="col">Sender</th>
                <th scope="col">Subject</th>
                <th scope="col">Received</th>
                <th scope="col">Phishing risk</th>
              </tr>
            </thead>
            <tbody>
              {page.rows.map((row) => (
                <tr key={row.id}>
                  <td>{row.sender || "Unknown sender"}</td>
                  <td>
                    <button
                      className="email-subject"
                      onClick={(event) =>
                        onOpen?.(page.accountId, row.id, event.currentTarget)
                      }
                    >
                      {row.subject || "(No subject)"}
                    </button>
                    {row.bodyState === "unavailable" && (
                      <small>Body unavailable</small>
                    )}
                  </td>
                  <td>
                    <time dateTime={new Date(row.receivedAt).toISOString()}>
                      {new Date(row.receivedAt).toLocaleString()}
                    </time>
                  </td>
                  <td>
                    {row.phishing ? (
                      <details
                        className={`risk-assessment risk-${row.phishing.risk.toLowerCase()}`}
                      >
                        <summary>
                          <span className="risk-badge">
                            {row.phishing.risk === "LOW"
                              ? "Low"
                              : row.phishing.risk === "MEDIUM"
                                ? "Medium"
                                : "High"}{" "}
                            · {row.phishing.score}/100
                          </span>
                        </summary>
                        <ol>
                          {row.phishing.reasons.map((reason) => (
                            <li key={reason.code}>
                              {reason.points > 0 ? "+" : ""}
                              {reason.points} {reason.explanation}
                            </li>
                          ))}
                        </ol>
                        <small>Analyzer: {row.phishing.version}</small>
                      </details>
                    ) : (
                      <span className="risk-unassessed">
                        Not assessed
                        <span className="sr-only">
                          {" "}
                          · Risk analysis unavailable; refresh to retry.
                        </span>
                      </span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <div className="account-actions mailbox-pagination">
        <span className="page-number">Page {offset / 10 + 1}</span>
        <button
          className="secondary-button"
          disabled={offset === 0}
          onClick={() => {
            setPage(null);
            setOffset(Math.max(0, offset - 10));
          }}
        >
          Previous
        </button>
        <button
          className="secondary-button"
          disabled={!page?.hasMore}
          onClick={() => {
            setPage(null);
            setOffset(offset + 10);
          }}
        >
          Next
        </button>
      </div>
    </section>
  );
}
