import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { manageAISettings, aiSettingsSchema } from "../../lib/ipc/ai";
import fixture from "../../../../contracts/ai-settings-v1.json";
import { AISettings } from "./AISettings";
vi.mock("../../lib/ipc/ai", async (original) => ({
  ...(await original<typeof import("../../lib/ipc/ai")>()),
  manageAISettings: vi.fn(),
}));
beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(manageAISettings).mockResolvedValue(
    aiSettingsSchema.parse(fixture.response),
  );
});
it("exposes provider selection and clears a submitted secret immediately", async () => {
  render(<AISettings />);
  await screen.findByLabelText("AI provider");
  expect(screen.getByRole("combobox")).toHaveValue("openai");
  const key = screen.getByLabelText("Provider API key");
  fireEvent.change(key, { target: { value: "synthetic-secret" } });
  fireEvent.click(screen.getByRole("button", { name: "Save AI settings" }));
  expect(key).toHaveValue("");
  await waitFor(() =>
    expect(manageAISettings).toHaveBeenCalledWith({
      operation: "configure",
      accountId: "fixture-account",
      provider: "openai",
      model: "synthetic-model",
      apiKey: "synthetic-secret",
    }),
  );
});
it("uses the selected registered provider and never puts keys in response state", async () => {
  vi.mocked(manageAISettings).mockResolvedValue(
    aiSettingsSchema.parse({
      ...fixture.response,
      providers: [
        ...fixture.response.providers,
        { id: "second-fixture", external: true },
      ],
    }),
  );
  render(<AISettings />);
  fireEvent.change(await screen.findByLabelText("AI provider"), {
    target: { value: "second-fixture" },
  });
  fireEvent.change(screen.getByLabelText("AI model"), {
    target: { value: "other-model" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Save AI settings" }));
  await waitFor(() =>
    expect(manageAISettings).toHaveBeenCalledWith({
      operation: "configure",
      accountId: "fixture-account",
      provider: "second-fixture",
      model: "other-model",
      apiKey: "",
    }),
  );
});

it("keeps provider disabling functional and clears any unsubmitted key", async () => {
  render(<AISettings />);
  const key = await screen.findByLabelText("Provider API key");
  fireEvent.change(key, { target: { value: "unsubmitted-synthetic-secret" } });
  fireEvent.click(
    screen.getByRole("button", {
      name: "Disable AI and remove selected provider key",
    }),
  );
  expect(key).toHaveValue("");
  expect(key).toHaveAttribute("type", "password");
  await waitFor(() =>
    expect(manageAISettings).toHaveBeenCalledWith({
      operation: "disable",
      accountId: "fixture-account",
    }),
  );
  expect(screen.queryByText("unsubmitted-synthetic-secret")).toBeNull();
});

afterEach(() => vi.useRealTimers());

it("allows retrying key removal after AI was disabled but credential deletion failed", async () => {
  const disabled = aiSettingsSchema.parse({
    ...fixture.response,
    enabled: false,
  });
  vi.mocked(manageAISettings)
    .mockResolvedValueOnce(disabled)
    .mockResolvedValueOnce(
      aiSettingsSchema.parse({
        ...disabled,
        code: "ai_credentials_unavailable",
      }),
    )
    .mockResolvedValue(disabled);
  render(<AISettings />);
  const remove = await screen.findByRole("button", {
    name: "Disable AI and remove selected provider key",
  });
  expect(remove).toBeEnabled();
  fireEvent.change(screen.getByLabelText("Provider API key"), {
    target: { value: "unsubmitted-secret" },
  });
  fireEvent.click(remove);
  expect(screen.getByLabelText("Provider API key")).toHaveValue("");
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "ai_credentials_unavailable",
  );
  await waitFor(() => expect(remove).toBeEnabled());
  fireEvent.click(remove);
  await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
  expect(manageAISettings).toHaveBeenLastCalledWith({
    operation: "disable",
    accountId: "fixture-account",
  });
  expect(remove).toBeEnabled();
});

it("removes a transient settings error after a successful status retry", async () => {
  vi.useFakeTimers();
  vi.mocked(manageAISettings).mockRejectedValueOnce(
    new Error("synthetic failure"),
  );
  render(<AISettings />);
  await act(async () => {});
  expect(screen.getByRole("alert")).toHaveTextContent(
    "Cannot read AI settings.",
  );
  await act(async () => {
    await vi.advanceTimersByTimeAsync(2000);
  });
  expect(screen.queryByRole("alert")).toBeNull();
  expect(screen.getByLabelText("AI provider")).toBeVisible();
});
