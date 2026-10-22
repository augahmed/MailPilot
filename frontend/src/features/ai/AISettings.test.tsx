import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
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
