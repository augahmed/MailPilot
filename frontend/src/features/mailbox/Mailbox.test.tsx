import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { Mailbox } from "./Mailbox";
import {
  getMailbox,
  syncCommand,
  manageCategories,
  type MailboxPage,
} from "../../lib/ipc/client";
vi.mock("../../lib/ipc/client", () => ({
  getMailbox: vi.fn(),
  syncCommand: vi.fn(),
  manageCategories: vi.fn(),
}));
beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(manageCategories).mockResolvedValue({
    accountId: "a",
    rows: [],
    hasMore: false,
  });
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
    hasMore: false,
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
  const rows = Array.from({ length: 31 }, (_, i) => ({
    id: String(i),
    sender: i ? `Sender ${i}` : "Sender",
    subject: i ? `Email ${i}` : '<img src="https://example.test">',
    receivedAt: 1700000000000 - i,
    bodyState: "complete" as const,
  }));
  vi.mocked(getMailbox).mockImplementation(async (offset = 0) => ({
    accountId: "a",
    rows: rows.slice(offset, offset + 10),
    hasMore: offset + 10 < rows.length,
  }));
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
  await waitFor(() => expect(getMailbox).toHaveBeenCalledWith(25));
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
  expect(screen.getByText(/Older Inbox emails remain/)).toBeInTheDocument();
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
  expect(
    screen.getByText("Sync finished with some errors"),
  ).toBeInTheDocument();
});

it("shows partial download success and explains failed-read and older-mail continuation", async () => {
  vi.mocked(syncCommand).mockResolvedValue({
    state: "completed",
    accountId: "a",
    limit: 500,
    discovered: 500,
    imported: 499,
    updated: 0,
    errors: 1,
    capped: true,
    more: true,
    snapshotLimited: true,
    code: "provider_error",
  });
  render(<Mailbox connectionRevision={0} />);
  await screen.findByRole("cell", { name: "Sender" });
  expect(
    screen.getByText("Sync finished with some errors"),
  ).toBeInTheDocument();
  expect(
    screen.getByText(/retry failed emails and continue older Inbox mail/),
  ).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Sync Gmail" })).toBeEnabled();
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

it("filters across cached pages, sorts categories and unclassified mail, and preserves reader navigation", async () => {
  const all: MailboxPage["rows"] = Array.from({ length: 60 }, (_, i) => ({
    id: `mail-${i}`,
    sender: "Synthetic sender",
    subject: `Message ${i}`,
    receivedAt: 1700000000000 - i,
    bodyState: "complete",
    classification: {
      state: i % 2 ? "unclassified" : "suggested",
      categoryId: i % 2 ? "" : "finance",
      categoryName: i % 2 ? "" : "Finance",
    },
  }));
  vi.mocked(getMailbox).mockImplementation(async (offset = 0) => ({
    accountId: "a",
    rows: all.slice(offset, offset + 10),
    hasMore: offset + 10 < all.length,
  }));
  vi.mocked(manageCategories).mockResolvedValue({
    accountId: "a",
    rows: [
      {
        id: "finance",
        name: "Finance",
        description: "Financial mail",
        enabled: true,
        created_at: 0,
        updated_at: 0,
      },
    ],
    hasMore: false,
  });
  const onOpen = vi.fn();
  render(<Mailbox connectionRevision={0} onOpen={onOpen} />);
  await screen.findByText("Message 24");
  const sort = screen.getByRole("combobox", { name: "Sort this page by" });
  fireEvent.change(sort, { target: { value: "unclassified" } });
  expect(screen.getAllByRole("row")[1]).toHaveTextContent("Message 1");
  fireEvent.change(sort, { target: { value: "category" } });
  expect(screen.getAllByRole("row")[1]).toHaveTextContent("Finance");
  fireEvent.change(
    screen.getByRole("combobox", { name: "Filter by category" }),
    { target: { value: "finance" } },
  );
  await screen.findByText("Message 48");
  expect(screen.queryByText("Message 1")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Next" }));
  await screen.findByText("Message 50");
  expect(screen.queryByText("Message 48")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Message 50" }));
  expect(onOpen).toHaveBeenCalledWith(
    "a",
    "mail-50",
    expect.any(HTMLButtonElement),
  );
  fireEvent.click(screen.getByRole("button", { name: "Previous" }));
  await screen.findByText("Message 48");
  fireEvent.change(
    screen.getByRole("combobox", { name: "Filter by category" }),
    { target: { value: "__unclassified__" } },
  );
  await screen.findByText("Message 49");
  expect(screen.queryByText("Message 0")).toBeNull();
});
