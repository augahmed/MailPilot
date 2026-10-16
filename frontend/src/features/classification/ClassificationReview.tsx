import { Fragment, useEffect, useRef, useState } from "react";
import {
  reviewClassifications,
  type ClassificationPage,
} from "../../lib/ipc/client";

const errors = {
  validation: "The review request is invalid. Refresh and try again.",
  unauthorized: "Connect Gmail to review this account’s cached mail.",
  conflict:
    "Mail, labels, or category definitions changed. Refresh and review again.",
  cancelled: "Review stopped. Successfully saved assessments remain available.",
  invalid_message:
    "This message or assessment exceeds the supported review limits.",
  embedding_unavailable:
    "This saved semantic assessment is retired. Run a new deterministic review.",
  storage_error: "Could not load or save a local assessment. Try again.",
};
const states = {
  not_reviewed: "Not reviewed yet",
  unclassified: "Unclassified — no clear category match",
  classified: "Predicted category",
  stale: "Review needed — inputs or classifier changed",
  error: "Could not review",
};
export function ClassificationReview() {
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
  async function act(operation: "list" | "run") {
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
  return (
    <section
      className="status-card classification-review"
      aria-labelledby="classification-heading"
    >
      <h2 id="classification-heading">Classification review</h2>
      <p>
        Review deterministic rule suggestions for downloaded mail, five emails
        at a time. Emails with weak or ambiguous evidence remain unclassified.
      </p>
      <p className="help">
        Rule strength is a heuristic score, not measured probabilities.
        Reviewing saves suggestions locally and does not assign categories or
        change Gmail.
      </p>
      <div role="status" aria-live="polite">
        {busy ? "Reviewing cached mail…" : message}
      </div>
      {error && <p role="alert">{error}</p>}
      <div className="account-actions">
        <button disabled={busy} onClick={() => void act("list")}>
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
        <div className="mail-table">
          <table>
            <caption>Predictions for cached emails</caption>
            <thead>
              <tr>
                <th scope="col">Email</th>
                <th scope="col">Predicted category</th>
                <th scope="col">Evidence score</th>
                <th scope="col">Reasons</th>
              </tr>
            </thead>
            <tbody>
              {page.rows.map((row) => (
                <Fragment key={row.email_id}>
                  {row.predictions.length > 0 ? (
                    row.predictions.map((prediction) => (
                      <tr key={prediction.category_id}>
                        <td>
                          <strong>{row.subject || "(No subject)"}</strong>
                          <small>{row.sender || "Unknown sender"}</small>
                        </td>
                        <td>
                          {prediction.category_name}
                          <small>
                            {row.assessment?.used_classifier ||
                              prediction.classifier_name}{" "}
                            · {prediction.classifier_version}
                          </small>
                        </td>
                        <td>
                          {prediction.score_kind === "cosine_similarity"
                            ? `Cosine ${prediction.confidence.toFixed(3)}`
                            : `Rule strength ${prediction.confidence.toFixed(2)}`}
                        </td>
                        <td>
                          <ul>
                            {prediction.reasons.map((reason, index) => (
                              <li key={index}>{reason}</li>
                            ))}
                          </ul>
                          <Candidates assessment={row.assessment} />
                          <small>
                            Reviewed{" "}
                            <time
                              dateTime={new Date(
                                prediction.timestamp,
                              ).toISOString()}
                            >
                              {new Date(prediction.timestamp).toLocaleString()}
                            </time>
                          </small>
                        </td>
                      </tr>
                    ))
                  ) : (
                    <tr>
                      <td>
                        <strong>{row.subject || "(No subject)"}</strong>
                        <small>{row.sender || "Unknown sender"}</small>
                        {row.bodyState !== "complete" && (
                          <small>
                            {row.bodyState === "unavailable"
                              ? "Body unavailable"
                              : "Body completeness unknown"}
                          </small>
                        )}
                      </td>
                      <td>{states[row.state]}</td>
                      <td>—</td>
                      <td>
                        {row.code
                          ? errors[row.code]
                          : row.state === "unclassified"
                            ? row.assessment?.reason ||
                              "No supported category has enough evidence."
                            : "Use Classify this page to run the local classifier."}
                        <Candidates assessment={row.assessment} />
                      </td>
                    </tr>
                  )}
                </Fragment>
              ))}
            </tbody>
          </table>
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

function Candidates({
  assessment,
}: {
  assessment: ClassificationPage["rows"][number]["assessment"];
}) {
  if (!assessment?.candidates.length) return null;
  return (
    <details>
      <summary>Top category candidates</summary>
      <ol>
        {assessment.candidates.map((candidate) => (
          <li key={candidate.category_id}>
            {candidate.category_name}: cosine {candidate.similarity.toFixed(3)}
          </li>
        ))}
      </ol>
    </details>
  );
}
