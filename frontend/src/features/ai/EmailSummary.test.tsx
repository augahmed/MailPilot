import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { summarizeEmail, summarySchema } from "../../lib/ipc/ai";
import fixture from "../../../../contracts/email-summary-v1.json";
import { EmailSummary } from "./EmailSummary";
vi.mock("../../lib/ipc/ai", async (original) => ({
  ...(await original<typeof import("../../lib/ipc/ai")>()),
  summarizeEmail: vi.fn(),
}));
let current = summarySchema.parse({
  ...fixture.response,
  state: "idle",
  email: "",
  policy: "",
  previewId: "",
  expiresAt: 0,
});
beforeEach(() => {
  vi.resetAllMocks();
  current = summarySchema.parse({
    ...fixture.response,
    state: "idle",
    email: "",
    policy: "",
    previewId: "",
    expiresAt: 0,
  });
  vi.mocked(summarizeEmail).mockImplementation(async (request) => {
    if (request.operation === "preview")
      current = summarySchema.parse({
        ...fixture.response,
        email:
          "Please confirm by Tuesday. <img src='https://example.test/pixel'>",
      });
    if (request.operation === "approve")
      current = summarySchema.parse({
        ...fixture.response,
        state: "completed",
        email: "",
        policy: "",
        previewId: "",
        expiresAt: 0,
        result: {
          summary: "Synthetic summary",
          requests: ["Please confirm"],
          deadlines: ["Tuesday"],
        },
      });
    return current;
  });
});
it("requires exact payload preview and explicit approval, then renders normalized results", async () => {
  const { container } = render(
    <EmailSummary accountId="fixture-account" emailId="selected" />,
  );
  await waitFor(() =>
    expect(summarizeEmail).toHaveBeenCalledWith({
      operation: "status",
      accountId: "fixture-account",
      emailId: "selected",
    }),
  );
  expect(
    vi
      .mocked(summarizeEmail)
      .mock.calls.every(([r]) => r.operation !== "approve"),
  ).toBe(true);
  fireEvent.click(
    screen.getByRole("button", { name: "Preview email summary" }),
  );
  const run = await screen.findByRole("button", {
    name: "Summarize selected email",
  });
  expect(run).toBeDisabled();
  expect(
    screen.getByText(/Please confirm by Tuesday. <img/),
  ).toBeInTheDocument();
  expect(container.querySelector("img")).toBeNull();
  fireEvent.click(screen.getByRole("checkbox"));
  fireEvent.click(run);
  await screen.findByText("Synthetic summary");
  expect(screen.getByText("Please confirm")).toBeInTheDocument();
  expect(screen.getByText("Tuesday")).toBeInTheDocument();
  expect(summarizeEmail).toHaveBeenCalledWith({
    operation: "approve",
    accountId: "fixture-account",
    emailId: "selected",
    previewId: "one-use-preview",
    approved: true,
  });
});
it("works with AI disabled without uploading a message", async () => {
  vi.mocked(summarizeEmail).mockResolvedValue(
    summarySchema.parse({ ...current, code: "ai_disabled" }),
  );
  render(<EmailSummary accountId="a" emailId="e" />);
  await screen.findByText(/AI is disabled/);
  expect(screen.queryByRole("checkbox")).toBeNull();
  expect(
    vi
      .mocked(summarizeEmail)
      .mock.calls.every(([r]) => r.operation === "status"),
  ).toBe(true);
});
it("drops consent and clears pending payload on reader exit", async () => {
  const { unmount } = render(
    <EmailSummary accountId="fixture-account" emailId="selected" />,
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Preview email summary" }),
  );
  await screen.findByRole("checkbox");
  unmount();
  expect(summarizeEmail).toHaveBeenCalledWith({
    operation: "clear",
    accountId: "fixture-account",
    emailId: "selected",
  });
});

afterEach(() => vi.useRealTimers());

it("clears an old status error after polling recovers without requesting generation", async () => {
  vi.useFakeTimers();
  vi.mocked(summarizeEmail).mockRejectedValueOnce(
    new Error("synthetic failure"),
  );
  render(<EmailSummary accountId="a" emailId="e" />);
  await act(async () => {});
  expect(screen.getByRole("alert")).toHaveTextContent(
    "Cannot read summary status.",
  );
  await act(async () => {
    await vi.advanceTimersByTimeAsync(1000);
  });
  expect(screen.queryByRole("alert")).toBeNull();
  expect(
    vi
      .mocked(summarizeEmail)
      .mock.calls.every(([request]) => request.operation === "status"),
  ).toBe(true);
});
