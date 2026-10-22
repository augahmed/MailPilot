import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { Organization } from "./Organization";
import {
  organize,
  manageCategories,
  type OrganizationResponse,
} from "../../lib/ipc/client";
import fixture from "../../../../contracts/organization-v1.json";
import riskFixture from "../../../../contracts/organization-phishing-v1.json";

vi.mock("../../lib/ipc/client", () => ({
  organize: vi.fn(),
  manageCategories: vi.fn(),
}));
const preview = fixture.response as OrganizationResponse;
const idle: OrganizationResponse = {
  ...preview,
  state: "idle",
  id: "",
  digest: "",
  kind: "",
  expiresAt: 0,
  rows: [],
  steps: [],
};
const page: OrganizationResponse = {
  ...idle,
  rows: preview.rows.map((row) => ({ ...row, change: "candidate" })),
};
const fresh = (
  changes: Partial<OrganizationResponse> = {},
): OrganizationResponse => ({
  ...preview,
  expiresAt: Date.now() + 300_000,
  ...changes,
});
const applied = (): OrganizationResponse =>
  fresh({
    state: "applied",
    rows: preview.rows.map((row) => ({
      ...row,
      updatedLabels: [...row.currentLabels, row.proposedLabel],
    })),
    steps: preview.steps.map((step) => ({ ...step, state: "applied" })),
  });
async function selectAndPreview() {
  fireEvent.click(
    await screen.findByRole("checkbox", { name: "Select Your receipt" }),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Preview Label Changes" }),
  );
  await screen.findByRole("heading", { name: "2. Review label changes" });
}
beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(manageCategories).mockResolvedValue({
    accountId: "fixture-account",
    rows: [],
    hasMore: false,
  });
  vi.mocked(organize).mockImplementation(async (request) =>
    request.operation === "list"
      ? page
      : request.operation === "preview"
        ? fresh()
        : idle,
  );
});

it("shows only selected emails on a separate review screen and preserves selection on Go Back", async () => {
  vi.mocked(organize).mockImplementation(async (request) =>
    request.operation === "list"
      ? {
          ...page,
          rows: [
            ...page.rows,
            { ...page.rows[0]!, emailId: "other", subject: "Unselected email" },
          ],
        }
      : request.operation === "preview"
        ? fresh()
        : idle,
  );
  render(<Organization />);
  expect(
    screen.getByRole("button", { name: "Preview Label Changes" }),
  ).toBeDisabled();
  await selectAndPreview();
  expect(screen.queryByText("Unselected email")).toBeNull();
  expect(screen.queryByRole("combobox")).toBeNull();
  expect(screen.queryByRole("checkbox")).toBeNull();
  expect(
    screen.getAllByRole("button").map((button) => button.textContent),
  ).toEqual(["Go Back", "Confirm Changes"]);
  expect(
    within(
      screen.getByRole("list", { name: "Organization progress" }),
    ).getAllByRole("listitem"),
  ).toHaveLength(3);
  expect(organize).toHaveBeenCalledWith(
    expect.objectContaining({
      operation: "preview",
      emailIds: ["fixture-email"],
    }),
  );
  expect(organize).not.toHaveBeenCalledWith(
    expect.objectContaining({ operation: "apply" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Go Back" }));
  expect(
    screen.getByRole("heading", { name: "1. Select emails" }),
  ).toBeVisible();
  expect(
    screen.getByRole("checkbox", { name: "Select Your receipt" }),
  ).toBeChecked();
  expect(screen.getByText("Unselected email")).toBeVisible();
});

it("applies the exact confirmed plan once, waits for verified success, and returns to selection with Undo", async () => {
  let applying = false;
  vi.mocked(organize).mockImplementation(async (request) => {
    if (request.operation === "list")
      return applying
        ? {
            ...page,
            rows: applied().rows.map((row) => ({
              ...row,
              currentLabels: row.updatedLabels!,
              change: "candidate",
            })),
          }
        : page;
    if (request.operation === "preview") return fresh();
    if (request.operation === "apply") {
      applying = true;
      return { ...idle, state: "running" };
    }
    return applying ? applied() : idle;
  });
  render(<Organization />);
  await selectAndPreview();
  const confirm = screen.getByRole("button", { name: "Confirm Changes" });
  fireEvent.click(confirm);
  fireEvent.click(confirm);
  expect(
    await screen.findByRole("heading", { name: "3. Updating Gmail" }),
  ).toBeVisible();
  expect(screen.queryByRole("button", { name: "Confirm Changes" })).toBeNull();
  await screen.findByText("Gmail label changes confirmed for 1 email.");
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Undo" })).toBeEnabled(),
  );
  expect(
    screen.getByRole("heading", { name: "1. Select emails" }),
  ).toBeVisible();
  expect(screen.getByText("0 of 25 selected")).toBeVisible();
  expect(screen.getByText("INBOX, Keep, MailPilot/Receipts")).toBeVisible();
  expect(
    screen.queryByRole("button", {
      name: /Dry Run|Prepare|Check Gmail outcomes|Refresh batch status/,
    }),
  ).toBeNull();
  const confirmation = screen.getByText(
    "Gmail label changes confirmed for 1 email.",
  ).parentElement!;
  expect(within(confirmation).getAllByRole("button")).toHaveLength(1);
  expect(organize).toHaveBeenCalledWith(
    expect.objectContaining({
      operation: "apply",
      accountId: "fixture-account",
      actionId: preview.id,
      digest: preview.digest,
      emailIds: [],
    }),
  );
  expect(
    vi
      .mocked(organize)
      .mock.calls.filter(([request]) => request.operation === "apply"),
  ).toHaveLength(1);
});

it("requires a separately reviewed and confirmed Undo and then returns to screen one", async () => {
  const undo = fresh({
    id: "undo-action",
    digest: "b".repeat(64),
    kind: "undo",
    rows: preview.rows.map((row) => ({ ...row, change: "remove" })),
    steps: [{ ...preview.steps[1]!, kind: "remove" }],
  });
  let result = applied();
  vi.mocked(organize).mockImplementation(async (request) => {
    if (request.operation === "list") return page;
    if (request.operation === "undo_preview") return undo;
    if (request.operation === "apply")
      result = {
        ...undo,
        state: "applied",
        steps: undo.steps.map((step) => ({ ...step, state: "applied" })),
      };
    return result;
  });
  render(<Organization />);
  fireEvent.click(await screen.findByRole("button", { name: "Undo" }));
  await screen.findByRole("heading", { name: "2. Review undo" });
  expect(screen.getByText("Remove: MailPilot/Receipts")).toBeVisible();
  expect(organize).not.toHaveBeenCalledWith(
    expect.objectContaining({ operation: "apply" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Go Back" }));
  expect(screen.getByText("0 of 25 selected")).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "Undo" }));
  await screen.findByRole("heading", { name: "2. Review undo" });
  fireEvent.click(screen.getByRole("button", { name: "Confirm Changes" }));
  await screen.findByText("Undo confirmed in Gmail for 1 email.");
  expect(
    screen.getByRole("heading", { name: "1. Select emails" }),
  ).toBeVisible();
  expect(screen.queryByRole("button", { name: "Undo" })).toBeNull();
  expect(organize).toHaveBeenCalledWith(
    expect.objectContaining({
      operation: "apply",
      actionId: "undo-action",
      digest: "b".repeat(64),
    }),
  );
});

it("keeps a failed preview on selection with a retryable selection instead of empty results", async () => {
  let fail = true;
  vi.mocked(organize).mockImplementation(async (request) =>
    request.operation === "list"
      ? page
      : request.operation === "preview"
        ? fail
          ? { ...idle, state: "error", code: "stale_preview" }
          : fresh()
        : idle,
  );
  render(<Organization />);
  fireEvent.click(
    await screen.findByRole("checkbox", { name: "Select Your receipt" }),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Preview Label Changes" }),
  );
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Go back and preview the current changes again",
  );
  expect(
    screen.getByRole("heading", { name: "1. Select emails" }),
  ).toBeVisible();
  expect(
    screen.getByRole("checkbox", { name: "Select Your receipt" }),
  ).toBeChecked();
  expect(screen.queryByRole("button", { name: "Confirm Changes" })).toBeNull();
  fail = false;
  fireEvent.click(
    screen.getByRole("button", { name: "Preview Label Changes" }),
  );
  await screen.findByRole("button", { name: "Confirm Changes" });
  expect(screen.queryByRole("alert")).toBeNull();
});

it("blocks confirmation when the preview expires and lets the user return to preview again", async () => {
  vi.useFakeTimers();
  try {
    vi.mocked(organize).mockImplementation(async (request) =>
      request.operation === "list"
        ? page
        : request.operation === "status"
          ? fresh({ expiresAt: Date.now() + 2000 })
          : idle,
    );
    await act(async () => {
      render(<Organization />);
    });
    expect(
      screen.getByRole("button", { name: "Confirm Changes" }),
    ).toBeEnabled();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(3000);
    });
    expect(
      screen.getByRole("button", { name: "Confirm Changes" }),
    ).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "Confirm Changes" }));
    expect(organize).not.toHaveBeenCalledWith(
      expect.objectContaining({ operation: "apply" }),
    );
    fireEvent.click(screen.getByRole("button", { name: "Go Back" }));
    expect(
      screen.getByRole("checkbox", { name: "Select Your receipt" }),
    ).toBeChecked();
  } finally {
    vi.useRealTimers();
  }
});

it("restores expired previews as current selectable mail rather than stale preview rows", async () => {
  vi.mocked(organize).mockImplementation(async (request) =>
    request.operation === "list"
      ? page
      : fresh({
          state: "expired",
          expiresAt: Date.now() - 1,
          rows: [{ ...preview.rows[0]!, subject: "Old preview" }],
        }),
  );
  render(<Organization />);
  await screen.findByText(/This preview expired/);
  expect(screen.getByText("Your receipt")).toBeVisible();
  expect(screen.queryByText("Old preview")).toBeNull();
  expect(screen.queryByRole("button", { name: "Confirm Changes" })).toBeNull();
});

it("preserves uncertain outcomes and only allows reconciliation before more batches", async () => {
  const interrupted = fresh({
    state: "uncertain",
    code: "reconciliation_required",
    steps: preview.steps.map((step) => ({ ...step, state: "uncertain" })),
  });
  vi.mocked(organize).mockImplementation(async (request) =>
    request.operation === "list"
      ? page
      : request.operation === "reconcile"
        ? applied()
        : interrupted,
  );
  render(<Organization />);
  await screen.findByRole("heading", { name: "3. Check Gmail outcomes" });
  expect(
    screen.getByRole("list", { name: "Recorded outcomes" }),
  ).toHaveTextContent("uncertain");
  expect(screen.queryByRole("button", { name: "Reset" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Go Back" })).toBeNull();
  expect(
    screen.queryByRole("button", { name: "Preview Label Changes" }),
  ).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Check Gmail outcomes" }));
  await screen.findByText("Gmail label changes confirmed for 1 email.");
  expect(organize).toHaveBeenCalledWith(
    expect.objectContaining({ operation: "reconcile" }),
  );
  expect(organize).not.toHaveBeenCalledWith(
    expect.objectContaining({ operation: "apply" }),
  );
});

it("treats an interrupted apply response as uncertain and never blindly retries", async () => {
  vi.mocked(organize).mockImplementation(async (request) => {
    if (request.operation === "list") return page;
    if (request.operation === "preview") return fresh();
    if (request.operation === "apply")
      throw new Error("synthetic lost connection after dispatch");
    return idle;
  });
  render(<Organization />);
  await selectAndPreview();
  fireEvent.click(screen.getByRole("button", { name: "Confirm Changes" }));
  await screen.findByText(/connection was interrupted while applying changes/);
  expect(
    screen.getByRole("button", { name: "Check Gmail outcomes" }),
  ).toBeEnabled();
  expect(screen.queryByRole("button", { name: "Confirm Changes" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Go Back" })).toBeNull();
});

it("retains partial failure details, successful additions and eligible Undo", async () => {
  const partial = fresh({
    state: "partial",
    code: "operation_failed",
    steps: [
      { ...preview.steps[0]!, state: "failed", code: "conflict" },
      { ...preview.steps[1]!, state: "applied" },
    ],
  });
  vi.mocked(organize).mockImplementation(async (request) =>
    request.operation === "list" ? page : partial,
  );
  render(<Organization />);
  await screen.findByRole("heading", { name: "3. Check Gmail outcomes" });
  expect(
    screen.getByRole("list", { name: "Recorded outcomes" }),
  ).toHaveTextContent("failed · conflict");
  expect(screen.getByRole("button", { name: "Undo" })).toBeEnabled();
  expect(screen.getByRole("button", { name: "Go Back" })).toBeEnabled();
});

it("keeps cache-refresh errors visible even after Gmail changes were verified", async () => {
  const result = applied();
  result.steps[1]!.code = "cache_refresh_failed";
  vi.mocked(organize).mockImplementation(async (request) =>
    request.operation === "list" ? page : result,
  );
  render(<Organization />);
  await screen.findByText("Gmail label changes confirmed for 1 email.");
  expect(screen.getByRole("alert")).toHaveTextContent(
    "cached labels could not be refreshed",
  );
  expect(screen.getByRole("button", { name: "Undo" })).toBeEnabled();
});

it("loads 25 emails, sorts including unclassified and pages without restoring old previews", async () => {
  const rows = Array.from({ length: 31 }, (_, index) => ({
    ...page.rows[0]!,
    emailId: `email-${index}`,
    subject: `Message ${index}`,
    proposedLabel:
      index % 3 === 0
        ? ""
        : index % 3 === 1
          ? "MailPilot/Work"
          : "MailPilot/Finance",
    change:
      index % 3 === 0 ? ("unclassified" as const) : ("candidate" as const),
  }));
  vi.mocked(organize).mockImplementation(async (request) => {
    const offset = request.offset ?? 0;
    return request.operation === "list"
      ? {
          ...page,
          rows: rows.slice(offset, offset + 5),
          hasMore: offset + 5 < rows.length,
        }
      : idle;
  });
  render(<Organization />);
  await screen.findByText("Message 24");
  const table = screen.getByRole("table");
  expect(within(table).getAllByRole("row")).toHaveLength(26);
  expect(within(table).getAllByRole("row")[1]).toHaveTextContent("Message 2");
  fireEvent.change(
    screen.getByRole("combobox", { name: "Sort this page by" }),
    { target: { value: "unclassified" } },
  );
  expect(within(table).getAllByRole("row")[1]).toHaveTextContent("Message 0");
  fireEvent.click(screen.getByRole("button", { name: "Next page" }));
  await screen.findByText("Message 30");
  expect(screen.queryByText("Message 0")).toBeNull();
  expect(screen.getByRole("button", { name: "Next page" })).toBeDisabled();
  expect(
    vi
      .mocked(organize)
      .mock.calls.filter(([request]) => request.operation === "status"),
  ).toHaveLength(1);
  fireEvent.click(screen.getByRole("button", { name: "Previous page" }));
  await screen.findByText("Message 0");
});

it("selects and resets all 25 eligible emails and sends exactly that selection to preview", async () => {
  const rows = Array.from({ length: 25 }, (_, index) => ({
    ...page.rows[0]!,
    emailId: `email-${index}`,
    subject: `Email ${index}`,
  }));
  vi.mocked(organize).mockImplementation(async (request) =>
    request.operation === "list"
      ? { ...page, rows }
      : request.operation === "preview"
        ? fresh({ rows: rows.map((row) => ({ ...row, change: "add" })) })
        : idle,
  );
  render(<Organization />);
  await screen.findByText("Email 24");
  fireEvent.click(screen.getByRole("button", { name: "Select all" }));
  expect(screen.getByText("25 of 25 selected")).toBeVisible();
  expect(
    screen
      .getAllByRole("checkbox")
      .every((box) => (box as HTMLInputElement).checked),
  ).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "Reset" }));
  expect(screen.getByText("0 of 25 selected")).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "Select all" }));
  fireEvent.click(
    screen.getByRole("button", { name: "Preview Label Changes" }),
  );
  await screen.findByRole("button", { name: "Confirm Changes" });
  expect(within(screen.getByRole("table")).getAllByRole("row")).toHaveLength(
    26,
  );
  expect(organize).toHaveBeenCalledWith(
    expect.objectContaining({
      operation: "preview",
      emailIds: rows.map((row) => row.emailId),
    }),
  );
});

it("loads every category and filters across cached pages with correct filtered pagination", async () => {
  const categories = Array.from({ length: 7 }, (_, index) => ({
    id: `category-${index}`,
    name: index === 6 ? "Target" : `Category ${index}`,
    description: "Synthetic category",
    enabled: true,
    created_at: 1,
    updated_at: 1,
  }));
  vi.mocked(manageCategories).mockImplementation(async (request) => {
    const offset = request.offset ?? 0;
    return {
      accountId: "fixture-account",
      rows: categories.slice(offset, offset + 5),
      hasMore: offset + 5 < categories.length,
    };
  });
  const emails = Array.from({ length: 95 }, (_, index) => ({
    ...page.rows[0]!,
    emailId: `filter-${index}`,
    subject: `Filtered message ${index}`,
    proposedLabel:
      index < 10
        ? ""
        : index >= 30 && index % 2 === 0
          ? "MailPilot/Target"
          : "MailPilot/Work",
    change: index < 10 ? ("unclassified" as const) : ("candidate" as const),
  }));
  vi.mocked(organize).mockImplementation(async (request) => {
    const offset = request.offset ?? 0;
    return request.operation === "list"
      ? {
          ...page,
          rows: emails.slice(offset, offset + 25),
          hasMore: offset + 25 < emails.length,
        }
      : idle;
  });
  render(<Organization />);
  await screen.findByRole("option", { name: "Target" });
  const filter = screen.getByRole("combobox", { name: "Filter by category" });
  expect(within(filter).getAllByRole("option")).toHaveLength(9);
  fireEvent.change(filter, { target: { value: "category-6" } });
  await screen.findByText("Filtered message 78");
  expect(screen.queryByText("Filtered message 0")).toBeNull();
  expect(within(screen.getByRole("table")).getAllByRole("row")).toHaveLength(
    26,
  );
  fireEvent.click(screen.getByRole("button", { name: "Select all" }));
  expect(screen.getByText("25 of 25 selected")).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "Next page" }));
  await screen.findByText("Filtered message 94");
  expect(screen.queryByText("Filtered message 30")).toBeNull();
  expect(screen.getByRole("button", { name: "Next page" })).toBeDisabled();
  fireEvent.click(screen.getByRole("button", { name: "Previous page" }));
  await screen.findByText("Filtered message 30");
  fireEvent.change(filter, { target: { value: "__unclassified__" } });
  await screen.findByText("Filtered message 0");
  expect(screen.queryByText("Filtered message 30")).toBeNull();
  expect(within(screen.getByRole("table")).getAllByRole("row")).toHaveLength(
    11,
  );
  expect(screen.getByRole("button", { name: "Select all" })).toBeDisabled();
});

it("does not expose a partial page after authorization fails during loading", async () => {
  vi.mocked(organize).mockImplementation(async (request) =>
    request.operation === "list" && !request.offset
      ? { ...page, hasMore: true }
      : { ...idle, accountId: "", code: "unauthorized" },
  );
  render(<Organization />);
  await screen.findByText("Connect Gmail to organize this account’s mail.");
  expect(screen.queryByText("Your receipt")).toBeNull();
  expect(organize).not.toHaveBeenCalledWith(
    expect.objectContaining({ operation: "status" }),
  );
});

it("clears private cached mail when the account session becomes unauthorized", async () => {
  render(<Organization />);
  await screen.findByText("Your receipt");
  vi.mocked(organize).mockResolvedValue({
    ...idle,
    accountId: "",
    code: "unauthorized",
  });
  fireEvent.click(screen.getByRole("button", { name: "Refresh list" }));
  await screen.findByRole("alert");
  expect(screen.queryByText("Your receipt")).toBeNull();
});

it("ignores an older initial status after a new preview and renders hostile subjects as text", async () => {
  let resolveOld!: (value: OrganizationResponse) => void;
  const old = new Promise<OrganizationResponse>((resolve) => {
    resolveOld = resolve;
  });
  const reviewed = fresh({
    id: "new-action",
    rows: [{ ...preview.rows[0]!, subject: "<img src=x onerror=alert(1)>" }],
  });
  vi.mocked(organize).mockImplementation(async (request) =>
    request.operation === "list"
      ? page
      : request.operation === "status"
        ? old
        : reviewed,
  );
  render(<Organization />);
  await selectAndPreview();
  await act(async () => {
    resolveOld(fresh({ id: "old-action" }));
  });
  expect(screen.getByText("<img src=x onerror=alert(1)>")).toBeVisible();
  expect(screen.queryByRole("img")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Confirm Changes" }));
  await waitFor(() =>
    expect(organize).toHaveBeenCalledWith(
      expect.objectContaining({ operation: "apply", actionId: "new-action" }),
    ),
  );
});

it("does not overlap slow background status polls", async () => {
  vi.useFakeTimers();
  try {
    await act(async () => {
      render(<Organization />);
    });
    let finish!: (value: OrganizationResponse) => void;
    vi.mocked(organize).mockClear();
    vi.mocked(organize).mockReturnValue(
      new Promise((resolve) => {
        finish = resolve;
      }),
    );
    await act(async () => {
      await vi.advanceTimersByTimeAsync(15_000);
    });
    expect(organize).toHaveBeenCalledTimes(1);
    await act(async () => {
      finish(idle);
    });
    vi.mocked(organize).mockResolvedValue(idle);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });
    expect(organize).toHaveBeenCalledTimes(2);
  } finally {
    vi.useRealTimers();
  }
});

it("preserves legacy manual assignment validation and phishing evidence before explicit confirmation", async () => {
  const risk = fresh({
    ...riskFixture.response,
    expiresAt: Date.now() + 300_000,
  } as OrganizationResponse);
  vi.mocked(organize).mockImplementation(async (request) =>
    request.operation === "list"
      ? request.source === "phishing"
        ? {
            ...idle,
            rows: risk.rows.map((row) => ({ ...row, change: "candidate" })),
          }
        : {
            ...page,
            rows: [
              {
                ...page.rows[0]!,
                change: "multiple_suggestions",
                proposedLabel: "",
                confidence: 0,
              },
            ],
          }
      : request.operation === "preview"
        ? risk
        : idle,
  );
  render(<Organization />);
  await screen.findByText("Multiple assignments");
  expect(screen.queryByRole("checkbox")).toBeNull();
  fireEvent.click(screen.getByText("Classification evidence"));
  expect(
    screen.getByText(/Choose a primary category in Categories/),
  ).toBeVisible();
  fireEvent.change(
    screen.getByRole("combobox", { name: "Review suggestions" }),
    { target: { value: "phishing" } },
  );
  await selectAndPreview();
  fireEvent.click(screen.getByText("Classification evidence"));
  expect(screen.getByText(/High phishing risk · 80\/100/)).toBeVisible();
  expect(
    screen.getByText(/Reply-To and sender use different sites/),
  ).toBeVisible();
  expect(organize).toHaveBeenCalledWith(
    expect.objectContaining({ operation: "preview", source: "phishing" }),
  );
  expect(organize).not.toHaveBeenCalledWith(
    expect.objectContaining({ operation: "apply" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Confirm Changes" }));
  await waitFor(() =>
    expect(organize).toHaveBeenCalledWith(
      expect.objectContaining({
        operation: "apply",
        source: undefined,
        actionId: risk.id,
        digest: risk.digest,
      }),
    ),
  );
});

it("reports cancellation failures while waiting without silently dismissing the pending operation", async () => {
  let finish!: (value: OrganizationResponse) => void;
  const pending = new Promise<OrganizationResponse>((resolve) => {
    finish = resolve;
  });
  vi.mocked(organize).mockImplementation(async (request) => {
    if (request.operation === "list") return page;
    if (request.operation === "preview") return pending;
    if (request.operation === "cancel")
      throw new Error("synthetic cancellation failure");
    return idle;
  });
  const { unmount } = render(<Organization />);
  fireEvent.click(
    await screen.findByRole("checkbox", { name: "Select Your receipt" }),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Preview Label Changes" }),
  );
  fireEvent.click(await screen.findByRole("button", { name: "Stop" }));
  await screen.findByText(/Could not stop the operation/);
  expect(
    screen.getByRole("button", { name: "Preview Label Changes" }),
  ).toBeDisabled();
  unmount();
  await act(async () => {
    finish(fresh());
  });
});

it("revokes the review screen immediately when a background check finds uncertain writes", async () => {
  vi.useFakeTimers();
  try {
    let status = fresh();
    vi.mocked(organize).mockImplementation(async (request) =>
      request.operation === "list" ? page : status,
    );
    await act(async () => {
      render(<Organization />);
    });
    expect(
      screen.getByRole("button", { name: "Confirm Changes" }),
    ).toBeEnabled();
    status = fresh({
      state: "uncertain",
      code: "reconciliation_required",
      steps: preview.steps.map((step) => ({ ...step, state: "uncertain" })),
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });
    expect(
      screen.queryByRole("button", { name: "Confirm Changes" }),
    ).toBeNull();
    expect(
      screen.getByRole("button", { name: "Check Gmail outcomes" }),
    ).toBeVisible();
    expect(organize).not.toHaveBeenCalledWith(
      expect.objectContaining({ operation: "apply" }),
    );
  } finally {
    vi.useRealTimers();
  }
});

it("clears account-scoped selections and previews on a background account mismatch", async () => {
  vi.useFakeTimers();
  try {
    let status = fresh();
    vi.mocked(organize).mockImplementation(async (request) =>
      request.operation === "list" ? page : status,
    );
    await act(async () => {
      render(<Organization />);
    });
    expect(screen.getByText("Your receipt")).toBeVisible();
    status = fresh({ accountId: "another-account" });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });
    expect(screen.queryByText("Your receipt")).toBeNull();
    expect(
      screen.queryByRole("button", { name: "Confirm Changes" }),
    ).toBeNull();
    expect(screen.getByRole("alert")).toHaveTextContent("Connect Gmail");
  } finally {
    vi.useRealTimers();
  }
});

it("does not present a truncated category list when a later category page fails", async () => {
  const categories = Array.from({ length: 7 }, (_, i) => ({
    id: `category-${i}`,
    name: `Category ${i}`,
    description: "Synthetic",
    enabled: true,
    created_at: 1,
    updated_at: 1,
  }));
  let fail = true;
  vi.mocked(manageCategories).mockImplementation(async (request) => {
    const offset = request.offset ?? 0;
    if (offset && fail) throw Error("synthetic category page failure");
    return {
      accountId: "fixture-account",
      rows: categories.slice(offset, offset + 5),
      hasMore: offset + 5 < categories.length,
    };
  });
  render(<Organization />);
  await screen.findByRole("button", { name: "Retry category filters" });
  const filter = screen.getByRole("combobox", { name: "Filter by category" });
  expect(filter).toBeDisabled();
  expect(
    within(filter).queryByRole("option", { name: "Category 0" }),
  ).toBeNull();
  expect(screen.getByText("Your receipt")).toBeVisible();
  fail = false;
  fireEvent.click(
    screen.getByRole("button", { name: "Retry category filters" }),
  );
  await screen.findByRole("option", { name: "Category 6" });
  expect(filter).toBeEnabled();
});

it("defaults to multiple suggestions, loads every option on demand and previews a chosen suggestion without assigning it", async () => {
  const options = Array.from({ length: 100 }, (_, i) => ({
    categoryId: `suggestion-${i}`,
    name: `Suggestion ${i}`,
    confidence: 0.7 - i / 10000,
  }));
  vi.mocked(organize).mockImplementation(async (request) => {
    if (request.operation === "list")
      return {
        ...page,
        rows: [
          {
            ...page.rows[0]!,
            change: "multiple_suggestions",
            confidence: 0,
            proposedLabel: "",
            suggestionCount: 100,
          },
        ],
      };
    if (request.operation === "suggestions")
      return {
        ...idle,
        rows: [
          {
            ...page.rows[0]!,
            change: "multiple_suggestions",
            suggestionCount: 100,
            suggestions: options,
          },
        ],
      };
    if (request.operation === "preview")
      return fresh({
        rows: [
          {
            ...preview.rows[0]!,
            proposedLabel: "MailPilot/Suggestion 99",
            scoreKind: "cosine_similarity",
          },
        ],
      });
    return idle;
  });
  render(<Organization />);
  await screen.findByText("Multiple suggestions");
  expect(
    screen.getByRole("combobox", { name: "Automatic suggestions" }),
  ).toHaveValue("choose");
  expect(
    screen.getByRole("checkbox", { name: "Select Your receipt" }),
  ).toBeDisabled();
  expect(organize).not.toHaveBeenCalledWith(
    expect.objectContaining({ operation: "suggestions" }),
  );
  fireEvent.click(screen.getByText("Choose a suggestion (100)"));
  const select = await screen.findByRole("combobox", {
    name: "Suggested category for Your receipt",
  });
  expect(within(select).getAllByRole("option")).toHaveLength(101);
  fireEvent.change(select, { target: { value: "suggestion-99" } });
  fireEvent.click(
    screen.getByRole("checkbox", { name: "Select Your receipt" }),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Preview Label Changes" }),
  );
  await screen.findByRole("button", { name: "Confirm Changes" });
  expect(screen.getByText("Add: MailPilot/Suggestion 99")).toBeVisible();
  expect(organize).toHaveBeenCalledWith(
    expect.objectContaining({
      operation: "preview",
      suggestionMode: "choose",
      choices: [{ emailId: "fixture-email", categoryId: "suggestion-99" }],
    }),
  );
  expect(manageCategories).not.toHaveBeenCalledWith(
    expect.objectContaining({ operation: "assign" }),
  );
  expect(organize).not.toHaveBeenCalledWith(
    expect.objectContaining({ operation: "apply" }),
  );
});

it("uses highest-ranked mode without requiring per-email choices and clears choices when switching modes", async () => {
  vi.mocked(organize).mockImplementation(async (request) => {
    if (request.operation === "list")
      return request.suggestionMode === "highest"
        ? {
            ...page,
            rows: [{ ...page.rows[0]!, scoreKind: "cosine_similarity" }],
          }
        : {
            ...page,
            rows: [
              {
                ...page.rows[0]!,
                change: "multiple_suggestions",
                suggestionCount: 2,
                proposedLabel: "",
              },
            ],
          };
    if (request.operation === "preview") return fresh();
    return idle;
  });
  render(<Organization />);
  await screen.findByText("Multiple suggestions");
  fireEvent.change(
    screen.getByRole("combobox", { name: "Automatic suggestions" }),
    { target: { value: "highest" } },
  );
  await waitFor(() =>
    expect(
      screen.getByRole("checkbox", { name: "Select Your receipt" }),
    ).toBeEnabled(),
  );
  fireEvent.click(screen.getByRole("button", { name: "Select all" }));
  fireEvent.click(
    screen.getByRole("button", { name: "Preview Label Changes" }),
  );
  await screen.findByRole("button", { name: "Confirm Changes" });
  expect(organize).toHaveBeenCalledWith(
    expect.objectContaining({
      operation: "preview",
      suggestionMode: "highest",
      choices: undefined,
      emailIds: ["fixture-email"],
    }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Go Back" }));
  fireEvent.change(
    screen.getByRole("combobox", { name: "Automatic suggestions" }),
    { target: { value: "choose" } },
  );
  await screen.findByText("Multiple suggestions");
  expect(screen.getByText("0 of 25 selected")).toBeVisible();
  expect(
    screen.getByRole("checkbox", { name: "Select Your receipt" }),
  ).toBeDisabled();
});

it("keeps incomplete or failed suggestion loads retryable without exposing partial options", async () => {
  let fail = true;
  vi.mocked(organize).mockImplementation(async (request) =>
    request.operation === "list"
      ? {
          ...page,
          rows: [
            {
              ...page.rows[0]!,
              change: "multiple_suggestions",
              suggestionCount: 2,
              proposedLabel: "",
            },
          ],
        }
      : request.operation === "suggestions"
        ? {
            ...idle,
            rows: [
              {
                ...page.rows[0]!,
                suggestionCount: 2,
                suggestions: fail
                  ? [{ categoryId: "one", name: "One", confidence: 0.6 }]
                  : [
                      { categoryId: "one", name: "One", confidence: 0.6 },
                      { categoryId: "two", name: "Two", confidence: 0.59 },
                    ],
              },
            ],
          }
        : idle,
  );
  render(<Organization />);
  await screen.findByText("Multiple suggestions");
  fireEvent.click(screen.getByText("Choose a suggestion (2)"));
  await screen.findByRole("button", { name: "Retry suggestions" });
  expect(
    screen.queryByRole("combobox", {
      name: "Suggested category for Your receipt",
    }),
  ).toBeNull();
  expect(
    screen.getByRole("checkbox", { name: "Select Your receipt" }),
  ).toBeDisabled();
  fail = false;
  fireEvent.click(screen.getByRole("button", { name: "Retry suggestions" }));
  const picker = await screen.findByRole("combobox", {
    name: "Suggested category for Your receipt",
  });
  expect(within(picker).getAllByRole("option")).toHaveLength(3);
});

it("clears private email rows when loading suggestions detects a changed account", async () => {
  vi.mocked(organize).mockImplementation(async (request) =>
    request.operation === "list"
      ? {
          ...page,
          rows: [
            {
              ...page.rows[0]!,
              change: "multiple_suggestions",
              suggestionCount: 2,
              proposedLabel: "",
            },
          ],
        }
      : request.operation === "suggestions"
        ? { ...idle, accountId: "different-account" }
        : idle,
  );
  render(<Organization />);
  await screen.findByText("Multiple suggestions");
  fireEvent.click(screen.getByText("Choose a suggestion (2)"));
  await screen.findByText("Connect Gmail to organize this account’s mail.");
  expect(screen.queryByText("Your receipt")).toBeNull();
  expect(
    screen.queryByRole("combobox", {
      name: "Suggested category for Your receipt",
    }),
  ).toBeNull();
});

it("does not retain an unresolved hidden choice when returning from a restored preview", async () => {
  vi.mocked(organize).mockImplementation(async (request) =>
    request.operation === "list"
      ? {
          ...page,
          rows: [
            {
              ...page.rows[0]!,
              change: "multiple_suggestions",
              suggestionCount: 2,
              proposedLabel: "",
            },
          ],
        }
      : fresh(),
  );
  render(<Organization />);
  await screen.findByRole("button", { name: "Confirm Changes" });
  fireEvent.click(screen.getByRole("button", { name: "Go Back" }));
  expect(screen.getByText("Multiple suggestions")).toBeVisible();
  expect(screen.getByText("0 of 25 selected")).toBeVisible();
  expect(
    screen.getByRole("button", { name: "Preview Label Changes" }),
  ).toBeDisabled();
});
