import { expect, it } from "vitest";
import { askRequestSchema, askResponseSchema } from "./ask";
import fixture from "../../../../contracts/ask-v1.json";
it("accepts the shared contract and rejects unapproved generation, tools and invented citations", () => {
  expect(askRequestSchema.safeParse(fixture.request).success).toBe(true);
  expect(askResponseSchema.safeParse(fixture.response).success).toBe(true);
  const generate = {
    ...fixture.request,
    operation: "generate",
    question: "",
    previewId: "p",
    approved: true,
  };
  expect(askRequestSchema.safeParse(generate).success).toBe(true);
  for (const change of [
    { approved: false },
    { question: "changed" },
    { accountId: "" },
    { tools: ["delete"] },
    { operation: "send" },
  ])
    expect(askRequestSchema.safeParse({ ...generate, ...change }).success).toBe(
      false,
    );
  expect(
    askResponseSchema.safeParse({
      ...fixture.response,
      answer: {
        insufficient: false,
        claims: [{ text: "claim", source: "S99", quote: "two interviews" }],
      },
    }).success,
  ).toBe(false);
  expect(
    askResponseSchema.safeParse({ ...fixture.response, apiKey: "secret" })
      .success,
  ).toBe(false);
});
