import { fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { CategoriesAndClassification } from "./CategoriesAndClassification";
import { manageCategories, reviewClassifications } from "../../lib/ipc/client";

vi.mock("../../lib/ipc/client", () => ({
  manageCategories: vi.fn(),
  reviewClassifications: vi.fn(),
}));
it("combines collapsed categories with independent classification review underneath", async () => {
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
