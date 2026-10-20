import { invoke, isTauri } from "@tauri-apps/api/core";
import { z } from "zod";

export const indexRequestSchema = z
  .object({
    operation: z.enum([
      "status",
      "start",
      "pause",
      "resume",
      "cancel",
      "rebuild",
    ]),
    accountId: z.string().max(100),
  })
  .strict()
  .refine((r) => r.operation === "status" || r.accountId.length > 0);
export type IndexRequest = z.infer<typeof indexRequestSchema>;
const count = z.number().int().nonnegative().max(Number.MAX_SAFE_INTEGER);
export const indexResponseSchema = z
  .object({
    accountId: z.string().max(100),
    status: z.enum([
      "idle",
      "pending",
      "running",
      "paused",
      "cancelled",
      "interrupted",
      "complete",
      "partial",
      "failed",
    ]),
    generation: z.string().max(128),
    modelId: z.string().max(128),
    modelVersion: z.string().max(128),
    fingerprint: z.string().max(64),
    dimension: z.number().int().min(0).max(8192),
    eligibleMessages: count,
    indexedMessages: count,
    completeMessages: count,
    partialMessages: count,
    failedMessages: count,
    pendingMessages: count,
    unavailableMessages: count,
    chunksCreated: count,
    embeddingsGenerated: count,
    code: z.enum([
      "",
      "unauthorized",
      "validation",
      "unavailable",
      "busy",
      "storage_error",
      "interrupted",
      "cache_changed",
      "model_changed",
      "model_unavailable",
      "inference_failed",
      "pending_changes",
    ]),
  })
  .strict()
  .refine(
    (r) =>
      r.completeMessages +
        r.partialMessages +
        r.failedMessages +
        r.pendingMessages +
        r.unavailableMessages ===
        r.eligibleMessages &&
      r.indexedMessages <= r.completeMessages + r.partialMessages &&
      r.embeddingsGenerated <= r.chunksCreated &&
      (r.accountId === "" ||
        (r.dimension > 0 && /^[0-9a-f]{64}$/.test(r.fingerprint))),
  );
export type IndexResponse = z.infer<typeof indexResponseSchema>;
export async function manageIndex(
  operation: IndexRequest["operation"],
  accountId = "",
): Promise<IndexResponse> {
  if (!isTauri()) throw new Error("Desktop app required");
  const request = indexRequestSchema.parse({ operation, accountId });
  const result = indexResponseSchema.parse(
    await invoke<unknown>("local_index", { request }),
  );
  if (accountId && result.accountId && result.accountId !== accountId)
    throw new Error("Invalid index account");
  return result;
}
