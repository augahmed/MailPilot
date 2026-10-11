import { useEffect, useState } from "react";
import { getHealth, type Health } from "../lib/ipc/client";

export function App() {
  const [health, setHealth] = useState<Health | null>(null);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    let active = true;
    let timer: ReturnType<typeof setTimeout>;
    const check = async () => {
      try {
        const result = await getHealth();
        if (active) {
          setHealth(result);
          setFailed(false);
        }
      } catch {
        if (active) {
          setHealth(null);
          setFailed(true);
        }
      } finally {
        if (active)
          timer = setTimeout(() => {
            void check();
          }, 5000);
      }
    };
    void check();
    return () => {
      active = false;
      clearTimeout(timer);
    };
  }, []);

  const backend = health
    ? "Backend connected"
    : failed
      ? "Backend disconnected"
      : "Connecting to backend…";
  const database =
    health?.database === "connected"
      ? "Database connected"
      : health || failed
        ? "Database disconnected"
        : "Checking database…";

  return (
    <main>
      <div className="brand-mark" aria-hidden="true">
        M
      </div>
      <p className="eyebrow">YOUR MAIL. YOUR DEVICE.</p>
      <h1>MailPilot</h1>
      <p className="subtitle">Local-first email assistant</p>
      <section className="status-card" aria-labelledby="status-heading">
        <h2 id="status-heading">Status:</h2>
        <div role="status" aria-live="polite">
          <p>
            <span
              className={`dot ${health ? "connected" : ""}`}
              aria-hidden="true"
            />
            {backend}
          </p>
          <p>
            <span
              className={`dot ${health?.database === "connected" ? "connected" : ""}`}
              aria-hidden="true"
            />
            {database}
          </p>
        </div>
        {failed && (
          <p className="help">
            Open MailPilot as a desktop app. If it is already open, restart it
            to reconnect.
          </p>
        )}
      </section>
      <footer>
        {health
          ? `Version ${health.version} · ${health.database === "connected" ? "Local storage ready" : "Database unavailable"}`
          : "Private by design. Built to stay local."}
      </footer>
    </main>
  );
}
