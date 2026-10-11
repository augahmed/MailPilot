import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { Categories } from "./Categories";
import { manageCategories, type CategoryPage } from "../../lib/ipc/client";
vi.mock("../../lib/ipc/client", () => ({ manageCategories: vi.fn() }));
const page: CategoryPage = {
  accountId: "a",
  hasMore: false,
  rows: [
    {
      id: "c",
      name: "School",
      description: "Classes and academic deadlines.",
      enabled: true,
      created_at: 1,
      updated_at: 1,
    },
  ],
};
beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(manageCategories).mockResolvedValue(page);
});
it("creates and edits descriptions and toggles enabled state", async () => {
  render(<Categories />);
  await screen.findByRole("button", { name: "Edit School" });
  fireEvent.click(screen.getByRole("button", { name: "Create category" }));
  fireEvent.change(screen.getByLabelText("Name"), {
    target: { value: "Travel" },
  });
  fireEvent.change(screen.getByLabelText("Description"), {
    target: { value: "Reservations and itineraries." },
  });
  fireEvent.click(screen.getByRole("button", { name: "Save category" }));
  await waitFor(() =>
    expect(manageCategories).toHaveBeenCalledWith(
      expect.objectContaining({
        operation: "save",
        accountId: "a",
        id: "",
        name: "Travel",
        description: "Reservations and itineraries.",
        enabled: true,
      }),
    ),
  );
  await screen.findByText("Category changes saved locally.");
  fireEvent.click(screen.getByRole("button", { name: "Edit School" }));
  expect(screen.getByLabelText("Description")).toHaveValue(
    "Classes and academic deadlines.",
  );
  fireEvent.change(screen.getByLabelText("Description"), {
    target: { value: "Lectures and assignments." },
  });
  fireEvent.click(screen.getByRole("button", { name: "Save category" }));
  await waitFor(() =>
    expect(manageCategories).toHaveBeenCalledWith(
      expect.objectContaining({
        id: "c",
        description: "Lectures and assignments.",
      }),
    ),
  );
  await waitFor(() =>
    expect(screen.queryByLabelText("Description")).not.toBeInTheDocument(),
  );
  fireEvent.click(screen.getByRole("button", { name: "Disable School" }));
  await waitFor(() =>
    expect(manageCategories).toHaveBeenCalledWith(
      expect.objectContaining({ operation: "save", id: "c", enabled: false }),
    ),
  );
});
it("confirms deletion and explains reference conflicts without hiding the category", async () => {
  render(<Categories />);
  await screen.findByRole("button", { name: "Delete School" });
  fireEvent.click(screen.getByRole("button", { name: "Delete School" }));
  expect(manageCategories).toHaveBeenCalledTimes(1);
  vi.mocked(manageCategories).mockResolvedValue({ ...page, code: "conflict" });
  fireEvent.click(screen.getByRole("button", { name: "Confirm deletion" }));
  await screen.findByText(/This name is already in use/);
  expect(screen.getByRole("button", { name: "Disable School" })).toBeEnabled();
  expect(manageCategories).toHaveBeenLastCalledWith({
    operation: "delete",
    accountId: "a",
    id: "c",
  });
});
it("clears categories when the account is no longer authorized", async () => {
  render(<Categories />);
  await screen.findByRole("button", { name: "Edit School" });
  vi.mocked(manageCategories).mockResolvedValue({
    accountId: "",
    rows: [],
    hasMore: false,
    code: "unauthorized",
  });
  fireEvent.click(screen.getByRole("button", { name: "Refresh categories" }));
  await screen.findByText(
    "Connect your account to manage its local categories.",
  );
  expect(screen.queryByText("School")).not.toBeInTheDocument();
});
it("pages and renders hostile names as text", async () => {
  vi.mocked(manageCategories).mockResolvedValue({
    ...page,
    hasMore: true,
    rows: [{ ...page.rows[0]!, name: '<img src="https://example.test">' }],
  });
  const { container } = render(<Categories />);
  await screen.findByRole("button", { name: "Next categories" });
  fireEvent.click(screen.getByRole("button", { name: "Next categories" }));
  await waitFor(() =>
    expect(manageCategories).toHaveBeenLastCalledWith({
      operation: "list",
      accountId: "a",
      offset: 5,
    }),
  );
  expect(container.querySelector("img")).toBeNull();
});

it("ignores a late initial list after a newer request denies account access", async () => {
  let resolveInitial!: (value: CategoryPage) => void;
  vi.mocked(manageCategories).mockReturnValueOnce(
    new Promise((resolve) => {
      resolveInitial = resolve;
    }),
  );
  vi.mocked(manageCategories).mockResolvedValue({
    accountId: "",
    rows: [],
    hasMore: false,
    code: "unauthorized",
  });
  render(<Categories />);
  fireEvent.click(screen.getByRole("button", { name: "Refresh categories" }));
  await screen.findByText(
    "Connect your account to manage its local categories.",
  );
  await act(async () => {
    resolveInitial(page);
  });
  expect(
    screen.queryByRole("button", { name: "Edit School" }),
  ).not.toBeInTheDocument();
});
