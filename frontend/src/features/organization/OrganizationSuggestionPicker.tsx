import { useEffect, useRef, useState } from "react";
import { organize, type OrganizationResponse } from "../../lib/ipc/client";

export type SuggestionChoice = { categoryId: string; name: string };
type Suggestion = NonNullable<
  OrganizationResponse["rows"][number]["suggestions"]
>[number];
export function OrganizationSuggestionPicker({
  accountId,
  row,
  value,
  disabled,
  onChange,
  onUnauthorized,
}: {
  accountId: string;
  row: OrganizationResponse["rows"][number];
  value?: SuggestionChoice;
  disabled: boolean;
  onChange: (value?: SuggestionChoice) => void;
  onUnauthorized: () => void;
}) {
  const [options, setOptions] = useState<Suggestion[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const mounted = useRef(false);
  const pending = useRef(false);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  async function load() {
    if (pending.current || options.length) return;
    pending.current = true;
    setLoading(true);
    setError("");
    try {
      const result = await organize({
        operation: "suggestions",
        accountId,
        emailIds: [row.emailId],
      });
      if (!mounted.current) return;
      if (result.code === "unauthorized" || result.accountId !== accountId) {
        onUnauthorized();
        return;
      }
      const current = result.rows[0];
      if (
        result.code ||
        result.rows.length !== 1 ||
        current?.emailId !== row.emailId ||
        !current.suggestions?.length ||
        current.suggestions.length !== current.suggestionCount ||
        new Set(current.suggestions.map((option) => option.categoryId)).size !==
          current.suggestions.length
      )
        throw Error("Suggestions changed");
      setOptions(current.suggestions);
    } catch {
      if (mounted.current)
        setError(
          "Could not load current suggestions. Retry or refresh the email list.",
        );
    } finally {
      pending.current = false;
      if (mounted.current) setLoading(false);
    }
  }
  return (
    <details
      onToggle={(event) => {
        if (event.currentTarget.open) void load();
      }}
    >
      <summary>Choose a suggestion ({row.suggestionCount})</summary>
      {loading && <small role="status">Loading suggestions…</small>}
      {error && (
        <p role="alert">
          {error}{" "}
          <button
            className="secondary-button"
            disabled={disabled || loading}
            onClick={() => void load()}
          >
            Retry suggestions
          </button>
        </p>
      )}
      {!!options.length && (
        <label>
          Suggested category
          <select
            aria-label={`Suggested category for ${row.subject || "email"}`}
            value={value?.categoryId ?? ""}
            disabled={disabled || loading}
            onChange={(event) => {
              const option = options.find(
                (option) => option.categoryId === event.target.value,
              );
              onChange(
                option
                  ? { categoryId: option.categoryId, name: option.name }
                  : undefined,
              );
            }}
          >
            <option value="">Choose a suggestion</option>
            {options.map((option) => (
              <option key={option.categoryId} value={option.categoryId}>
                {option.name} · {option.confidence.toFixed(3)}
              </option>
            ))}
          </select>
        </label>
      )}
    </details>
  );
}
