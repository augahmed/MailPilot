import { useState } from "react";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { MailWorkspace } from "./MailWorkspace";
import type { OpenMail } from "../../lib/ipc/reader";
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
  SemanticSearch: ({ onOpen }: { onOpen: OpenMail }) => (
    <section>
      <input aria-label="Saved search" />
      <button onClick={(e) => onOpen("a", "s", e.currentTarget)}>
        Open search message
      </button>
    </section>
  ),
}));
vi.mock("../ask/AskMailPilot", () => ({
  AskMailPilot: ({ onOpen }: { onOpen: OpenMail }) => (
    <button onClick={(e) => onOpen("a", "ask-email", e.currentTarget)}>
      Open cited email
    </button>
  ),
}));
vi.mock("./MailReader", () => ({
  MailReader: ({ onBack, source }: { onBack: () => void; source: string }) => (
    <button onClick={onBack}>Back to {source}</button>
  ),
}));
it("returns to the same mailbox page and search query with focus restored", async () => {
  render(<MailWorkspace connectionRevision={0} />);
  fireEvent.click(screen.getByText("Next mailbox page"));
  fireEvent.change(screen.getByLabelText("Saved search"), {
    target: { value: "invoice" },
  });
  const mailbox = screen.getByText("Open mailbox message");
  fireEvent.click(mailbox);
  expect(
    screen.queryByRole("button", { name: "Next mailbox page" }),
  ).toBeNull();
  fireEvent.click(screen.getByText("Back to mailbox"));
  expect(screen.getByText("Mailbox page 1")).toBeVisible();
  await waitFor(() => expect(mailbox).toHaveFocus());
  fireEvent.click(screen.getByText("Open search message"));
  fireEvent.click(screen.getByText("Back to search results"));
  expect(screen.getByLabelText("Saved search")).toHaveValue("invoice");
  const citation = screen.getByText("Open cited email");
  fireEvent.click(citation);
  fireEvent.click(screen.getByText("Back to Ask MailPilot"));
  await waitFor(() => expect(citation).toHaveFocus());
});
