import { useEffect, useRef, useState } from "react";
import { gmailCommand, type GmailStatus } from "../../lib/ipc/client";

const messages: Record<NonNullable<GmailStatus["code"]>, string> = {
  oauth_not_configured:
    "Set up a Google Desktop OAuth client using docs/GMAIL_SETUP.md, then restart MailPilot.",
  credential_store_unavailable:
    "The OS credential store is unavailable or access was denied. Unlock it and retry. No file fallback is used.",
  consent_denied: "Google consent was denied. You can try connecting again.",
  required_scope_missing:
    "The required Gmail permission was not granted. Reconnect and review the consent screen.",
  reauth_required:
    "Google authorization has expired or was revoked. Connect Gmail again.",
  authentication_timeout: "Sign-in timed out. Connect Gmail to try again.",
  authentication_cancelled: "Sign-in was cancelled.",
  authentication_failed:
    "Could not verify Gmail authorization. Check your network and OAuth setup, then retry.",
  account_storage_failed:
    "Could not save the account locally. Check local storage and retry.",
  account_cleanup_failed:
    "Credentials were removed, but local account metadata could not be updated.",
  revocation_failed:
    "Local credentials were removed. Google revocation could not be confirmed; remove MailPilot access in your Google Account permissions.",
};
const states: Record<GmailStatus["state"], string> = {
  unconfigured: "Gmail setup required",
  disconnected: "Gmail disconnected",
  checking: "Checking Gmail connection…",
  connecting: "Waiting for Google sign-in in your browser…",
  connected: "Gmail connected",
  disconnecting: "Disconnecting Gmail…",
  error: "Gmail connection failed",
  reauth_required: "Gmail reconnection required",
};
export function GmailConnection({
  onDisconnect,
}: {
  onDisconnect?: () => void;
}) {
  const [status, setStatus] = useState<GmailStatus | null>(null);
  const [error, setError] = useState(false);
  const [busy, setBusy] = useState(false);
  const revision = useRef(0);
  const mounted = useRef(false);
  const actionPending = useRef(false);
  useEffect(() => {
    mounted.current = true;
    let active = true;
    let timer: ReturnType<typeof setTimeout>;
    const poll = async () => {
      const current = revision.current;
      try {
        const result = await gmailCommand("gmail_status");
        if (active && !actionPending.current && current === revision.current) {
          setStatus(result);
          setError(false);
        }
      } catch {
        if (active && !actionPending.current && current === revision.current) {
          setStatus(null);
          setError(true);
        }
      } finally {
        if (active)
          timer = setTimeout(() => {
            void poll();
          }, 1500);
      }
    };
    void poll();
    return () => {
      active = false;
      mounted.current = false;
      clearTimeout(timer);
    };
  }, []);
  const act = async (
    command: "gmail_connect" | "gmail_check" | "gmail_disconnect",
  ) => {
    if (actionPending.current) return;
    revision.current++;
    actionPending.current = true;
    setBusy(true);
    setError(false);
    if (command === "gmail_disconnect") setStatus({ state: "disconnecting" });
    try {
      const result = await gmailCommand(command);
      if (mounted.current) {
        setStatus(result);
        if (command === "gmail_disconnect" || command === "gmail_connect")
          onDisconnect?.();
      }
    } catch {
      if (mounted.current) {
        setStatus(null);
        setError(true);
      }
    } finally {
      revision.current++;
      actionPending.current = false;
      if (mounted.current) setBusy(false);
    }
  };
  const working =
    status &&
    ["connecting", "checking", "disconnecting"].includes(status.state);
  return (
    <section className="status-card" aria-labelledby="gmail-heading">
      <h2 id="gmail-heading">Gmail</h2>
      <div role="status" aria-live="polite">
        <p>
          {error
            ? "Gmail status unavailable"
            : status
              ? states[status.state]
              : "Checking Gmail status…"}
        </p>
        {status?.state === "connected" && status.email && <p>{status.email}</p>}
      </div>
      {error && (
        <p role="alert">
          Could not contact the desktop backend or open the browser. Retry or
          restart MailPilot.
        </p>
      )}
      {status?.code && <p className="help">{messages[status.code]}</p>}
      <div className="account-actions">
        <button
          disabled={
            busy ||
            !status ||
            !!working ||
            status.state === "unconfigured" ||
            status.state === "connected"
          }
          onClick={() => {
            void act("gmail_connect");
          }}
        >
          Connect Gmail
        </button>
        <button
          disabled={busy || !!working || status?.state === "unconfigured"}
          onClick={() => {
            void act("gmail_check");
          }}
        >
          Check connection
        </button>
        <button
          disabled={
            busy ||
            !status ||
            ["unconfigured", "disconnected", "disconnecting"].includes(
              status.state,
            )
          }
          onClick={() => {
            void act("gmail_disconnect");
          }}
        >
          {status?.state === "connecting"
            ? "Cancel sign-in / disconnect"
            : "Disconnect"}
        </button>
      </div>
      <p className="help">
        Google sign-in opens in your browser. The requested permission allows
        reading email and applying labels; Google also includes sending in this
        scope. MailPilot does not send or delete mail. Only your account profile
        is retrieved now.
      </p>
      <p className="help">
        Disconnect removes this device’s credentials and attempts to revoke
        Google access. Cached mail stays on this device.
      </p>
    </section>
  );
}
