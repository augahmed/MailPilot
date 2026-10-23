import { useCallback, useEffect, useRef, useState } from "react";
import {
  organize,
  manageCategories,
  type Category,
  type OrganizationRequest,
  type OrganizationResponse,
} from "../../lib/ipc/client";
import {
  classificationLabel,
  listOrganizationEmails,
  organizationBatchSize,
  type OrganizationPage,
} from "./listEmails";
import type { SuggestionChoice } from "./OrganizationSuggestionPicker";
import { OrganizationEmailTable } from "./OrganizationEmailTable";

type View = "select" | "review" | "applying" | "recovery";
const messages: Record<string, string> = {
  unauthorized: "Connect Gmail to organize this account’s mail.",
  stale_preview:
    "The selected mail or its classification changed while preparing or reviewing this preview. Go back and preview the current changes again. No new changes were authorized.",
  reconciliation_required:
    "An interrupted batch needs its Gmail outcomes checked before another batch can run.",
  not_found:
    "A selected email has no current category suggestion. Refresh classifications or resume Local Email Index if its content changed, then preview again.",
  cancelled: "Work stopped. Review any recorded outcomes before continuing.",
  reauth_required: "Reconnect Gmail before continuing.",
  unsupported:
    "This provider does not support the required Gmail label operations.",
  operation_failed:
    "The operation failed. Review any recorded outcomes before continuing.",
  validation:
    "This preview is invalid or too large. Select up to 25 eligible emails; use a smaller selection if their label details exceed the supported size.",
  busy: "An earlier account operation is still stopping.",
};
const conflictMessages: Record<string, string> = {
  classification_changed:
    "The local classification evidence changed after this preview. Go Back, refresh classifications if needed, and preview again. No new Gmail changes were authorized.",
  mail_changed:
    "The current Gmail message differs from this preview. Go Back and preview the current mail again. No new Gmail changes were authorized.",
  labels_changed:
    "Gmail labels on a selected message changed after this preview. Go Back and preview its current labels again. No new Gmail changes were authorized.",
  target_label_changed:
    "A target Gmail label was created, renamed or replaced after this preview. Go Back and preview again to use the current label. No new Gmail changes were authorized.",
  expired:
    "This preview expired. Go Back and preview the current changes again. No new Gmail changes were authorized.",
  plan_changed:
    "This saved preview is no longer available for confirmation. Go Back and prepare a fresh preview. No new Gmail changes were authorized.",
};
function stepMessage(code: string) {
  if (code === "label_exists_ownership_unknown")
    return "The label exists in Gmail, but the earlier creation could not be verified. Go Back and prepare a fresh preview to reuse it. This batch did not add it to the selected email.";
  if (code === "blocked_by_uncertainty")
    return "Skipped because an earlier Gmail operation could not be verified. Check recorded outcomes before preparing a fresh preview.";
  return code.replaceAll("_", " ");
}
function uncertain(result: OrganizationResponse | null) {
  return (
    result?.state === "uncertain" ||
    result?.state === "applying" ||
    result?.code === "reconciliation_required" ||
    result?.steps.some(
      (step) => step.state === "uncertain" || step.state === "in_flight",
    )
  );
}
function hasUndo(result: OrganizationResponse | null) {
  return (
    result?.kind === "label_delta" &&
    result.steps.some((step) => step.kind === "add" && step.state === "applied")
  );
}
function completion(result: OrganizationResponse) {
  const count = new Set(
    result.steps
      .filter(
        (step) =>
          step.state === "applied" &&
          step.kind === (result.kind === "undo" ? "remove" : "add"),
      )
      .map((step) => step.emailId),
  ).size;
  if (result.kind === "undo")
    return `Undo confirmed in Gmail for ${count} ${count === 1 ? "email" : "emails"}.`;
  return count
    ? `Gmail label changes confirmed for ${count} ${count === 1 ? "email" : "emails"}.`
    : "The selected emails already have the reviewed Gmail labels. No additions were needed.";
}
export function Organization({ active: visible = true }: { active?: boolean }) {
  const [view, setView] = useState<View>("select");
  const [suggestionMode, setSuggestionMode] = useState<"choose" | "highest">(
    "choose",
  );
  const [choices, setChoices] = useState<Record<string, SuggestionChoice>>({});
  const [source, setSource] = useState<"" | "phishing">("");
  const [page, setPage] = useState<OrganizationPage | null>(null);
  const [batch, setBatch] = useState<OrganizationResponse | null>(null);
  const [completed, setCompleted] = useState<OrganizationResponse | null>(null);
  const [notice, setNotice] = useState("");
  const [selected, setSelected] = useState<string[]>([]);
  const [offset, setOffset] = useState(0);
  const [history, setHistory] = useState<number[]>([]);
  const [reload, setReload] = useState(0);
  const [categoryId, setCategoryId] = useState("");
  const [categoryReload, setCategoryReload] = useState(0);
  const [categories, setCategories] = useState<{
    accountId: string;
    rows: Category[];
    failed: boolean;
  } | null>(null);
  const categoryName = categories?.rows.find(
    (category) => category.id === categoryId,
  )?.name;
  const [sort, setSort] = useState("category");
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [now, setNow] = useState(Date.now);
  const mounted = useRef(false);
  const pending = useRef(false);
  const revision = useRef(0);
  const account = useRef("");
  const restored = useRef(false);
  const stageHeading = useRef<HTMLHeadingElement>(null);
  const focusStage = useCallback(() => {
    requestAnimationFrame(() => {
      if (!mounted.current || stageHeading.current?.closest("[hidden]")) return;
      stageHeading.current?.focus({ preventScroll: true });
      stageHeading.current?.scrollIntoView?.({ block: "start" });
    });
  }, []);
  const clearAccount = useCallback(() => {
    account.current = "";
    restored.current = false;
    setPage(null);
    setBatch(null);
    setCompleted(null);
    setNotice("");
    setCategories(null);
    setCategoryId("");
    setHistory([]);
    setOffset(0);
    setSelected([]);
    setChoices({});
    setView("select");
  }, []);
  const receive = useCallback(
    (result: OrganizationResponse, refresh = true) => {
      if (
        result.code === "unauthorized" ||
        (result.accountId &&
          account.current &&
          result.accountId !== account.current)
      ) {
        clearAccount();
        setError(messages.unauthorized!);
        return;
      }
      setError(
        result.code === "stale_preview" && result.conflictReason
          ? (conflictMessages[result.conflictReason] ?? messages.stale_preview!)
          : (messages[result.code] ?? ""),
      );
      if (result.state === "applied" && !result.code) {
        setCompleted(result);
        setNotice(completion(result));
        setBatch(null);
        setSelected([]);
        setChoices({});
        setView("select");
        setOffset(0);
        setHistory([]);
        if (result.steps.some((step) => step.code === "cache_refresh_failed"))
          setError(
            "Gmail changed, but cached labels could not be refreshed. Sync Gmail to update MailPilot; do not blindly reapply this batch.",
          );
        if (refresh) {
          setLoading(true);
          setReload((value) => value + 1);
        }
      } else if (
        uncertain(result) ||
        (result.id &&
          result.steps.length &&
          ["partial", "failed", "cancelled", "error"].includes(result.state))
      ) {
        setBatch(result);
        setView("recovery");
      } else if (
        result.state === "preview" &&
        result.id &&
        result.expiresAt > Date.now()
      ) {
        setBatch(result);
        if (result.kind !== "undo")
          setSelected(result.rows.map((row) => row.emailId));
        setView("review");
      } else {
        // A failed preview made no Gmail writes. Keep the selection visible and
        // retryable instead of disguising it as an empty results screen.
        setBatch(null);
        setView("select");
        if (refresh) {
          setChoices({});
          setLoading(true);
          setReload((value) => value + 1);
        }
        if (
          result.state === "expired" ||
          (result.state === "preview" && result.expiresAt <= Date.now())
        )
          setError(
            "This preview expired. Select emails and preview their current labels again.",
          );
      }
      focusStage();
    },
    [clearAccount, focusStage],
  );
  useEffect(() => {
    mounted.current = true;
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => {
      mounted.current = false;
      clearInterval(timer);
    };
  }, []);
  useEffect(() => {
    let active = true;
    const accountId = page?.accountId ?? "";
    if (!accountId || !visible) return;
    async function loadCategories() {
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
            throw new Error("Category source changed");
          for (const row of result.rows) {
            if (rows.has(row.id)) throw new Error("Duplicate category");
            rows.set(row.id, row);
          }
          if (!result.hasMore) {
            const available = [...rows.values()].filter((row) => row.enabled);
            setCategories({ accountId, rows: available, failed: false });
            setCategoryId((current) =>
              current === "__unclassified__" ||
              available.some((category) => category.id === current)
                ? current
                : "",
            );
            return;
          }
        }
        throw new Error("Category source exceeds supported bounds");
      } catch {
        if (active) setCategories({ accountId, rows: [], failed: true });
      }
    }
    void loadCategories();
    return () => {
      active = false;
    };
  }, [page?.accountId, categoryReload, visible]);
  useEffect(() => {
    if (!visible) return;
    let active = true;
    const current = ++revision.current;
    async function load() {
      setLoading(true);
      try {
        const result = await listOrganizationEmails(
          {
            accountId: account.current || undefined,
            offset,
            source: source || undefined,
            suggestionMode: source ? undefined : suggestionMode,
          },
          () => active && !pending.current && current === revision.current,
          { categoryName, unclassified: categoryId === "__unclassified__" },
        );
        if (!result) return;
        if (
          result.code === "unauthorized" ||
          (account.current && account.current !== result.accountId)
        ) {
          clearAccount();
          setError(messages.unauthorized!);
          return;
        }
        account.current = result.accountId;
        setPage(result);
        // The backend may have refreshed sources or suggestions during preview.
        // Revalidate selection against this list, never the discarded page.
        setChoices({});
        setSelected((current) =>
          current.filter((id) =>
            result.rows.some(
              (row) => row.emailId === id && row.change === "candidate",
            ),
          ),
        );
        setLoading(false);
        if (result.code)
          setError(messages[result.code] ?? "Could not load cached emails.");
        if (!result.code && !restored.current) {
          restored.current = true;
          const saved = await organize({
            operation: "status",
            accountId: result.accountId,
          });
          if (active && !pending.current && current === revision.current) {
            if (saved.state !== "idle" || saved.code) receive(saved, false);
          }
        }
      } catch {
        if (active && !pending.current && current === revision.current) {
          setPage(null);
          setSelected([]);
          setChoices({});
          setError(
            "Could not load cached emails. Refresh the list or restart MailPilot.",
          );
        }
      } finally {
        if (active && current === revision.current) setLoading(false);
      }
    }
    void load();
    return () => {
      active = false;
    };
  }, [
    offset,
    source,
    suggestionMode,
    categoryId,
    categoryName,
    reload,
    clearAccount,
    receive,
    visible,
  ]);
  useEffect(() => {
    if (!visible) return;
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
          (result.accountId && result.accountId !== expected) ||
          uncertain(result)
        )
          receive(result, false);
      } catch {
        if (
          active &&
          !pending.current &&
          current === revision.current &&
          expected === account.current
        )
          setError(
            "Could not check recorded Gmail outcomes. Refresh the list before continuing if the connection was interrupted.",
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
  }, [receive, visible]);
  async function act(operation: OrganizationRequest["operation"]) {
    if (pending.current || !account.current) return;
    if (
      operation === "apply" &&
      (!batch ||
        batch.state !== "preview" ||
        batch.code ||
        batch.expiresAt <= Date.now() ||
        uncertain(batch))
    )
      return;
    revision.current++;
    pending.current = true;
    setBusy(true);
    setError("");
    if (operation === "apply") {
      setView("applying");
      focusStage();
    }
    const expected = account.current;
    try {
      let result = await organize({
        operation,
        accountId: expected,
        source: operation === "preview" ? source || undefined : undefined,
        suggestionMode:
          operation === "preview" && !source ? suggestionMode : undefined,
        choices:
          operation === "preview" && !source && suggestionMode === "choose"
            ? selected.flatMap((emailId) =>
                choices[emailId]
                  ? [{ emailId, categoryId: choices[emailId]!.categoryId }]
                  : [],
              )
            : undefined,
        emailIds: operation === "preview" ? selected : [],
        actionId: operation === "apply" ? batch?.id : undefined,
        digest: operation === "apply" ? batch?.digest : undefined,
      });
      while (mounted.current && result.state === "running") {
        await new Promise((resolve) => setTimeout(resolve, 250));
        if (!mounted.current || expected !== account.current) return;
        result = await organize({ operation: "status", accountId: expected });
      }
      if (!mounted.current || expected !== account.current) return;
      receive(result);
    } catch {
      if (mounted.current && expected === account.current) {
        if (operation === "apply") {
          // Dispatch may have reached Gmail. Keep the exact plan available and
          // force a status check instead of allowing an unconfirmed retry.
          setView("recovery");
          setError(
            "The connection was interrupted while applying changes. Check Gmail outcomes before continuing; changes may already have reached Gmail.",
          );
          if (batch)
            setBatch({
              ...batch,
              state: "uncertain",
              code: "reconciliation_required",
            });
        } else {
          setError(
            "Could not prepare or check Gmail label changes. No new apply was authorized. Try again or reconnect Gmail.",
          );
        }
      }
    } finally {
      pending.current = false;
      if (mounted.current) setBusy(false);
    }
  }
  const unresolved = !!uncertain(batch);
  const locked = busy || loading || unresolved;
  const canConfirm =
    !!batch &&
    batch.state === "preview" &&
    !batch.code &&
    batch.expiresAt > now &&
    !busy &&
    !unresolved &&
    batch.accountId === page?.accountId;
  const rows = [...(page?.rows ?? [])].sort((a, b) => {
    if (source || sort === "recent") return 0;
    if (sort === "unclassified") {
      const order =
        Number(classificationLabel(b) === "Unclassified") -
        Number(classificationLabel(a) === "Unclassified");
      if (order) return order;
    }
    const order = classificationLabel(a).localeCompare(classificationLabel(b));
    return sort === "category-desc" ? -order : order;
  });
  function choose(ids: string[]) {
    setSelected(ids);
    setError("");
  }
  function resetSelection(resetFilters = false) {
    revision.current++;
    setSelected([]);
    setChoices({});
    setError("");
    if (resetFilters) {
      setCategoryId("");
      setSort("category");
      setOffset(0);
      setHistory([]);
      setLoading(true);
      setReload((value) => value + 1);
    }
  }
  function goBack() {
    revision.current++;
    setBatch(null);
    setChoices({});
    setLoading(true);
    setReload((value) => value + 1);
    setCategoryReload((value) => value + 1);
    setSelected((current) =>
      current.filter((id) =>
        page?.rows.some(
          (row) =>
            row.emailId === id && (row.change === "candidate" || !!choices[id]),
        ),
      ),
    );
    setView("select");
    setError("");
    focusStage();
  }
  const stage = view === "select" ? 1 : view === "review" ? 2 : 3;
  const stageTitle =
    view === "select"
      ? "1. Select emails"
      : view === "review"
        ? batch?.kind === "undo"
          ? "2. Review undo"
          : "2. Review label changes"
        : view === "applying"
          ? "3. Updating Gmail"
          : "3. Check Gmail outcomes";
  return (
    <section
      className="status-card feature-panel organization-panel"
      aria-labelledby="organization-heading"
      aria-busy={busy || loading}
    >
      <h2 id="organization-heading">Organize Gmail labels</h2>
      <p>
        Select emails, review their label changes, then confirm to update Gmail.
        Emails remain in the Inbox; nothing is deleted or archived.
      </p>
      <ol className="workflow-steps" aria-label="Organization progress">
        {["Select emails", "Review changes", "Update Gmail"].map(
          (label, index) => (
            <li
              key={label}
              aria-current={stage === index + 1 ? "step" : undefined}
            >
              <span>{index + 1}</span>
              {label}
            </li>
          ),
        )}
      </ol>
      <h3 ref={stageHeading} tabIndex={-1} id="organization-stage-heading">
        {stageTitle}
      </h3>
      {error && (
        <p className="attention-panel" role="alert">
          {error}
        </p>
      )}
      {view === "select" && notice && (
        <div className="organization-completion" role="status">
          <strong>{notice}</strong>
          {hasUndo(completed) && (
            <button
              className="secondary-button"
              disabled={busy || loading}
              onClick={() => void act("undo_preview")}
            >
              Undo
            </button>
          )}
        </div>
      )}
      {view === "select" && (
        <>
          <p className="help">
            The list uses cached classifications and labels. Preview refreshes
            your selected emails from Gmail and checks their current
            classifications. Single automatic suggestions are ready to use.
            Choose among multiple suggestions, or switch to the highest-ranked
            mode. Manual categories take priority. Gmail uses labels such as
            MailPilot/Receipts rather than moving messages into folders.
          </p>
          <label>
            Review suggestions
            <select
              value={source}
              disabled={busy || loading || unresolved}
              onChange={(event) => {
                revision.current++;
                setSource(event.target.value === "phishing" ? "phishing" : "");
                setOffset(0);
                setHistory([]);
                setCategoryId("");
                setSelected([]);
                setChoices({});
                setPage(null);
                setBatch(null);
                setLoading(true);
                setError("");
              }}
            >
              <option value="">Classification labels</option>
              <option value="phishing">Potential Phishing</option>
            </select>
          </label>
          {!source && (
            <>
              <label>
                Automatic suggestions
                <select
                  value={suggestionMode}
                  disabled={locked}
                  onChange={(event) => {
                    revision.current++;
                    setSuggestionMode(
                      event.target.value === "highest" ? "highest" : "choose",
                    );
                    setSelected([]);
                    setChoices({});
                    setBatch(null);
                    setOffset(0);
                    setHistory([]);
                    setLoading(true);
                    setError("");
                  }}
                >
                  <option value="choose">
                    Multiple suggestions (choose when needed)
                  </option>
                  <option value="highest">
                    Highest-ranked suggestion only
                  </option>
                </select>
              </label>
              <div className="organization-filters">
                <label>
                  Filter by category
                  <select
                    value={categoryId}
                    disabled={
                      busy ||
                      unresolved ||
                      !categories ||
                      categories.accountId !== page?.accountId ||
                      categories.failed
                    }
                    onChange={(event) => {
                      revision.current++;
                      setCategoryId(event.target.value);
                      setOffset(0);
                      setHistory([]);
                      setSelected([]);
                      setChoices({});
                      setLoading(true);
                      setError("");
                    }}
                  >
                    <option value="">All categories</option>
                    <option value="__unclassified__">Unclassified</option>
                    {categories &&
                      categories.accountId === page?.accountId &&
                      categories.rows.map((category) => (
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
                    disabled={locked}
                    onChange={(event) => setSort(event.target.value)}
                  >
                    <option value="category">Category (A–Z)</option>
                    <option value="category-desc">Category (Z–A)</option>
                    <option value="unclassified">Unclassified first</option>
                    <option value="recent">Newest first</option>
                  </select>
                </label>
              </div>
            </>
          )}
          {!source && categories?.failed && (
            <p role="alert">
              Could not load category filters.{" "}
              <button
                className="secondary-button"
                disabled={busy}
                onClick={() => setCategoryReload((value) => value + 1)}
              >
                Retry category filters
              </button>
            </p>
          )}
          <div className="organization-selection-tools">
            <button
              className="secondary-button"
              title="Select eligible emails on this page"
              disabled={
                locked ||
                !rows.some(
                  (row) => row.change === "candidate" || !!choices[row.emailId],
                )
              }
              onClick={() =>
                choose(
                  rows
                    .filter(
                      (row) =>
                        row.change === "candidate" || !!choices[row.emailId],
                    )
                    .map((row) => row.emailId),
                )
              }
            >
              Select all
            </button>
            <button
              className="secondary-button"
              disabled={
                busy ||
                unresolved ||
                (!selected.length &&
                  !categoryId &&
                  sort === "category" &&
                  !history.length)
              }
              onClick={() => resetSelection(true)}
            >
              Reset
            </button>
          </div>
          <div className="organization-selection-actions">
            <strong role="status">
              {selected.length} of {organizationBatchSize} selected
            </strong>
            <button
              disabled={locked || !selected.length || !page || !!page.code}
              onClick={() => void act("preview")}
            >
              Preview Label Changes
            </button>
          </div>
          {loading && <p role="status">Loading cached emails…</p>}
          {busy && (
            <p role="status">
              Preparing your preview from current Gmail labels…
            </p>
          )}
          {!!rows.length && (
            <OrganizationEmailTable
              key={`${page?.accountId}:${offset}:${categoryId}:${suggestionMode}:${reload}`}
              accountId={page?.accountId ?? ""}
              choices={choices}
              onChoice={(emailId, value) => {
                setChoices((current) => {
                  const next = { ...current };
                  if (value) next[emailId] = value;
                  else delete next[emailId];
                  return next;
                });
                if (!value)
                  setSelected((current) =>
                    current.filter((id) => id !== emailId),
                  );
              }}
              onUnauthorized={() => {
                clearAccount();
                setError(messages.unauthorized!);
              }}
              rows={rows}
              selected={selected}
              disabled={locked}
              onSelect={choose}
            />
          )}
          {!rows.length && !loading && (
            <p>
              No matching cached emails. Sync Gmail and review classifications
              if needed.
            </p>
          )}
          <div className="account-actions">
            <button
              className="secondary-button"
              disabled={locked || !history.length}
              onClick={() => {
                setOffset(history[history.length - 1] ?? 0);
                setHistory((values) => values.slice(0, -1));
                setSelected([]);
                setChoices({});
                setLoading(true);
              }}
            >
              Previous page
            </button>
            <span>Page {history.length + 1} · up to 25 emails</span>
            <button
              className="secondary-button"
              disabled={locked || !page?.hasMore}
              onClick={() => {
                setHistory((values) => [...values, offset]);
                setOffset(page?.nextOffset ?? offset + 25);
                setSelected([]);
                setChoices({});
                setLoading(true);
              }}
            >
              Next page
            </button>
            <button
              className="secondary-button"
              disabled={locked}
              onClick={() => {
                setSelected([]);
                setChoices({});
                setError("");
                setLoading(true);
                setReload((value) => value + 1);
                setCategoryReload((value) => value + 1);
              }}
            >
              Refresh list
            </button>
          </div>
        </>
      )}
      {view === "review" && batch && (
        <>
          <p>
            {batch.kind === "undo"
              ? "Review the verified MailPilot label additions to remove. Unrelated labels and created label definitions will remain."
              : "Only the emails below will change. Nothing has been written to Gmail yet."}
          </p>
          <OrganizationEmailTable
            rows={batch.rows}
            undo={batch.kind === "undo"}
          />
          {batch.expiresAt <= now && (
            <p role="alert">
              This preview expired. Go back and preview the current changes
              again.
            </p>
          )}
          <p className="help">
            Preview valid until {new Date(batch.expiresAt).toLocaleTimeString()}
            . Confirming authorizes exactly these changes.
          </p>
          <div className="organization-confirm-actions">
            <button
              className="secondary-button"
              disabled={busy}
              onClick={goBack}
            >
              Go Back
            </button>
            <button disabled={!canConfirm} onClick={() => void act("apply")}>
              Confirm Changes
            </button>
          </div>
        </>
      )}
      {view === "applying" && (
        <>
          <p role="status">
            Applying your confirmed label changes through Gmail… MailPilot
            verifies each result before reporting success.
          </p>
          {batch && (
            <OrganizationEmailTable
              rows={batch.rows}
              undo={batch.kind === "undo"}
            />
          )}
        </>
      )}
      {view === "recovery" && batch && (
        <>
          <p>
            {unresolved
              ? "Some Gmail outcomes are uncertain. Check them before starting another batch. Nothing will be retried automatically."
              : "This batch did not finish successfully. Review the recorded outcomes below."}
          </p>
          {!!batch.rows.length && (
            <ul className="result-email-list">
              {batch.rows.map((row) => (
                <li key={row.emailId}>
                  <strong>{row.subject || "(No subject)"}</strong>
                  <p>
                    {row.updatedLabels
                      ? "Current cached Gmail labels: "
                      : "Labels before this batch: "}
                    {(row.updatedLabels ?? row.currentLabels).join(", ") ||
                      "No labels"}
                  </p>
                </li>
              ))}
            </ul>
          )}
          {!!batch.steps.length && (
            <ul className="operation-list" aria-label="Recorded outcomes">
              {batch.steps.map((step, index) => (
                <li key={index}>
                  <strong>
                    {step.kind === "create_label"
                      ? "Create label"
                      : step.kind === "add"
                        ? "Add label"
                        : "Remove label"}
                    : {step.name}
                  </strong>
                  <span>
                    {step.state.replaceAll("_", " ")}
                    {step.code ? ` · ${stepMessage(step.code)}` : ""}
                  </span>
                </li>
              ))}
            </ul>
          )}
          {unresolved ? (
            <button disabled={busy} onClick={() => void act("reconcile")}>
              Check Gmail outcomes
            </button>
          ) : (
            <div className="account-actions">
              {hasUndo(batch) && (
                <button
                  disabled={busy}
                  onClick={() => void act("undo_preview")}
                >
                  Undo
                </button>
              )}
              <button
                className="secondary-button"
                disabled={busy}
                onClick={() => {
                  setNotice(
                    "The previous batch needs attention; its recorded outcomes are retained.",
                  );
                  setCompleted(hasUndo(batch) ? batch : null);
                  goBack();
                  setLoading(true);
                  setReload((value) => value + 1);
                }}
              >
                Go Back
              </button>
            </div>
          )}
        </>
      )}
      {busy && (
        <button
          className="secondary-button"
          onClick={() => {
            const expected = account.current;
            void organize({ operation: "cancel", accountId: expected }).catch(
              () => {
                if (mounted.current && expected === account.current)
                  setError(
                    "Could not stop the operation. Wait for the recorded Gmail outcomes before continuing.",
                  );
              },
            );
          }}
        >
          Stop
        </button>
      )}
    </section>
  );
}
