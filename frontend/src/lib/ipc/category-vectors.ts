import { invoke, isTauri } from "@tauri-apps/api/core";
import { z } from "zod";
const text = (max: number) =>
  z
    .string()
    .refine(
      (s) => new TextEncoder().encode(s).length <= max && !s.includes("\0"),
    );
const id = text(100).refine((s) => s.length > 0);
const hash = z.string().regex(/^[a-f0-9]{64}$/);
export const categoryVectorRequestSchema = z
  .object({
    operation: z.enum(["inspect", "confirm_example", "remove_example"]),
    accountId: id,
    categoryId: id,
    emailId: text(100),
    revision: z.string(),
    approved: z.boolean(),
  })
  .strict()
  .refine((r) =>
    r.operation === "confirm_example"
      ? r.emailId !== "" && hash.safeParse(r.revision).success && r.approved
      : r.revision === "" &&
        !r.approved &&
        (r.operation === "remove_example"
          ? r.emailId !== ""
          : r.emailId === ""),
  );
export type CategoryVectorRequest = z.infer<typeof categoryVectorRequestSchema>;
export const categoryVectorResponseSchema = z
  .object({
    accountId: text(100),
    categoryId: text(100),
    status: z.enum(["ready", "partial", "pending", "disabled", "unavailable"]),
    code: z.enum([
      "",
      "validation",
      "unauthorized",
      "unavailable",
      "not_found",
      "stale",
      "cancelled",
      "storage_error",
      "too_large",
    ]),
    sourceHash: z.string(),
    modelId: text(128),
    modelVersion: text(128),
    dimension: z.number().int().min(0).max(8192),
    recipe: text(128),
    definitionInputs: z.array(z.string()).max(20),
    examples: z
      .array(
        z
          .object({
            emailId: id,
            subject: text(120),
            revision: hash,
            status: z.enum(["included", "index_pending", "source_changed"]),
            chunks: z
              .array(
                z
                  .object({
                    chunkId: text(128).refine((s) => s.length > 0),
                    section: text(32),
                    text: text(160),
                    context: text(1024),
                    truncated: z.boolean(),
                  })
                  .strict(),
              )
              .max(3),
          })
          .strict()
          .refine((e) => e.status !== "included" || e.chunks.length > 0),
      )
      .max(10),
  })
  .strict()
  .refine((r) =>
    r.code
      ? r.status === "unavailable" &&
        !r.accountId &&
        !r.categoryId &&
        r.definitionInputs.length === 0 &&
        r.examples.length === 0
      : r.status !== "unavailable" &&
        !!r.accountId &&
        !!r.categoryId &&
        r.dimension > 0 &&
        !!r.modelId &&
        !!r.modelVersion &&
        r.recipe ===
          "category-prototype-v1-definition-and-examples-equal-unit-mean" &&
        r.definitionInputs.length > 0 &&
        new TextEncoder().encode(r.definitionInputs.join("")).length <= 16000 &&
        (["ready", "partial"].includes(r.status)
          ? hash.safeParse(r.sourceHash).success
          : r.sourceHash === ""),
  );
export type CategoryVectorResponse = z.infer<
  typeof categoryVectorResponseSchema
>;
export async function manageCategoryVectors(
  request: CategoryVectorRequest,
): Promise<CategoryVectorResponse> {
  if (!isTauri()) throw new Error("Desktop app required");
  const r = categoryVectorResponseSchema.parse(
    await invoke<unknown>("category_vectors", {
      request: categoryVectorRequestSchema.parse(request),
    }),
  );
  if (
    !r.code &&
    (r.accountId !== request.accountId || r.categoryId !== request.categoryId)
  )
    throw new Error("Invalid category account");
  return r;
}
