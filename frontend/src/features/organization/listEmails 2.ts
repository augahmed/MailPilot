import {
  organize,
  type OrganizationRequest,
  type OrganizationResponse,
} from "../../lib/ipc/client";

export const organizationPageSize = 25;
export const organizationBatchSize = 25;
export type OrganizationFilter = {
  categoryName?: string;
  unclassified?: boolean;
};
export type OrganizationPage = OrganizationResponse & { nextOffset: number };

// Scan the existing bounded local list API when filtering, so categories beyond
// the first page are reachable. The cursor points to the first unshown match.
// No retrieval/classification or Gmail operation is performed by this filter.
export async function listOrganizationEmails(
  request: Pick<
    OrganizationRequest,
    "accountId" | "offset" | "source" | "suggestionMode"
  >,
  active: () => boolean,
  filter: OrganizationFilter = {},
): Promise<OrganizationPage | null> {
  let page: OrganizationResponse | null = null;
  const rows: OrganizationResponse["rows"] = [];
  const seen = new Set<string>();
  let cursor = request.offset ?? 0;
  const filtered = !!filter.categoryName || !!filter.unclassified;
  while (active() && cursor <= 1_000_000) {
    const result = await organize({
      ...request,
      operation: "list",
      ...(page ? { accountId: page.accountId } : {}),
      offset: cursor,
    });
    if (!active()) return null;
    if (result.code) return { ...result, rows: [], nextOffset: cursor };
    if (page && page.accountId !== result.accountId)
      return { ...result, code: "unauthorized", rows: [], nextOffset: cursor };
    page = result;
    for (let index = 0; index < result.rows.length; index++) {
      const row = result.rows[index]!;
      if (seen.has(row.emailId))
        throw new Error("The cached email list changed. Refresh organization.");
      seen.add(row.emailId);
      const matches = filter.unclassified
        ? row.change === "unclassified"
        : !filter.categoryName ||
          row.proposedLabel === `MailPilot/${filter.categoryName}`;
      if (!matches) continue;
      if (rows.length === organizationPageSize)
        return { ...result, rows, hasMore: true, nextOffset: cursor + index };
      rows.push(row);
    }
    cursor += result.rows.length;
    if (
      !result.hasMore ||
      !result.rows.length ||
      (!filtered && rows.length === organizationPageSize)
    )
      return { ...result, rows, hasMore: result.hasMore, nextOffset: cursor };
  }
  if (!active() || !page) return null;
  throw new Error("The cached email list exceeds the supported range.");
}

export function classificationLabel(row: OrganizationResponse["rows"][number]) {
  if (row.change === "multiple_suggestions")
    return row.suggestionCount
      ? "Multiple suggestions"
      : "Multiple assignments";
  return row.proposedLabel.replace(/^MailPilot\//, "") || "Unclassified";
}
