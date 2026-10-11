import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { Mailbox } from "./Mailbox";
import { getMailbox, syncCommand } from "../../lib/ipc/client";
vi.mock("../../lib/ipc/client", () => ({
  getMailbox: vi.fn(),
  syncCommand: vi.fn(),
}));
beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(syncCommand).mockResolvedValue({
    state: "completed",
    accountId: "a",
    limit: 500,
    discovered: 2,
    imported: 1,
    updated: 1,
    errors: 0,
    capped: true,
  });
  vi.mocked(getMailbox).mockResolvedValue({
    accountId: "a",
    hasMore: true,
    rows: [
      {
        id: "1",
        sender: "Sender",
        subject: '<img src="https://example.test">',
        receivedAt: 1700000000000,
        bodyState: "complete",
      },
    ],
  });
});
it("shows progress and escaped cached mail, starts sync and pages locally", async () => {
  const { container } = render(<Mailbox connectionRevision={0} />);
  expect(
    await screen.findByRole("cell", { name: "Sender" }),
  ).toBeInTheDocument();
  expect(
    screen.getByText('<img src="https://example.test">'),
  ).toBeInTheDocument();
  expect(container.querySelector("img")).toBeNull();
  expect(screen.getByText("Messages imported")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Sync inbox" }));
  await waitFor(() => expect(syncCommand).toHaveBeenCalledWith(true));
  fireEvent.click(screen.getByRole("button", { name: "Next" }));
  await waitFor(() => expect(getMailbox).toHaveBeenCalledWith(10));
});
it("does not allow sync while disconnected", async () => {
  vi.mocked(getMailbox).mockResolvedValue({
    accountId: "",
    rows: [],
    hasMore: false,
  });
  render(<Mailbox connectionRevision={0} />);
  await screen.findByText("Connect Gmail to view this account’s cache.");
  expect(screen.getByRole("button", { name: "Sync inbox" })).toBeDisabled();
});
it("reports bridge failure without retaining mailbox content", async () => {
  vi.mocked(getMailbox).mockRejectedValue(new Error("offline"));
  render(<Mailbox connectionRevision={0} />);
  expect(await screen.findByRole("alert")).toHaveTextContent("Cannot reach");
  expect(screen.queryByText("Sender")).not.toBeInTheDocument();
});
