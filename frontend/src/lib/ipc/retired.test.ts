import { beforeEach, expect, it, vi } from "vitest";
import { invoke } from "@tauri-apps/api/core";
import { askMail } from "./ask";
import { semanticSearch } from "./search";
import { manageReplies } from "./reply";

vi.mock("@tauri-apps/api/core", () => ({
  invoke: vi.fn(),
  isTauri: () => true,
}));
beforeEach(() => vi.clearAllMocks());

it("never invokes retired Ask, semantic search or vector indexing commands", async () => {
  await expect(
    askMail({ operation: "prepare", accountId: "a", question: "mailbox Q&A" }),
  ).rejects.toThrow("retired");
  for (const operation of ["status", "query", "index", "cancel"] as const)
    await expect(
      semanticSearch({ operation, accountId: "a", query: "private" }),
    ).rejects.toThrow("retired");
  expect(invoke).not.toHaveBeenCalled();
});

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
      manageReplies({ accountId: "a", ...request }),
    ).rejects.toThrow();
  expect(invoke).not.toHaveBeenCalled();
});
