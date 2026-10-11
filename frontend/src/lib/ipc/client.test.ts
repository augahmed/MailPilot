import { describe, expect, it } from "vitest";
import { healthSchema } from "./client";

describe("health contract", () => {
  it("rejects incompatible protocol and malformed status", () => {
    const payload = {
      name: "MailPilot",
      version: "0.1.0",
      protocolVersion: 1,
      backend: "connected",
      database: "connected",
      schemaVersion: 1,
    };
    expect(healthSchema.safeParse(payload).success).toBe(true);
    expect(
      healthSchema.safeParse({ ...payload, protocolVersion: 2 }).success,
    ).toBe(false);
    expect(healthSchema.safeParse({ ...payload, database: true }).success).toBe(
      false,
    );
  });
});

import { gmailStatusSchema } from "./client";
it("rejects secret fields and unknown authentication states", () => {
  expect(
    gmailStatusSchema.safeParse({
      state: "connected",
      email: "reader@example.test",
    }).success,
  ).toBe(true);
  expect(
    gmailStatusSchema.safeParse({
      state: "connected",
      accessToken: "synthetic",
    }).success,
  ).toBe(false);
  expect(gmailStatusSchema.safeParse({ state: "fake_success" }).success).toBe(
    false,
  );
});

import { categoryPageSchema } from "./client";
import categoryFixture from "../../../../contracts/categories-v1.json";
it("validates the shared v1 category fixture and rejects malformed fields", () => {
  expect(categoryPageSchema.safeParse(categoryFixture.response).success).toBe(
    true,
  );
  expect(
    categoryPageSchema.safeParse({
      ...categoryFixture.response,
      rows: [{ ...categoryFixture.response.rows[0], enabled: "true" }],
    }).success,
  ).toBe(false);
  expect(
    categoryPageSchema.safeParse({
      ...categoryFixture.response,
      rows: [{ ...categoryFixture.response.rows[0], extra: "untrusted" }],
    }).success,
  ).toBe(false);
});

import { classificationPageSchema } from "./client";
import classificationFixture from "../../../../contracts/classification-v1.json";
it("validates the shared classification contract and rejects unjustified or mismatched predictions", () => {
  const payload = classificationFixture.response;
  expect(classificationPageSchema.safeParse(payload).success).toBe(true);
  const row = payload.rows[0]!;
  const prediction = row.predictions[0]!;
  for (const invalid of [
    { ...prediction, reasons: [] },
    { ...prediction, confidence: 1.01 },
    { ...prediction, email_id: "other" },
    { ...prediction, timestamp: 0 },
    { ...prediction, classifier_name: "" },
  ]) {
    expect(
      classificationPageSchema.safeParse({
        ...payload,
        rows: [{ ...row, predictions: [invalid] }],
      }).success,
    ).toBe(false);
  }
  expect(
    classificationPageSchema.safeParse({
      ...payload,
      rows: [{ ...row, state: "stale" }],
    }).success,
  ).toBe(false);
  expect(
    classificationPageSchema.safeParse({
      ...payload,
      rows: [{ ...row, body: "unrequested content" }],
    }).success,
  ).toBe(false);
});
it("accepts bounded Unicode display text without confusing code points with UTF-16 units", () => {
  const row = classificationFixture.response.rows[0]!;
  expect(
    classificationPageSchema.safeParse({
      ...classificationFixture.response,
      rows: [{ ...row, subject: "📨".repeat(200), sender: "🧑".repeat(100) }],
    }).success,
  ).toBe(true);
});
