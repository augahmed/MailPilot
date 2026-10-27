import { useEffect, useRef } from "react";
import { PrivacyNotice } from "../privacy/PrivacyNotice";
import type { GmailStatus } from "../../lib/ipc/client";
import type { GmailConnectionState } from "./useGmailConnection";

const messages: Record<NonNullable<GmailStatus["code"]>, string> = {
  demo_mode: "Synthetic demo: Google sign-in and account changes are disabled.",
  oauth_not_configured:
    "Set up a Google Desktop OAuth client using docs/GMAIL_SETUP.md, then restart MailPilot.",
  credential_store_unavailable:
    "The OS credential store is unavailable or access was denied. Unlock it and retry. No file fallback is used.",
  consent_denied: "Google consent was denied. You can try connecting again.",
  required_scope_missing:
    "The required Gmail permission was not granted. Reconnect and review the consent screen.",
  reauth_required:
    "Google authorization has expired or was revoked. Connect Gmail again.",
  authentication_timeout:
    "Sign-in timed out. Try again when your connection is available.",
  authentication_cancelled: "Sign-in was cancelled.",
  authentication_failed:
    "Gmail could not be reached or authorization could not be verified. Check your connection and retry.",
  account_storage_failed:
    "Could not save the account locally. Check local storage and retry.",
  account_cleanup_failed:
    "Credentials were removed, but local account metadata could not be updated.",
  revocation_failed:
    "Local credentials were removed. Google revocation could not be confirmed; remove MailPilot access in your Google Account permissions.",
};
export const connectionStates: Record<GmailStatus["state"], string> = {
  unconfigured: "Gmail setup required",
  disconnected: "Gmail disconnected",
  checking: "Checking Gmail connection…",
  connecting: "Waiting for Google sign-in in your browser…",
  connected: "Gmail connected",
  disconnecting: "Disconnecting Gmail…",
  error: "Gmail connection unavailable",
  reauth_required: "Gmail reconnection required",
};

export function ConnectionStatus({
  connection,
}: {
  connection: GmailConnectionState;
}) {
  const { status, error, email } = connection;
  return (
    <>
      <p role="status" aria-live="polite">
        {error
          ? "Gmail status unavailable"
          : status?.demo
            ? "Synthetic demo · offline sample mailbox"
            : status
              ? connectionStates[status.state]
              : "Restoring your Gmail session…"}
      </p>
      {error && (
        <p role="alert">
          Could not contact the desktop backend or open the browser. Retry or
          restart MailPilot.
        </p>
      )}
      {status?.code && <p className="help">{messages[status.code]}</p>}
      {email && (error || status?.state === "error") && (
        <p className="help">
          Your authorized local cache remains available. Gmail is currently
          unreachable or unverified.
        </p>
      )}
    </>
  );
}

export function GmailConnection({
  connection,
}: {
  connection: GmailConnectionState;
}) {
  const { status, busy, act, error } = connection;
  const heading = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    heading.current?.focus();
  }, []);
  const working =
    busy ||
    (!!status &&
      ["checking", "connecting", "disconnecting"].includes(status.state));
  return (
    <section className="connection-landing" aria-labelledby="welcome-heading">
      <p className="eyebrow">YOUR MAIL. YOUR DEVICE.</p>
      <h1 id="welcome-heading" ref={heading} tabIndex={-1}>
        A calmer place
        <br />
        for your email.
      </h1>
      <p className="landing-description">
        Browse, find, and organize your Gmail with tools that put your local
        email cache to work.
      </p>
      <div className="connection-panel">
        <h2>Connect your Gmail</h2>
        <p>Sign in securely with Google to get started.</p>
        <button
          className="primary-button connect-button"
          disabled={working || !status || status.state === "unconfigured"}
          onClick={() => {
            void act("gmail_connect");
          }}
        >
          Connect Gmail <span aria-hidden="true">→</span>
        </button>
        <ConnectionStatus connection={connection} />
        {(error ||
          status?.state === "error" ||
          status?.state === "reauth_required") && (
          <button
            className="secondary-button"
            disabled={working}
            onClick={() => {
              void act("gmail_check");
            }}
          >
            Retry connection
          </button>
        )}
        {status?.state === "connecting" && (
          <button
            className="secondary-button"
            disabled={busy}
            onClick={() => {
              void act("gmail_disconnect");
            }}
          >
            Cancel sign-in
          </button>
        )}
      </div>
      <p className="privacy-note">
        Email is cached on this device. MailPilot does not encrypt the local
        cache. Optional external AI receives only the content you explicitly
        approve.
      </p>
      <details className="connection-details">
        <summary>Privacy and local data</summary>
        <PrivacyNotice />
      </details>
      <details className="connection-details">
        <summary>About Google permissions</summary>
        <p>
          Google sign-in opens in your browser. The requested permission allows
          reading email and applying labels; Google also includes sending in
          this scope. MailPilot does not send or delete mail. Only your account
          profile is retrieved during sign-in.
        </p>
      </details>
    </section>
  );
}
