import { invoke, isTauri } from "@tauri-apps/api/core";
import { z } from "zod";
import { emptyFilter } from "./search";

const text = (n: number) =>
  z
    .string()
    .refine(
      (s) => new TextEncoder().encode(s).length <= n && !s.includes("\0"),
    );
const hash = z.string().regex(/^[a-f0-9]{64}$/);
const sourceId = z.string().regex(/^S[1-8]$/);
const filter = z
  .object({
    sender: text(200),
    after: z.number().int().min(0).max(253402300799999),
    before: z.number().int().min(0).max(253402300799999),
    categoryId: text(100),
  })
  .strict()
  .refine((f) => !f.before || f.after < f.before);
export const answerRequestSchema = z
  .object({
    operation: z.enum([
      "retrieve",
      "select",
      "approve",
      "status",
      "cancel",
      "clear",
    ]),
    accountId: text(100),
    question: text(2048),
    filter,
    jobId: text(32),
    previewId: text(64),
    digest: text(64),
    sourceIds: z.array(sourceId).max(8),
    approved: z.boolean(),
  })
  .strict()
  .refine((r) => {
    const base =
      !r.question && JSON.stringify(r.filter) === JSON.stringify(emptyFilter);
    const controls =
      !r.jobId &&
      !r.previewId &&
      !r.digest &&
      !r.sourceIds.length &&
      !r.approved;
    if (r.operation === "retrieve")
      return !!r.accountId && controls && !!r.question.trim();
    if (r.operation === "status")
      return (
        base &&
        !r.previewId &&
        !r.digest &&
        !r.sourceIds.length &&
        !r.approved &&
        (!r.jobId || r.jobId.length === 32)
      );
    if (!r.accountId || !base) return false;
    if (r.operation === "clear") return controls;
    if (r.operation === "cancel")
      return (
        r.jobId.length === 32 &&
        !r.previewId &&
        !r.digest &&
        !r.sourceIds.length &&
        !r.approved
      );
    if (r.operation === "select")
      return (
        !r.jobId &&
        r.previewId.length === 64 &&
        !r.digest &&
        !r.approved &&
        r.sourceIds.length > 0 &&
        new Set(r.sourceIds).size === r.sourceIds.length
      );
    return (
      !r.jobId &&
      r.previewId.length === 64 &&
      r.digest.length === 64 &&
      !r.sourceIds.length &&
      r.approved
    );
  });
export type AnswerRequest = z.infer<typeof answerRequestSchema>;
const evidence = z
  .object({
    sourceId,
    emailId: text(100).refine(Boolean),
    chunkId: text(128).refine(Boolean),
    sourceRevision: hash,
    readerRevision: hash,
    subject: text(120),
    sender: text(120),
    receivedAt: z.number().int(),
    excerpt: text(600).refine((s) => !!s.trim()),
    partialContent: z.boolean(),
  })
  .strict();
const citation = z
  .object({ sourceId, quote: text(300).refine((s) => !!s.trim()) })
  .strict();
const result = z
  .object({
    outcome: z.enum(["answered", "insufficient", "conflicting"]),
    statements: z
      .array(
        z
          .object({
            text: text(800).refine((s) => !!s.trim()),
            citations: z.array(citation).min(1).max(3),
          })
          .strict(),
      )
      .max(6),
    limitations: z.array(text(300).refine((s) => !!s.trim())).max(6),
  })
  .strict();
export const answerResponseSchema = z
  .object({
    accountId: text(100),
    state: z.enum([
      "idle",
      "retrieving",
      "preview",
      "running",
      "completed",
      "error",
    ]),
    jobId: text(32),
    previewId: text(64),
    digest: text(64),
    expiresAt: z.number().int().nonnegative(),
    question: text(2048),
    provider: text(64),
    model: text(128),
    external: z.boolean(),
    policy: text(4096),
    payload: text(12288),
    evidence: z.array(evidence).max(8),
    selectedIds: z.array(sourceId).max(8),
    coverage: z
      .object({
        eligible: z.number().int().nonnegative(),
        semantic: z.number().int().nonnegative(),
        keyword: z.number().int().nonnegative(),
      })
      .strict(),
    mode: z.enum(["", "keyword", "hybrid"]),
    warning: z.enum(["", "semantic_unavailable"]),
    exhaustive: z.literal(false),
    aggregate: z.boolean(),
    result: result.nullable(),
    code: text(64),
  })
  .strict()
  .refine((r) => {
    if (
      r.coverage.semantic > r.coverage.eligible ||
      r.coverage.keyword > r.coverage.eligible ||
      new Set(r.evidence.map((e) => e.sourceId)).size !== r.evidence.length ||
      new Set(r.evidence.map((e) => e.chunkId)).size !== r.evidence.length ||
      new Set(r.selectedIds).size !== r.selectedIds.length ||
      r.selectedIds.some((id) => !r.evidence.some((e) => e.sourceId === id))
    )
      return false;
    if (
      r.state === "preview" &&
      (!r.accountId ||
        !hash.safeParse(r.previewId).success ||
        !hash.safeParse(r.digest).success ||
        !r.expiresAt ||
        !r.policy ||
        !r.payload ||
        !r.selectedIds.length ||
        r.result ||
        r.code)
    )
      return false;
    if (
      r.evidence.length &&
      (r.code ||
        !r.accountId ||
        !["preview", "running", "completed"].includes(r.state))
    )
      return false;
    if (r.result) {
      const a = r.result;
      if (
        r.state !== "completed" ||
        r.code ||
        (a.outcome !== "insufficient" && !a.statements.length) ||
        (a.outcome === "insufficient" && !a.limitations.length) ||
        (r.aggregate && a.outcome !== "insufficient")
      )
        return false;
      for (const statement of a.statements)
        for (const c of statement.citations) {
          if (
            !r.selectedIds.includes(c.sourceId) ||
            !r.evidence.some(
              (e) => e.sourceId === c.sourceId && e.excerpt.includes(c.quote),
            )
          )
            return false;
        }
    }
    return new TextEncoder().encode(JSON.stringify(r)).length <= 48 * 1024;
  });
export type AnswerResponse = z.infer<typeof answerResponseSchema>;
export type AnswerEvidence = AnswerResponse["evidence"][number];
export const answerRequest = (
  operation: AnswerRequest["operation"],
  accountId = "",
  patch: Partial<AnswerRequest> = {},
): AnswerRequest => ({
  operation,
  accountId,
  question: "",
  filter: emptyFilter,
  jobId: "",
  previewId: "",
  digest: "",
  sourceIds: [],
  approved: false,
  ...patch,
});
export async function answerEmail(
  request: AnswerRequest,
): Promise<AnswerResponse> {
  if (!isTauri()) throw new Error("Desktop app required");
  const input = answerRequestSchema.parse(request);
  const output = answerResponseSchema.parse(
    await invoke<unknown>("email_answer", { request: input }),
  );
  if (
    input.accountId &&
    output.accountId &&
    input.accountId !== output.accountId
  )
    throw new Error("Invalid answer scope");
  return output;
}
