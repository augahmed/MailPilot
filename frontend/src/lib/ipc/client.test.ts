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
