import { beforeEach, expect, it, vi } from "vitest";
import { invoke } from "@tauri-apps/api/core";
import type { ReplyRequest } from "./reply";
import { manageReplies } from "./reply";

vi.mock("@tauri-apps/api/core", () => ({
  invoke: vi.fn(),
  isTauri: () => true,
}));
beforeEach(() => vi.clearAllMocks());

it("blocks even explicitly approved reply generation and draft creation before IPC", async () => {
  for (const request of [
    {
      operation: "prepare" as const,
      emailId: "e",
      tone: "Professional" as const,
      includeThread: true,
      instruction: "arbitrary",
    },
    { operation: "generate" as const, previewId: "p", approved: true },
    {
      operation: "preview_draft" as const,
      previewId: "p",
      body: "reviewed body",
    },
    {
      operation: "create" as const,
      previewId: "p",
      actionId: "a",
      approved: true,
    },
  ])
    await expect(
      manageReplies({
        accountId: "a",
        ...request,
      } as unknown as Partial<ReplyRequest>),
    ).rejects.toThrow();
  expect(invoke).not.toHaveBeenCalled();
});
