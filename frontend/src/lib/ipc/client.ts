import { invoke, isTauri } from "@tauri-apps/api/core";
import { z } from "zod";

export const healthSchema = z
  .object({
    name: z.literal("MailPilot"),
    version: z.string().min(1),
    protocolVersion: z.literal(1),
    backend: z.literal("connected"),
    database: z.enum(["connected", "disconnected"]),
    schemaVersion: z.number().int().nonnegative(),
  })
  .strict();

export type Health = z.infer<typeof healthSchema>;

export async function getHealth(): Promise<Health> {
  if (!isTauri())
    throw new Error("Open the desktop app to connect to the backend.");
  return healthSchema.parse(await invoke<unknown>("health"));
}

export const gmailStatusSchema = z
  .object({
    state: z.enum([
      "unconfigured",
      "disconnected",
      "checking",
      "connecting",
      "connected",
      "disconnecting",
      "error",
      "reauth_required",
    ]),
    email: z.string().max(320).optional(),
    code: z
      .enum([
        "oauth_not_configured",
        "authentication_failed",
        "credential_store_unavailable",
        "consent_denied",
        "required_scope_missing",
        "reauth_required",
        "authentication_timeout",
        "authentication_cancelled",
        "account_storage_failed",
        "account_cleanup_failed",
        "revocation_failed",
      ])
      .optional(),
  })
  .strict();
export type GmailStatus = z.infer<typeof gmailStatusSchema>;
export async function gmailCommand(
  command:
    "gmail_status" | "gmail_connect" | "gmail_check" | "gmail_disconnect",
): Promise<GmailStatus> {
  if (!isTauri()) throw new Error("Desktop app required");
  return gmailStatusSchema.parse(await invoke<unknown>(command));
}

export const syncSchema = z
  .object({
    mode: z.enum(["initial", "incremental", "recovery"]).optional(),
    removed: z.number().int().nonnegative().optional(),
    more: z.boolean().optional(),
    snapshotLimited: z.boolean().optional(),
    state: z.enum(["idle", "running", "completed", "cancelled", "error"]),
    accountId: z.string(),
    limit: z.number().int(),
    discovered: z.number().int().nonnegative(),
    imported: z.number().int().nonnegative(),
    updated: z.number().int().nonnegative(),
    errors: z.number().int().nonnegative(),
    capped: z.boolean(),
    code: z
      .enum([
        "reauth_required",
        "invalid_limit",
        "pagination_error",
        "storage_error",
        "body_unavailable",
        "chunking_failed",
        "provider_error",
        "rate_limited",
        "invalid_message",
      ])
      .optional(),
  })
  .strict();
export type SyncProgress = z.infer<typeof syncSchema>;
export const mailboxSchema = z
  .object({
    accountId: z.string(),
    rows: z
      .array(
        z
          .object({
            id: z.string(),
            sender: z.string(),
            subject: z.string(),
            receivedAt: z.number().int(),
            bodyState: z.enum(["", "complete", "unavailable"]),
            classification: z
              .object({
                state: z.enum([
                  "unclassified",
                  "suggested",
                  "assigned",
                  "multiple_suggestions",
                  "multiple_assignments",
                  "stale",
                ]),
                categoryId: z.string().max(100),
                categoryName: z.string().max(101),
              })
              .strict()
              .optional(),
            phishing: z
              .object({
                score: z.number().int().min(0).max(100),
                risk: z.enum(["LOW", "MEDIUM", "HIGH"]),
                version: z.string(),
                reasons: z
                  .array(
                    z
                      .object({
                        code: z.string(),
                        points: z.number().int().min(-20).max(100),
                        explanation: z.string(),
                      })
                      .strict(),
                  )
                  .max(12),
              })
              .strict()
              .optional(),
          })
          .strict(),
      )
      .max(10),
    hasMore: z.boolean(),
    code: z.literal("storage_error").optional(),
  })
  .strict();
export type MailboxPage = z.infer<typeof mailboxSchema>;
export async function syncCommand(
  start: boolean | "cancel" = false,
): Promise<SyncProgress> {
  if (!isTauri()) throw new Error("Desktop app required");
  return syncSchema.parse(
    await invoke<unknown>(
      start === "cancel" ? "sync_cancel" : start ? "sync_start" : "sync_status",
    ),
  );
}
export async function getMailbox(offset = 0): Promise<MailboxPage> {
  if (!isTauri()) throw new Error("Desktop app required");
  return mailboxSchema.parse(await invoke<unknown>("mailbox_list", { offset }));
}

export const categorySchema = z
  .object({
    id: z.string().min(1),
    name: z.string().min(1),
    description: z.string(),
    enabled: z.boolean(),
    created_at: z.number().int(),
    updated_at: z.number().int(),
  })
  .strict();
export type Category = z.infer<typeof categorySchema>;
export const categoryPageSchema = z
  .object({
    accountId: z.string(),
    rows: z.array(categorySchema).max(5),
    hasMore: z.boolean(),
    code: z
      .enum([
        "validation",
        "unauthorized",
        "conflict",
        "not_found",
        "storage_error",
      ])
      .optional(),
  })
  .strict();
export type CategoryPage = z.infer<typeof categoryPageSchema>;
export type CategoryRequest = {
  operation: "list" | "save" | "delete";
  accountId: string;
  offset: number;
  id: string;
  name: string;
  description: string;
  enabled: boolean | null;
};
export async function manageCategories(
  request: Partial<CategoryRequest> & Pick<CategoryRequest, "operation">,
): Promise<CategoryPage> {
  if (!isTauri()) throw new Error("Desktop app required");
  return categoryPageSchema.parse(
    await invoke<unknown>("categories_manage", {
      request: {
        accountId: "",
        offset: 0,
        id: "",
        name: "",
        description: "",
        enabled: null,
        ...request,
      },
    }),
  );
}

export {
  classificationPredictionSchema,
  classificationPageSchema,
  reviewClassifications,
  type ClassificationPage,
  type ClassificationPrediction,
} from "./classification";
export {
  organizationResponseSchema,
  organize,
  type OrganizationRequest,
  type OrganizationResponse,
} from "./organization";

export {
  manageReplies,
  replyRequestSchema,
  replyResponseSchema,
  type ReplyRequest,
  type ReplyResponse,
} from "./reply";
