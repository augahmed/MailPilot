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
            phishing: z
              .object({
                score: z.number().int().min(31).max(100),
                risk: z.literal("HIGH"),
                version: z.string().min(1).max(200),
                threshold: z.number().int().min(31).max(100),
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
                  .min(1)
                  .max(12),
              })
              .strict()
              .refine((p) => p.score >= p.threshold)
              .optional(),
            change: z.enum([
              "candidate",
              "unclassified",
              "below_threshold",
              "risk_unavailable",
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
            name: z
              .string()
              .max(110)
              .refine(
                (name) =>
                  name.startsWith("MailPilot/") ||
                  name === "Potential Phishing",
              ),
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
  source?: "phishing";
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
  request: Partial<z.infer<typeof semanticSearchRequestSchema>> = {},
): Promise<SemanticSearchResponse> {
  if (!isTauri()) throw new Error("Desktop app required");
  const parameters = semanticSearchRequestSchema.parse({
    operation: "status",
    accountId: "",
    jobId: "",
    query: "",
    topK: 0,
    sender: "",
    categoryId: "",
    from: null,
    until: null,
    ...request,
  });
  return semanticSearchResponseSchema.parse(
    await invoke<unknown>("semantic_search", { request: parameters }),
  );
}

export {
  manageReplies,
  replyRequestSchema,
  replyResponseSchema,
  type ReplyRequest,
  type ReplyResponse,
} from "./reply";
