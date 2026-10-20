import { useRef, useState } from "react";
import { Mailbox } from "../mailbox/Mailbox";
import { SemanticSearch } from "../search/SemanticSearch";
import { MailReader } from "./MailReader";
export function MailWorkspace({
  connectionRevision,
}: {
  connectionRevision: number;
}) {
  const [selection, setSelection] = useState<{
    accountId: string;
    emailId: string;
    source: string;
  } | null>(null);
  const [evidence, setEvidence] = useState<{
    text: string;
    revision: string;
  } | null>(null);
  const trigger = useRef<HTMLButtonElement | null>(null);
  const list = useRef<HTMLDivElement | null>(null);
  const open = (
    source: string,
    accountId: string,
    emailId: string,
    button: HTMLButtonElement,
  ) => {
    trigger.current = button;
    setSelection({ accountId, emailId, source });
  };
  return (
    <>
      <div
        ref={list}
        tabIndex={-1}
        aria-label="Cached email workspace"
        hidden={selection !== null}
      >
        <SemanticSearch
          onEvidence={setEvidence}
          onOpen={(accountId, emailId, button) =>
            open("search", accountId, emailId, button)
          }
        />
        <Mailbox
          connectionRevision={connectionRevision}
          onOpen={(accountId, emailId, button) => {
            setEvidence(null);
            open("mailbox", accountId, emailId, button);
          }}
        />
      </div>
      {selection && (
        <MailReader
          key={`${selection.accountId}/${selection.emailId}`}
          {...selection}
          evidence={evidence ?? undefined}
          onBack={() => {
            setSelection(null);
            requestAnimationFrame(() => {
              if (trigger.current?.isConnected) trigger.current.focus();
              else list.current?.focus();
            });
          }}
        />
      )}
    </>
  );
}
