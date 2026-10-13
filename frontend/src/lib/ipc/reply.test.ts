import { expect, it } from "vitest";
import { replyRequestSchema, replyResponseSchema } from "./reply";
import fixture from "../../../../contracts/reply-drafting-v1.json";
it("validates shared reply contract and rejects secrets in responses or unapproved writes", () => {
  expect(replyRequestSchema.safeParse(fixture.request).success).toBe(true);
  expect(replyResponseSchema.safeParse(fixture.response).success).toBe(true);
  for (const change of [
    { operation: "send" },
    { tone: "Rude" },
    { accountId: "" },
    { apiKey: "synthetic-secret" },
    { body: "unexpected body" },
  ])
    expect(
      replyRequestSchema.safeParse({ ...fixture.request, ...change }).success,
    ).toBe(false);
  expect(
    replyResponseSchema.safeParse({ ...fixture.response, apiKey: "synthetic" })
      .success,
  ).toBe(false);
  expect(
    replyResponseSchema.safeParse({ ...fixture.response, undoAvailable: true })
      .success,
  ).toBe(false);
  const create = {
    ...fixture.request,
    operation: "create",
    emailId: "",
    tone: "",
    actionId: "a",
    previewId: "digest",
    approved: true,
  };
  expect(replyRequestSchema.safeParse(create).success).toBe(true);
  expect(
    replyRequestSchema.safeParse({ ...create, approved: false }).success,
  ).toBe(false);
  expect(
    replyRequestSchema.safeParse({ ...create, body: "changed after approval" })
      .success,
  ).toBe(false);
});
