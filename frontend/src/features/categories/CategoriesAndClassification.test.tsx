import {
  fireEvent,
  render,
  screen,
  within,
  waitFor,
} from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { CategoriesAndClassification } from "./CategoriesAndClassification";
import { manageCategories, reviewClassifications } from "../../lib/ipc/client";

vi.mock("../../lib/ipc/client", () => ({
  manageCategories: vi.fn(),
  reviewClassifications: vi.fn(),
}));
beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(manageCategories).mockResolvedValue({
    accountId: "a",
    hasMore: false,
    rows: [
      {
        id: "school",
        name: "School",
        description: "School messages",
        enabled: true,
        created_at: 0,
        updated_at: 0,
      },
    ],
  });
  vi.mocked(reviewClassifications).mockResolvedValue({
    accountId: "a",
    rows: [],
    hasMore: false,
    processed: 0,
    errors: 0,
  });
});
it("combines collapsed categories with independent classification review underneath", async () => {
  render(<CategoriesAndClassification />);
  await screen.findByText("School");
  const categories = screen.getByRole("region", { name: "Categories" });
  const review = screen.getByRole("region", { name: "Classification review" });
  expect(
    categories.compareDocumentPosition(review) &
      Node.DOCUMENT_POSITION_FOLLOWING,
  ).toBeTruthy();
  expect(
    within(categories).getByRole("button", { name: "Edit School" }),
  ).not.toBeVisible();
  expect(
    within(categories).getByRole("button", { name: "Add Category" }),
  ).toBeVisible();
  expect(
    within(review).getByRole("button", { name: "Classify this page" }),
  ).toBeDisabled();
  expect(reviewClassifications).toHaveBeenCalledExactlyOnceWith({
    operation: "list",
    offset: 0,
  });
  fireEvent.click(within(categories).getByText("School"));
  expect(
    within(categories).getByRole("button", { name: "Disable School" }),
  ).toBeVisible();
  expect(
    screen.getByRole("link", { name: "Classification Review" }),
  ).toHaveAttribute("href", "#classification-heading");
});

it("offers the index route after saving and refreshes review without starting inference", async () => {
  const onUpdateClassification = vi.fn();
  render(
    <CategoriesAndClassification
      onUpdateClassification={onUpdateClassification}
    />,
  );
  await screen.findByText("School");
  expect(
    screen.queryByRole("button", { name: "Update classification" }),
  ).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Add Category" }));
  fireEvent.change(screen.getByLabelText("Name"), {
    target: { value: "Travel" },
  });
  fireEvent.change(screen.getByLabelText("Description"), {
    target: { value: "Reservations and itineraries" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Save category" }));
  const update = await screen.findByRole("button", {
    name: "Update classification",
  });
  expect(onUpdateClassification).not.toHaveBeenCalled();
  await waitFor(() => expect(reviewClassifications).toHaveBeenCalledTimes(2));
  expect(
    vi
      .mocked(reviewClassifications)
      .mock.calls.every(([request]) => request.operation === "list"),
  ).toBe(true);
  fireEvent.click(update);
  expect(onUpdateClassification).toHaveBeenCalledOnce();
});

it("does not offer an update route when category storage rejects a change", async () => {
  const onUpdateClassification = vi.fn();
  render(
    <CategoriesAndClassification
      onUpdateClassification={onUpdateClassification}
    />,
  );
  fireEvent.click(await screen.findByText("School"));
  vi.mocked(manageCategories).mockResolvedValue({
    accountId: "a",
    rows: [],
    hasMore: false,
    code: "conflict",
  });
  fireEvent.click(screen.getByRole("button", { name: "Disable School" }));
  await screen.findByText(/This name is already in use/);
  expect(
    screen.queryByRole("button", { name: "Update classification" }),
  ).toBeNull();
  expect(onUpdateClassification).not.toHaveBeenCalled();
  expect(reviewClassifications).toHaveBeenCalledOnce();
});
