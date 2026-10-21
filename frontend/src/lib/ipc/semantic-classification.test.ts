import { expect, it, vi } from "vitest";
import { invoke } from "@tauri-apps/api/core";
import fixture from "../../../../contracts/classification-local-v1.json";
import {
  classificationPageSchema,
  reviewClassifications,
} from "./classification";
vi.mock("@tauri-apps/api/core", () => ({
  invoke: vi.fn(),
  isTauri: () => true,
}));
it("separates suggested, assigned and unclassified with bounded raw evidence", async () => {
  expect(classificationPageSchema.parse(fixture.response)).toEqual(
    fixture.response,
  );
  const bad = structuredClone(fixture.response);
  bad.rows[0]!.semantic.scores[0]!.score = 1.01;
  expect(classificationPageSchema.safeParse(bad).success).toBe(false);
  const missing = structuredClone(fixture.response) as Record<string, unknown>;
  const rows = missing.rows as Array<Record<string, unknown>>;
  delete rows[0]!.semantic;
  expect(classificationPageSchema.safeParse(missing).success).toBe(false);
  vi.mocked(invoke).mockResolvedValue(fixture.response);
  await reviewClassifications({
    ...fixture.overrideRequest,
    operation: "assigned",
  });
  expect(invoke).toHaveBeenLastCalledWith("classification_review", {
    request: fixture.overrideRequest,
  });
});

it("accepts all close suggestions and rejects duplicates or out-of-lead predictions", () => {
  const row = fixture.response.rows[3]!;
  expect(row.predictions).toHaveLength(5);
  expect(classificationPageSchema.safeParse(fixture.response).success).toBe(
    true,
  );
  const duplicate = structuredClone(fixture.response);
  duplicate.rows[3]!.predictions[1] = duplicate.rows[3]!.predictions[0]!;
  expect(classificationPageSchema.safeParse(duplicate).success).toBe(false);
  const outside = structuredClone(fixture.response);
  outside.rows[3]!.semantic.scores[4]!.score = 0.68;
  outside.rows[3]!.predictions[4]!.confidence = 0.68;
  expect(classificationPageSchema.safeParse(outside).success).toBe(false);
});
