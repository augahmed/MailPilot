import { useEffect, useRef, useState } from "react";
import { askMail, type AskRequest, type AskResponse } from "../../lib/ipc/ask";
import type { OpenMail } from "../../lib/ipc/reader";
export function AskMailPilot({ onOpen }: { onOpen: OpenMail }) {
  const [response, setResponse] = useState<AskResponse | null>(null);
  const [question, setQuestion] = useState("");
  const [sender, setSender] = useState("");
  const [from, setFrom] = useState("");
  const [until, setUntil] = useState("");
  const [error, setError] = useState("");
  const [pending, setPending] = useState(false);
  const mounted = useRef(false);
  const revision = useRef(0);
  const requesting = useRef(false);
  useEffect(() => {
    mounted.current = true;
    let active = true;
    let timer: ReturnType<typeof setTimeout>;
    const poll = async () => {
      const rev = revision.current;
      try {
        if (requesting.current) return;
        const r = await askMail();
        if (active && rev === revision.current && !requesting.current) {
          setResponse(r);
          setError("");
          if (r.code === "unauthorized") {
            setQuestion("");
            setSender("");
            setFrom("");
            setUntil("");
          }
        }
      } catch {
        if (active && rev === revision.current)
          setError(
            "Cannot reach Ask MailPilot. Open the desktop app or retry.",
          );
      } finally {
        if (active) timer = setTimeout(() => void poll(), 1000);
      }
    };
    void poll();
    return () => {
      active = false;
      mounted.current = false;
      clearTimeout(timer);
    };
  }, []);
  const act = async (p: Partial<AskRequest>) => {
    if (!response?.accountId || requesting.current) return;
    requesting.current = true;
    const rev = ++revision.current;
    setPending(true);
    setError("");
    try {
      const r = await askMail({ accountId: response.accountId, ...p });
      if (mounted.current && rev === revision.current) setResponse(r);
    } catch {
      if (mounted.current && rev === revision.current)
        setError("Ask request failed. Check your inputs and retry.");
    } finally {
      requesting.current = false;
      if (mounted.current) setPending(false);
    }
  };
  const busy =
    response?.state === "retrieving" || response?.state === "generating";
  return (
    <section className="status-card" aria-labelledby="ask-heading">
      <h2 id="ask-heading">Ask MailPilot</h2>
      <p>
        Search cached mail locally, review the selected evidence, then approve
        AI generation. Configure AI in Draft a reply first. No mailbox changes
        are available to the model.
      </p>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void act({
            operation: "prepare",
            question,
            sender,
            from: from ? Date.parse(`${from}T00:00:00Z`) : null,
            until: until ? Date.parse(`${until}T00:00:00Z`) + 86400000 : null,
          });
        }}
      >
        <label>
          Question
          <input
            required
            maxLength={250}
            value={question}
            onChange={(e) => setQuestion(e.target.value)}
            placeholder="Which recruiter mentioned two technical interviews?"
          />
        </label>
        <label>
          Ask sender contains
          <input
            maxLength={64}
            value={sender}
            onChange={(e) => setSender(e.target.value)}
          />
        </label>
        <label>
          Ask from date (UTC)
          <input
            type="date"
            value={from}
            onChange={(e) => setFrom(e.target.value)}
          />
        </label>
        <label>
          Ask through date (UTC)
          <input
            type="date"
            value={until}
            onChange={(e) => setUntil(e.target.value)}
          />
        </label>
        <button
          disabled={!response?.accountId || busy || pending || !question.trim()}
        >
          Find evidence locally
        </button>
      </form>
      {error && <p role="alert">{error}</p>}
      {response?.code && (
        <p role="alert">
          {response.code === "ai_disabled"
            ? "Enable AI in Draft a reply settings to use Ask MailPilot."
            : response.code === "embedding_unavailable"
              ? "Configure local MiniLM and index cached mail in Search first."
              : response.code === "unauthorized"
                ? "Connect Gmail to access cached mail."
                : `Ask could not complete (${response.code}). Retrieve evidence again before retrying.`}
        </p>
      )}
      {busy && (
        <p role="status">
          {response.state === "retrieving"
            ? "Retrieving local evidence…"
            : "Generating from approved evidence…"}
        </p>
      )}
      {busy && (
        <button
          disabled={pending}
          onClick={() =>
            void act({ operation: "cancel", jobId: response.jobId })
          }
        >
          Cancel Ask
        </button>
      )}
      {(response?.partial || response?.truncated) && (
        <p>
          Context is partial or excerpted. Answers may miss details or other
          messages.
        </p>
      )}
      {response?.state === "preview" && (
        <div>
          <h3>Review outbound AI context</h3>
          <p>
            {response.external ? "External provider" : "Local provider"}:{" "}
            {response.provider} · {response.model}
          </p>
          <p>
            Expires {new Date(response.expiresAt).toLocaleString()}. Only the
            question and evidence below will be submitted with this policy.
            Email instructions are untrusted data.
          </p>
          <h4>Policy</h4>
          <pre>{response.policy}</pre>
          <h4>Question</h4>
          <pre>{response.instruction}</pre>
          <h4>Untrusted email context</h4>
          <pre>{response.context}</pre>
          <button
            disabled={pending}
            onClick={() =>
              void act({
                operation: "generate",
                previewId: response.previewId,
                approved: true,
              })
            }
          >
            Approve context and generate answer
          </button>
        </div>
      )}
      {response?.state === "answered" && (
        <div aria-live="polite">
          <h3>Answer</h3>
          {response.answer.insufficient ? (
            <p>
              There is insufficient evidence in the retrieved cached mail to
              answer this question.
            </p>
          ) : (
            <>
              <p>AI interpretation; check the quoted evidence.</p>
              {response.answer.claims.map((c, i) => {
                const source = response.sources.find(
                  (s) => s.ref === c.source,
                )!;
                return (
                  <div key={i}>
                    <p>{c.text}</p>
                    <blockquote>{c.quote}</blockquote>
                    <button
                      onClick={(e) =>
                        onOpen(
                          response.accountId,
                          source.emailId,
                          e.currentTarget,
                        )
                      }
                    >
                      Open {source.ref}: {source.subject || "(No subject)"}
                    </button>
                    <p>
                      {source.sender} ·{" "}
                      {new Date(source.receivedAt).toLocaleString()}
                    </p>
                  </div>
                );
              })}
            </>
          )}
        </div>
      )}
      {response && response.state !== "idle" && !busy && (
        <button
          disabled={pending}
          onClick={() => void act({ operation: "clear" })}
        >
          Clear Ask context and answer
        </button>
      )}
      <p className="help">
        Answers cover indexed cached mail only, not your entire mailbox. No
        attachments or unrelated thread messages are included. Cancellation
        cannot recall an external request already sent.
      </p>
    </section>
  );
}
