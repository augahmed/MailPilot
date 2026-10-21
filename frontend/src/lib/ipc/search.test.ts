import { expect, it, vi } from "vitest";
import { invoke } from "@tauri-apps/api/core";
import hybridFixture from "../../../../contracts/hybrid-search-v1.json";
import fixture from "../../../../contracts/vector-search-v1.json";
import {
  searchRequestSchema,
  searchResponseSchema,
  vectorSearch,
} from "./search";
vi.mock("@tauri-apps/api/core", () => ({
  invoke: vi.fn(),
  isTauri: () => true,
}));
it("validates shared search fixtures and bounded private capabilities", () => {
  expect(searchRequestSchema.parse(fixture.request)).toEqual(fixture.request);
  expect(searchResponseSchema.parse(fixture.response)).toEqual(
    fixture.response,
  );
  expect(searchRequestSchema.parse(hybridFixture.request)).toEqual(
    hybridFixture.request,
  );
  expect(searchResponseSchema.parse(hybridFixture.response)).toEqual(
    hybridFixture.response,
  );
  for (const patch of [
    { mode: "remote" },
    { modelPath: "/tmp/no" },
    { limit: 21 },
    { accountId: "" },
    { query: "é".repeat(1025) },
    { filter: { ...fixture.request.filter, after: 10, before: 5 } },
  ])
    expect(
      searchRequestSchema.safeParse({ ...fixture.request, ...patch }).success,
    ).toBe(false);
  expect(
    searchResponseSchema.safeParse({ ...fixture.response, status: "error" })
      .success,
  ).toBe(false);
  expect(
    searchResponseSchema.safeParse({
      ...fixture.response,
      emails: [
        {
          ...fixture.response.emails[0],
          evidence: [
            { ...fixture.response.emails[0]!.evidence[0], score: NaN },
          ],
        },
      ],
    }).success,
  ).toBe(false);
});
it("rejects cross-account search responses", async () => {
  vi.mocked(invoke).mockResolvedValue(fixture.response);
  await expect(
    vectorSearch(
      searchRequestSchema.parse({ ...fixture.request, accountId: "another" }),
    ),
  ).rejects.toThrow("Invalid search scope");
  await expect(
    vectorSearch(searchRequestSchema.parse(fixture.request)),
  ).resolves.toEqual(fixture.response);
  expect(invoke).toHaveBeenLastCalledWith("vector_search", {
    request: fixture.request,
  });
});
