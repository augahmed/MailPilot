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
          })
          .strict(),
      )
      .max(10),
    hasMore: z.boolean(),
    code: z.literal("storage_error").optional(),
  })
  .strict();
export type MailboxPage = z.infer<typeof mailboxSchema>;
export async function syncCommand(start = false): Promise<SyncProgress> {
  if (!isTauri()) throw new Error("Desktop app required");
  return syncSchema.parse(
    await invoke<unknown>(start ? "sync_start" : "sync_status"),
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

export const classificationPredictionSchema = z
  .object({
    email_id: z.string().min(1).max(100),
    category_id: z.string().min(1).max(100),
    category_name: z.string().refine((value) => [...value].length <= 101),
    confidence: z.number().gt(0).max(1),
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
    timestamp: z.number().int().nullable(),
    code: z
      .enum(["conflict", "cancelled", "invalid_message", "storage_error"])
      .optional(),
  })
  .strict()
  .superRefine((row, ctx) => {
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
    for (const prediction of row.predictions)
      if (
        prediction.email_id !== row.email_id ||
        prediction.timestamp !== row.timestamp
      )
        ctx.addIssue({
          code: "custom",
          message: "Inconsistent prediction identity",
        });
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

export const organizationResponseSchema = z
  .object({
    accountId: z.string().max(100),
    state: z.enum([
      "idle",
      "running",
      "error",
      "dry_run",
      "preview",
      "applying",
      "applied",
      "partial",
      "failed",
      "cancelled",
      "uncertain",
      "expired",
    ]),
    code: z
      .enum([
        "",
        "validation",
        "unauthorized",
        "busy",
        "stale_preview",
        "not_found",
        "cancelled",
        "reauth_required",
        "unsupported",
        "operation_failed",
        "reconciliation_required",
      ])
      .default(""),
    id: z.string().max(100),
    digest: z.string().max(64),
    kind: z.enum(["", "label_delta", "undo"]),
    expiresAt: z.number().int(),
    dryRun: z.boolean(),
    hasMore: z.boolean(),
    rows: z
      .array(
        z
          .object({
            emailId: z.string().min(1).max(100),
            sender: z.string(),
            subject: z.string(),
            currentLabels: z.array(z.string()).max(50),
            proposedLabel: z.string().max(110),
            confidence: z.number().min(0).max(1),
            change: z.enum([
              "candidate",
              "unclassified",
              "add",
              "remove",
              "none",
            ]),
          })
          .strict(),
      )
      .max(5),
    steps: z
      .array(
        z
          .object({
            kind: z.enum(["create_label", "add", "remove"]),
            name: z.string().startsWith("MailPilot/").max(110),
            emailId: z.string().max(100),
            state: z.enum([
              "pending",
              "in_flight",
              "applied",
              "failed",
              "uncertain",
              "conflict",
              "skipped",
            ]),
            code: z.string().max(64),
          })
          .strict(),
      )
      .max(10),
  })
  .strict();
export type OrganizationResponse = z.infer<typeof organizationResponseSchema>;
export type OrganizationRequest = {
  operation:
    | "list"
    | "status"
    | "preview"
    | "dry_run"
    | "apply"
    | "undo_preview"
    | "reconcile"
    | "cancel";
  accountId?: string;
  offset?: number;
  emailIds?: string[];
  actionId?: string;
  digest?: string;
};
export async function organize(
  request: OrganizationRequest,
): Promise<OrganizationResponse> {
  if (!isTauri()) throw new Error("Desktop app required");
  return organizationResponseSchema.parse(
    await invoke<unknown>("organization_manage", {
      request: {
        accountId: "",
        offset: 0,
        emailIds: [],
        actionId: "",
        digest: "",
        ...request,
      },
    }),
  );
}
