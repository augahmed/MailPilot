import { expect, it, vi } from "vitest";
import { invoke } from "@tauri-apps/api/core";
import {
  aiSettingsRequestSchema,
  aiSettingsSchema,
  summaryRequestSchema,
  summarySchema,
  summarizeEmail,
} from "./ai";
import settings from "../../../../contracts/ai-settings-v1.json";
import summary from "../../../../contracts/email-summary-v1.json";
vi.mock("@tauri-apps/api/core", () => ({
  invoke: vi.fn(),
  isTauri: () => true,
}));
it("validates versioned provider settings and normalized summary contracts", () => {
  expect(aiSettingsRequestSchema.safeParse(settings.request).success).toBe(
    true,
  );
  expect(aiSettingsSchema.safeParse(settings.response).success).toBe(true);
  expect(summaryRequestSchema.safeParse(summary.request).success).toBe(true);
  expect(summarySchema.safeParse(summary.response).success).toBe(true);
  expect(
    aiSettingsSchema.safeParse({
      ...settings.response,
      apiKey: "synthetic-key",
    }).success,
  ).toBe(false);
});
it("rejects prompts, thread selection, AI classification and arbitrary provider payloads before IPC", async () => {
  for (const patch of [
    { prompt: "arbitrary" },
    { instruction: "arbitrary" },
    { includeThread: true },
    { emailIds: ["one", "two"] },
    { tools: ["gmail"] },
    { operation: "chat" },
    { operation: "classify" },
    { operation: "summarize_thread" },
    { operation: "generate" },
    { operation: "create" },
  ]) {
    await expect(
      summarizeEmail({ ...summary.request, ...patch } as unknown as Parameters<
        typeof summarizeEmail
      >[0]),
    ).rejects.toThrow();
  }
  expect(invoke).not.toHaveBeenCalled();
});
