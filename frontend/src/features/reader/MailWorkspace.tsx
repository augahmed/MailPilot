import { AskMailPilot } from "../ask/AskMailPilot";
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
  const trigger = useRef<HTMLButtonElement | null>(null);
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
      <div hidden={selection !== null}>
        <Mailbox
          connectionRevision={connectionRevision}
          onOpen={(accountId, emailId, button) =>
            open("mailbox", accountId, emailId, button)
          }
        />
        <AskMailPilot
          onOpen={(accountId, emailId, button) =>
            open("Ask MailPilot", accountId, emailId, button)
          }
        />
        <SemanticSearch
          onOpen={(accountId, emailId, button) =>
            open("search results", accountId, emailId, button)
          }
        />
      </div>
      {selection && (
        <MailReader
          key={`${selection.accountId}/${selection.emailId}`}
          {...selection}
          onBack={() => {
            setSelection(null);
            requestAnimationFrame(() => trigger.current?.focus());
          }}
        />
      )}
    </>
  );
}
