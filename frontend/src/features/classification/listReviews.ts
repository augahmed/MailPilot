import {
  reviewClassifications,
  type ClassificationPage,
} from "../../lib/ipc/client";

export const reviewPageSize = 25;
export const reviewWirePageSize = 5;

// Keep detailed semantic evidence within each existing bounded IPC response.
// Only an explicit run recomputes assessments; listing/paging never does.
export async function listReviews(
  request: { operation: "list" | "run"; accountId?: string; offset: number },
  active: () => boolean,
): Promise<ClassificationPage | null> {
  let page: ClassificationPage | null = null;
  const rows: ClassificationPage["rows"] = [];
  const seen = new Set<string>();
  let processed = 0;
  let errors = 0;
  for (let index = 0; index < reviewPageSize; index += reviewWirePageSize) {
    if (!active()) return null;
    const result = await reviewClassifications({
      ...request,
      ...(page ? { accountId: page.accountId } : {}),
      offset: request.offset + index,
    });
    if (!active()) return null;
    if (result.code) return { ...result, rows: [] };
    if (page && result.accountId !== page.accountId)
      return { ...result, accountId: "", rows: [], code: "unauthorized" };
    for (const row of result.rows) {
      if (seen.has(row.email_id)) throw Error("Cached review list changed");
      seen.add(row.email_id);
      rows.push(row);
    }
    processed += result.processed;
    errors += result.errors;
    page = result;
    if (!result.hasMore) break;
  }
  return page && { ...page, rows, processed, errors };
}
