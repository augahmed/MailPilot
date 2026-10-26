import { useId } from "react";

export function PrivacyNotice() {
  const heading = useId();
  return (
    <section
      className="status-card feature-panel privacy-notice"
      aria-labelledby={heading}
    >
      <h2 id={heading}>Privacy and local data</h2>
      <h3>What stays on this device</h3>
      <p>
        MailPilot caches email content and metadata, local search indexes,
        categories, and recovery history in your device’s application data.
        Local search and classification do not send email to an AI provider.
        Remote images and attachments are not loaded by the email reader.
      </p>
      <p>
        MailPilot does not encrypt the local mail cache. Use your device’s
        full-disk encryption and screen lock to help protect it. Other apps
        running as your user, device administrators, and copies in backups may
        still have access. Uninstalling the app may leave this data behind.
      </p>
      <h3>Credentials and Google access</h3>
      <p>
        Google sign-in opens in your browser. Refresh tokens and optional AI
        keys use your operating system’s credential store, with no plaintext
        fallback. Sync and explicitly confirmed label changes communicate with
        Gmail. MailPilot does not send or delete email.
      </p>
      <h3>Optional external AI</h3>
      <p>
        Summary and email questions send only the exact request content you
        separately approve to the selected provider. They are optional; reading,
        search, and organization work without an AI key. The provider’s charges
        and retention policies apply. Cancelling cannot recall content already
        sent. MailPilot adds no background external AI or default telemetry.
      </p>
      <h3>Disconnecting and removing keys</h3>
      <p>
        Disconnect removes this Gmail connection’s stored credential and
        attempts to revoke Google access. Cached email, local indexes,
        categories, and recovery history remain on this device. Disconnect is
        not a data-removal operation and does not remove saved AI provider keys.
      </p>
      <p>
        Before disconnecting, use Optional AI settings and choose Disable AI and
        remove selected provider key. If key removal fails, retry after
        unlocking the operating system’s credential store. Other providers’
        saved keys remain separate. There is currently no in-app cache-removal
        control or guarantee of erasure from backups or device storage.
      </p>
    </section>
  );
}
