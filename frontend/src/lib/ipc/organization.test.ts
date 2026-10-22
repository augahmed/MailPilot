import { beforeEach, expect, it, vi } from "vitest";
import { invoke } from "@tauri-apps/api/core";
import fixture from "../../../../contracts/organization-v1.json";
import { organize, organizationResponseSchema } from "./organization";
import batchFixture from "../../../../contracts/organization-batch-v1.json";

vi.mock("@tauri-apps/api/core", () => ({
  invoke: vi.fn(),
  isTauri: () => true,
}));
beforeEach(() => {
  vi.mocked(invoke).mockReset();
  vi.mocked(invoke).mockResolvedValue(fixture.response);
});
it("accepts 25-email/50-step responses and separately displays current cached labels", () => {
  expect(
    organizationResponseSchema.safeParse(batchFixture.response).success,
  ).toBe(true);
  const response = structuredClone(batchFixture.response);
  response.rows.push({ ...response.rows[0]!, emailId: "email-26" });
  expect(organizationResponseSchema.safeParse(response).success).toBe(false);
  expect(
    organizationResponseSchema.parse({
      ...fixture.response,
      state: "applied",
      rows: [
        {
          ...fixture.response.rows[0],
          updatedLabels: ["INBOX", "MailPilot/Receipts"],
        },
      ],
    }).rows[0]?.updatedLabels,
  ).toEqual(["INBOX", "MailPilot/Receipts"]);
});

it.each(["dry_run", "preview", "list"] as const)(
  "keeps required Rust fields when %s callers supply undefined optional values",
  async (operation) => {
    await organize({
      operation,
      accountId: "fixture-account",
      offset: undefined,
      emailIds: operation === "list" ? [] : ["fixture-email"],
      actionId: undefined,
      digest: undefined,
      source: undefined,
    });
    const [, args] = vi.mocked(invoke).mock.calls[0]!;
    // Native IPC serializes JSON: undefined properties disappear on the wire.
    expect(JSON.parse(JSON.stringify(args))).toEqual({
      request: {
        operation,
        accountId: "fixture-account",
        offset: 0,
        emailIds: operation === "list" ? [] : ["fixture-email"],
        actionId: "",
        digest: "",
      },
    });
  },
);

it("preserves the exact reviewed action and digest when applying", async () => {
  await organize({
    operation: "apply",
    accountId: fixture.request.accountId,
    actionId: fixture.response.id,
    digest: fixture.response.digest,
    emailIds: undefined,
  });
  expect(invoke).toHaveBeenCalledWith("organization_manage", {
    request: {
      operation: "apply",
      accountId: fixture.request.accountId,
      offset: 0,
      emailIds: [],
      actionId: fixture.response.id,
      digest: fixture.response.digest,
    },
  });
});

import suggestionFixture from "../../../../contracts/organization-suggestions-v1.json";
it("decodes complete per-email suggestions and sends only the chosen category identity", async () => {
  vi.mocked(invoke).mockResolvedValue(suggestionFixture.response);
  const options = await organize(
    suggestionFixture.request as Parameters<typeof organize>[0],
  );
  expect(options.rows[0]?.suggestions).toHaveLength(3);
  await organize(
    suggestionFixture.choicesRequest as Parameters<typeof organize>[0],
  );
  expect(invoke).toHaveBeenLastCalledWith("organization_manage", {
    request: suggestionFixture.choicesRequest,
  });
  const tooMany = structuredClone(suggestionFixture.response);
  tooMany.rows[0]!.suggestions = Array.from(
    { length: 101 },
    () => tooMany.rows[0]!.suggestions[0]!,
  );
  expect(organizationResponseSchema.safeParse(tooMany).success).toBe(false);
});
