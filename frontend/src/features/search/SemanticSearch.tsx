import { useEffect, useRef, useState } from "react";
import {
  emptyFilter,
  searchControl,
  vectorSearch,
  type SearchResponse,
} from "../../lib/ipc/search";
import { manageCategories, type Category } from "../../lib/ipc/client";
import type { OpenMail } from "../../lib/ipc/reader";

const messages: Record<string, string> = {
  unauthorized: "Connect Gmail to search its local cache.",
  validation: "Use a shorter query (up to 254 model tokens) and valid dates.",
  model_unavailable:
    "The bundled local model is unavailable. Ordinary mail reading still works.",
  inference_failed: "Local inference failed. Try again.",
  stale: "Cached evidence changed. Search again for current results.",
  cancelled: "Search cancelled.",
  timeout: "Search timed out. Try again after indexing finishes.",
  busy: "The previous search is stopping. Try again shortly.",
  storage_error: "Local storage could not complete search.",
  unavailable: "Local search is unavailable.",
  too_large: "Search results were too large. Request fewer results.",
};
export function SemanticSearch({
  onOpen,
  onEvidence,
  active: visible = true,
}: {
  onOpen: OpenMail;
  active?: boolean;
  onEvidence: (evidence: { text: string; revision: string }) => void;
}) {
  const [result, setResult] = useState<SearchResponse | null>(null);
  const [mode, setMode] = useState<"keyword" | "semantic" | "hybrid">("hybrid");
  const [query, setQuery] = useState("");
  const [sender, setSender] = useState("");
  const [after, setAfter] = useState("");
  const [before, setBefore] = useState("");
  const [categoryId, setCategoryId] = useState("");
  const [categoryOptions, setCategoryOptions] = useState<{
    accountId: string;
    rows: Category[];
    failed: boolean;
  } | null>(null);
  const [categoryRefresh, setCategoryRefresh] = useState(0);
  const [limit, setLimit] = useState(10);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const epoch = useRef(0);
  const mounted = useRef(false);
  const pending = useRef(false);
  const current = useRef<SearchResponse | null>(null);
  useEffect(() => {
    mounted.current = true;
    let active = true;
    let timer: ReturnType<typeof setTimeout>;
    const poll = async () => {
      const revision = epoch.current;
      try {
        const r = await vectorSearch(
          searchControl(
            "status",
            current.current?.accountId ?? "",
            current.current?.jobId ?? "",
          ),
        );
        if (active && revision === epoch.current && !pending.current) {
          current.current = r;
          setResult(r);
          setError("");
        }
      } catch {
        if (active && revision === epoch.current && !pending.current) {
          current.current = null;
          setResult(null);
          setError("Could not read local search status.");
        }
      } finally {
        if (active)
          timer = setTimeout(
            () => {
              void poll();
            },
            current.current?.status === "running" ? 350 : 5000,
          );
      }
    };
    void poll();
    return () => {
      active = false;
      mounted.current = false;
      clearTimeout(timer);
      const r = current.current;
      if (r?.status === "running")
        void vectorSearch(searchControl("cancel", r.accountId, r.jobId)).catch(
          () => {},
        );
    };
  }, []);
  useEffect(() => {
    let active = true;
    const accountId = result?.accountId;
    if (!accountId || !visible) return;
    const load = async () => {
      setCategoryOptions(null);
      const rows = new Map<string, Category>();
      try {
        // The existing category contract returns five rows per page. Publish
        // only a complete account-scoped list, never a misleading partial menu.
        for (let offset = 0; offset <= 1_000_000; offset += 5) {
          const page = await manageCategories({
            operation: "list",
            accountId,
            offset,
          });
          if (!active) return;
          if (
            page.code ||
            page.accountId !== accountId ||
            (page.hasMore && page.rows.length !== 5)
          )
            throw new Error("Category list unavailable");
          for (const category of page.rows) {
            if (rows.has(category.id)) throw new Error("Category list changed");
            rows.set(category.id, category);
          }
          if (!page.hasMore) {
            setCategoryOptions({
              accountId,
              rows: [...rows.values()],
              failed: false,
            });
            setCategoryId((selected) => (rows.has(selected) ? selected : ""));
            return;
          }
        }
        throw new Error("Category list exceeds supported pagination");
      } catch {
        if (active) setCategoryOptions({ accountId, rows: [], failed: true });
      }
    };
    void load();
    return () => {
      active = false;
    };
  }, [result?.accountId, categoryRefresh, visible]);
  const act = async (cancel = false) => {
    if (!result?.accountId || pending.current) return;
    pending.current = true;
    epoch.current++;
    setBusy(true);
    setError("");
    try {
      const r = await vectorSearch(
        cancel
          ? searchControl("cancel", result.accountId, result.jobId)
          : {
              operation: "start",
              accountId: result.accountId,
              jobId: "",
              query,
              mode,
              limit,
              filter: {
                ...emptyFilter,
                sender,
                after: after ? Date.parse(`${after}T00:00:00Z`) : 0,
                before: before ? Date.parse(`${before}T00:00:00Z`) : 0,
                categoryId,
              },
            },
      );
      if (mounted.current) {
        current.current = r;
        setResult(r);
      }
    } catch {
      if (mounted.current) {
        current.current = null;
        setResult(null);
        setError(
          "Search failed. Check your query and filters, then try again.",
        );
      }
    } finally {
      pending.current = false;
      epoch.current++;
      if (mounted.current) setBusy(false);
    }
  };
  const running = result?.status === "running";
  const loadedCategories =
    categoryOptions?.accountId === result?.accountId ? categoryOptions : null;
  return (
    <section
      className="status-card feature-panel semantic-search"
      aria-labelledby="semantic-search-heading"
      aria-busy={busy || running}
    >
      <h2 id="semantic-search-heading">Email search</h2>
      <p>
        Search cached mail with keywords, meaning, or both. Search runs only
        when you submit; no email or query is sent to an AI provider.
      </p>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void act();
        }}
      >
        <label className="search-field">
          Search
          <input
            type="search"
            placeholder="Find an email, topic, or reference…"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            required
            maxLength={2048}
          />
        </label>
        <fieldset className="filter-grid">
          <legend>Search options</legend>
          <label>
            Search mode
            <select
              value={mode}
              onChange={(e) => setMode(e.target.value as typeof mode)}
            >
              <option value="hybrid">Hybrid</option>
              <option value="keyword">Keyword only</option>
              <option value="semantic">Semantic only</option>
            </select>
          </label>
          <label>
            Sender contains
            <input
              value={sender}
              onChange={(e) => setSender(e.target.value)}
              maxLength={200}
            />
          </label>
          <label>
            Received on or after (UTC)
            <input
              type="date"
              value={after}
              onChange={(e) => setAfter(e.target.value)}
            />
          </label>
          <label>
            Received before (UTC)
            <input
              type="date"
              value={before}
              onChange={(e) => setBefore(e.target.value)}
            />
          </label>
          <label>
            Assigned categories
            <select
              disabled={!loadedCategories || loadedCategories.failed}
              value={loadedCategories ? categoryId : ""}
              onChange={(e) => setCategoryId(e.target.value)}
            >
              <option value="">All categories</option>
              {(loadedCategories?.rows ?? []).map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                  {!c.enabled ? " (disabled)" : ""}
                </option>
              ))}
            </select>
          </label>
          <label>
            Maximum emails
            <select
              value={limit}
              onChange={(e) => setLimit(Number(e.target.value))}
            >
              {[5, 10, 20].map((n) => (
                <option key={n} value={n}>
                  {n}
                </option>
              ))}
            </select>
          </label>
        </fieldset>
        <div className="category-filter-status">
          {result?.accountId && !loadedCategories && (
            <span role="status">Loading all category options…</span>
          )}
          {loadedCategories?.failed && (
            <span role="alert">
              Could not load all categories. Refresh to retry; other filters
              still work.
            </span>
          )}
          <button
            className="text-button"
            type="button"
            disabled={!result?.accountId || !loadedCategories}
            onClick={() => {
              setCategoryOptions(null);
              setCategoryRefresh((value) => value + 1);
            }}
          >
            Refresh category options
          </button>
        </div>
        <div className="account-actions">
          <button
            disabled={
              busy ||
              running ||
              !result?.accountId ||
              !query.trim() ||
              (!!categoryId && (!loadedCategories || loadedCategories.failed))
            }
          >
            Search locally
          </button>
          {running && (
            <button
              type="button"
              disabled={busy}
              onClick={() => void act(true)}
            >
              Cancel search
            </button>
          )}
        </div>
      </form>
      <p role="status" className="feature-status">
        {error ||
          (result?.code
            ? messages[result.code]
            : running
              ? "Searching locally…"
              : result?.status === "complete"
                ? `${result.emails.length} emails · ${result.elapsedMs.toFixed(1)} ms · ${result.scannedChunks} chunks searched`
                : "Ready for a local query.")}
      </p>
      {result?.accountId && (
        <p>
          Keyword coverage: {result.keywordMessages} of{" "}
          {result.eligibleMessages} cached emails. Semantic coverage:{" "}
          {result.searchableMessages} of {result.eligibleMessages}.
          {result.searchableMessages < result.eligibleMessages &&
            " Semantic index is partial; unfinished or incompatible vectors are excluded."}
          {result.keywordMessages < result.eligibleMessages &&
            " Sync to prepare missing or changed cached text."}{" "}
          Keyword results may contain only the available part of a message. Use
          Local Email Index to update semantic coverage.
        </p>
      )}
      {result?.status === "complete" && (
        <p>
          Mode: {result.mode}.{" "}
          {result.warning === "semantic_unavailable" &&
            "Semantic retrieval unavailable; showing keyword-only results."}
        </p>
      )}
      <p className="help">
        Matches are relevance candidates, even for unrelated queries. Similarity
        is not confidence. Exact phrases and identifiers receive priority in
        keyword and hybrid modes. Separate replies may disagree; read the
        original evidence.
      </p>
      {result?.status === "complete" && result.emails.length === 0 && (
        <div className="empty-state">
          <h3>No matching cached emails</h3>
          <p>
            Try a different phrase, a broader date range, or another search
            mode.
          </p>
        </div>
      )}
      {result?.status === "complete" && (
        <ol className="email-results">
          {result.emails.map((email) => (
            <li key={email.emailId}>
              <button
                className="result-subject"
                onClick={(e) => {
                  onEvidence({
                    text: email.evidence.map((v) => v.excerpt).join("\n\n"),
                    revision: email.readerRevision,
                  });
                  onOpen(result.accountId, email.emailId, e.currentTarget);
                }}
              >
                {email.subject || "(No subject)"}
              </button>
              <p>
                {email.sender} · {new Date(email.receivedAt).toLocaleString()}
              </p>
              <details className="result-details">
                <summary>Ranking details</summary>
                <p>
                  {email.exactMatch && "Exact literal match · "}
                  {email.keywordRank > 0 &&
                    `Keyword rank ${email.keywordRank} · `}
                  {email.semanticRank > 0 &&
                    `Semantic rank ${email.semanticRank} · `}
                  {result.mode === "hybrid" &&
                    `Fusion score ${email.rankScore.toFixed(4)} · `}
                  {email.partialContent && "Partial cached content"}
                </p>
              </details>
              {email.evidence.map((v) => (
                <div key={v.chunkId}>
                  <p>
                    {v.scoreKind === "cosine"
                      ? `Cosine similarity ${v.score.toFixed(3)}`
                      : "Keyword evidence"}
                  </p>
                  <blockquote>{v.excerpt}</blockquote>
                </div>
              ))}
            </li>
          ))}
        </ol>
      )}
    </section>
  );
}
