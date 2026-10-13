import { invoke, isTauri } from "@tauri-apps/api/core";
import { z } from "zod";
const text = (max: number) =>
  z.string().refine((v) => new TextEncoder().encode(v).length <= max);
const address = z.object({ name: text(256), address: text(512) }).strict();
const count = z.number().int().min(0).max(1048576);
export const readerSchema = z
  .object({
    accountId: text(100),
    emailId: text(100),
    revision: z.string(),
    offset: count,
    nextOffset: count,
    totalBytes: count,
    body: text(16384),
    code: z.enum([
      "",
      "unauthorized",
      "validation",
      "not_found",
      "storage_error",
      "stale",
      "cancelled",
      "too_large",
    ]),
    message: z
      .object({
        subject: text(2048),
        sender: address,
        recipients: z.array(address).max(100),
        cc: z.array(address).max(100),
        receivedAt: z.number().int(),
        cachedAt: z.number().int(),
        attachments: z
          .array(
            z
              .object({
                filename: text(512),
                mimeType: text(128),
                sizeBytes: z.number().nonnegative().nullable(),
                inline: z.boolean(),
              })
              .strict(),
          )
          .max(200),
        labels: z
          .array(z.object({ name: text(256), kind: z.string() }).strict())
          .max(100),
        categories: z
          .array(
            z
              .object({
                name: text(100),
                source: z.enum(["assigned", "suggested"]),
              })
              .strict(),
          )
          .max(101),
        categoryState: z.enum([
          "not_reviewed",
          "unavailable",
          "stale",
          "unclassified",
          "classified",
        ]),
        phishing: z
          .object({
            score: z.number().int().min(0).max(100),
            risk: z.string(),
            version: z.string(),
            reasons: z.array(
              z
                .object({
                  code: z.string(),
                  points: z.number().int(),
                  explanation: z.string(),
                })
                .strict(),
            ),
          })
          .strict()
          .nullable(),
        bodyState: z.enum(["", "complete", "unavailable"]),
        bodyFormat: z.enum(["plain_text", "html_text"]),
        bodyError: z.boolean(),
        metadataTruncated: z.boolean(),
      })
      .strict()
      .nullable(),
  })
  .strict()
  .refine((v) =>
    v.code
      ? v.message === null && v.body === ""
      : v.message !== null &&
        /^[a-f0-9]{64}$/.test(v.revision) &&
        v.nextOffset === v.offset + new TextEncoder().encode(v.body).length &&
        v.nextOffset <= v.totalBytes &&
        (v.nextOffset === v.totalBytes || v.nextOffset > v.offset),
  );
export type ReadMailResponse = z.infer<typeof readerSchema>;
export type OpenMail = (
  accountId: string,
  emailId: string,
  trigger: HTMLButtonElement,
) => void;
export async function readCachedMail(
  accountId: string,
  emailId: string,
  offset = 0,
  revision = "",
): Promise<ReadMailResponse> {
  if (!isTauri()) throw new Error("Desktop app required");
  const result = readerSchema.safeParse(
    await invoke<unknown>("mailbox_read", {
      request: { accountId, emailId, offset, revision },
    }),
  );
  if (!result.success) throw new Error("Invalid cached message response");
  const r = result.data;
  if (
    !r.code &&
    (r.accountId !== accountId ||
      r.emailId !== emailId ||
      r.offset !== offset ||
      (revision && r.revision !== revision))
  )
    throw new Error("Cached message changed");
  return r;
}
