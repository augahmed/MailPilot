import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { MailReader } from "./MailReader";
import {
  readCachedMail,
  readerSchema,
  type ReadMailResponse,
} from "../../lib/ipc/reader";
vi.mock("../ai/EmailSummary", () => ({ EmailSummary: () => null }));
vi.mock("../../lib/ipc/reader", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../lib/ipc/reader")>()),
  readCachedMail: vi.fn(),
}));
const body =
  '<script>alert(1)</script><img src="https://example.test/pixel"> https://example.test';
const fixture: ReadMailResponse = {
  accountId: "a",
  emailId: "e",
  revision: "a".repeat(64),
  offset: 0,
  nextOffset: body.length,
  totalBytes: body.length,
  body,
  code: "",
  message: {
    subject: "A cached message",
    sender: { name: "Sender", address: "sender@example.test" },
    recipients: [{ name: "", address: "to@example.test" }],
    cc: [{ name: "", address: "cc@example.test" }],
    receivedAt: 1000,
    cachedAt: 2000,
    attachments: [
      {
        filename: "report.pdf",
        mimeType: "application/pdf",
        sizeBytes: 123,
        inline: false,
      },
    ],
    labels: [{ name: "INBOX", kind: "system" }],
    categories: [{ name: "Work", source: "suggested" }],
    categoryState: "classified",
    phishing: {
      score: 20,
      risk: "low",
      version: "risk-v1",
      reasons: [
        { code: "example", points: 20, explanation: "Synthetic risk reason" },
      ],
    },
    bodyState: "complete",
    bodyFormat: "plain_text",
    bodyError: false,
    metadataTruncated: false,
  },
};
beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(readCachedMail).mockResolvedValue(fixture);
});
it("renders cached metadata and hostile text without active HTML or links", async () => {
  const back = vi.fn();
  const { container } = render(
    <MailReader
      accountId="a"
      emailId="e"
      source="search results"
      onBack={back}
    />,
  );
  expect(await screen.findByText(body)).toBeInTheDocument();
  expect(container.querySelector("script,img,iframe,a")).toBeNull();
  for (const value of [
    "cc@example.test",
    "to@example.test",
    "INBOX",
    "Synthetic risk reason (＋20)".replace("＋", "+"),
  ])
    expect(screen.getByText(value)).toBeInTheDocument();
  expect(screen.getByText(/report.pdf/)).toBeInTheDocument();
  fireEvent.click(
    screen.getByRole("button", { name: "Back to search results" }),
  );
  expect(back).toHaveBeenCalledOnce();
});
it("appends cached pages using the same revision and clears stale content", async () => {
  vi.mocked(readCachedMail)
    .mockResolvedValueOnce({
      ...fixture,
      body: "first",
      nextOffset: 5,
      totalBytes: 9,
    })
    .mockResolvedValueOnce({
      ...fixture,
      offset: 5,
      body: "last",
      nextOffset: 9,
      totalBytes: 9,
    });
  render(
    <MailReader accountId="a" emailId="e" source="mailbox" onBack={() => {}} />,
  );
  fireEvent.click(
    await screen.findByRole("button", { name: "Load more cached text" }),
  );
  expect(await screen.findByText("firstlast")).toBeInTheDocument();
  expect(readCachedMail).toHaveBeenLastCalledWith(
    "a",
    "e",
    5,
    fixture.revision,
  );
});
it("reports missing cached content without fetching or displaying metadata", async () => {
  vi.mocked(readCachedMail).mockResolvedValue({
    ...fixture,
    body: "",
    message: null,
    code: "not_found",
  });
  render(
    <MailReader accountId="a" emailId="e" source="mailbox" onBack={() => {}} />,
  );
  await waitFor(() =>
    expect(screen.getByRole("alert")).toHaveTextContent(
      "no longer in the cache",
    ),
  );
  expect(screen.queryByText("cc@example.test")).toBeNull();
});
it("rejects HTML payload fields and invalid body paging contracts", () => {
  expect(readerSchema.safeParse(fixture).success).toBe(true);
  expect(
    readerSchema.safeParse({ ...fixture, html: "<script/>" }).success,
  ).toBe(false);
  expect(readerSchema.safeParse({ ...fixture, nextOffset: 1 }).success).toBe(
    false,
  );
});

it("accepts the shared version 1 cached reader contract", async () => {
  const fixture = await import("../../../../contracts/mail-reader-v1.json");
  expect(readerSchema.safeParse(fixture.default).success).toBe(true);
});

it("shows cleaned offer text while keeping links and images inactive", async () => {
  const text =
    "Double rewards October 5–16.\nSave 14% at Northshop\nUnique quoted appointment at noon.";
  const bytes = new TextEncoder().encode(text).length;
  vi.mocked(readCachedMail).mockResolvedValue({
    ...fixture,
    body: text,
    nextOffset: bytes,
    totalBytes: bytes,
    message: { ...fixture.message!, bodyFormat: "html_text" },
  });
  const { container } = render(
    <MailReader accountId="a" emailId="e" source="mailbox" onBack={() => {}} />,
  );
  expect(await screen.findByText(/Save 14% at Northshop/)).toBeInTheDocument();
  expect(
    screen.getByText(/Cleaned text from the cached email/),
  ).toBeInTheDocument();
  expect(container.querySelector("script,img,iframe,a")).toBeNull();
});

it("binds search evidence to the cached reader revision and clears it on stale", async () => {
  const evidence = {
    text: "Relevant quoted passage",
    revision: fixture.revision,
  };
  render(
    <MailReader
      accountId="a"
      emailId="e"
      source="search"
      onBack={vi.fn()}
      evidence={evidence}
    />,
  );
  expect(await screen.findByText(evidence.text)).toBeVisible();
  expect(readCachedMail).toHaveBeenCalledWith("a", "e", 0, fixture.revision);
});
it("does not show old search evidence when the original source changed", async () => {
  vi.mocked(readCachedMail).mockResolvedValue({
    ...fixture,
    code: "stale",
    message: null,
    body: "",
  });
  render(
    <MailReader
      accountId="a"
      emailId="e"
      source="search"
      onBack={vi.fn()}
      evidence={{ text: "Old evidence", revision: fixture.revision }}
    />,
  );
  await screen.findByText(/cached message changed/i);
  expect(screen.queryByText("Old evidence")).toBeNull();
});

it("reloads the current original after stale search evidence without showing the old excerpt", async () => {
  vi.mocked(readCachedMail).mockResolvedValueOnce({
    ...fixture,
    code: "stale",
    body: "",
    message: null,
  });
  render(
    <MailReader
      accountId="a"
      emailId="e"
      source="search"
      onBack={vi.fn()}
      evidence={{ text: "Old search evidence", revision: fixture.revision }}
    />,
  );
  await screen.findByRole("alert");
  const current = {
    ...fixture,
    revision: "b".repeat(64),
    body: "Current cached text",
    nextOffset: 19,
    totalBytes: 19,
  };
  vi.mocked(readCachedMail).mockResolvedValue(current);
  fireEvent.click(
    screen.getByRole("button", { name: "Reload cached message" }),
  );
  expect(await screen.findByText("Current cached text")).toBeVisible();
  expect(readCachedMail).toHaveBeenLastCalledWith("a", "e", 0, "");
  expect(screen.queryByText("Old search evidence")).toBeNull();
});
it("clears a pagination error after loading more text succeeds", async () => {
  vi.mocked(readCachedMail)
    .mockResolvedValueOnce({
      ...fixture,
      body: "first",
      nextOffset: 5,
      totalBytes: 9,
    })
    .mockRejectedValueOnce(new Error("synthetic failure"));
  render(
    <MailReader accountId="a" emailId="e" source="mailbox" onBack={vi.fn()} />,
  );
  fireEvent.click(
    await screen.findByRole("button", { name: "Load more cached text" }),
  );
  await screen.findByRole("alert");
  vi.mocked(readCachedMail).mockResolvedValue({
    ...fixture,
    offset: 5,
    body: "last",
    nextOffset: 9,
    totalBytes: 9,
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Load more cached text" }),
  );
  expect(await screen.findByText("firstlast")).toBeVisible();
  expect(screen.queryByRole("alert")).toBeNull();
});
