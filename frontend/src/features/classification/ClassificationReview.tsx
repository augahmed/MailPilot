import { useEffect, useRef, useState } from "react";
import {
  reviewClassifications,
  type ClassificationPage,
} from "../../lib/ipc/client";

import { SemanticEvidence } from "./SemanticEvidence";
import { MailReader } from "../reader/MailReader";

const errors = {
  validation: "The review request is invalid. Refresh and try again.",
  unauthorized: "Connect Gmail to review this account’s cached mail.",
  conflict:
    "Mail, labels, or category definitions changed. Refresh and review again.",
  cancelled: "Review stopped. Successfully saved assessments remain available.",
  invalid_message:
    "This message or assessment exceeds the supported review limits.",
  storage_error: "Could not load or save a local assessment. Try again.",
};
const states = {
  not_reviewed: "Not reviewed yet",
  unclassified: "Unclassified — no clear category match",
  classified: "Predicted category",
  suggested: "Suggested categories",
  assigned: "Assigned category",
  stale: "Review needed — inputs or classifier changed",
  error: "Could not review",
};
export function ClassificationReview() {
  const [opened, setOpened] = useState<{
    emailId: string;
    revision: string;
    text: string;
    accountId: string;
  } | null>(null);
  const [choices, setChoices] = useState<Record<string, string>>({});
  const heading = useRef<HTMLHeadingElement>(null);
  const [page, setPage] = useState<ClassificationPage | null>(null);
  const [offset, setOffset] = useState(0);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  const mounted = useRef(false);
  const pending = useRef(false);
  const revision = useRef(0);
  useEffect(() => {
    mounted.current = true;
    let active = true;
    let timer: ReturnType<typeof setTimeout>;
    const poll = async () => {
      const current = revision.current;
      try {
        if (pending.current) return;
        const result = await reviewClassifications({
          operation: "list",
          offset,
        });
        if (active && current === revision.current && !pending.current) {
          if (result.code) {
            setError(errors[result.code]);
            setPage(null);
          } else {
            setPage(result);
            setError("");
          }
        }
      } catch {
        if (active && current === revision.current && !pending.current) {
          setPage(null);
          setError(
            "Cannot reach classification storage. Retry or restart MailPilot.",
          );
        }
      } finally {
        if (active)
          timer = setTimeout(() => {
            void poll();
          }, 5000);
      }
    };
    void poll();
    return () => {
      active = false;
      mounted.current = false;
      clearTimeout(timer);
    };
  }, [offset]);
  async function act(
    operation: "list" | "run" | "assigned" | "unclassified" | "auto",
    row?: ClassificationPage["rows"][number],
  ) {
    if (pending.current) return;
    pending.current = true;
    revision.current++;
    setBusy(true);
    setMessage("");
    setError("");
    try {
      const result = await reviewClassifications({
        operation,
        accountId: page?.accountId ?? "",
        offset,
        ...(row
          ? {
              emailId: row.email_id,
              revision: row.readerRevision ?? "",
              categoryId:
                operation === "assigned" ? (choices[row.email_id] ?? "") : "",
            }
          : {}),
      });
      if (!mounted.current) return;
      if (result.code) {
        setError(errors[result.code]);
        setPage(null);
      } else {
        setPage(result);
        if (operation === "run")
          setMessage(
            `Saved assessments for ${result.processed} cached emails. ${result.errors} errors.`,
          );
      }
    } catch {
      if (mounted.current) {
        setPage(null);
        setError(
          "Cannot reach classification storage. Retry or restart MailPilot.",
        );
      }
    } finally {
      pending.current = false;
      if (mounted.current) setBusy(false);
    }
  }
  function controls(row: ClassificationPage["rows"][number]) {
    if (!page?.categoryOptions || !row.readerRevision) return null;
    return (
      <div
        className="review-actions"
        role="group"
        aria-label={`Local category override for ${row.subject || row.email_id}`}
      >
        <label>
          Primary category for {row.subject || row.email_id}
          <select
            disabled={busy}
            value={choices[row.email_id] ?? ""}
            onChange={(e) =>
              setChoices({ ...choices, [row.email_id]: e.target.value })
            }
          >
            <option value="">Choose a category</option>
            {page.categoryOptions.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </select>
        </label>
        <button
          disabled={busy || !choices[row.email_id]}
          onClick={() => void act("assigned", row)}
        >
          Assign primary category
        </button>
        <button
          className="secondary-button"
          disabled={busy}
          onClick={() => void act("unclassified", row)}
        >
          Leave unclassified
        </button>
        <button
          className="secondary-button"
          disabled={busy}
          onClick={() => void act("auto", row)}
        >
          Use automatic suggestions
        </button>
        <small>
          Local only. This does not confirm a training example or change Gmail.
          Legacy assignments are retained.
        </small>
      </div>
    );
  }
  function semanticEvidence(row: ClassificationPage["rows"][number]) {
    return (
      row.semantic && (
        <SemanticEvidence
          evidence={row.semantic}
          onOpen={(text) => {
            if (row.readerRevision && page?.accountId)
              setOpened({
                emailId: row.email_id,
                revision: row.readerRevision,
                text,
                accountId: page.accountId,
              });
          }}
        />
      )
    );
  }
  if (opened)
    return (
      <MailReader
        accountId={opened.accountId}
        emailId={opened.emailId}
        source="Classification evidence"
        evidence={{ text: opened.text, revision: opened.revision }}
        onBack={() => {
          setOpened(null);
          setTimeout(() => heading.current?.focus(), 0);
        }}
      />
    );
  return (
    <section
      className="status-card feature-panel classification-review"
      aria-labelledby="classification-heading"
    >
      <h2 ref={heading} tabIndex={-1} id="classification-heading">
        Classification review
      </h2>
      <p>
        Review local semantic and optional decisive-rule suggestions for cached
        mail, five emails at a time. Emails with insufficient evidence remain
        unclassified; closely scoring categories are suggested together.
      </p>
      <p className="help">
        Suggestions and corrections stay local. Similarity and rule strength are
        evidence scores, not probabilities. Gmail labels require a separate
        organization preview and confirmation.
      </p>
      <div className="feature-status" role="status" aria-live="polite">
        {busy ? "Reviewing cached mail…" : message}
      </div>
      {error && <p role="alert">{error}</p>}
      <div className="account-actions">
        <button
          className="secondary-button"
          disabled={busy}
          onClick={() => void act("list")}
        >
          Refresh reviews
        </button>
        <button
          disabled={busy || !page?.accountId || page.rows.length === 0}
          onClick={() => void act("run")}
        >
          Classify this page
        </button>
      </div>
      {!page && !error && <p>Loading saved reviews…</p>}
      {page && page.rows.length === 0 && (
        <p>No cached emails on this page. Sync your Inbox to download mail.</p>
      )}
      {page && page.rows.length > 0 && (
        <div className="review-cards">
          {page.rows.map((row, index) => (
            <article
              className="review-card"
              key={row.email_id}
              aria-labelledby={`review-email-${index}`}
            >
              <div className="review-card-header">
                <div>
                  <h3 id={`review-email-${index}`}>
                    {row.subject || "(No subject)"}
                  </h3>
                  <p className="email-meta">{row.sender || "Unknown sender"}</p>
                </div>
                <span className="neutral-badge">{states[row.state]}</span>
              </div>
              {row.bodyState !== "complete" && (
                <p className="help">
                  {row.bodyState === "unavailable"
                    ? "Body unavailable"
                    : "Body completeness unknown"}
                </p>
              )}
              {row.semantic?.assigned.length ? (
                <p className="existing-assignment">
                  Existing assignment:{" "}
                  {row.semantic.assigned
                    .map((category) => category.name)
                    .join(", ")}
                </p>
              ) : null}
              {row.predictions.length > 0 ? (
                <ul className="prediction-list">
                  {row.predictions.map((prediction) => (
                    <li key={prediction.category_id}>
                      <div className="prediction-heading">
                        <h4>
                          {row.state === "assigned"
                            ? "Assigned: "
                            : row.state === "suggested"
                              ? "Suggested: "
                              : ""}
                          {prediction.category_name}
                        </h4>
                        <span className="evidence-score">
                          {prediction.score_kind === "user_assignment"
                            ? "User assigned"
                            : `${prediction.score_kind === "cosine_similarity" ? "Cosine score" : "Rule strength"} ${prediction.confidence.toFixed(2)}`}
                        </span>
                      </div>
                      <ul className="prediction-reasons">
                        {prediction.reasons.map((reason, i) => (
                          <li key={i}>{reason}</li>
                        ))}
                      </ul>
                    </li>
                  ))}
                </ul>
              ) : (
                <p>
                  {row.code
                    ? errors[row.code]
                    : row.state === "unclassified"
                      ? row.assessment?.reason ||
                        "No supported category has enough evidence."
                      : row.assessment?.reason ||
                        "Use Classify this page to run the local classifier."}
                </p>
              )}
              {row.predictions.length > 0 && row.assessment?.reason && (
                <p>{row.assessment.reason}</p>
              )}
              {row.predictions.length > 1 && (
                <p className="help">
                  Several categories are suggested. Choose a primary category
                  below to prepare a Gmail label preview.
                </p>
              )}
              {semanticEvidence(row)}
              {controls(row)}
              {row.predictions.length > 0 && (
                <details className="technical-details">
                  <summary>Review details</summary>
                  {row.predictions.map((prediction) => (
                    <p key={prediction.category_id}>
                      {row.assessment?.used_classifier ||
                        prediction.classifier_name}{" "}
                      · {prediction.classifier_version} · Reviewed{" "}
                      <time
                        dateTime={new Date(prediction.timestamp).toISOString()}
                      >
                        {new Date(prediction.timestamp).toLocaleString()}
                      </time>
                    </p>
                  ))}
                </details>
              )}
            </article>
          ))}
        </div>
      )}
      <div className="account-actions">
        <button
          disabled={busy || offset === 0}
          onClick={() => {
            setPage(null);
            setMessage("");
            setOffset(Math.max(0, offset - 5));
          }}
        >
          Previous review page
        </button>
        <button
          disabled={busy || !page?.hasMore}
          onClick={() => {
            setPage(null);
            setMessage("");
            setOffset(offset + 5);
          }}
        >
          Next review page
        </button>
      </div>
    </section>
  );
}
