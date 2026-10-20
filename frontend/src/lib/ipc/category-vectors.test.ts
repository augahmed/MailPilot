import { expect, it, vi } from "vitest";
import { invoke } from "@tauri-apps/api/core";
import fixture from "../../../../contracts/category-vectors-v1.json";
import {
  categoryVectorRequestSchema,
  categoryVectorResponseSchema,
  manageCategoryVectors,
} from "./category-vectors";
vi.mock("@tauri-apps/api/core", () => ({
  invoke: vi.fn(),
  isTauri: () => true,
}));
it("requires explicit confirmation and bounds source explanations", async () => {
  const request = categoryVectorRequestSchema.parse(fixture.request);
  expect(categoryVectorResponseSchema.parse(fixture.response)).toEqual(
    fixture.response,
  );
  expect(
    categoryVectorRequestSchema.safeParse({ ...request, approved: false })
      .success,
  ).toBe(false);
  expect(
    categoryVectorRequestSchema.safeParse({ ...request, modelPath: "private" })
      .success,
  ).toBe(false);
  expect(
    categoryVectorResponseSchema.safeParse({
      ...fixture.response,
      examples: Array(11).fill(fixture.response.examples[0]),
    }).success,
  ).toBe(false);
  expect(
    categoryVectorResponseSchema.safeParse({
      ...fixture.response,
      sourceHash: "",
    }).success,
  ).toBe(false);
  vi.mocked(invoke).mockResolvedValue(fixture.response);
  await expect(manageCategoryVectors(request)).resolves.toEqual(
    fixture.response,
  );
  expect(invoke).toHaveBeenLastCalledWith("category_vectors", { request });
  await expect(
    manageCategoryVectors({ ...request, accountId: "other" }),
  ).rejects.toThrow("Invalid category account");
  await expect(
    manageCategoryVectors({ ...request, categoryId: "other" }),
  ).rejects.toThrow("Invalid category account");
});
