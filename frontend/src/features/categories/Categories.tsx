import { useEffect, useRef, useState } from "react";
import {
  manageCategories,
  type Category,
  type CategoryPage,
  type CategoryRequest,
} from "../../lib/ipc/client";

const messages = {
  unauthorized: "Connect your account to manage its local categories.",
  validation:
    "Enter a name (up to 100 UTF-8 bytes) and a meaningful description (up to 1,000 UTF-8 bytes).",
  conflict:
    "This name is already in use, or the category is referenced by saved mail or analysis. Referenced categories can be disabled instead of deleted.",
  not_found: "This category no longer exists. Refresh the list.",
  storage_error: "Categories could not be saved or loaded. Try again.",
};
export function Categories() {
  const [page, setPage] = useState<CategoryPage | null>(null);
  const [offset, setOffset] = useState(0);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [editing, setEditing] = useState<Category | "new" | null>(null);
  const [deleting, setDeleting] = useState<Category | null>(null);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [enabled, setEnabled] = useState(true);
  const active = useRef(true);
  const pending = useRef(false);
  const revision = useRef(0);
  const heading = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    active.current = true;
    return () => {
      active.current = false;
    };
  }, []);
  async function run(
    request: Partial<CategoryRequest> & Pick<CategoryRequest, "operation">,
  ) {
    if (pending.current) return;
    pending.current = true;
    revision.current++;
    setBusy(true);
    setMessage("");
    try {
      const result = await manageCategories(request);
      if (!active.current) return;
      if (result.code) {
        setMessage(messages[result.code]);
        if (result.code === "unauthorized") {
          setPage(null);
          setEditing(null);
          setDeleting(null);
        }
      } else {
        setPage(result);
        setOffset(request.offset ?? 0);
        if (request.operation !== "list") {
          setEditing(null);
          setDeleting(null);
          setMessage("Category changes saved locally.");
          heading.current?.focus();
        }
      }
    } catch {
      if (active.current)
        setMessage("Cannot reach category storage. Try again.");
    } finally {
      pending.current = false;
      if (active.current) setBusy(false);
    }
  }
  useEffect(() => {
    let current = true;
    const initialRevision = revision.current;
    void manageCategories({ operation: "list" })
      .then((result) => {
        if (!current || pending.current || initialRevision !== revision.current)
          return;
        if (result.code) setMessage(messages[result.code]);
        else setPage(result);
      })
      .catch(() => {
        if (current && !pending.current && initialRevision === revision.current)
          setMessage("Cannot reach category storage. Try again.");
      });
    return () => {
      current = false;
    };
  }, []);
  function edit(category: Category | "new") {
    setEditing(category);
    setDeleting(null);
    setName(category === "new" ? "" : category.name);
    setDescription(category === "new" ? "" : category.description);
    setEnabled(category === "new" ? true : category.enabled);
  }
  return (
    <section className="status-card" aria-labelledby="categories-heading">
      <h2 id="categories-heading" tabIndex={-1} ref={heading}>
        Categories
      </h2>
      <p>
        Local categories for this account. Descriptions define what belongs in
        each category. The starter rules use four default category names.
        Renamed and custom categories have no automatic rule mapping.
      </p>
      <p role="status">{busy ? "Working…" : message}</p>
      <button
        disabled={busy}
        onClick={() =>
          void run({
            operation: "list",
            accountId: page?.accountId ?? "",
            offset,
          })
        }
      >
        Refresh categories
      </button>
      {page && (
        <>
          <button disabled={busy} onClick={() => edit("new")}>
            Create category
          </button>
          <ul className="category-list">
            {page.rows.map((category) => (
              <li key={category.id}>
                <h3>
                  {category.name} {category.enabled ? "" : "(disabled)"}
                </h3>
                <p>{category.description}</p>
                <button disabled={busy} onClick={() => edit(category)}>
                  Edit {category.name}
                </button>
                <button
                  disabled={busy}
                  onClick={() =>
                    void run({
                      operation: "save",
                      accountId: page.accountId,
                      id: category.id,
                      name: category.name,
                      description: category.description,
                      enabled: !category.enabled,
                    })
                  }
                >
                  {category.enabled ? "Disable" : "Enable"} {category.name}
                </button>
                <button
                  disabled={busy}
                  onClick={() => {
                    setDeleting(category);
                    setEditing(null);
                  }}
                >
                  Delete {category.name}
                </button>
              </li>
            ))}
          </ul>
          {page.rows.length === 0 && <p>No categories on this page.</p>}
          <button
            disabled={busy || offset === 0}
            onClick={() =>
              void run({
                operation: "list",
                accountId: page.accountId,
                offset: Math.max(0, offset - 5),
              })
            }
          >
            Previous categories
          </button>
          <button
            disabled={busy || !page.hasMore}
            onClick={() =>
              void run({
                operation: "list",
                accountId: page.accountId,
                offset: offset + 5,
              })
            }
          >
            Next categories
          </button>
          {editing && (
            <form
              onSubmit={(event) => {
                event.preventDefault();
                void run({
                  operation: "save",
                  accountId: page.accountId,
                  id: editing === "new" ? "" : editing.id,
                  name,
                  description,
                  enabled,
                });
              }}
            >
              <h3>{editing === "new" ? "New category" : "Edit category"}</h3>
              <label>
                Name
                <input
                  autoFocus
                  required
                  maxLength={100}
                  value={name}
                  onChange={(event) => setName(event.target.value)}
                  disabled={busy}
                />
              </label>
              <label>
                Description
                <textarea
                  required
                  maxLength={1000}
                  value={description}
                  onChange={(event) => setDescription(event.target.value)}
                  disabled={busy}
                />
              </label>
              <p>
                Describe the topics and types of messages that belong here,
                including useful distinctions from other categories.
              </p>
              <label>
                <input
                  type="checkbox"
                  checked={enabled}
                  onChange={(event) => setEnabled(event.target.checked)}
                  disabled={busy}
                />
                Enabled
              </label>
              <button disabled={busy} type="submit">
                Save category
              </button>
              <button
                disabled={busy}
                type="button"
                onClick={() => {
                  setEditing(null);
                  heading.current?.focus();
                }}
              >
                Cancel
              </button>
            </form>
          )}
          {deleting && (
            <div role="group" aria-label="Confirm category deletion">
              <p>
                Delete “{deleting.name}”? This removes its local definition.
                Referenced categories cannot be deleted; disable them instead.
              </p>
              <button
                autoFocus
                disabled={busy}
                onClick={() =>
                  void run({
                    operation: "delete",
                    accountId: page.accountId,
                    id: deleting.id,
                  })
                }
              >
                Confirm deletion
              </button>
              <button
                disabled={busy}
                onClick={() => {
                  setDeleting(null);
                  heading.current?.focus();
                }}
              >
                Cancel deletion
              </button>
            </div>
          )}
        </>
      )}
    </section>
  );
}
