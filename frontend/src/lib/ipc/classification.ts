import { invoke, isTauri } from "@tauri-apps/api/core";
import { z } from "zod";

const classificationAssessmentSchema = z
  .object({
    used_classifier: z.string().min(1).max(100),
    reason: z.string().min(1).max(160),
    candidates: z
      .array(
        z
          .object({
            category_id: z.string().min(1).max(100),
            category_name: z
              .string()
              .refine((value) => [...value].length <= 101),
            similarity: z.number().min(-1).max(1),
          })
          .strict(),
      )
      .max(5),
  })
  .strict()
  .superRefine((assessment, ctx) => {
    const seen = new Set<string>();
    assessment.candidates.forEach((candidate, i) => {
      const previous = assessment.candidates[i - 1];
      if (
        seen.has(candidate.category_id) ||
        (previous &&
          (previous.similarity < candidate.similarity ||
            (previous.similarity === candidate.similarity &&
              previous.category_id > candidate.category_id)))
      )
        ctx.addIssue({ code: "custom", message: "Invalid candidate ranking" });
      seen.add(candidate.category_id);
    });
  });

export const classificationPredictionSchema = z
  .object({
    email_id: z.string().min(1).max(100),
    category_id: z.string().min(1).max(100),
    category_name: z.string().refine((value) => [...value].length <= 101),
    confidence: z.number().gt(0).max(1),
    score_kind: z.enum(["rule_confidence", "cosine_similarity"]).optional(),
    classifier_name: z.string().min(1).max(100),
    classifier_version: z.string().min(1).max(100),
    reasons: z.array(z.string().trim().min(1).max(160)).min(1).max(6),
    timestamp: z.number().int(),
  })
  .strict();
const classificationRowSchema = z
  .object({
    email_id: z.string().min(1).max(100),
    sender: z.string().refine((value) => [...value].length <= 101),
    subject: z.string().refine((value) => [...value].length <= 201),
    bodyState: z.enum(["", "complete", "unavailable"]),
    state: z.enum([
      "not_reviewed",
      "unclassified",
      "classified",
      "stale",
      "error",
    ]),
    predictions: z.array(classificationPredictionSchema).max(1),
    assessment: classificationAssessmentSchema.optional(),
    timestamp: z.number().int().nullable(),
    code: z
      .enum([
        "conflict",
        "cancelled",
        "invalid_message",
        "storage_error",
        "embedding_unavailable",
      ])
      .optional(),
  })
  .strict()
  .superRefine((row, ctx) => {
    if (
      row.assessment &&
      row.state !== "classified" &&
      row.state !== "unclassified"
    )
      ctx.addIssue({ code: "custom", message: "Assessment on unreviewed row" });
    if ((row.state === "classified") !== row.predictions.length > 0)
      ctx.addIssue({
        code: "custom",
        message: "Inconsistent prediction state",
      });
    if (
      (row.state === "classified" || row.state === "unclassified") !==
      (row.timestamp !== null)
    )
      ctx.addIssue({
        code: "custom",
        message: "Inconsistent review timestamp",
      });
    if ((row.state === "error") !== (row.code !== undefined))
      ctx.addIssue({ code: "custom", message: "Inconsistent review error" });
    for (const prediction of row.predictions) {
      if (
        prediction.score_kind === "cosine_similarity" &&
        (!row.assessment?.candidates[0] ||
          row.assessment.candidates[0].category_id !== prediction.category_id ||
          row.assessment.candidates[0].similarity !== prediction.confidence)
      )
        ctx.addIssue({ code: "custom", message: "Invalid semantic winner" });
      if (
        prediction.email_id !== row.email_id ||
        prediction.timestamp !== row.timestamp
      )
        ctx.addIssue({
          code: "custom",
          message: "Inconsistent prediction identity",
        });
    }
  });
export const classificationPageSchema = z
  .object({
    accountId: z.string().max(100),
    rows: z.array(classificationRowSchema).max(5),
    hasMore: z.boolean(),
    processed: z.number().int().min(0).max(5),
    errors: z.number().int().min(0).max(5),
    code: z
      .enum([
        "validation",
        "unauthorized",
        "conflict",
        "cancelled",
        "invalid_message",
        "storage_error",
      ])
      .optional(),
  })
  .strict();
export type ClassificationPage = z.infer<typeof classificationPageSchema>;
export type ClassificationPrediction = z.infer<
  typeof classificationPredictionSchema
>;
export async function reviewClassifications(request: {
  operation: "list" | "run";
  accountId?: string;
  offset?: number;
}): Promise<ClassificationPage> {
  if (!isTauri()) throw new Error("Desktop app required");
  return classificationPageSchema.parse(
    await invoke<unknown>("classification_review", {
      request: { accountId: "", offset: 0, ...request },
    }),
  );
}
