import { useEffect, useRef, useState } from "react";
import { gmailCommand, type GmailStatus } from "../../lib/ipc/client";

export function useGmailConnection() {
  const [status, setStatus] = useState<GmailStatus | null>(null);
  const [email, setEmail] = useState<string | null>(null);
  const [error, setError] = useState(false);
  const [busy, setBusy] = useState(false);
  const revision = useRef(0);
  const mounted = useRef(false);
  const pending = useRef(false);

  const accept = (result: GmailStatus) => {
    setStatus(result);
    setError(false);
    if (result.state === "connected") setEmail(result.email ?? "Gmail account");
    // Match the backend's definitive session rejection. Transient network
    // errors and refresh checks preserve an already authorized local session.
    else if (
      [
        "disconnected",
        "disconnecting",
        "reauth_required",
        "unconfigured",
      ].includes(result.state) ||
      [
        "credential_store_unavailable",
        "required_scope_missing",
        "consent_denied",
        "reauth_required",
      ].includes(result.code ?? "")
    )
      setEmail(null);
  };

  useEffect(() => {
    mounted.current = true;
    let active = true;
    let timer: ReturnType<typeof setTimeout>;
    const poll = async () => {
      const current = revision.current;
      try {
        if (!pending.current) {
          const result = await gmailCommand("gmail_status");
          if (active && !pending.current && current === revision.current)
            accept(result);
        }
      } catch {
        if (active && !pending.current && current === revision.current)
          setError(true);
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
    if (pending.current) return;
    revision.current++;
    pending.current = true;
    setBusy(true);
    setError(false);
    if (command === "gmail_disconnect") {
      setEmail(null);
      setStatus({ state: "disconnecting" });
    }
    try {
      const result = await gmailCommand(command);
      if (mounted.current) accept(result);
    } catch {
      if (mounted.current) setError(true);
    } finally {
      revision.current++;
      pending.current = false;
      if (mounted.current) setBusy(false);
    }
  };
  return { status, email, error, busy, act };
}
export type GmailConnectionState = ReturnType<typeof useGmailConnection>;
