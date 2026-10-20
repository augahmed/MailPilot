import { useEffect, useRef, useState } from "react";
import { EmailSummary } from "../ai/EmailSummary";
import { readCachedMail, type ReadMailResponse } from "../../lib/ipc/reader";

export function MailReader({
  accountId,
  emailId,
  source,
  onBack,
}: {
  accountId: string;
  emailId: string;
  source: string;
  onBack: () => void;
}) {
  const [page, setPage] = useState<ReadMailResponse | null>(null);
  const [body, setBody] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(true);
  const [retry, setRetry] = useState(0);
  const heading = useRef<HTMLHeadingElement>(null);
  const live = useRef(false);
  const generation = useRef(0);
  useEffect(() => {
    live.current = true;
    generation.current++;
    let active = true;
    let timer: ReturnType<typeof setTimeout>;
    heading.current?.focus();
    const fail = (code: string) => {
      generation.current++;
      setBusy(false);
      setPage(null);
      setBody("");
      setError(
        code === "unauthorized"
          ? "Connect Gmail to read this account’s cache."
          : code === "stale"
            ? "The cached message changed. Reload it to continue."
            : code === "not_found"
              ? "This message is no longer in the cache."
              : "Cannot read this cached message. Retry or restart MailPilot.",
      );
    };
    const load = async () => {
      try {
        const result = await readCachedMail(accountId, emailId);
        if (!active) return;
        if (result.code) {
          fail(result.code);
          return;
        }
        setPage(result);
        setBody(result.body);
        setError("");
        const check = async () => {
          try {
            const status = await readCachedMail(
              accountId,
              emailId,
              0,
              result.revision,
            );
            if (!active) return;
            if (status.code) {
              fail(status.code);
              return;
            }
            timer = setTimeout(() => {
              void check();
            }, 5000);
          } catch {
            if (active) fail("storage_error");
          }
        };
        timer = setTimeout(() => {
          void check();
        }, 5000);
      } catch {
        if (active) fail("storage_error");
      } finally {
        if (active) setBusy(false);
      }
    };
    void load();
    return () => {
      active = false;
      live.current = false;
      clearTimeout(timer);
    };
  }, [accountId, emailId, retry]);
  const more = async () => {
    if (!page || busy) return;
    setBusy(true);
    const current = generation.current;
    try {
      const result = await readCachedMail(
        accountId,
        emailId,
        page.nextOffset,
        page.revision,
      );
      if (!live.current || current !== generation.current) return;
      if (result.code) {
        setPage(null);
        setBody("");
        setError(
          "The cached message is unavailable or changed. Reload to continue.",
        );
      } else {
        setBody((value) => value + result.body);
        setPage(result);
      }
    } catch {
      if (live.current && current === generation.current)
        setError("Could not load more cached text. Retry.");
    } finally {
      if (live.current && current === generation.current) setBusy(false);
    }
  };
  const m = page?.message;
  const addresses = (values: { name: string; address: string }[]) =>
    values
      .map((a) => (a.name ? `${a.name} <${a.address}>` : a.address))
      .join(", ") || "None cached";
  return (
    <section
      className="status-card mail-reader"
      aria-labelledby="reader-heading"
    >
      <button onClick={onBack}>Back to {source}</button>
      <h2 id="reader-heading" ref={heading} tabIndex={-1}>
        {m?.subject || (m ? "(No subject)" : "Cached message")}
      </h2>
      {busy && <p role="status">Loading cached text…</p>}
      {error && <p role="alert">{error}</p>}
      {error && (
        <button
          disabled={busy}
          onClick={() => {
            setBusy(true);
            setPage(null);
            setBody("");
            setRetry((v) => v + 1);
          }}
        >
          Reload cached message
        </button>
      )}
      {m && (
        <>
          <dl>
            <dt>From</dt>
            <dd>{addresses([m.sender])}</dd>
            <dt>To</dt>
            <dd>{addresses(m.recipients)}</dd>
            <dt>CC</dt>
            <dd>{addresses(m.cc)}</dd>
            <dt>Received</dt>
            <dd>{new Date(m.receivedAt).toLocaleString()}</dd>
            <dt>Gmail labels (cached)</dt>
            <dd>{m.labels.map((l) => l.name).join(", ") || "None cached"}</dd>
            <dt>MailPilot category</dt>
            <dd>
              {m.categories.map((c) => `${c.name} (${c.source})`).join(", ") ||
                "No assigned category"}
              . Classification: {m.categoryState.replaceAll("_", " ")}
            </dd>
          </dl>
          <p className="help">
            Local cache updated {new Date(m.cachedAt).toLocaleString()}. Opening
            this message does not change Gmail read status.
          </p>
          {m.metadataTruncated && (
            <p role="status">
              Some metadata is omitted because it exceeds the display limit.
            </p>
          )}
          <h3>Phishing analysis</h3>
          {m.phishing ? (
            <>
              <p>
                {m.phishing.score}/100 · {m.phishing.risk}
              </p>
              <ul>
                {m.phishing.reasons.map((r) => (
                  <li key={r.code}>
                    {r.explanation} ({r.points > 0 ? "+" : ""}
                    {r.points})
                  </li>
                ))}
              </ul>
              <small>Analyzer: {m.phishing.version}</small>
            </>
          ) : (
            <p>Risk analysis unavailable.</p>
          )}
          <p className="help">
            This local heuristic is not a probability or guarantee of safety.
            Missing content can hide risk.
          </p>
          <h3>Message body</h3>
          <p className="help">
            Cleaned text from the cached email. Links remain inactive and remote
            images are blocked.
            {m.bodyFormat === "html_text" &&
              " Includes text extracted from cached HTML."}
          </p>
          {m.bodyError && (
            <p role="alert">Cached body could not be displayed safely.</p>
          )}
          {m.bodyState === "unavailable" && (
            <p>
              Some body content was not downloaded. Only available cached text
              is shown.
            </p>
          )}
          {!body && !m.bodyError && <p>No readable body is cached.</p>}
          <div
            className="reader-body"
            style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}
          >
            {body}
          </div>
          {page && page.nextOffset < page.totalBytes && (
            <>
              <p>
                {page.nextOffset} of {page.totalBytes} cleaned text bytes
                loaded.
              </p>
              <button
                disabled={busy}
                onClick={() => {
                  void more();
                }}
              >
                Load more cached text
              </button>
            </>
          )}
          <EmailSummary
            key={`${accountId}/${emailId}/${page?.revision}`}
            accountId={accountId}
            emailId={emailId}
          />
          <h3>Attachments</h3>
          <p className="help">
            Metadata only; files are not downloaded or opened.
          </p>
          {m.attachments.length ? (
            <ul>
              {m.attachments.map((a, i) => (
                <li key={i}>
                  {a.filename || "Unnamed attachment"} ·{" "}
                  {a.mimeType || "Unknown type"} ·{" "}
                  {a.sizeBytes === null
                    ? "Size unknown"
                    : `${a.sizeBytes} bytes`}
                  {a.inline ? " · inline" : ""}
                </li>
              ))}
            </ul>
          ) : (
            <p>No attachment metadata cached.</p>
          )}
        </>
      )}
    </section>
  );
}
