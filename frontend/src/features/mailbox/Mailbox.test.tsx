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
  fireEvent.click(screen.getByRole("button", { name: "Sync Gmail" }));
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
  expect(screen.getByRole("button", { name: "Sync Gmail" })).toBeDisabled();
});
it("reports bridge failure without retaining mailbox content", async () => {
  vi.mocked(getMailbox).mockRejectedValue(new Error("offline"));
  render(<Mailbox connectionRevision={0} />);
  expect(await screen.findByRole("alert")).toHaveTextContent("Cannot reach");
  expect(screen.queryByText("Sender")).not.toBeInTheDocument();
  expect(
    screen.queryByText("Connect Gmail to view this account’s cache."),
  ).toBeNull();
});
it("shows signed, ordered risk explanations and uncertainty", async () => {
  vi.mocked(getMailbox).mockResolvedValue({
    accountId: "a",
    hasMore: false,
    rows: [
      {
        id: "1",
        sender: "Sender",
        subject: "Example",
        receivedAt: 1700000000000,
        bodyState: "complete",
        phishing: {
          score: 7,
          risk: "LOW",
          version: "risk-test",
          reasons: [
            {
              code: "credentials",
              points: 15,
              explanation: "Credential-related language",
            },
            { code: "aligned_links", points: -8, explanation: "Aligned links" },
            {
              code: "incomplete",
              points: 0,
              explanation: "Analysis is incomplete",
            },
          ],
        },
      },
    ],
  });
  render(<Mailbox connectionRevision={0} />);
  expect(await screen.findByText("Low · 7/100")).toBeInTheDocument();
  fireEvent.click(screen.getByText("Low · 7/100"));
  expect(
    screen.getAllByRole("listitem").map((item) => item.textContent),
  ).toEqual([
    "+15 Credential-related language",
    "-8 Aligned links",
    "0 Analysis is incomplete",
  ]);
  expect(
    screen.getByText(/not a probability or a guarantee/),
  ).toBeInTheDocument();
});

it("shows recovery, durable continuation and removed counts, and allows cancellation", async () => {
  vi.mocked(syncCommand).mockResolvedValue({
    state: "running",
    accountId: "a",
    limit: 500,
    discovered: 5,
    imported: 1,
    updated: 2,
    errors: 1,
    capped: false,
    mode: "recovery",
    removed: 2,
    more: true,
    snapshotLimited: true,
  });
  render(<Mailbox connectionRevision={0} />);
  expect(
    await screen.findByText("Sync mode: Checkpoint recovery"),
  ).toBeInTheDocument();
  expect(screen.getByText(/continue from saved progress/)).toBeInTheDocument();
  expect(screen.getByText("Removed from cache")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Stop sync" }));
  await waitFor(() => expect(syncCommand).toHaveBeenCalledWith("cancel"));
});

it("keeps cached mail readable when local processing fails", async () => {
  vi.mocked(syncCommand).mockResolvedValue({
    state: "completed",
    accountId: "a",
    limit: 500,
    discovered: 1,
    imported: 1,
    updated: 0,
    errors: 1,
    capped: false,
    code: "chunking_failed",
  });
  render(<Mailbox connectionRevision={0} />);
  expect(
    await screen.findByRole("cell", { name: "Sender" }),
  ).toBeInTheDocument();
  expect(
    await screen.findByText(/Their original content is saved/),
  ).toBeInTheDocument();
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
});

it.each([
  ["LOW", 12, "Low"],
  ["MEDIUM", 42, "Medium"],
  ["HIGH", 83, "High"],
] as const)(
  "displays the supplied %s risk band and score",
  async (risk, score, label) => {
    vi.mocked(getMailbox).mockResolvedValue({
      accountId: "a",
      hasMore: false,
      rows: [
        {
          id: "1",
          sender: "Sender",
          subject: "Example",
          receivedAt: 1700000000000,
          bodyState: "complete",
          phishing: { score, risk, version: "risk-test", reasons: [] },
        },
      ],
    });
    render(<Mailbox connectionRevision={0} />);
    expect(await screen.findByText(`${label} · ${score}/100`)).toBeVisible();
    expect(getMailbox).toHaveBeenCalledTimes(1);
  },
);
it("distinguishes missing risk assessments without inventing a safe score", async () => {
  render(<Mailbox connectionRevision={0} />);
  expect(await screen.findByText("Not assessed")).toBeVisible();
  expect(screen.queryByText(/Low ·/)).toBeNull();
});
