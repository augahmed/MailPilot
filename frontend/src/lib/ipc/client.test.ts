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

import { organizationResponseSchema } from "./client";
import organizationFixture from "../../../../contracts/organization-v1.json";
it("checks the shared organization contract and rejects destructive operations and secret fields", () => {
  const response = organizationFixture.response;
  expect(organizationResponseSchema.safeParse(response).success).toBe(true);
  expect(
    organizationResponseSchema.safeParse({ ...response, token: "synthetic" })
      .success,
  ).toBe(false);
  expect(
    organizationResponseSchema.safeParse({
      ...response,
      steps: [{ ...response.steps[0], kind: "delete_label" }],
    }).success,
  ).toBe(false);
  expect(
    organizationResponseSchema.safeParse({
      ...response,
      rows: [{ ...response.rows[0], confidence: 1.1 }],
    }).success,
  ).toBe(false);
});

import { mailboxSchema } from "./client";
import phishingFixture from "../../../../contracts/phishing-v1.json";
it("validates the phishing v1 contract and rejects invalid scores and verdicts", () => {
  expect(mailboxSchema.safeParse(phishingFixture).success).toBe(true);
  const row = phishingFixture.rows[0];
  if (!row) throw new Error("Missing fixture row");
  for (const change of [
    { score: 101 },
    { score: -1 },
    { risk: "SAFE" },
    { reasons: [{ code: "bad", points: 200, explanation: "bad" }] },
  ]) {
    expect(
      mailboxSchema.safeParse({
        ...phishingFixture,
        rows: [{ ...row, phishing: { ...row.phishing, ...change } }],
      }).success,
    ).toBe(false);
  }
  const { phishing: _assessment, ...oldRow } = row;
  expect(_assessment).toBeDefined();
  expect(
    mailboxSchema.safeParse({ ...phishingFixture, rows: [oldRow] }).success,
  ).toBe(true);
});

import organizationRiskFixture from "../../../../contracts/organization-phishing-v1.json";
it("accepts reviewed phishing organization evidence and rejects under-threshold or unsafe labels", () => {
  expect(
    organizationResponseSchema.safeParse(organizationRiskFixture.response)
      .success,
  ).toBe(true);
  const row = organizationRiskFixture.response.rows[0];
  const step = organizationRiskFixture.response.steps[0];
  if (!row || !step) throw new Error("Missing fixture");
  expect(
    organizationResponseSchema.safeParse({
      ...organizationRiskFixture.response,
      rows: [{ ...row, phishing: { ...row.phishing, score: 59 } }],
    }).success,
  ).toBe(false);
  expect(
    organizationResponseSchema.safeParse({
      ...organizationRiskFixture.response,
      steps: [{ ...step, name: "SPAM" }],
    }).success,
  ).toBe(false);
});

import semanticFixture from "../../../../contracts/classification-semantic-v1.json";
it("validates semantic evidence and candidate rankings while retaining the old contract", () => {
  expect(
    classificationPageSchema.safeParse(semanticFixture.response).success,
  ).toBe(true);
  const row = semanticFixture.response.rows[0];
  if (!row) throw new Error("Missing fixture");
  for (const candidates of [
    row.assessment.candidates.slice().reverse(),
    [...row.assessment.candidates, row.assessment.candidates[0]],
    [{ category_id: "different", category_name: "Different", similarity: 0.6 }],
  ]) {
    expect(
      classificationPageSchema.safeParse({
        ...semanticFixture.response,
        rows: [{ ...row, assessment: { ...row.assessment, candidates } }],
      }).success,
    ).toBe(false);
  }
});

import searchFixture from "../../../../contracts/search-semantic-v1.json";
import {
  semanticSearchRequestSchema,
  semanticSearchResponseSchema,
} from "./client";
it("validates semantic request scopes, filters and ranked bounded results", () => {
  expect(
    semanticSearchRequestSchema.safeParse(searchFixture.request).success,
  ).toBe(true);
  expect(
    semanticSearchResponseSchema.safeParse(searchFixture.response).success,
  ).toBe(true);
  for (const change of [
    { topK: 21 },
    { query: "" },
    { from: 10, until: 5 },
    { operation: "status" },
    { url: "https://example.test" },
    { query: "😀".repeat(251) },
  ]) {
    expect(
      semanticSearchRequestSchema.safeParse({
        ...searchFixture.request,
        ...change,
      }).success,
    ).toBe(false);
  }
  const row = searchFixture.response.rows[0]!;
  for (const rows of [
    [{ ...row, similarity: 1.1 }],
    [{ ...row, similarity: NaN }],
    searchFixture.response.rows.slice().reverse(),
    [row, row],
  ]) {
    expect(
      semanticSearchResponseSchema.safeParse({
        ...searchFixture.response,
        rows,
      }).success,
    ).toBe(false);
  }
  expect(
    semanticSearchResponseSchema.safeParse({
      ...searchFixture.response,
      code: "unauthorized",
    }).success,
  ).toBe(false);
});
