import { invoke, isTauri } from "@tauri-apps/api/core";
import { z } from "zod";

const hash = z.string().regex(/^[0-9a-f]{64}$/);
const categoryOption = z
  .object({ id: z.string().min(1).max(100), name: z.string().max(101) })
  .strict();
const semanticSchema = z
  .object({
    state: z.enum(["suggested", "assigned", "unclassified"]),
    modelVersion: z.string().min(1).max(128),
    classifierVersion: z.string().min(1).max(100),
    sourceRevision: hash,
    minimumScore: z.number().gt(0).max(1),
    minimumMargin: z.number().min(0).max(1),
    assigned: z.array(categoryOption).max(1000),
    scores: z
      .array(
        z
          .object({
            categoryId: z.string().min(1).max(100),
            categoryName: z.string().max(101),
            score: z.number().min(-1).max(1),
            prototypeHash: hash,
            prototypeStatus: z.enum(["ready", "partial"]),
            chunkCount: z.number().int().min(2).max(4096),
            evidence: z
              .array(
                z
                  .object({
                    chunkId: z.string().min(1).max(128),
                    section: z.string().max(64),
                    text: z.string().max(160),
                    similarity: z.number().min(-1).max(1),
                    weight: z.number().gt(0).max(1),
                  })
                  .strict(),
              )
              .min(2)
              .max(4),
          })
          .strict(),
      )
      .max(100),
  })
  .strict();
export type SemanticReview = z.infer<typeof semanticSchema>;

const classificationAssessmentSchema = z
  .object({
    used_classifier: z.string().min(1).max(100),
    reason: z.string().min(1).max(160),
    // Retained empty field for the existing deterministic wire shape.
    candidates: z.array(z.never()).max(0),
  })
  .strict();

export const classificationPredictionSchema = z
  .object({
    email_id: z.string().min(1).max(100),
    category_id: z.string().min(1).max(100),
    category_name: z.string().refine((value) => [...value].length <= 101),
    confidence: z.number().gt(0).max(1),
    score_kind: z
      .enum(["rule_confidence", "cosine_similarity", "user_assignment"])
      .optional(),
    classifier_name: z.string().min(1).max(100),
    classifier_version: z.string().min(1).max(100),
    reasons: z.array(z.string().trim().min(1).max(160)).min(1).max(6),
    timestamp: z.number().int(),
  })
  .strict();
const classificationRowSchema = z
  .object({
    semantic: semanticSchema.optional(),
    readerRevision: hash.optional(),
    email_id: z.string().min(1).max(100),
    sender: z.string().refine((value) => [...value].length <= 101),
    subject: z.string().refine((value) => [...value].length <= 201),
    bodyState: z.enum(["", "complete", "unavailable"]),
    state: z.enum([
      "not_reviewed",
      "unclassified",
      "classified",
      "suggested",
      "assigned",
      "stale",
      "error",
    ]),
    predictions: z.array(classificationPredictionSchema).max(100),
    assessment: classificationAssessmentSchema.optional(),
    timestamp: z.number().int().nullable(),
    code: z
      .enum(["conflict", "cancelled", "invalid_message", "storage_error"])
      .optional(),
  })
  .strict()
  .superRefine((row, ctx) => {
    if (
      new Set(row.predictions.map((p) => p.category_id)).size !==
        row.predictions.length ||
      (row.predictions.length > 1 &&
        (row.state !== "suggested" ||
          row.assessment?.used_classifier !== "semantic" ||
          row.predictions.some((p) => p.score_kind !== "cosine_similarity")))
    )
      ctx.addIssue({ code: "custom", message: "Invalid multiple suggestions" });
    if (
      row.semantic &&
      row.state === "suggested" &&
      row.assessment?.used_classifier === "semantic"
    ) {
      const best = row.semantic.scores[0]?.score ?? -1;
      const expected = row.semantic.scores.filter(
        (s) =>
          s.score >= row.semantic!.minimumScore &&
          best - s.score <= row.semantic!.minimumMargin + 1e-12,
      );
      if (
        expected.length !== row.predictions.length ||
        expected.some(
          (s, i) =>
            s.categoryId !== row.predictions[i]?.category_id ||
            s.score !== row.predictions[i]?.confidence,
        )
      )
        ctx.addIssue({
          code: "custom",
          message: "Inconsistent close-category suggestions",
        });
    }
    if (
      row.assessment &&
      row.state !== "classified" &&
      row.state !== "unclassified" &&
      row.state !== "suggested" &&
      row.state !== "assigned"
    )
      ctx.addIssue({ code: "custom", message: "Assessment on unreviewed row" });
    if (
      (row.state === "classified" ||
        row.state === "suggested" ||
        (row.state === "assigned" && row.semantic?.assigned.length === 1)) !==
      row.predictions.length > 0
    )
      ctx.addIssue({
        code: "custom",
        message: "Inconsistent prediction state",
      });
    if (
      (row.state === "classified" ||
        row.state === "unclassified" ||
        row.state === "assigned" ||
        row.state === "suggested") !==
      (row.timestamp !== null)
    )
      ctx.addIssue({
        code: "custom",
        message: "Inconsistent review timestamp",
      });
    if ((row.state === "error") !== (row.code !== undefined))
      ctx.addIssue({ code: "custom", message: "Inconsistent review error" });
    if (
      row.semantic &&
      (row.semantic.state !== row.state || !row.readerRevision)
    )
      ctx.addIssue({ code: "custom", message: "Inconsistent semantic state" });
    if (
      (row.state === "suggested" || row.state === "assigned") &&
      !row.semantic
    )
      ctx.addIssue({ code: "custom", message: "Missing semantic evidence" });
    for (const prediction of row.predictions) {
      if (
        row.semantic &&
        (prediction.classifier_version !== row.semantic.classifierVersion ||
          (prediction.score_kind === "cosine_similarity" &&
            !row.semantic.scores.some(
              (s) =>
                s.categoryId === prediction.category_id &&
                s.score === prediction.confidence,
            )) ||
          (prediction.score_kind === "user_assignment" &&
            (row.semantic.assigned.length !== 1 ||
              row.semantic.assigned[0]?.id !== prediction.category_id)))
      )
        ctx.addIssue({
          code: "custom",
          message: "Inconsistent semantic score identity",
        });
      if (
        (prediction.score_kind === "cosine_similarity" ||
          prediction.score_kind === "user_assignment") &&
        !row.semantic
      )
        ctx.addIssue({
          code: "custom",
          message: "Missing versioned score evidence",
        });
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
    categoryOptions: z.array(categoryOption).max(100).optional(),
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
  operation: "list" | "run" | "assigned" | "unclassified" | "auto";
  emailId?: string;
  categoryId?: string;
  revision?: string;
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
