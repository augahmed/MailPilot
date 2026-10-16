import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { manageReplies } from "../../lib/ipc/client";
import { replyResponseSchema } from "../../lib/ipc/reply";
import fixture from "../../../../contracts/reply-drafting-v1.json";
import { AIMaintenance } from "./AIMaintenance";

vi.mock("../../lib/ipc/client", () => ({ manageReplies: vi.fn() }));
const response = (changes = {}) =>
  replyResponseSchema.parse({
    ...fixture.response,
    state: "idle",
    previewId: "",
    expiresAt: 0,
    context: "",
    instruction: "",
    policy: "",
    ...changes,
  });
beforeEach(() => vi.resetAllMocks());

it("retains settings without exposing any email selection, prompt or creation controls", async () => {
  vi.mocked(manageReplies).mockResolvedValue(response());
  render(<AIMaintenance />);
  await screen.findByText(/Configured model:/);
  expect(screen.queryByRole("combobox")).toBeNull();
  expect(
    screen.queryByRole("textbox", { name: /instruction|reply body/i }),
  ).toBeNull();
  expect(
    screen.queryByRole("button", { name: /generate|create|preview/i }),
  ).toBeNull();
  fireEvent.change(screen.getByLabelText("OpenAI model"), {
    target: { value: "synthetic-model" },
  });
  const key = screen.getByLabelText("OpenAI API key");
  fireEvent.change(key, { target: { value: "synthetic-secret" } });
  fireEvent.click(screen.getByRole("button", { name: "Save AI settings" }));
  expect(key).toHaveValue("");
  await waitFor(() =>
    expect(manageReplies).toHaveBeenCalledWith({
      accountId: "fixture-account",
      operation: "configure",
      model: "synthetic-model",
      apiKey: "synthetic-secret",
    }),
  );
});

it("offers only read-only reconciliation and separately confirmed historical resolution", async () => {
  vi.mocked(manageReplies).mockResolvedValue(
    response({ state: "uncertain", actionId: "old", reconciled: true }),
  );
  render(<AIMaintenance />);
  await screen.findByRole("button", { name: "Reconcile historical draft" });
  const close = screen.getByRole("button", { name: "Close reviewed action" });
  expect(close).toBeDisabled();
  fireEvent.click(screen.getByRole("checkbox"));
  fireEvent.click(close);
  await waitFor(() =>
    expect(manageReplies).toHaveBeenCalledWith({
      accountId: "fixture-account",
      operation: "resolve",
      actionId: "old",
      approved: true,
    }),
  );
  expect(screen.queryByRole("button", { name: /create|generate/i })).toBeNull();
});

it("shows historical previews as inert evidence without restoring an editor", async () => {
  vi.mocked(manageReplies).mockResolvedValue(
    response({
      state: "draft_preview",
      actionId: "old",
      body: "HISTORICAL_BODY",
    }),
  );
  render(<AIMaintenance />);
  await screen.findByText(/historical preview was not submitted/i);
  expect(screen.queryByText("HISTORICAL_BODY")).toBeNull();
  expect(
    screen.queryByRole("button", { name: /create|generate|preview/i }),
  ).toBeNull();
});
