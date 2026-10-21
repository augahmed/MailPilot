import { expect, it, vi } from "vitest";
import { invoke } from "@tauri-apps/api/core";
import fixture from "../../../../contracts/email-answer-v1.json";
import {
  answerEmail,
  answerRequestSchema,
  answerResponseSchema,
} from "./answers";
vi.mock("@tauri-apps/api/core", () => ({
  invoke: vi.fn(),
  isTauri: () => true,
}));
it("validates the exact preview and cited answer contract with no extra authority", () => {
  expect(answerRequestSchema.parse(fixture.request)).toEqual(fixture.request);
  expect(answerRequestSchema.parse(fixture.approve)).toEqual(fixture.approve);
  expect(answerResponseSchema.parse(fixture.preview)).toEqual(fixture.preview);
  expect(answerResponseSchema.parse(fixture.response)).toEqual(
    fixture.response,
  );
  for (const patch of [
    { tools: ["gmail"] },
    { sql: "SELECT *" },
    { provider: "other" },
    { question: "é".repeat(1025) },
    { operation: "approve", approved: false },
  ])
    expect(
      answerRequestSchema.safeParse({ ...fixture.request, ...patch }).success,
    ).toBe(false);
  const response = structuredClone(fixture.response);
  response.result.statements[0]!.citations[0]!.sourceId = "S8";
  expect(answerResponseSchema.safeParse(response).success).toBe(false);
  expect(
    answerResponseSchema.safeParse({ ...fixture.response, exhaustive: true })
      .success,
  ).toBe(false);
  expect(
    answerResponseSchema.safeParse({
      ...fixture.response,
      code: "stale_preview",
    }).success,
  ).toBe(false);
});
it("rejects foreign-account responses", async () => {
  vi.mocked(invoke).mockResolvedValue({
    ...fixture.preview,
    accountId: "foreign",
  });
  await expect(
    answerEmail(answerRequestSchema.parse(fixture.request)),
  ).rejects.toThrow("Invalid answer scope");
  vi.mocked(invoke).mockResolvedValue(fixture.preview);
  await expect(
    answerEmail(answerRequestSchema.parse(fixture.request)),
  ).resolves.toEqual(fixture.preview);
  expect(invoke).toHaveBeenLastCalledWith("email_answer", {
    request: fixture.request,
  });
});
