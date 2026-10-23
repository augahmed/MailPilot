import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { ClassificationReview } from "./ClassificationReview";
import {
  reviewClassifications,
  type ClassificationPage,
} from "../../lib/ipc/client";
import fixture from "../../../../contracts/classification-v1.json";
vi.mock("../../lib/ipc/client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../lib/ipc/client")>()),
  reviewClassifications: vi.fn(),
}));
const saved = fixture.response as ClassificationPage;
const before: ClassificationPage = {
  ...saved,
  processed: 0,
  rows: saved.rows.map((row) => ({
    ...row,
    state: "not_reviewed",
    predictions: [],
    timestamp: null,
  })),
};
beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(reviewClassifications).mockResolvedValue(before);
});
it("disables Reset To Default without a label and clears a chosen category after reset", async () => {
  const fixture =
    await import("../../../../contracts/classification-local-v1.json");
  const row = fixture.default.response.rows[0]!;
  vi.mocked(reviewClassifications).mockResolvedValue({
    ...fixture.default.response,
    rows: [
      {
        ...row,
        state: "unclassified",
        predictions: [],
        semantic: { ...row.semantic, assigned: [], scores: [] },
      },
    ],
  } as ClassificationPage);
  render(<ClassificationReview />);
  const group = await screen.findByRole("group", {
    name: `Local category override for ${row.subject}`,
  });
  const reset = within(group).getByRole("button", { name: "Reset To Default" });
  expect(reset).toBeDisabled();
  fireEvent.change(within(group).getByRole("combobox"), {
    target: { value: "personal" },
  });
  expect(reset).toBeEnabled();
  fireEvent.click(reset);
  await waitFor(() =>
    expect(reviewClassifications).toHaveBeenLastCalledWith(
      expect.objectContaining({
        operation: "auto",
        emailId: row.email_id,
        categoryId: "",
        revision: row.readerRevision,
      }),
    ),
  );
  await waitFor(() =>
    expect(within(group).getByRole("combobox")).toHaveValue(""),
  );
  expect(reset).toBeDisabled();
});
it("loads saved reviews without classifying and runs only the requested cached page", async () => {
  render(<ClassificationReview />);
  await screen.findByText("Your receipt");
  expect(reviewClassifications).toHaveBeenCalledWith({
    operation: "list",
    offset: 0,
  });
  expect(screen.getAllByText("Not reviewed yet")).toHaveLength(2);
  vi.mocked(reviewClassifications).mockResolvedValue(saved);
  fireEvent.click(screen.getByRole("button", { name: "Classify this page" }));
  await screen.findByText("Rule strength 0.60");
  expect(screen.getByText("Receipts")).toBeInTheDocument();
  expect(
    screen.getByText(saved.rows[0]!.predictions[0]!.reasons[0]!),
  ).toBeInTheDocument();
  expect(
    screen.getByText("Unclassified — no clear category match"),
  ).toBeInTheDocument();
  expect(reviewClassifications).toHaveBeenLastCalledWith({
    operation: "run",
    accountId: "fixture-account",
    offset: 0,
  });
  expect(screen.getByRole("status")).toHaveTextContent(
    "Saved assessments for 2 cached emails. 0 errors.",
  );
});
it("reloads current saved assessments immediately when returning from the index", async () => {
  const { rerender } = render(<ClassificationReview refreshRevision={0} />);
  await screen.findByText("Your receipt");
  expect(screen.getAllByText("Not reviewed yet")).toHaveLength(2);
  vi.mocked(reviewClassifications).mockResolvedValue(saved);
  rerender(<ClassificationReview refreshRevision={1} />);
  await screen.findByText("Rule strength 0.60");
  expect(screen.queryByText("Not reviewed yet")).toBeNull();
  expect(
    vi
      .mocked(reviewClassifications)
      .mock.calls.every(([request]) => request.operation === "list"),
  ).toBe(true);
});

it("shows partial failure counts and preserves successful suggestions", async () => {
  render(<ClassificationReview />);
  await screen.findByText("Your receipt");
  vi.mocked(reviewClassifications).mockResolvedValue({
    ...saved,
    processed: 1,
    errors: 1,
    rows: [
      saved.rows[0]!,
      {
        ...saved.rows[1]!,
        state: "error",
        timestamp: null,
        code: "storage_error",
      },
    ],
  });
  fireEvent.click(screen.getByRole("button", { name: "Classify this page" }));
  await screen.findByText("Rule strength 0.60");
  expect(screen.getByRole("status")).toHaveTextContent(
    "1 cached email. 1 error.",
  );
  expect(
    screen.getByText("Could not load or save a local assessment. Try again."),
  ).toBeInTheDocument();
});
it("does not display stale category or confidence and explains unavailable bodies", async () => {
  vi.mocked(reviewClassifications).mockResolvedValue({
    ...before,
    rows: [
      { ...before.rows[0]!, state: "stale" },
      {
        ...before.rows[1]!,
        state: "unclassified",
        timestamp: 1791676800000,
        bodyState: "unavailable",
      },
    ],
  });
  render(<ClassificationReview />);
  await screen.findByText("Review needed — inputs or classifier changed");
  expect(screen.queryByText("Receipts")).not.toBeInTheDocument();
  expect(screen.queryByText("Rule strength 0.60")).not.toBeInTheDocument();
  expect(screen.getByText("Body unavailable")).toBeInTheDocument();
});
it("pages locally and escapes hostile message content and reasons", async () => {
  vi.mocked(reviewClassifications).mockImplementation(async (request) => ({
    ...saved,
    hasMore: true,
    rows: [
      {
        ...saved.rows[0]!,
        email_id: `mail-${request.offset ?? 0}`,
        subject: request.offset
          ? `Email ${request.offset}`
          : '<img src="https://example.test">',
        predictions: [
          {
            ...saved.rows[0]!.predictions[0]!,
            reasons: ['<script>alert("fixture")</script>'],
          },
        ],
      },
    ],
  }));
  const { container } = render(<ClassificationReview />);
  await screen.findByText('<img src="https://example.test">');
  expect(container.querySelector("img")).toBeNull();
  expect(container.querySelector("script")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Next review page" }));
  await waitFor(() =>
    expect(reviewClassifications).toHaveBeenCalledWith({
      operation: "list",
      offset: 25,
    }),
  );
});

it("loads and classifies 25 emails using bounded requests, and applies overrides to the correct subpage", async () => {
  const fixture =
    await import("../../../../contracts/classification-local-v1.json");
  const template = fixture.default.response.rows[0]!;
  const rows = Array.from({ length: 30 }, (_, i) => ({
    ...template,
    email_id: `email-${i}`,
    subject: `Message ${i}`,
    predictions: template.predictions.map((p) => ({
      ...p,
      email_id: `email-${i}`,
    })),
  }));
  vi.mocked(reviewClassifications).mockImplementation(async (request) => {
    const offset = request.offset ?? 0;
    return {
      ...fixture.default.response,
      rows: rows.slice(offset, offset + 5),
      hasMore: offset + 5 < rows.length,
      processed: request.operation === "run" ? 5 : 0,
      errors: 0,
    } as ClassificationPage;
  });
  render(<ClassificationReview />);
  await screen.findByText("Message 24");
  expect(screen.queryByText("Message 25")).toBeNull();
  expect(reviewClassifications).not.toHaveBeenCalledWith(
    expect.objectContaining({ operation: "run" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Classify this page" }));
  await screen.findByText("Saved assessments for 25 cached emails. 0 errors.");
  expect(
    vi
      .mocked(reviewClassifications)
      .mock.calls.filter(([r]) => r.operation === "run")
      .map(([r]) => r.offset),
  ).toEqual([0, 5, 10, 15, 20]);
  fireEvent.change(screen.getByLabelText("Primary category for Message 24"), {
    target: { value: "personal" },
  });
  fireEvent.click(
    within(
      screen.getByRole("group", {
        name: "Local category override for Message 24",
      }),
    ).getByRole("button", { name: "Assign primary category" }),
  );
  await waitFor(() =>
    expect(reviewClassifications).toHaveBeenCalledWith(
      expect.objectContaining({
        operation: "assigned",
        emailId: "email-24",
        offset: 20,
      }),
    ),
  );
  expect(screen.getByText("Message 0")).toBeVisible();
  expect(screen.getByText("Message 24")).toBeVisible();
});
it("clears displayed mail on bridge failure or authorization loss", async () => {
  vi.mocked(reviewClassifications).mockResolvedValue(saved);
  render(<ClassificationReview />);
  await screen.findByText("Your receipt");
  vi.mocked(reviewClassifications).mockRejectedValue(new Error("unavailable"));
  fireEvent.click(screen.getByRole("button", { name: "Refresh reviews" }));
  await screen.findByRole("alert");
  expect(screen.queryByText("Your receipt")).not.toBeInTheDocument();
  vi.mocked(reviewClassifications).mockResolvedValue({
    accountId: "",
    rows: [],
    hasMore: false,
    processed: 0,
    errors: 0,
    code: "unauthorized",
  });
  fireEvent.click(screen.getByRole("button", { name: "Refresh reviews" }));
  await screen.findByText(
    "Connect Gmail to review this account’s cached mail.",
  );
  expect(
    screen.getByRole("button", { name: "Classify this page" }),
  ).toBeDisabled();
});
it("ignores an old in-flight result after disconnect/remount", async () => {
  let resolveRun: (page: ClassificationPage) => void = () => {};
  const { unmount } = render(<ClassificationReview />);
  await screen.findByText("Your receipt");
  vi.mocked(reviewClassifications).mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        resolveRun = resolve;
      }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Classify this page" }));
  unmount();
  vi.mocked(reviewClassifications).mockResolvedValue({
    accountId: "",
    rows: [],
    hasMore: false,
    processed: 0,
    errors: 0,
    code: "unauthorized",
  });
  render(<ClassificationReview />);
  await screen.findByText(
    "Connect Gmail to review this account’s cached mail.",
  );
  resolveRun(saved);
  await waitFor(() =>
    expect(screen.queryByText("Your receipt")).not.toBeInTheDocument(),
  );
});

it("shows semantic evidence and requires explicit local override actions", async () => {
  const semantic =
    await import("../../../../contracts/classification-local-v1.json");
  vi.mocked(reviewClassifications).mockResolvedValue({
    ...semantic.default.response,
    rows: semantic.default.response.rows.slice(0, 3),
  } as ClassificationPage);
  render(<ClassificationReview />);
  await screen.findByText("Suggested: Jobs");
  expect(screen.getByText("Assigned: Personal")).toBeInTheDocument();
  expect(screen.getByText("Cosine score 0.72")).toBeInTheDocument();
  expect(screen.queryByText(/72%/)).not.toBeInTheDocument();
  expect(
    screen.getByText("Please choose a time for your technical interview."),
  ).toBeInTheDocument();
  fireEvent.change(
    screen.getByLabelText("Primary category for Next hiring stage"),
    { target: { value: "personal" } },
  );
  const group = screen.getByRole("group", {
    name: "Local category override for Next hiring stage",
  });
  fireEvent.click(group.querySelector("button")!);
  await waitFor(() =>
    expect(reviewClassifications).toHaveBeenLastCalledWith({
      operation: "assigned",
      accountId: "account-a",
      offset: 0,
      emailId: "email-a",
      categoryId: "personal",
      revision: "b".repeat(64),
    }),
  );
});

it("displays multiple close categories with one shared evidence and primary-choice control", async () => {
  const fixture =
    await import("../../../../contracts/classification-local-v1.json");
  const multi = fixture.default.response.rows[3]!;
  vi.mocked(reviewClassifications).mockResolvedValue({
    ...fixture.default.response,
    rows: [multi],
  } as ClassificationPage);
  render(<ClassificationReview />);
  await screen.findByText("Suggested: School");
  expect(screen.getByText("Suggested: Finance")).toBeInTheDocument();
  expect(screen.getByText("Suggested: Other")).toBeInTheDocument();
  expect(
    screen.queryByText("Unclassified — no clear category match"),
  ).not.toBeInTheDocument();
  expect(
    screen.getAllByText("Category scores and matching passages"),
  ).toHaveLength(1);
  expect(
    screen.getAllByRole("button", { name: "Assign primary category" }),
  ).toHaveLength(1);
  expect(
    screen.getByText(/Several categories are suggested/),
  ).toBeInTheDocument();
});
