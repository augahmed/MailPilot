import { invoke, isTauri } from "@tauri-apps/api/core";
import { z } from "zod";
const bytes = (max: number) =>
  z
    .string()
    .refine(
      (s) => new TextEncoder().encode(s).length <= max && !s.includes("\0"),
    );
export const emptyFilter = { sender: "", after: 0, before: 0, categoryId: "" };
const filter = z
  .object({
    sender: bytes(200),
    after: z.number().int().min(0).max(253402300799999),
    before: z.number().int().min(0).max(253402300799999),
    categoryId: bytes(100),
  })
  .strict()
  .refine((f) => f.before === 0 || f.after < f.before);
export const searchRequestSchema = z
  .object({
    operation: z.enum(["start", "status", "cancel"]),
    accountId: bytes(100),
    jobId: z.string().max(32),
    query: bytes(2048),
    limit: z.number().int().min(0).max(20),
    filter,
  })
  .strict()
  .refine((r) =>
    r.operation === "start"
      ? r.accountId !== "" &&
        r.jobId === "" &&
        r.query.trim() !== "" &&
        r.limit >= 1
      : r.query === "" &&
        r.limit === 0 &&
        JSON.stringify(r.filter) === JSON.stringify(emptyFilter) &&
        (r.jobId === "" || r.jobId.length === 32) &&
        (r.operation === "status" || (r.accountId !== "" && r.jobId !== "")),
  );
export type SearchRequest = z.infer<typeof searchRequestSchema>;
const evidence = z
  .object({
    chunkId: bytes(128).refine(Boolean),
    score: z.number().min(-1).max(1),
    excerpt: bytes(600).refine(Boolean),
    sourceRevision: z.string().regex(/^[a-f0-9]{64}$/),
  })
  .strict();
export const searchResponseSchema = z
  .object({
    accountId: bytes(100),
    jobId: z.string().refine((s) => s === "" || s.length === 32),
    status: z.enum(["idle", "running", "complete", "cancelled", "error"]),
    code: z.enum([
      "",
      "unauthorized",
      "validation",
      "unavailable",
      "busy",
      "storage_error",
      "model_unavailable",
      "inference_failed",
      "stale",
      "cancelled",
      "timeout",
      "too_large",
    ]),
    emails: z
      .array(
        z
          .object({
            readerRevision: z.string().regex(/^[a-f0-9]{64}$/),
            emailId: bytes(100).refine(Boolean),
            subject: bytes(240),
            sender: bytes(240),
            receivedAt: z.number().int(),
            evidence: z.array(evidence).min(1).max(3),
          })
          .strict(),
      )
      .max(20),
    scannedChunks: z.number().int().nonnegative(),
    elapsedMs: z.number().nonnegative(),
    searchableMessages: z.number().int().nonnegative(),
    eligibleMessages: z.number().int().nonnegative(),
  })
  .strict()
  .refine(
    (r) =>
      r.searchableMessages <= r.eligibleMessages &&
      (r.emails.length === 0 ||
        (r.status === "complete" && r.code === "" && r.accountId !== "")),
  );
export type SearchResponse = z.infer<typeof searchResponseSchema>;
export const searchControl = (
  operation: "status" | "cancel",
  accountId = "",
  jobId = "",
): SearchRequest => ({
  operation,
  accountId,
  jobId,
  query: "",
  limit: 0,
  filter: emptyFilter,
});
export async function vectorSearch(
  request: SearchRequest,
): Promise<SearchResponse> {
  if (!isTauri()) throw new Error("Desktop app required");
  const r = searchRequestSchema.parse(request);
  const result = searchResponseSchema.parse(
    await invoke<unknown>("vector_search", { request: r }),
  );
  if (
    (r.accountId && result.accountId && r.accountId !== result.accountId) ||
    (r.jobId && result.jobId && r.jobId !== result.jobId)
  )
    throw new Error("Invalid search scope");
  return result;
}
