import { invoke, isTauri } from "@tauri-apps/api/core";
import { z } from "zod";

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
            scoreKind: z
              .enum(["rule_confidence", "cosine_similarity", "user_assignment"])
              .optional(),
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
              "multiple_suggestions",
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
