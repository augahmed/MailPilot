import { z } from "zod";

export const semanticSearchRequestSchema = z
  .object({
    operation: z.enum(["status", "index", "query", "cancel"]),
    accountId: z.string().max(100),
    jobId: z.string().max(100),
    query: z
      .string()
      .refine((value) => new TextEncoder().encode(value).length <= 1000),
    topK: z.number().int().min(0).max(20),
    sender: z
      .string()
      .refine((value) => new TextEncoder().encode(value).length <= 256),
    categoryId: z.string().max(100),
    from: z.number().int().min(0).max(253402300800000).nullable(),
    until: z.number().int().min(0).max(253402300800000).nullable(),
  })
  .strict()
  .superRefine((request, ctx) => {
    const empty =
      !request.query &&
      request.topK === 0 &&
      !request.sender &&
      !request.categoryId &&
      request.from === null &&
      request.until === null;
    if (
      (request.from !== null &&
        request.until !== null &&
        request.from >= request.until) ||
      (request.operation === "query"
        ? !request.accountId ||
          !!request.jobId ||
          !request.query.trim() ||
          request.topK < 1
        : !empty ||
          (request.operation !== "status" && !request.accountId) ||
          (request.operation === "cancel" && !request.jobId) ||
          (request.operation === "index" && !!request.jobId))
    )
      ctx.addIssue({
        code: "custom",
        message: "Invalid semantic search request",
      });
  });
const semanticSearchHitSchema = z
  .object({
    emailId: z.string().min(1).max(100),
    sender: z.string().refine((value) => [...value].length <= 81),
    subject: z.string().refine((value) => [...value].length <= 121),
    receivedAt: z.number().int(),
    similarity: z.number().min(-1).max(1),
    excerpt: z.string().refine((value) => [...value].length <= 241),
    passageKind: z.enum(["subject", "body"]).optional(),
    passageIndex: z.number().int().min(0).max(8191).optional(),
  })
  .strict();
export const semanticSearchResponseSchema = z
  .object({
    accountId: z.string().max(100),
    jobId: z.string().max(100),
    operation: z.enum(["", "index", "query"]),
    state: z.enum([
      "idle",
      "indexing",
      "searching",
      "completed",
      "cancelled",
      "error",
    ]),
    counts: z
      .object({
        cached: z.number().int().nonnegative(),
        eligible: z.number().int().nonnegative(),
        indexed: z.number().int().nonnegative(),
      })
      .strict(),
    processed: z.number().int().min(0).max(10000),
    errors: z.number().int().min(0).max(10000),
    scanned: z.number().int().min(0).max(10000),
    partial: z.boolean(),
    capped: z.boolean(),
    excerptFallback: z.boolean(),
    rows: z.array(semanticSearchHitSchema).max(20),
    code: z
      .enum([
        "unauthorized",
        "validation",
        "conflict",
        "cancelled",
        "embedding_unavailable",
        "storage_error",
        "busy",
        "response_too_large",
      ])
      .optional(),
  })
  .strict()
  .superRefine((response, ctx) => {
    if (
      response.counts.indexed > response.counts.eligible ||
      response.counts.eligible > response.counts.cached ||
      (response.rows.length > 0 &&
        (response.state !== "completed" || response.operation !== "query")) ||
      (response.code === "unauthorized" &&
        (!!response.accountId || response.rows.length > 0)) ||
      (response.state !== "idle" &&
        (!response.jobId || !response.accountId || !response.operation)) ||
      (response.capped && !response.partial) ||
      (response.errors > 0 && !response.partial)
    )
      ctx.addIssue({ code: "custom", message: "Inconsistent search response" });
    const seen = new Set<string>();
    response.rows.forEach((row, i) => {
      const previous = response.rows[i - 1];
      if (
        seen.has(row.emailId) ||
        (previous && previous.similarity < row.similarity)
      )
        ctx.addIssue({ code: "custom", message: "Invalid search ranking" });
      seen.add(row.emailId);
    });
  });
export type SemanticSearchResponse = z.infer<
  typeof semanticSearchResponseSchema
>;
export async function semanticSearch(
  ..._request: [Partial<z.infer<typeof semanticSearchRequestSchema>>?]
): Promise<SemanticSearchResponse> {
  void _request;
  throw new Error("Semantic search is retired.");
}
