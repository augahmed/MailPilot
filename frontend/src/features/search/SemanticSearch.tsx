import { useEffect, useRef, useState, type FormEvent } from "react";
import {
  semanticSearch,
  manageCategories,
  type SemanticSearchResponse,
  type Category,
} from "../../lib/ipc/client";

const messages = {
  unauthorized: "Connect Gmail to search this account’s cached mail.",
  validation: "Check the query, filters, date range and result limit.",
  conflict: "The job, mail or filter changed. Refresh and search again.",
  cancelled: "Stopped. Saved local embeddings remain available.",
  embedding_unavailable:
    "Install and configure the local MiniLM model to index and search mail.",
  storage_error: "Could not read the local index. Retry or restart MailPilot.",
  busy: "Another local search or indexing job is running.",
  response_too_large:
    "The result exceeds the supported display size. Try fewer results.",
};
export function SemanticSearch() {
  const [response, setResponse] = useState<SemanticSearchResponse | null>(null);
  const [query, setQuery] = useState("");
  const [sender, setSender] = useState("");
  const [categoryId, setCategoryId] = useState("");
  const [from, setFrom] = useState("");
  const [until, setUntil] = useState("");
  const [topK, setTopK] = useState(10);
  const [categories, setCategories] = useState<Category[]>([]);
  const [categoryOffset, setCategoryOffset] = useState(0);
  const [moreCategories, setMoreCategories] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const mounted = useRef(false);
  const revision = useRef(0);
  const requesting = useRef(false);
  useEffect(() => {
    mounted.current = true;
    let active = true;
    let timer: ReturnType<typeof setTimeout>;
    const poll = async () => {
      const current = revision.current;
      try {
        if (requesting.current) return;
        const result = await semanticSearch();
        if (active && current === revision.current && !requesting.current) {
          setResponse(result);
          setError(result.code ? messages[result.code] : "");
          if (result.code === "unauthorized") {
            setQuery("");
            setSender("");
            setCategoryId("");
            setFrom("");
            setUntil("");
            setCategories([]);
          }
        }
      } catch {
        if (active && current === revision.current) {
          setResponse(null);
          setError("Cannot reach local search. Retry or restart MailPilot.");
        }
      } finally {
        if (active)
          timer = setTimeout(() => {
            void poll();
          }, 1000);
      }
    };
    void poll();
    void manageCategories({ operation: "list" })
      .then((page) => {
        if (active && !page.code) {
          setCategories(page.rows);
          setMoreCategories(page.hasMore);
        }
      })
      .catch(() => {
        if (active) setError("Could not load category filters.");
      });
    return () => {
      active = false;
      mounted.current = false;
      clearTimeout(timer);
    };
  }, []);
  const running =
    response?.state === "indexing" || response?.state === "searching";
  const action = async (operation: "query" | "index" | "cancel") => {
    if (!response?.accountId || requesting.current) return;
    const current = ++revision.current;
    requesting.current = true;
    setPending(true);
    setError("");
    // Hide earlier results while the next operation starts.
    if (operation !== "cancel") setResponse({ ...response, rows: [] });
    try {
      const parameters =
        operation === "query"
          ? {
              query,
              topK,
              sender,
              categoryId,
              from: from ? Date.parse(`${from}T00:00:00Z`) : null,
              until: until ? Date.parse(`${until}T00:00:00Z`) + 86400000 : null,
            }
          : {};
      const result = await semanticSearch({
        operation,
        accountId: response.accountId,
        jobId: operation === "cancel" ? response.jobId : "",
        ...parameters,
      });
      if (mounted.current && current === revision.current) {
        setResponse(result);
        setError(result.code ? messages[result.code] : "");
      }
    } catch {
      if (mounted.current && current === revision.current) {
        setError("The local search request failed. Check inputs and retry.");
      }
    } finally {
      requesting.current = false;
      if (mounted.current && current === revision.current) setPending(false);
    }
  };
  const submit = (event: FormEvent) => {
    event.preventDefault();
    void action("query");
  };
  const loadCategories = async () => {
    try {
      const offset = categoryOffset + 5;
      const page = await manageCategories({ operation: "list", offset });
      if (
        mounted.current &&
        !page.code &&
        page.accountId === response?.accountId
      ) {
        setCategories((previous) => [
          ...previous,
          ...page.rows.filter(
            (row) => !previous.some((old) => old.id === row.id),
          ),
        ]);
        setCategoryOffset(offset);
        setMoreCategories(page.hasMore);
      }
    } catch {
      if (mounted.current) setError("Could not load more category filters.");
    }
  };
  const available =
    !!response?.accountId && response.code !== "embedding_unavailable";
  return (
    <section
      className="status-card mailbox-search"
      aria-labelledby="search-heading"
    >
      <h2 id="search-heading">Search your mailbox</h2>
      <p>
        Semantic search finds related cached mail by meaning. Retrieval and
        indexing run on your device. Gmail keyword search is a separate provider
        feature.
      </p>
      <p className="help">
        Results cover indexed, complete cached messages, not your entire Gmail
        mailbox. Similarity is a cosine score, not a probability. Results
        reflect the cache at the last search.
      </p>
      {error && <p role="alert">{error}</p>}
      {response && (
        <div role="status" aria-live="polite">
          <p>
            {response.counts.indexed} indexed of {response.counts.eligible}{" "}
            complete cached messages ({response.counts.cached} cached total).
          </p>
          {response.counts.indexed < response.counts.eligible && (
            <p>Index remaining cached messages to broaden coverage.</p>
          )}
          {response.counts.cached > response.counts.eligible && (
            <p>
              {response.counts.cached - response.counts.eligible} messages have
              missing or incomplete bodies and cannot be indexed.
            </p>
          )}
          {response.state === "indexing" && (
            <p>
              Indexing locally… {response.processed} saved; {response.errors}{" "}
              errors.
            </p>
          )}
          {response.state === "searching" && <p>Searching local embeddings…</p>}
          {response.state === "completed" && response.operation === "index" && (
            <p>
              Indexing finished: {response.processed} saved; {response.errors}{" "}
              errors.
            </p>
          )}
          {response.state === "completed" && response.operation === "query" && (
            <p>
              {response.rows.length} matches from {response.scanned} indexed
              candidates.
            </p>
          )}
          {response.partial && (
            <p>
              Partial results: {response.errors} errors.{" "}
              {response.capped
                ? "The scan limit was reached; refine filters or continue indexing."
                : "Some inputs changed or could not be processed."}
            </p>
          )}
          {response.excerptFallback && (
            <p>
              Passage ranking was unavailable; excerpts use local text matching.
            </p>
          )}
        </div>
      )}
      <div className="account-actions">
        <button
          disabled={!available || pending || running}
          onClick={() => void action("index")}
        >
          Index cached mail
        </button>
        <button
          disabled={!running || pending}
          onClick={() => void action("cancel")}
        >
          Cancel local job
        </button>
      </div>
      <form onSubmit={submit}>
        <label>
          Meaning to search for
          <input
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            maxLength={250}
            required
            placeholder="e.g. scheduling a technical interview"
          />
        </label>
        <label>
          Sender contains
          <input
            value={sender}
            onChange={(event) => setSender(event.target.value)}
            maxLength={64}
          />
        </label>
        <label>
          Category
          <select
            value={categoryId}
            onChange={(event) => setCategoryId(event.target.value)}
          >
            <option value="">All categories</option>
            {categories.map((category) => (
              <option key={category.id} value={category.id}>
                {category.name}
                {category.enabled ? "" : " (disabled)"}
              </option>
            ))}
          </select>
        </label>
        {moreCategories && (
          <button type="button" onClick={() => void loadCategories()}>
            Load more category filters
          </button>
        )}
        <label>
          From date (UTC)
          <input
            type="date"
            value={from}
            onChange={(event) => setFrom(event.target.value)}
          />
        </label>
        <label>
          Through date (UTC)
          <input
            type="date"
            value={until}
            onChange={(event) => setUntil(event.target.value)}
          />
        </label>
        <label>
          Maximum results
          <input
            type="number"
            min={1}
            max={20}
            step={1}
            value={topK}
            onChange={(event) => setTopK(Number(event.target.value))}
          />
        </label>
        <button
          type="submit"
          disabled={!available || pending || running || !query.trim()}
        >
          Search locally
        </button>
      </form>
      {response?.state === "completed" &&
        response.operation === "query" &&
        response.rows.length === 0 && (
          <p>No indexed messages match these filters.</p>
        )}
      {response && response.rows.length > 0 && (
        <ol aria-label="Semantic search results">
          {response.rows.map((row) => (
            <li key={row.emailId}>
              <h3>{row.subject || "(No subject)"}</h3>
              <p>
                {row.sender || "Unknown sender"} ·{" "}
                <time dateTime={new Date(row.receivedAt).toISOString()}>
                  {new Date(row.receivedAt).toLocaleString()}
                </time>
              </p>
              <p>Similarity: {row.similarity.toFixed(3)}</p>
              <p>{row.excerpt || "No cached text excerpt is available."}</p>
            </li>
          ))}
        </ol>
      )}
    </section>
  );
}
