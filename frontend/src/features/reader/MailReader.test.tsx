import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { MailReader } from "./MailReader";
import {
  readCachedMail,
  readerSchema,
  type ReadMailResponse,
} from "../../lib/ipc/reader";
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
