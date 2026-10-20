import { useEffect, useRef, useState } from "react";
import {
  manageIndex,
  type IndexRequest,
  type IndexResponse,
} from "../../lib/ipc/indexing";

const states: Record<IndexResponse["status"], string> = {
  idle: "Not started",
  pending: "Cached changes waiting to be indexed",
  running: "Indexing cached email…",
  paused: "Paused",
  cancelled: "Cancelled — saved progress retained",
  interrupted: "Interrupted — resume when ready",
  complete: "Index complete",
  partial: "Finished with partial coverage",
  failed: "Indexing stopped",
};
const messages: Partial<Record<IndexResponse["code"], string>> = {
  unauthorized: "Connect Gmail to access this account’s local cache.",
  validation: "The index request was invalid.",
  unavailable: "The local index is unavailable.",
  busy: "The previous batch is stopping. Try Resume again shortly.",
  storage_error:
    "Local storage could not complete the operation. Saved progress is retained.",
  interrupted:
    "The previous run was interrupted. Resume reuses saved embeddings.",
  cache_changed:
    "Cached mail was removed. Resume to index the remaining cache.",
  preprocessing_changed:
    "Email text cleanup changed. Resume indexing to refresh the saved chunks and embeddings.",
  model_changed:
    "The embedding model changed. Resume builds a compatible generation.",
  model_unavailable:
    "The local model could not load. Restart MailPilot and try again. If this continues, rebuild or reinstall the app with its bundled model resources. Supported systems require Apple Silicon macOS 13.4 or later.",
  inference_failed:
    "Local inference failed. Resume retries unfinished messages using saved progress.",
  pending_changes:
    "Cached mail changed after this run. Start indexing to update coverage.",
};
export function LocalEmailIndex() {
  const [status, setStatus] = useState<IndexResponse | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(false);
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
        const result = await manageIndex("status");
        if (active && !pending.current && current === revision.current) {
          setStatus(result);
          setError(false);
        }
      } catch {
        if (active && !pending.current && current === revision.current) {
          setStatus(null);
          setError(true);
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
      mounted.current = false;
      clearTimeout(timer);
    };
  }, []);
  const act = async (operation: IndexRequest["operation"]) => {
    if (!status?.accountId || pending.current) return;
    revision.current++;
    pending.current = true;
    setBusy(true);
    setError(false);
    try {
      const result = await manageIndex(operation, status.accountId);
      if (mounted.current) setStatus(result);
    } catch {
      if (mounted.current) {
        setStatus(null);
        setError(true);
      }
    } finally {
      revision.current++;
      pending.current = false;
      if (mounted.current) setBusy(false);
    }
  };
  const running = status?.status === "running";
  const canResume =
    status &&
    ["paused", "cancelled", "interrupted", "failed"].includes(status.status);
  const disabled = busy || !status?.accountId;
  return (
    <section
      className="status-card local-email-index"
      aria-labelledby="local-index-heading"
      aria-busy={busy}
    >
      <h2 id="local-index-heading">Local Email Index</h2>
      <p>
        Index all eligible cached messages and enabled category definitions on
        this device using the bundled CPU model (90 MB on disk; several hundred
        MB of memory once loaded). No email is sent to an AI provider, and no
        Gmail fetch is performed.
      </p>
      <p role="status" aria-live="polite">
        {error
          ? "Could not read local index status."
          : status
            ? states[status.status]
            : "Checking local index…"}
      </p>
      {status && (
        <>
          <dl className="index-counts">
            <div>
              <dt>Eligible messages</dt>
              <dd>{status.eligibleMessages}</dd>
            </div>
            <div>
              <dt>Indexed messages</dt>
              <dd>{status.indexedMessages}</dd>
            </div>
            <div>
              <dt>Chunks created</dt>
              <dd>{status.chunksCreated}</dd>
            </div>
            <div>
              <dt>Embeddings generated</dt>
              <dd>{status.embeddingsGenerated}</dd>
            </div>
          </dl>
          <progress
            aria-label="Messages with all available chunks embedded"
            value={status.indexedMessages}
            max={Math.max(1, status.eligibleMessages)}
          />
          <p>
            {status.completeMessages} complete · {status.partialMessages}{" "}
            partial · {status.failedMessages} failed · {status.pendingMessages}{" "}
            pending · {status.unavailableMessages} unavailable
          </p>
          <p>
            Category representations: {status.readyCategories} ready ·{" "}
            {status.partialCategories} partial · {status.pendingCategories}{" "}
            pending of {status.eligibleCategories} enabled.
          </p>
          {status.code && <p className="help">{messages[status.code]}</p>}
        </>
      )}
      <div className="account-actions">
        <button
          disabled={disabled || running}
          onClick={() => void act(canResume ? "resume" : "start")}
        >
          {canResume ? "Resume indexing" : "Start indexing"}
        </button>
        <button
          disabled={disabled || !running}
          onClick={() => void act("pause")}
        >
          Pause indexing
        </button>
        <button
          disabled={disabled || (!running && !canResume)}
          onClick={() => void act("cancel")}
        >
          Cancel indexing
        </button>
        <button
          disabled={disabled || running}
          onClick={() => void act("rebuild")}
        >
          Rebuild index
        </button>
      </div>
      <p className="help">
        Saved progress survives pause, cancellation and restart. Rebuild
        rechecks every cached message and reuses unchanged vectors. Counts
        describe the current compatible index; partial bodies do not imply
        complete email coverage.
      </p>
    </section>
  );
}
