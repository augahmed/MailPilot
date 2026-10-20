import { describe, expect, it, vi } from "vitest";
import fixture from "../../../../contracts/local-index-v1.json";
import {
  indexRequestSchema,
  indexResponseSchema,
  manageIndex,
} from "./indexing";
import { invoke } from "@tauri-apps/api/core";
vi.mock("@tauri-apps/api/core", () => ({
  invoke: vi.fn(),
  isTauri: () => true,
}));
describe("local index contract", () => {
  it("accepts the shared fixture and rejects capabilities and inconsistent counts", () => {
    expect(indexRequestSchema.parse(fixture.request)).toEqual(fixture.request);
    expect(indexResponseSchema.parse(fixture.response)).toEqual(
      fixture.response,
    );
    expect(
      indexRequestSchema.safeParse({
        ...fixture.request,
        modelPath: "/tmp/model",
      }).success,
    ).toBe(false);
    expect(
      indexRequestSchema.safeParse({ operation: "start", accountId: "" })
        .success,
    ).toBe(false);
    expect(
      indexResponseSchema.safeParse({
        ...fixture.response,
        embeddingsGenerated: 66,
      }).success,
    ).toBe(false);
    expect(
      indexResponseSchema.safeParse({ ...fixture.response, pendingMessages: 0 })
        .success,
    ).toBe(false);
  });
  it("binds mutations to the requested account", async () => {
    vi.mocked(invoke).mockResolvedValue(fixture.response);
    await expect(manageIndex("start", "account-b")).rejects.toThrow(
      "Invalid index account",
    );
    await expect(manageIndex("start", "account-a")).resolves.toEqual(
      fixture.response,
    );
    expect(invoke).toHaveBeenLastCalledWith("local_index", {
      request: fixture.request,
    });
  });
});
