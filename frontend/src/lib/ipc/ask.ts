import { z } from "zod";
const text = (max: number) =>
  z
    .string()
    .refine(
      (v) => new TextEncoder().encode(v).length <= max && !v.includes("\0"),
    );
const id = text(100).refine((v) => !/[\r\n]/.test(v));
const date = z.number().int().min(0).max(253402300800000).nullable();
export const askRequestSchema = z
  .object({
    operation: z.enum(["status", "prepare", "generate", "cancel", "clear"]),
    accountId: id,
    question: text(1000),
    sender: text(256),
    from: date,
    until: date,
    previewId: id,
    jobId: id,
    approved: z.boolean(),
  })
  .strict()
  .refine((p) => {
    const selection =
      !p.question && !p.sender && p.from === null && p.until === null;
    const control = !p.previewId && !p.jobId && !p.approved;
    if (p.from !== null && p.until !== null && p.from >= p.until) return false;
    if (p.operation === "status") return selection && control;
    if (!p.accountId) return false;
    if (p.operation === "prepare") return !!p.question.trim() && control;
    if (p.operation === "generate")
      return selection && !!p.previewId && !p.jobId && p.approved;
    if (p.operation === "clear") return selection && control;
    return selection && !!p.jobId && !p.previewId && !p.approved;
  });
const source = z
  .object({
    ref: text(3),
    emailId: id,
    subject: text(256),
    sender: text(320),
    receivedAt: z.number().int(),
    evidence: text(1400),
  })
  .strict();
const claim = z
  .object({
    text: text(1000),
    source: text(3),
    quote: text(700).refine((v) => new TextEncoder().encode(v).length >= 8),
  })
  .strict();
export const askResponseSchema = z
  .object({
    accountId: id,
    state: z.enum([
      "idle",
      "retrieving",
      "preview",
      "generating",
      "answered",
      "error",
    ]),
    jobId: id,
    previewId: id,
    expiresAt: z.number().int().min(0),
    provider: text(64),
    model: text(128),
    external: z.boolean(),
    context: text(8000),
    instruction: text(1000),
    policy: text(2000),
    version: z.literal("ask-context-v1"),
    partial: z.boolean(),
    truncated: z.boolean(),
    sources: z.array(source).max(5),
    answer: z
      .object({ insufficient: z.boolean(), claims: z.array(claim).max(6) })
      .strict(),
    code: text(64),
  })
  .strict()
  .refine(
    (r) =>
      (!r.answer.insufficient || r.answer.claims.length === 0) &&
      r.answer.claims.every((c) =>
        r.sources.some(
          (s) => s.ref === c.source && s.evidence.includes(c.quote),
        ),
      ),
  );
export type AskRequest = z.infer<typeof askRequestSchema>;
export type AskResponse = z.infer<typeof askResponseSchema>;
export async function askMail(
  ..._request: [Partial<AskRequest>?]
): Promise<AskResponse> {
  void _request;
  throw new Error("Ask MailPilot is retired.");
}
