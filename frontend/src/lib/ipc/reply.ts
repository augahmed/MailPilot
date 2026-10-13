import { invoke, isTauri } from "@tauri-apps/api/core";
import { z } from "zod";
const text = (max: number) =>
  z
    .string()
    .max(max)
    .refine(
      (v) => new TextEncoder().encode(v).length <= max && !v.includes("\0"),
    );
export const replyRequestSchema = z
  .object({
    operation: z.enum([
      "status",
      "configure",
      "disable",
      "prepare",
      "generate",
      "preview_draft",
      "create",
      "reconcile",
      "resolve",
      "undo_preview",
      "cancel",
    ]),
    accountId: text(100),
    emailId: text(100),
    includeThread: z.boolean(),
    instruction: text(1000),
    tone: z.enum(["", "Professional", "Friendly", "Concise", "Formal"]),
    previewId: text(100),
    jobId: text(100),
    actionId: text(100),
    approved: z.boolean(),
    body: text(6000),
    model: text(128),
    apiKey: text(2048),
  })
  .strict()
  .refine((p) => {
    const selection =
      !p.emailId && !p.includeThread && !p.instruction && !p.tone;
    const control =
      !p.previewId && !p.jobId && !p.actionId && !p.approved && !p.body;
    const config = !p.model && !p.apiKey;
    if (p.operation === "status") return selection && control && config;
    if (!p.accountId) return false;
    switch (p.operation) {
      case "configure":
        return selection && control && !!p.model;
      case "disable":
        return selection && control && config;
      case "prepare":
        return !!p.emailId && !!p.tone && control && config;
      case "generate":
        return (
          selection &&
          config &&
          !!p.previewId &&
          p.approved &&
          !p.jobId &&
          !p.actionId &&
          !p.body
        );
      case "preview_draft":
        return (
          selection &&
          config &&
          !!p.previewId &&
          !p.approved &&
          !p.jobId &&
          !p.actionId &&
          !!p.body.trim()
        );
      case "create":
        return (
          selection &&
          config &&
          !!p.previewId &&
          !!p.actionId &&
          p.approved &&
          !p.jobId &&
          !p.body
        );
      case "reconcile":
      case "undo_preview":
        return (
          selection &&
          config &&
          !!p.actionId &&
          !p.previewId &&
          !p.jobId &&
          !p.approved &&
          !p.body
        );
      case "resolve":
        return (
          selection &&
          config &&
          !!p.actionId &&
          !p.previewId &&
          !p.jobId &&
          p.approved &&
          !p.body
        );
      case "cancel":
        return (
          selection &&
          config &&
          !!p.jobId &&
          !p.previewId &&
          !p.actionId &&
          !p.approved &&
          !p.body
        );
    }
  });
export const replyResponseSchema = z
  .object({
    accountId: text(100),
    state: z.enum([
      "idle",
      "configuring",
      "ai_preview",
      "generating",
      "editing",
      "draft_preview",
      "creating",
      "reconciling",
      "created",
      "uncertain",
      "conflict",
      "resolved",
      "error",
    ]),
    configured: z.boolean(),
    provider: text(64),
    model: text(128),
    external: z.boolean(),
    jobId: text(100),
    previewId: text(100),
    reviewId: text(100),
    expiresAt: z.number().int().nonnegative(),
    context: text(6000),
    instruction: text(1500),
    policy: text(1000),
    to: text(320),
    subject: text(512),
    body: text(6000),
    sourceCount: z.number().int().min(0).max(5),
    truncated: z.boolean(),
    actionId: text(100),
    draftId: text(1024),
    reconciled: z.boolean(),
    undoAvailable: z.literal(false),
    code: z.enum([
      "",
      "unauthorized",
      "validation",
      "busy",
      "cancelled",
      "not_found",
      "stale_preview",
      "context_unavailable",
      "ai_disabled",
      "ai_credentials_unavailable",
      "ai_authentication_failed",
      "ai_rate_limited",
      "ai_refused",
      "ai_incomplete",
      "operation_failed",
      "unsupported",
      "reauth_required",
      "response_too_large",
      "outcome_unknown",
      "journal_unavailable",
      "draft_changed",
      "reconcile_required",
      "manually_resolved",
      "undo_unavailable",
    ]),
  })
  .strict();
export type ReplyRequest = z.infer<typeof replyRequestSchema>;
export type ReplyResponse = z.infer<typeof replyResponseSchema>;
export async function manageReplies(
  request: Partial<ReplyRequest> = {},
): Promise<ReplyResponse> {
  if (!isTauri()) throw new Error("Desktop app required");
  const parameters = replyRequestSchema.parse({
    operation: "status",
    accountId: "",
    emailId: "",
    includeThread: false,
    instruction: "",
    tone: "",
    previewId: "",
    jobId: "",
    actionId: "",
    approved: false,
    body: "",
    model: "",
    apiKey: "",
    ...request,
  });
  try {
    return replyResponseSchema.parse(
      await invoke<unknown>("reply_manage", { request: parameters }),
    );
  } finally {
    parameters.apiKey = "";
  }
}
