import { useState } from "react";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { MailWorkspace } from "./MailWorkspace";
import type { OpenMail } from "../../lib/ipc/reader";
beforeEach(() => {
  vi.spyOn(window, "scrollTo").mockImplementation(() => {});
});
afterEach(() => {
  vi.restoreAllMocks();
  Object.defineProperty(window, "scrollY", { configurable: true, value: 0 });
});
vi.mock("../mailbox/Mailbox", () => ({
  Mailbox: ({ onOpen }: { onOpen: OpenMail }) => {
    const [page, setPage] = useState(0);
    return (
      <section>
        <button onClick={() => setPage(page + 1)}>Next mailbox page</button>
        <p>Mailbox page {page}</p>
        <button onClick={(e) => onOpen("a", "e", e.currentTarget)}>
          Open mailbox message
        </button>
      </section>
    );
  },
}));
vi.mock("../search/SemanticSearch", () => ({
  SemanticSearch: ({
    onOpen,
    onEvidence,
  }: {
    onOpen: OpenMail;
    onEvidence: (v: { text: string; revision: string }) => void;
  }) => {
    const [query, setQuery] = useState("");
    const [hasResult, setHasResult] = useState(true);
    return (
      <section>
        <label>
          Saved search
          <input value={query} onChange={(e) => setQuery(e.target.value)} />
        </label>
        <button onClick={() => setHasResult(false)}>
          Clear search results
        </button>
        {hasResult && (
          <button
            onClick={(e) => {
              onEvidence({ text: "Matching chunk", revision: "a".repeat(64) });
              onOpen("a", "e", e.currentTarget);
            }}
          >
            Open search result
          </button>
        )}
      </section>
    );
  },
}));
vi.mock("./MailReader", () => ({
  MailReader: ({ onBack, source }: { onBack: () => void; source: string }) => (
    <button onClick={onBack}>Back to {source}</button>
  ),
}));
it("preserves mailbox state and focus without mounting retired entry points", async () => {
  render(<MailWorkspace connectionRevision={0} />);
  fireEvent.click(screen.getByText("Next mailbox page"));
  const mailbox = screen.getByText("Open mailbox message");
  fireEvent.click(mailbox);
  expect(
    screen.queryByRole("button", { name: "Next mailbox page" }),
  ).toBeNull();
  fireEvent.click(screen.getByText("Back to mailbox"));
  expect(screen.getByText("Mailbox page 1")).toBeVisible();
  await waitFor(() => expect(mailbox).toHaveFocus());
  expect(screen.queryByText("Open search result")).toBeNull();
  expect(screen.queryByText("Open cited email")).toBeNull();
});

it("preserves search state and returns focus after reading its original email", async () => {
  render(
    <MailWorkspace
      connectionRevision={0}
      page="search"
      visited={new Set(["dashboard", "search"])}
    />,
  );
  fireEvent.change(screen.getByLabelText("Saved search"), {
    target: { value: "paraphrased query" },
  });
  const trigger = screen.getByText("Open search result");
  fireEvent.click(trigger);
  expect(screen.getByText("Back to search")).toBeVisible();
  fireEvent.click(screen.getByText("Back to search"));
  expect(screen.getByLabelText("Saved search")).toHaveValue(
    "paraphrased query",
  );
  await waitFor(() => expect(trigger).toHaveFocus());
});

it("restores focus to the workspace when a stale result disappeared while reading", async () => {
  render(
    <MailWorkspace
      connectionRevision={0}
      page="search"
      visited={new Set(["dashboard", "search"])}
    />,
  );
  fireEvent.click(screen.getByText("Open search result"));
  fireEvent.click(screen.getByText("Clear search results"));
  fireEvent.click(screen.getByText("Back to search"));
  await waitFor(() =>
    expect(screen.getByLabelText("Cached email workspace")).toHaveFocus(),
  );
});

it("restores the previous list scroll position without scrolling the focused trigger", async () => {
  Object.defineProperty(window, "scrollY", { configurable: true, value: 280 });
  render(<MailWorkspace connectionRevision={0} />);
  const trigger = screen.getByText("Open mailbox message");
  const focus = vi.spyOn(trigger, "focus");
  fireEvent.click(trigger);
  Object.defineProperty(window, "scrollY", { configurable: true, value: 0 });
  fireEvent.click(screen.getByText("Back to mailbox"));
  await waitFor(() => expect(window.scrollTo).toHaveBeenCalledWith(0, 280));
  expect(focus).toHaveBeenCalledWith({ preventScroll: true });
});
