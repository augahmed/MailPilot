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
  expect(screen.queryByText("Open search message")).toBeNull();
  expect(screen.queryByText("Open cited email")).toBeNull();
});
