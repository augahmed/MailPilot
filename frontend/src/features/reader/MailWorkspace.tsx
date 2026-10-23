import { useRef, useState } from "react";
import type { Page } from "../../app/navigation";
import { Mailbox } from "../mailbox/Mailbox";
import { SemanticSearch } from "../search/SemanticSearch";
import { EmailQuestion } from "../ai/EmailQuestion";
import { MailReader } from "./MailReader";
const initialPages: ReadonlySet<Page> = new Set(["dashboard"]);

export function MailWorkspace({
  connectionRevision,
  page = "dashboard",
  visited = initialPages,
}: {
  connectionRevision: number;
  page?: Page;
  visited?: ReadonlySet<Page>;
}) {
  const [selection, setSelection] = useState<{
    accountId: string;
    emailId: string;
    source: string;
    page: Page;
  } | null>(null);
  const [evidence, setEvidence] = useState<{
    text: string;
    revision: string;
  } | null>(null);
  const trigger = useRef<HTMLButtonElement | null>(null);
  const list = useRef<HTMLDivElement | null>(null);
  const scroll = useRef(0);
  const open = (
    source: string,
    accountId: string,
    emailId: string,
    button: HTMLButtonElement,
  ) => {
    scroll.current = window.scrollY;
    trigger.current = button;
    setSelection({ accountId, emailId, source, page });
  };
  return (
    <>
      <div
        ref={list}
        tabIndex={-1}
        aria-label="Cached email workspace"
        hidden={
          !["dashboard", "search", "ask"].includes(page) ||
          selection?.page === page
        }
      >
        {visited.has("search") && (
          <div hidden={page !== "search"}>
            <SemanticSearch
              active={page === "search"}
              onEvidence={setEvidence}
              onOpen={(accountId, emailId, button) =>
                open("search", accountId, emailId, button)
              }
            />
          </div>
        )}
        {visited.has("ask") && (
          <div hidden={page !== "ask"}>
            <EmailQuestion
              onEvidence={setEvidence}
              onOpen={(accountId, emailId, button) =>
                open("email questions", accountId, emailId, button)
              }
            />
          </div>
        )}
        <div hidden={page !== "dashboard"}>
          <Mailbox
            active={page === "dashboard" && selection?.page !== page}
            connectionRevision={connectionRevision}
            onOpen={(accountId, emailId, button) => {
              setEvidence(null);
              open("mailbox", accountId, emailId, button);
            }}
          />
        </div>
      </div>
      {selection && (
        <div hidden={selection.page !== page}>
          <MailReader
            key={`${selection.accountId}/${selection.emailId}`}
            {...selection}
            evidence={evidence ?? undefined}
            onBack={() => {
              setSelection(null);
              requestAnimationFrame(() => {
                if (trigger.current?.isConnected)
                  trigger.current.focus({ preventScroll: true });
                else list.current?.focus({ preventScroll: true });
                window.scrollTo(0, scroll.current);
              });
            }}
          />
        </div>
      )}
    </>
  );
}
