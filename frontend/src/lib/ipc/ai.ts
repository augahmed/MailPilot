import { invoke, isTauri } from "@tauri-apps/api/core";
import { z } from "zod";
const text = (max: number) =>
  z
    .string()
    .refine(
      (v) => new TextEncoder().encode(v).length <= max && !v.includes("\0"),
    );
const code = text(64);
export const aiSettingsRequestSchema = z
  .object({
    operation: z.enum(["status", "configure", "disable"]),
    accountId: text(100),
    provider: text(64),
    model: text(128),
    apiKey: text(2048),
  })
  .strict()
  .refine((p) => {
    if (p.operation === "status") return !p.provider && !p.model && !p.apiKey;
    if (!p.accountId) return false;
    return p.operation === "configure"
      ? !!p.provider && !!p.model
      : !p.provider && !p.model && !p.apiKey;
  });
export const aiSettingsSchema = z
  .object({
    accountId: text(100),
    provider: text(64),
    model: text(128),
    enabled: z.boolean(),
    providers: z
      .array(z.object({ id: text(64), external: z.boolean() }).strict())
      .max(20),
    code,
  })
  .strict();
export type AISettings = z.infer<typeof aiSettingsSchema>;
export async function manageAISettings(
  request: Partial<z.infer<typeof aiSettingsRequestSchema>> = {},
): Promise<AISettings> {
  if (!isTauri()) throw new Error("Desktop app required");
  const parameters = aiSettingsRequestSchema.parse({
    operation: "status",
    accountId: "",
    provider: "",
    model: "",
    apiKey: "",
    ...request,
  });
  try {
    return aiSettingsSchema.parse(
      await invoke<unknown>("ai_settings", { request: parameters }),
    );
  } finally {
    parameters.apiKey = "";
  }
}
export const summaryRequestSchema = z
  .object({
    operation: z.enum(["preview", "approve", "status", "clear", "cancel"]),
    accountId: text(100).refine(Boolean),
    emailId: text(100).refine(Boolean),
    previewId: text(100),
    jobId: text(100),
    approved: z.boolean(),
  })
  .strict()
  .refine((p) => {
    if (p.operation === "approve")
      return !!p.previewId && !p.jobId && p.approved;
    if (p.operation === "cancel")
      return !p.previewId && !!p.jobId && !p.approved;
    return !p.previewId && !p.jobId && !p.approved;
  });
export const summarySchema = z
  .object({
    accountId: text(100),
    emailId: text(100),
    state: z.enum(["idle", "preview", "running", "completed", "error"]),
    jobId: text(100),
    previewId: text(100),
    expiresAt: z.number().int().nonnegative(),
    provider: text(64),
    model: text(128),
    external: z.boolean(),
    email: text(6000),
    policy: text(2000),
    truncated: z.boolean(),
    result: z
      .object({
        summary: text(4000),
        requests: z.array(text(500)).max(10),
        deadlines: z.array(text(500)).max(10),
      })
      .strict()
      .nullable(),
    code,
  })
  .strict();
export type EmailSummary = z.infer<typeof summarySchema>;
export async function summarizeEmail(
  request: Pick<
    z.infer<typeof summaryRequestSchema>,
    "operation" | "accountId" | "emailId"
  > &
    Partial<z.infer<typeof summaryRequestSchema>>,
): Promise<EmailSummary> {
  if (!isTauri()) throw new Error("Desktop app required");
  const parameters = summaryRequestSchema.parse({
    previewId: "",
    jobId: "",
    approved: false,
    ...request,
  });
  return summarySchema.parse(
    await invoke<unknown>("email_summarize", { request: parameters }),
  );
}
