import { useEffect, useRef, useState } from "react";
import { LocalEmailIndex } from "../features/indexing/LocalEmailIndex";
import { AISettings } from "../features/ai/AISettings";
import { PrivacyNotice } from "../features/privacy/PrivacyNotice";
import { AIMaintenance } from "../features/replies/AIMaintenance";
import { MailWorkspace } from "../features/reader/MailWorkspace";
import { Organization } from "../features/organization/Organization";
import { CategoriesAndClassification } from "../features/categories/CategoriesAndClassification";
import { GmailConnection } from "../features/accounts/GmailConnection";
import { AccountDropdown } from "../features/accounts/AccountDropdown";
import { useGmailConnection } from "../features/accounts/useGmailConnection";
import { getHealth, type Health } from "../lib/ipc/client";
import { Brand, features, Icon, type Page } from "./navigation";

function AuthorizedApp({
  connection,
}: {
  connection: ReturnType<typeof useGmailConnection>;
}) {
  const [page, setPage] = useState<Page>("dashboard");
  const [indexReturn, setIndexReturn] = useState<"dashboard" | "categories">(
    "dashboard",
  );
  const [categoryReviewRevision, setCategoryReviewRevision] = useState(0);
  const [visited, setVisited] = useState<Set<Page>>(
    () => new Set(["dashboard"]),
  );
  const heading = useRef<HTMLHeadingElement>(null);
  const scroll = useRef<Partial<Record<Page, number>>>({});
  const card = useRef<HTMLButtonElement | null>(null);
  useEffect(() => {
    heading.current?.focus();
  }, []);
  const navigate = (
    next: Page,
    from: "dashboard" | "categories" = "dashboard",
  ) => {
    if (next === "index") setIndexReturn(from);
    if (next === "categories" && page === "index")
      setCategoryReviewRevision((value) => value + 1);
    scroll.current[page] = window.scrollY;
    setVisited((prior) => new Set([...prior, next]));
    setPage(next);
    requestAnimationFrame(() => {
      if (next === "dashboard" && card.current?.isConnected)
        card.current.focus({ preventScroll: true });
      else heading.current?.focus({ preventScroll: true });
      window.scrollTo(0, scroll.current[next] ?? 0);
    });
  };
  const title =
    features.find((feature) => feature.id === page)?.title ??
    (page === "settings"
      ? "Optional AI settings"
      : page === "privacy"
        ? "Privacy and local data"
        : "Historical recovery");
  const backPage = page === "index" ? indexReturn : "dashboard";
  return (
    <>
      <header className="app-header">
        <button
          className="brand-button"
          aria-label="MailPilot dashboard"
          onClick={() => navigate("dashboard")}
        >
          <Brand />
        </button>
        <AccountDropdown connection={connection} navigate={navigate} />
      </header>
      <main id="main-content" className="app-content" tabIndex={-1}>
        {page === "dashboard" ? (
          <>
            <div className="dashboard-intro">
              <div>
                <p className="eyebrow">YOUR LOCAL WORKSPACE</p>
                <h1 ref={heading} tabIndex={-1}>
                  Your mail, in focus.
                </h1>
                <p className="subtitle">
                  A little more clarity. All from your local cache.
                </p>
              </div>
              <span className="local-badge">
                <span className="dot connected" />
                Local-first
              </span>
            </div>
          </>
        ) : (
          <div className="page-heading">
            <button
              className="back-button secondary-button"
              onClick={() => navigate(backPage)}
            >
              <span aria-hidden="true">←</span>{" "}
              {backPage === "categories"
                ? "Back to Categories & Classification"
                : "Back to dashboard"}
            </button>
            <h1 ref={heading} tabIndex={-1}>
              {title}
            </h1>
          </div>
        )}
        <nav
          className="feature-grid"
          aria-label="Email tools"
          hidden={page !== "dashboard"}
        >
          {features.map((feature) => (
            <button
              key={feature.id}
              className="feature-card"
              onClick={(event) => {
                card.current = event.currentTarget;
                navigate(feature.id);
              }}
            >
              <span className="feature-icon">
                <Icon name={feature.icon} />
              </span>
              <span className="feature-title">{feature.title}</span>
              <span className="feature-description">{feature.description}</span>
              <span className="feature-arrow" aria-hidden="true">
                ↗
              </span>
            </button>
          ))}
        </nav>
        {(visited.has("ask") || visited.has("settings")) && (
          <div hidden={page !== "ask" && page !== "settings"}>
            <AISettings />
          </div>
        )}
        <MailWorkspace connectionRevision={0} page={page} visited={visited} />
        {page === "privacy" && <PrivacyNotice />}
        {visited.has("index") && (
          <div hidden={page !== "index"}>
            {indexReturn === "categories" && (
              <aside
                className="attention-panel"
                aria-label="Refresh category classifications"
              >
                <h2>Apply your category changes</h2>
                <p>
                  Choose Rebuild index to refresh category profiles and
                  classification suggestions. When indexing finishes, use Back
                  to Categories &amp; Classification to review the updated
                  results.
                </p>
              </aside>
            )}
            <LocalEmailIndex />
          </div>
        )}
        {visited.has("categories") && (
          <div hidden={page !== "categories"}>
            <CategoriesAndClassification
              onUpdateClassification={() => navigate("index", "categories")}
              reviewRevision={categoryReviewRevision}
            />
          </div>
        )}
        {visited.has("organization") && (
          <div hidden={page !== "organization"}>
            <Organization active={page === "organization"} />
          </div>
        )}
        {visited.has("recovery") && (
          <div hidden={page !== "recovery"}>
            <AIMaintenance />
          </div>
        )}
      </main>
    </>
  );
}

export function App() {
  const connection = useGmailConnection();
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
  return (
    <div className="app-shell">
      <a className="skip-link" href="#main-content">
        Skip to content
      </a>
      {connection.email !== null ? (
        <AuthorizedApp key={connection.email} connection={connection} />
      ) : (
        <>
          <header className="app-header landing-header">
            <Brand />
            <span className="header-note">Local-first email assistant</span>
          </header>
          <main id="main-content" className="landing-content" tabIndex={-1}>
            <GmailConnection connection={connection} />
          </main>
        </>
      )}
      <footer className="app-footer">
        <span>MailPilot · Your mail. Your device.</span>
        <span role="status">
          {health
            ? health.database === "connected"
              ? `Local storage ready · v${health.version}`
              : "Database unavailable"
            : failed
              ? "Desktop backend unavailable. Open or restart the desktop app."
              : "Connecting to desktop…"}
        </span>
      </footer>
    </div>
  );
}
