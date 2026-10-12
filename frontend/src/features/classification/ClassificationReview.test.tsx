import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { ClassificationReview } from "./ClassificationReview";
import {
  reviewClassifications,
  type ClassificationPage,
} from "../../lib/ipc/client";
import fixture from "../../../../contracts/classification-v1.json";
vi.mock("../../lib/ipc/client", () => ({ reviewClassifications: vi.fn() }));
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
  await screen.findByRole("cell", { name: "60% rule confidence" });
  expect(screen.getByRole("cell", { name: /Receipts/ })).toBeInTheDocument();
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
  await screen.findByRole("cell", { name: "60% rule confidence" });
  expect(screen.getByRole("status")).toHaveTextContent(
    "1 cached emails. 1 errors.",
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
  expect(screen.queryByText("60% rule confidence")).not.toBeInTheDocument();
  expect(screen.getByText("Body unavailable")).toBeInTheDocument();
});
it("pages locally and escapes hostile message content and reasons", async () => {
  vi.mocked(reviewClassifications).mockResolvedValue({
    ...saved,
    hasMore: true,
    rows: [
      {
        ...saved.rows[0]!,
        subject: '<img src="https://example.test">',
        predictions: [
          {
            ...saved.rows[0]!.predictions[0]!,
            reasons: ['<script>alert("fixture")</script>'],
          },
        ],
      },
    ],
  });
  const { container } = render(<ClassificationReview />);
  await screen.findByText('<img src="https://example.test">');
  expect(container.querySelector("img")).toBeNull();
  expect(container.querySelector("script")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Next review page" }));
  await waitFor(() =>
    expect(reviewClassifications).toHaveBeenLastCalledWith({
      operation: "list",
      offset: 5,
    }),
  );
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

import semanticFixture from "../../../../contracts/classification-semantic-v1.json";
it("shows cosine scores and ranked candidates for selected and unclassified mail", async () => {
  vi.mocked(reviewClassifications).mockResolvedValue(
    semanticFixture.response as ClassificationPage,
  );
  render(<ClassificationReview />);
  expect(await screen.findByText("Cosine 0.600")).toBeInTheDocument();
  expect(screen.getAllByText("Top category candidates")).toHaveLength(2);
  expect(screen.getByText("Finance: cosine 0.200")).toBeInTheDocument();
  expect(
    screen.getByText(
      "Leading categories are within the 0.050 ambiguity margin; no category chosen.",
    ),
  ).toBeInTheDocument();
});
