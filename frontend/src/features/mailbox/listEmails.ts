import { getMailbox, type MailboxPage } from "../../lib/ipc/client";

export const mailboxPageSize = 25;
export type MailboxViewPage = MailboxPage & { nextOffset: number };
type Row = MailboxPage["rows"][number];
export function mailboxCategory(row: Row) {
  if (row.classification?.categoryName) return row.classification.categoryName;
  switch (row.classification?.state) {
    case "multiple_suggestions":
      return "Multiple suggestions";
    case "multiple_assignments":
      return "Multiple assignments";
    case "stale":
      return "Review needed";
    default:
      return "Unclassified";
  }
}

export async function listMailboxEmails(
  offset: number,
  categoryId: string,
  active: () => boolean,
): Promise<MailboxViewPage | null> {
  let page: MailboxPage | null = null;
  const rows: Row[] = [];
  const seen = new Set<string>();
  let cursor = offset;
  while (active() && cursor <= 1_000_000) {
    const result = await getMailbox(cursor);
    if (!active()) return null;
    if (result.code || !result.accountId)
      return { ...result, rows: [], nextOffset: cursor };
    if (page && result.accountId !== page.accountId)
      throw Error("Mailbox account changed");
    page = result;
    for (let index = 0; index < result.rows.length; index++) {
      const row = result.rows[index]!;
      if (seen.has(row.id)) throw Error("Cached mailbox changed");
      seen.add(row.id);
      const matches =
        categoryId === "__unclassified__"
          ? !row.classification || row.classification.state === "unclassified"
          : !categoryId || row.classification?.categoryId === categoryId;
      if (!matches) continue;
      if (rows.length === mailboxPageSize)
        return { ...result, rows, hasMore: true, nextOffset: cursor + index };
      rows.push(row);
    }
    cursor += result.rows.length;
    if (!result.hasMore || !result.rows.length)
      return { ...result, rows, hasMore: false, nextOffset: cursor };
  }
  if (!active() || !page) return null;
  throw Error("Cached mailbox exceeds the supported range");
}
