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
    operation: z.enum(["status", "reconcile", "resolve", "cancel"]),
    accountId: text(100),
    jobId: text(100),
    actionId: text(100),
    approved: z.boolean(),
  })
  .strict()
  .refine((p) => {
    if (p.operation === "status") return !p.jobId && !p.actionId && !p.approved;
    if (!p.accountId) return false;
    if (p.operation === "cancel")
      return !!p.jobId && !p.actionId && !p.approved;
    return (
      !p.jobId &&
      !!p.actionId &&
      (p.operation === "resolve" ? p.approved : !p.approved)
    );
  });
export const replyResponseSchema = z
  .object({
    accountId: text(100),
    state: z.enum([
      "idle",
      "draft_preview",
      "reconciling",
      "created",
      "uncertain",
      "conflict",
      "resolved",
      "error",
    ]),
    jobId: text(100),
    to: text(320),
    subject: text(512),
    actionId: text(100),
    draftId: text(1024),
    reconciled: z.boolean(),
    code: text(64),
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
    jobId: "",
    actionId: "",
    approved: false,
    ...request,
  });
  return replyResponseSchema.parse(
    await invoke<unknown>("reply_manage", { request: parameters }),
  );
}
