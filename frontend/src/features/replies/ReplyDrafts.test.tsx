import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { ReplyDrafts } from "./ReplyDrafts";
import {
  getMailbox,
  manageReplies,
  type ReplyResponse,
} from "../../lib/ipc/client";
import fixture from "../../../../contracts/reply-drafting-v1.json";
vi.mock("../../lib/ipc/client", () => ({
  getMailbox: vi.fn(),
  manageReplies: vi.fn(),
}));
const preview = fixture.response as ReplyResponse;
let current: ReplyResponse;
beforeEach(() => {
  vi.resetAllMocks();
  current = {
    ...preview,
    state: "idle",
    context: "",
    instruction: "",
    policy: "",
    previewId: "",
    expiresAt: 0,
  };
  vi.mocked(getMailbox).mockResolvedValue({
    accountId: "fixture-account",
    rows: [
      {
        id: "fixture-message",
        sender: "Host",
        subject: "Synthetic meeting",
        receivedAt: 1791810000000,
        bodyState: "complete",
      },
    ],
    hasMore: false,
  });
  vi.mocked(manageReplies).mockImplementation(async (p = {}) => {
    switch (p.operation) {
      case "prepare":
        current = { ...preview };
        break;
      case "generate":
        current = {
          ...current,
          state: "editing",
          context: "",
          instruction: "",
          policy: "",
          previewId: "",
          reviewId: "review",
          body: "Synthetic proposed reply.",
        };
        break;
      case "preview_draft":
        current = {
          ...current,
          state: "draft_preview",
          body: p.body ?? "",
          actionId: "action",
          previewId: "digest",
        };
        break;
      case "create":
        current = {
          ...current,
          state: "created",
          draftId: "draft",
          reviewId: "",
        };
        break;
    }
    return { ...current };
  });
});
afterEach(cleanup);
async function selectAndPreview() {
  await screen.findByRole("combobox", { name: "Email" });
  await fireEvent.change(screen.getByRole("combobox", { name: "Email" }), {
    target: { value: "fixture-message" },
  });
  await fireEvent.change(screen.getByRole("combobox", { name: "Tone" }), {
    target: { value: "Friendly" },
  });
  await fireEvent.change(screen.getByLabelText("Optional instruction"), {
    target: { value: "Offer Thursday." },
  });
  await fireEvent.click(
    screen.getByRole("button", { name: "Preview AI context" }),
  );
}
it("requires separate context and draft approvals and preserves human edits", async () => {
  render(<ReplyDrafts />);
  await selectAndPreview();
  expect(manageReplies).toHaveBeenCalledWith(
    expect.objectContaining({
      operation: "prepare",
      emailId: "fixture-message",
      includeThread: false,
      tone: "Friendly",
      instruction: "Offer Thursday.",
    }),
  );
  expect(
    await screen.findByText(
      /email content below will be sent to external provider/,
    ),
  ).toBeInTheDocument();
  expect(
    screen.getByRole("button", { name: "Generate proposed reply" }),
  ).toBeDisabled();
  expect(
    vi
      .mocked(manageReplies)
      .mock.calls.some(([p]) => p?.operation === "generate"),
  ).toBe(false);
  await fireEvent.click(
    await screen.findByLabelText("I approve this exact AI request"),
  );
  await fireEvent.click(
    screen.getByRole("button", { name: "Generate proposed reply" }),
  );
  const editor = await screen.findByLabelText("Reply body");
  await fireEvent.change(editor, { target: { value: "" } });
  await fireEvent.change(editor, { target: { value: "My edited reply." } });
  await fireEvent.click(
    screen.getByRole("button", { name: "Preview Gmail draft" }),
  );
  expect(manageReplies).toHaveBeenCalledWith(
    expect.objectContaining({
      operation: "preview_draft",
      previewId: "review",
      body: "My edited reply.",
    }),
  );
  expect(
    await screen.findByRole("button", { name: "Create Gmail draft" }),
  ).toBeDisabled();
  expect(
    vi
      .mocked(manageReplies)
      .mock.calls.some(([p]) => p?.operation === "create"),
  ).toBe(false);
  await fireEvent.click(
    await screen.findByLabelText("I approve creating this exact Gmail draft"),
  );
  await fireEvent.change(editor, {
    target: { value: "My edited reply. One more edit." },
  });
  expect(
    screen.queryByRole("button", { name: "Create Gmail draft" }),
  ).not.toBeInTheDocument();
  await fireEvent.click(
    screen.getByRole("button", { name: "Preview Gmail draft" }),
  );
  await fireEvent.click(
    await screen.findByLabelText("I approve creating this exact Gmail draft"),
  );
  await fireEvent.click(
    await screen.findByRole("button", { name: "Create Gmail draft" }),
  );
  expect(
    await screen.findByText(/Gmail draft created. Nothing was sent/),
  ).toBeInTheDocument();
  expect(manageReplies).toHaveBeenCalledWith(
    expect.objectContaining({
      operation: "create",
      actionId: "action",
      previewId: "digest",
      approved: true,
    }),
  );
  expect(
    vi
      .mocked(manageReplies)
      .mock.calls.some(([p]) => String(p?.operation).includes("send")),
  ).toBe(false);
});
it("invalidates disclosure when selection or tone changes and escapes hostile content", async () => {
  vi.mocked(manageReplies).mockImplementation(async (p = {}) =>
    p.operation === "prepare"
      ? {
          ...preview,
          context: '<img src="https://example.test/track"> hostile context',
        }
      : current,
  );
  render(<ReplyDrafts />);
  await selectAndPreview();
  expect(await screen.findByText(/hostile context/)).toBeInTheDocument();
  expect(document.querySelector("img")).toBeNull();
  await fireEvent.click(
    await screen.findByLabelText("I approve this exact AI request"),
  );
  await fireEvent.change(screen.getByRole("combobox", { name: "Tone" }), {
    target: { value: "Formal" },
  });
  expect(
    screen.queryByRole("button", { name: "Generate proposed reply" }),
  ).not.toBeInTheDocument();
});
it("clears the API key immediately and does not require AI for other UI", async () => {
  current = { ...current, configured: false, model: "" };
  render(<ReplyDrafts />);
  await screen.findByText(/AI generation is disabled/);
  expect(
    screen.getByRole("button", { name: "Preview AI context" }),
  ).toBeDisabled();
  await fireEvent.click(screen.getByText("Optional AI settings"));
  await fireEvent.change(screen.getByLabelText("OpenAI model"), {
    target: { value: "fixture-model" },
  });
  const key = screen.getByLabelText("OpenAI API key");
  await fireEvent.change(key, { target: { value: "synthetic-secret" } });
  vi.mocked(manageReplies).mockImplementation(async () => {
    throw new Error("offline");
  });
  await fireEvent.click(
    screen.getByRole("button", { name: "Save AI settings" }),
  );
  expect(key).toHaveValue("");
  await screen.findByText(/The request failed/);
  expect(document.body.textContent).not.toContain("synthetic-secret");
});
it("never retries an uncertain creation and offers read-only reconciliation", async () => {
  current = {
    ...current,
    state: "uncertain",
    actionId: "action",
    code: "outcome_unknown",
  };
  render(<ReplyDrafts />);
  await fireEvent.click(
    await screen.findByRole("button", { name: "Reconcile draft" }),
  );
  expect(manageReplies).toHaveBeenCalledWith(
    expect.objectContaining({ operation: "reconcile", actionId: "action" }),
  );
  expect(
    vi
      .mocked(manageReplies)
      .mock.calls.some(([p]) => p?.operation === "create"),
  ).toBe(false);
});
it("does not show a late generated reply after disconnect/remount", async () => {
  let finish!: (r: ReplyResponse) => void;
  const original = vi.mocked(manageReplies).getMockImplementation()!;
  vi.mocked(manageReplies).mockImplementation((p = {}) =>
    p.operation === "generate"
      ? new Promise((resolve) => {
          finish = resolve;
        })
      : original(p),
  );
  const { rerender } = render(<ReplyDrafts key="one" />);
  await selectAndPreview();
  await fireEvent.click(
    await screen.findByLabelText("I approve this exact AI request"),
  );
  await fireEvent.click(
    screen.getByRole("button", { name: "Generate proposed reply" }),
  );
  current = {
    ...current,
    accountId: "",
    state: "idle",
    body: "",
    code: "unauthorized",
  };
  rerender(<ReplyDrafts key="disconnected" />);
  await screen.findByText("Connect Gmail to draft a reply.");
  finish({
    ...preview,
    state: "editing",
    reviewId: "late",
    body: "PRIVATE_LATE_REPLY",
  });
  await waitFor(() =>
    expect(screen.queryByText("PRIVATE_LATE_REPLY")).not.toBeInTheDocument(),
  );
  expect(screen.queryByLabelText("Reply body")).not.toBeInTheDocument();
});
