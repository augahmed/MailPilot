import { MailReader } from "../reader/MailReader";
import { useEffect, useRef, useState } from "react";
import {
  manageCategoryVectors,
  type CategoryVectorResponse,
  type CategoryVectorRequest,
} from "../../lib/ipc/category-vectors";
import {
  getMailbox,
  type MailboxPage,
  type Category,
} from "../../lib/ipc/client";
import { readCachedMail, type ReadMailResponse } from "../../lib/ipc/reader";

const errors: Record<Exclude<CategoryVectorResponse["code"], "">, string> = {
  unauthorized: "Connect your account to view its local representations.",
  unavailable: "The local representation service is unavailable.",
  validation:
    "The example could not be accepted. Check the category and try again.",
  not_found: "The category or email is no longer in this account’s cache.",
  stale: "The cached email changed. Preview it again before confirming.",
  cancelled: "The operation was cancelled. Saved progress is retained.",
  storage_error: "Local storage could not complete the operation. Try again.",
  too_large:
    "This explanation is too large to display. Reduce the confirmed examples and try again.",
};

export function CategoryRepresentation({
  accountId,
  category,
}: {
  accountId: string;
  category: Category;
}) {
  const [opened, setOpened] = useState<{
    emailId: string;
    revision: string;
    text: string;
  } | null>(null);
  const [representation, setRepresentation] =
    useState<CategoryVectorResponse | null>(null);
  const [mailbox, setMailbox] = useState<MailboxPage | null>(null);
  const [preview, setPreview] = useState<ReadMailResponse | null>(null);
  const [offset, setOffset] = useState(0);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const active = useRef(false);
  const heading = useRef<HTMLHeadingElement>(null);
  const pending = useRef(false);
  const version = useRef(0);
  const request = (
    operation: CategoryVectorRequest["operation"] = "inspect",
    emailId = "",
    revision = "",
  ): CategoryVectorRequest => ({
    operation,
    accountId,
    categoryId: category.id,
    emailId,
    revision,
    approved: operation === "confirm_example",
  });
  useEffect(() => {
    active.current = true;
    let current = true;
    let timer: ReturnType<typeof setTimeout>;
    const poll = async () => {
      const v = version.current;
      try {
        const r = await manageCategoryVectors({
          operation: "inspect",
          accountId,
          categoryId: category.id,
          emailId: "",
          revision: "",
          approved: false,
        });
        if (current && !pending.current && v === version.current) {
          setRepresentation(r.code ? null : r);
          if (r.code) {
            setOpened(null);
            setMailbox(null);
            setPreview(null);
            setMessage(errors[r.code]);
          }
        }
      } catch {
        if (current && !pending.current && v === version.current) {
          setRepresentation(null);
          setOpened(null);
          setMailbox(null);
          setPreview(null);
          setMessage("Cannot load the local representation.");
        }
      } finally {
        if (current) timer = setTimeout(() => void poll(), 2000);
      }
    };
    void poll();
    return () => {
      current = false;
      active.current = false;
      clearTimeout(timer);
    };
  }, [accountId, category.id]);
  async function run(action: () => Promise<void>) {
    if (pending.current) return;
    pending.current = true;
    version.current++;
    setBusy(true);
    setMessage("");
    try {
      await action();
    } catch {
      if (active.current) {
        setPreview(null);
        setMailbox(null);
        setMessage(
          "Could not complete the local operation. Refresh and try again.",
        );
      }
    } finally {
      pending.current = false;
      version.current++;
      if (active.current) setBusy(false);
    }
  }
  async function changeExample(
    operation: "confirm_example" | "remove_example",
    emailId: string,
    revision = "",
  ) {
    const r = await manageCategoryVectors(
      request(operation, emailId, revision),
    );
    if (!active.current) return;
    setPreview(null);
    setRepresentation(r.code ? null : r);
    setMessage(
      r.code
        ? errors[r.code]
        : "Examples saved locally. Start or resume Local Email Index to update the representation.",
    );
    if (r.code) setMailbox(null);
  }
  async function loadMail(nextOffset: number) {
    const page = await getMailbox(nextOffset);
    if (!active.current) return;
    setPreview(null);
    if (page.accountId !== accountId || page.code)
      throw new Error("Account changed");
    setMailbox(page);
    setOffset(nextOffset);
  }
  if (opened)
    return (
      <MailReader
        accountId={accountId}
        emailId={opened.emailId}
        source={`Category ${category.name}`}
        evidence={{ text: opened.text, revision: opened.revision }}
        onBack={() => {
          setOpened(null);
          setTimeout(() => heading.current?.focus(), 0);
        }}
      />
    );
  return (
    <section
      className="category-representation"
      aria-label={`Local representation for ${category.name}`}
      aria-busy={busy}
    >
      <h3 ref={heading} tabIndex={-1}>
        Examples & local profile
      </h3>
      <p>
        Built on this device with the email embedding model. Start or resume
        Local Email Index to build pending representations. This does not
        classify messages.
      </p>
      <p role="status">{message}</p>
      {representation && (
        <>
          <p>Local profile: {representation.status}.</p>
          <details className="technical-details">
            <summary>Model details</summary>
            <p>
              {representation.modelId} · {representation.modelVersion}
            </p>
          </details>
          <p>
            The definition and each included example email have equal weight.
            Each example uses up to three non-overlapping shared chunks. Changed
            or incompletely indexed examples are excluded until ready; changed
            emails need confirmation again.
          </p>
          <details>
            <summary>Text contributing to the definition</summary>
            {representation.definitionInputs.map((input, i) => (
              <pre key={i} className="mail-body">
                {input}
              </pre>
            ))}
          </details>
          <ul>
            {representation.examples.map((e) => (
              <li key={e.emailId}>
                <p>
                  {e.subject || "(No subject)"} —{" "}
                  {e.status.replaceAll("_", " ")}
                </p>
                {e.chunks.map((c) => (
                  <div key={c.chunkId}>
                    <pre className="mail-body">{c.context}</pre>
                    <blockquote>
                      {c.text}
                      {c.truncated ? "… (excerpt)" : ""}
                    </blockquote>
                    <small>Chunk {c.chunkId}</small>
                  </div>
                ))}
                {e.status === "included" && (
                  <button
                    disabled={busy}
                    onClick={() =>
                      setOpened({
                        emailId: e.emailId,
                        revision: e.revision,
                        text: e.chunks.map((c) => c.text).join("\n\n"),
                      })
                    }
                  >
                    Open original example: {e.subject || e.emailId}
                  </button>
                )}
                <button
                  disabled={busy}
                  onClick={() =>
                    void run(() => changeExample("remove_example", e.emailId))
                  }
                >
                  Remove example: {e.subject || e.emailId}
                </button>
              </li>
            ))}
          </ul>
          {representation.examples.length === 0 && (
            <p>No confirmed examples. The definition alone is sufficient.</p>
          )}
          <button
            disabled={
              busy || !category.enabled || representation.examples.length >= 10
            }
            onClick={() => void run(() => loadMail(0))}
          >
            Choose a confirmed example
          </button>
        </>
      )}
      {mailbox && (
        <div role="group" aria-label="Choose example email">
          <p>
            Select an email you have reviewed as belonging to this category.
            Existing assignments and predictions are never added automatically.
          </p>
          {mailbox.rows.map((e) => (
            <button
              key={e.id}
              disabled={busy || e.bodyState !== "complete"}
              onClick={() =>
                void run(async () => {
                  setPreview(null);
                  const r = await readCachedMail(accountId, e.id);
                  if (active.current) {
                    if (
                      r.code ||
                      !r.message ||
                      r.message.bodyState !== "complete"
                    ) {
                      setMessage(
                        "Email is unavailable or changed. Select it again.",
                      );
                      return;
                    }
                    setPreview(r);
                  }
                })
              }
            >
              Preview example: {e.subject || "(No subject)"}
            </button>
          ))}
          <button
            disabled={busy || offset === 0}
            onClick={() => void run(() => loadMail(Math.max(0, offset - 10)))}
          >
            Previous example emails
          </button>
          <button
            disabled={busy || !mailbox.hasMore}
            onClick={() => void run(() => loadMail(offset + 10))}
          >
            Next example emails
          </button>
          <button
            disabled={busy}
            onClick={() => {
              setMailbox(null);
              setPreview(null);
            }}
          >
            Close example picker
          </button>
        </div>
      )}
      {preview && (
        <div role="group" aria-label="Confirm category example">
          <h4>{preview.message?.subject}</h4>
          <p>From: {preview.message?.sender.address}</p>
          <pre className="mail-body">{preview.body}</pre>
          {preview.nextOffset < preview.totalBytes && (
            <p>
              The preview continues in the original email. Confirm only if you
              have reviewed the message.
            </p>
          )}
          <button
            disabled={busy || !category.enabled}
            onClick={() =>
              void run(() =>
                changeExample(
                  "confirm_example",
                  preview.emailId,
                  preview.revision,
                ),
              )
            }
          >
            Confirm this email as a {category.name} example
          </button>
          <button disabled={busy} onClick={() => setPreview(null)}>
            Cancel example confirmation
          </button>
        </div>
      )}
    </section>
  );
}
