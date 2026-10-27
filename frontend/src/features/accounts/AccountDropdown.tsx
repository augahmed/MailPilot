import { useEffect, useRef, useState } from "react";
import type { Page } from "../../app/navigation";
import { ConfirmDialog } from "../../components/ConfirmDialog";
import { ConnectionStatus } from "./GmailConnection";
import type { GmailConnectionState } from "./useGmailConnection";

export function AccountDropdown({
  connection,
  navigate,
}: {
  connection: GmailConnectionState;
  navigate: (page: Page) => void;
}) {
  const demo = connection.status?.demo === true;
  const [open, setOpen] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const container = useRef<HTMLDivElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const disconnect = useRef<HTMLButtonElement>(null);
  const working =
    connection.busy ||
    ["checking", "connecting", "disconnecting"].includes(
      connection.status?.state ?? "",
    );
  useEffect(() => {
    if (!open) return;
    const close = (event: PointerEvent) => {
      if (!container.current?.contains(event.target as Node)) {
        setOpen(false);
        setConfirming(false);
      }
    };
    document.addEventListener("pointerdown", close);
    return () => document.removeEventListener("pointerdown", close);
  }, [open]);
  const cancelDisconnect = () => {
    setConfirming(false);
    requestAnimationFrame(() => disconnect.current?.focus());
  };
  return (
    <div
      className="account-dropdown"
      ref={container}
      onBlur={(event) => {
        // Transient blur events can have no destination while controls change.
        // Only a known outside target should dismiss the panel here; outside
        // pointer presses and Escape are handled separately.
        if (
          event.relatedTarget &&
          !event.currentTarget.contains(event.relatedTarget)
        ) {
          setOpen(false);
          setConfirming(false);
        }
      }}
      onKeyDown={(event) => {
        if (event.key === "Escape" && !confirming) {
          setOpen(false);
          setConfirming(false);
          trigger.current?.focus();
        }
      }}
    >
      <button
        className="account-trigger secondary-button"
        ref={trigger}
        aria-expanded={open}
        aria-controls="account-panel"
        onClick={() => {
          setOpen(!open);
          setConfirming(false);
        }}
      >
        <span className="account-avatar" aria-hidden="true">
          {connection.email?.slice(0, 1).toUpperCase()}
        </span>
        <span className="account-address">{connection.email}</span>
        <span aria-hidden="true">⌄</span>
        <span className="sr-only">Gmail account</span>
      </button>
      {open && (
        <div id="account-panel" className="account-panel">
          <p className="account-label">
            {demo ? "SYNTHETIC DEMO" : "GMAIL ACCOUNT"}
          </p>
          <strong className="account-email">{connection.email}</strong>
          <ConnectionStatus connection={connection} />
          <button
            className="dropdown-action"
            disabled={working || demo}
            onClick={() => {
              void connection.act("gmail_check");
            }}
          >
            Check Connection
          </button>
          <button
            className="dropdown-action"
            onClick={() => {
              navigate("settings");
              setOpen(false);
            }}
          >
            Optional AI settings
          </button>
          <button
            className="dropdown-action"
            onClick={() => {
              navigate("privacy");
              setOpen(false);
            }}
          >
            Privacy and local data
          </button>
          <button
            className="dropdown-action"
            onClick={() => {
              navigate("recovery");
              setOpen(false);
            }}
          >
            Historical recovery
          </button>
          <button
            className="dropdown-action danger-text"
            ref={disconnect}
            disabled={connection.busy || demo}
            onClick={() => setConfirming(true)}
          >
            Disconnect
          </button>
        </div>
      )}
      <ConfirmDialog
        open={open && confirming}
        title="Disconnect Gmail?"
        confirmLabel="Confirm disconnect"
        destructive
        onCancel={cancelDisconnect}
        onConfirm={() => void connection.act("gmail_disconnect")}
      >
        <p className="account-email">{connection.email}</p>
        <p>
          This removes this device’s credentials and attempts to revoke Google
          access. Cached mail stays on this device. You will need to reconnect
          to access it. Saved AI provider keys remain separate; remove them in
          Optional AI settings before disconnecting.
        </p>
      </ConfirmDialog>
    </div>
  );
}
