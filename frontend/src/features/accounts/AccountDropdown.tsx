import { useEffect, useRef, useState } from "react";
import type { Page } from "../../app/navigation";
import { ConnectionStatus } from "./GmailConnection";
import type { GmailConnectionState } from "./useGmailConnection";

export function AccountDropdown({
  connection,
  navigate,
}: {
  connection: GmailConnectionState;
  navigate: (page: Page) => void;
}) {
  const [open, setOpen] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const container = useRef<HTMLDivElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const cancel = useRef<HTMLButtonElement>(null);
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
  useEffect(() => {
    if (confirming) cancel.current?.focus();
  }, [confirming]);
  return (
    <div
      className="account-dropdown"
      ref={container}
      onBlur={(event) => {
        // Replacing the focused Disconnect button can emit a blur with no
        // destination before the confirmation controls receive focus. Only a
        // known outside focus target should dismiss the panel here; outside
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
        if (event.key === "Escape") {
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
          <p className="account-label">GMAIL ACCOUNT</p>
          <strong className="account-email">{connection.email}</strong>
          <ConnectionStatus connection={connection} />
          {confirming ? (
            <div
              className="disconnect-confirmation"
              role="group"
              aria-labelledby="disconnect-heading"
            >
              <h2 id="disconnect-heading">Disconnect Gmail?</h2>
              <p>
                This removes this device’s credentials and attempts to revoke
                Google access. Cached mail stays on this device. You will need
                to reconnect to access it.
              </p>
              <div className="account-actions">
                <button
                  className="secondary-button"
                  ref={cancel}
                  onClick={() => {
                    setConfirming(false);
                    requestAnimationFrame(() => disconnect.current?.focus());
                  }}
                >
                  Cancel
                </button>
                <button
                  className="danger-button"
                  onClick={() => {
                    void connection.act("gmail_disconnect");
                  }}
                >
                  Confirm disconnect
                </button>
              </div>
            </div>
          ) : (
            <>
              <button
                className="dropdown-action"
                disabled={working}
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
                  navigate("recovery");
                  setOpen(false);
                }}
              >
                Historical recovery
              </button>
              <button
                className="dropdown-action danger-text"
                ref={disconnect}
                disabled={connection.busy}
                onClick={() => setConfirming(true)}
              >
                Disconnect
              </button>
            </>
          )}
        </div>
      )}
    </div>
  );
}
