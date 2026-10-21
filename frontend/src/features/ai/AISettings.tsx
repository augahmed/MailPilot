import { useEffect, useRef, useState } from "react";
import {
  manageAISettings,
  type AISettings as Settings,
} from "../../lib/ipc/ai";

export function AISettings() {
  const [view, setView] = useState<Settings | null>(null);
  const [provider, setProvider] = useState("");
  const [model, setModel] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const key = useRef<HTMLInputElement>(null);
  const live = useRef(false);
  const sequence = useRef(0);
  const sending = useRef(false);
  const identity = useRef("");
  const accept = (next: Settings) => {
    if (next.code === "unauthorized") {
      setView(null);
      setProvider("");
      setModel("");
      identity.current = "";
      if (key.current) key.current.value = "";
      return;
    }
    if (next.code) {
      setError(`AI settings could not be updated (${next.code}).`);
      return;
    }
    if (identity.current !== next.accountId) {
      identity.current = next.accountId;
      setProvider(next.provider || next.providers[0]?.id || "");
      setModel(next.model);
      if (key.current) key.current.value = "";
    }
    setView(next);
  };
  const acceptRef = useRef(accept);
  useEffect(() => {
    acceptRef.current = accept;
  });
  useEffect(() => {
    live.current = true;
    let timer: ReturnType<typeof setTimeout>;
    let active = true;
    const poll = async () => {
      const revision = sequence.current;
      try {
        if (sending.current) return;
        const next = await manageAISettings();
        if (active && revision === sequence.current) acceptRef.current(next);
      } catch {
        if (active && revision === sequence.current) {
          setView(null);
          setProvider("");
          setModel("");
          identity.current = "";
          if (key.current) key.current.value = "";
          setError("Cannot read AI settings.");
        }
      } finally {
        if (active)
          timer = setTimeout(() => {
            void poll();
          }, 2000);
      }
    };
    void poll();
    return () => {
      active = false;
      live.current = false;
      clearTimeout(timer);
    };
  }, []);
  const save = async (disable = false) => {
    const apiKey = key.current?.value || "";
    if (key.current) key.current.value = "";
    const revision = ++sequence.current;
    sending.current = true;
    setBusy(true);
    setError("");
    try {
      const next = await manageAISettings(
        disable
          ? { operation: "disable", accountId: view?.accountId || "" }
          : {
              operation: "configure",
              accountId: view?.accountId || "",
              provider,
              model,
              apiKey,
            },
      );
      if (live.current && revision === sequence.current)
        acceptRef.current(next);
    } catch {
      if (live.current && revision === sequence.current)
        setError("Could not update AI settings.");
    } finally {
      if (revision === sequence.current) {
        sending.current = false;
        if (live.current) setBusy(false);
      }
    }
  };
  return (
    <section className="status-card" aria-labelledby="ai-settings-heading">
      <h2 id="ai-settings-heading">Optional AI settings</h2>
      <p>
        AI can summarize selected mail or answer from selected excerpts after
        separate approval for each request. Reading, sync, organization and risk
        analysis work with AI disabled.
      </p>
      {!view?.accountId ? (
        <p>Connect Gmail to manage AI settings.</p>
      ) : (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void save();
          }}
        >
          <label>
            AI provider{" "}
            <select
              value={provider}
              onChange={(e) => {
                setProvider(e.target.value);
                setModel("");
                if (key.current) key.current.value = "";
              }}
              disabled={busy}
            >
              {view.providers.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.id}
                </option>
              ))}
            </select>
          </label>
          <label>
            AI model{" "}
            <input
              value={model}
              onChange={(e) => setModel(e.target.value)}
              maxLength={128}
              required
              disabled={busy}
            />
          </label>
          <label>
            Provider API key{" "}
            <input
              ref={key}
              type="password"
              autoComplete="off"
              spellCheck={false}
              maxLength={2048}
              disabled={busy}
            />
          </label>
          <p>
            Keys use OS credential storage, separately for each provider and
            account. Leave blank to use that provider’s saved key. Saving
            settings never uploads email.
          </p>
          <p>
            AI: {view.enabled ? `${view.provider} / ${view.model}` : "Disabled"}
          </p>
          <button type="submit" disabled={busy || !provider}>
            Save AI settings
          </button>
          <button
            type="button"
            disabled={busy || !view.enabled}
            onClick={() => {
              void save(true);
            }}
          >
            Disable AI and remove selected provider key
          </button>
        </form>
      )}
      {error && <p role="alert">{error}</p>}
    </section>
  );
}
