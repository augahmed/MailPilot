import type { OpenMail } from "../../lib/ipc/reader";
import { useDemo } from "../../app/DemoContext";
import { useEffect, useRef, useState } from "react";
import {
  manageCategories,
  syncCommand,
  type Category,
  type SyncProgress,
} from "../../lib/ipc/client";
import {
  listMailboxEmails,
  mailboxCategory,
  type MailboxViewPage,
} from "./listEmails";

export function Mailbox({
  connectionRevision,
  onOpen,
  active: visible = true,
}: {
  connectionRevision: number;
  onOpen?: OpenMail;
  active?: boolean;
}) {
  const demo = useDemo();
  const [progress, setProgress] = useState<SyncProgress | null>(null);
  const [page, setPage] = useState<MailboxViewPage | null>(null);
  const [offset, setOffset] = useState(0);
  const [history, setHistory] = useState<number[]>([]);
  const [categoryId, setCategoryId] = useState("");
  const [sort, setSort] = useState("recent");
  const [categories, setCategories] = useState<{
    accountId: string;
    rows: Category[];
    failed: boolean;
  } | null>(null);
  const [categoryReload, setCategoryReload] = useState(0);
  const [error, setError] = useState(false);
  const [starting, setStarting] = useState(false);
  const revision = useRef(0);
  useEffect(() => {
    if (!visible) return;
    let active = true;
    let timer: ReturnType<typeof setTimeout>;
    const current = ++revision.current;
    const poll = async () => {
      try {
        const status = await syncCommand();
        const mailbox = await listMailboxEmails(
          offset,
          categoryId,
          () => active && current === revision.current,
        );
        if (!mailbox) return;
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
  }, [offset, categoryId, connectionRevision, visible]);
  useEffect(() => {
    const accountId = page?.accountId;
    if (!accountId || !visible) return;
    let active = true;
    async function load() {
      const rows = new Map<string, Category>();
      try {
        for (let offset = 0; offset <= 1_000_000; offset += 5) {
          const result = await manageCategories({
            operation: "list",
            accountId,
            offset,
          });
          if (!active) return;
          if (
            result.code ||
            result.accountId !== accountId ||
            (result.hasMore && result.rows.length !== 5)
          )
            throw Error("Categories changed");
          for (const row of result.rows) {
            if (rows.has(row.id)) throw Error("Duplicate category");
            rows.set(row.id, row);
          }
          if (!result.hasMore) {
            const enabled = [...rows.values()].filter((row) => row.enabled);
            setCategories({
              accountId: accountId!,
              rows: enabled,
              failed: false,
            });
            setCategoryId((id) =>
              id === "__unclassified__" || enabled.some((row) => row.id === id)
                ? id
                : "",
            );
            return;
          }
        }
        throw Error("Too many categories");
      } catch {
        if (active)
          setCategories({ accountId: accountId!, rows: [], failed: true });
      }
    }
    void load();
    return () => {
      active = false;
    };
  }, [page?.accountId, categoryReload, visible]);
  const rows = [...(page?.rows ?? [])].sort((a, b) => {
    if (sort === "recent")
      return b.receivedAt - a.receivedAt || a.id.localeCompare(b.id);
    if (sort === "unclassified") {
      const order =
        Number(mailboxCategory(b) === "Unclassified") -
        Number(mailboxCategory(a) === "Unclassified");
      if (order) return order;
    }
    const order = mailboxCategory(a).localeCompare(mailboxCategory(b));
    return sort === "category-desc" ? -order : order;
  });
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
          <p className="eyebrow">
            {demo ? "SYNTHETIC SAMPLES" : "CACHED GMAIL"}
          </p>
          <h2 id="sync-heading">Your Emails</h2>
        </div>
        <div className="account-actions sync-actions">
          <button
            disabled={
              demo ||
              starting ||
              progress?.state === "running" ||
              !page?.accountId
            }
            onClick={() => {
              void start();
            }}
          >
            {starting || progress?.state === "running"
              ? "Syncing Gmail…"
              : demo
                ? "Offline demo"
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
      <div className="organization-filters">
        <label>
          Filter by category
          <select
            value={categoryId}
            disabled={
              !categories ||
              categories.accountId !== page?.accountId ||
              categories.failed
            }
            onChange={(event) => {
              revision.current++;
              setCategoryId(event.target.value);
              setPage(null);
              setOffset(0);
              setHistory([]);
            }}
          >
            <option value="">All categories</option>
            <option value="__unclassified__">Unclassified</option>
            {categories?.accountId === page?.accountId &&
              categories?.rows.map((category) => (
                <option key={category.id} value={category.id}>
                  {category.name}
                </option>
              ))}
          </select>
        </label>
        <label>
          Sort this page by
          <select
            value={sort}
            onChange={(event) => setSort(event.target.value)}
          >
            <option value="recent">Newest first</option>
            <option value="category">Category (A–Z)</option>
            <option value="category-desc">Category (Z–A)</option>
            <option value="unclassified">Unclassified first</option>
          </select>
        </label>
        <button
          className="secondary-button"
          disabled={!categoryId && sort === "recent" && !history.length}
          onClick={() => {
            setCategoryId("");
            setSort("recent");
            setOffset(0);
            setHistory([]);
          }}
        >
          Reset filters
        </button>
      </div>
      {categories?.failed && (
        <p role="alert">
          Could not load category filters.{" "}
          <button
            className="secondary-button"
            onClick={() => setCategoryReload((value) => value + 1)}
          >
            Retry category filters
          </button>
        </p>
      )}
      <details className="sync-details" open={progress?.state === "running"}>
        <summary aria-live="polite">
          {progress?.state === "running"
            ? "Syncing inbox…"
            : progress?.state === "completed"
              ? progress.errors > 0
                ? "Sync finished with some errors"
                : "Sync finished"
              : progress?.state === "cancelled"
                ? "Sync stopped"
                : progress?.state === "error"
                  ? "Sync failed"
                  : "Sync details"}
        </summary>
        <p>
          Imports the newest {progress?.limit ?? "…"} Inbox emails first. Later
          syncs continue into older Inbox mail and check for changes. Each run
          handles up to {progress?.limit ?? "…"} Inbox emails plus{" "}
          {progress?.limit ?? "…"} mailbox updates or retries.
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
          {progress?.more && !progress.snapshotLimited && (
            <p>
              More synchronization work remains. Run sync again to continue from
              saved progress.
            </p>
          )}
          {progress?.snapshotLimited && (
            <p>
              Older Inbox emails remain. Run sync again to import the next batch
              from saved progress.
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
          This sync reached its batch limit. Run sync again to continue.
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
                : progress.state === "completed" && progress.errors > 0
                  ? "Some emails could not be downloaded. Successful downloads are saved. Sync again to retry failed emails and continue older Inbox mail."
                  : progress.code === "rate_limited"
                    ? "Gmail temporarily limited requests. Saved progress is kept; wait a little and run sync again."
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
          <h3>
            {categoryId
              ? "No matching cached messages"
              : "No cached messages on this page"}
          </h3>
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
                <th scope="col">Category</th>
                <th scope="col">Phishing risk</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((row) => (
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
                    {mailboxCategory(row)}
                    {row.classification?.categoryId && (
                      <small>
                        {row.classification.state === "assigned"
                          ? "User assigned"
                          : "Suggested"}
                      </small>
                    )}
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
        <span className="page-number">
          Page {history.length + 1} · up to 25 emails
        </span>
        <button
          className="secondary-button"
          disabled={!history.length}
          onClick={() => {
            setPage(null);
            setOffset(history[history.length - 1] ?? 0);
            setHistory((values) => values.slice(0, -1));
          }}
        >
          Previous
        </button>
        <button
          className="secondary-button"
          disabled={!page?.hasMore}
          onClick={() => {
            setPage(null);
            setHistory((values) => [...values, offset]);
            setOffset(page?.nextOffset ?? offset + 25);
          }}
        >
          Next
        </button>
      </div>
    </section>
  );
}
