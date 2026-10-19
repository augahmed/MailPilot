import { expect, it } from "vitest";
import { replyRequestSchema, replyResponseSchema } from "./reply";
import fixture from "../../../../contracts/reply-drafting-v1.json";
it("rejects retired drafting contracts and secrets at the active boundary", () => {
  expect(replyRequestSchema.safeParse(fixture.request).success).toBe(false);
  const base = {
    operation: "status",
    accountId: "a",
    jobId: "",
    actionId: "",
    approved: false,
  };
  expect(replyRequestSchema.safeParse(base).success).toBe(true);
  for (const operation of [
    "generate",
    "create",
    "prepare",
    "configure",
    "disable",
    "chat",
    "classify",
    "summarize_thread",
  ])
    expect(replyRequestSchema.safeParse({ ...base, operation }).success).toBe(
      false,
    );
  expect(
    replyResponseSchema.safeParse({ ...fixture.response, apiKey: "synthetic" })
      .success,
  ).toBe(false);
});
