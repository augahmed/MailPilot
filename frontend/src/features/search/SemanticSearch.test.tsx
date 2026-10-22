import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import fixture from "../../../../contracts/vector-search-v1.json";
import { SemanticSearch } from "./SemanticSearch";
import {
  vectorSearch,
  searchControl,
  type SearchResponse,
} from "../../lib/ipc/search";
import { manageCategories } from "../../lib/ipc/client";
vi.mock("../../lib/ipc/search", async (original) => ({
  ...(await original<typeof import("../../lib/ipc/search")>()),
  vectorSearch: vi.fn(),
}));
vi.mock("../../lib/ipc/client", () => ({ manageCategories: vi.fn() }));
const complete = fixture.response as SearchResponse;
const idle: SearchResponse = {
  ...complete,
  status: "idle",
  jobId: "",
  emails: [],
};
beforeEach(() => {
  vi.mocked(vectorSearch).mockReset();
  vi.mocked(vectorSearch).mockResolvedValue(idle);
  vi.mocked(manageCategories).mockReset();
  vi.mocked(manageCategories).mockResolvedValue({
    accountId: idle.accountId,
    rows: [
      {
        id: "finance",
        name: "Finance",
        description: "",
        enabled: true,
        created_at: 0,
        updated_at: 0,
      },
    ],
    hasMore: false,
  });
});
afterEach(() => vi.useRealTimers());
it("only searches on submit and opens original revision-bound evidence", async () => {
  const onOpen = vi.fn(),
    onEvidence = vi.fn();
  render(<SemanticSearch onOpen={onOpen} onEvidence={onEvidence} />);
  await waitFor(() =>
    expect(
      screen.getByRole("button", { name: "Search locally" }),
    ).toBeDisabled(),
  );
  expect(vectorSearch).toHaveBeenCalledExactlyOnceWith(searchControl("status"));
  fireEvent.change(screen.getByLabelText("Search"), {
    target: { value: "meeting paraphrase" },
  });
  fireEvent.change(screen.getByLabelText("Sender contains"), {
    target: { value: "Alex" },
  });
  await screen.findByRole("option", { name: "Finance" });
  fireEvent.change(screen.getByLabelText("Assigned categories"), {
    target: { value: "finance" },
  });
  vi.mocked(vectorSearch).mockResolvedValue(complete);
  fireEvent.click(screen.getByRole("button", { name: "Search locally" }));
  const open = await screen.findByRole("button", { name: "Project meeting" });
  expect(vectorSearch).toHaveBeenLastCalledWith(
    expect.objectContaining({
      operation: "start",
      query: "meeting paraphrase",
      filter: expect.objectContaining({
        sender: "Alex",
        categoryId: "finance",
      }),
    }),
  );
  expect(screen.getByText("The project meeting is Friday.")).toBeVisible();
  fireEvent.click(open);
  expect(onOpen).toHaveBeenCalledWith(
    complete.accountId,
    "fixture-email",
    open,
  );
  expect(onEvidence).toHaveBeenCalledWith({
    text: "The project meeting is Friday.",
    revision: complete.emails[0]!.readerRevision,
  });
});
it("cancels a running query and ignores late status polls", async () => {
  vi.useFakeTimers();
  vi.mocked(vectorSearch).mockResolvedValue({
    ...idle,
    status: "running",
    jobId: complete.jobId,
  });
  render(<SemanticSearch onOpen={vi.fn()} onEvidence={vi.fn()} />);
  await act(async () => {});
  let resolve!: (r: SearchResponse) => void;
  vi.mocked(vectorSearch).mockImplementationOnce(
    () =>
      new Promise((r) => {
        resolve = r;
      }),
  );
  await act(async () => {
    await vi.advanceTimersByTimeAsync(350);
  });
  vi.mocked(vectorSearch).mockResolvedValue({
    ...idle,
    status: "cancelled",
    code: "cancelled",
    jobId: complete.jobId,
  });
  await act(async () => {
    fireEvent.click(screen.getByRole("button", { name: "Cancel search" }));
  });
  await act(async () => {
    resolve(complete);
  });
  expect(screen.queryByText("Project meeting")).toBeNull();
  expect(screen.getByText("Search cancelled.")).toBeVisible();
  expect(vectorSearch).toHaveBeenCalledWith(
    searchControl("cancel", complete.accountId, complete.jobId),
  );
});
it("clears evidence after cache invalidation", async () => {
  vi.useFakeTimers();
  vi.mocked(vectorSearch).mockResolvedValue(complete);
  render(<SemanticSearch onOpen={vi.fn()} onEvidence={vi.fn()} />);
  await act(async () => {});
  expect(screen.getByText("Project meeting")).toBeVisible();
  vi.mocked(vectorSearch).mockResolvedValue({
    ...idle,
    status: "error",
    code: "stale",
  });
  await act(async () => {
    await vi.advanceTimersByTimeAsync(5000);
  });
  expect(screen.queryByText("Project meeting")).toBeNull();
  expect(screen.getByText(/Cached evidence changed/)).toBeVisible();
});

it("selects keyword mode and discloses fallback and partial index coverage", async () => {
  vi.mocked(vectorSearch).mockResolvedValue(idle);
  render(<SemanticSearch onOpen={vi.fn()} onEvidence={vi.fn()} />);
  await waitFor(() => expect(vectorSearch).toHaveBeenCalled());
  fireEvent.change(screen.getByLabelText("Search mode"), {
    target: { value: "keyword" },
  });
  fireEvent.change(screen.getByLabelText("Search"), {
    target: { value: "Q7M4ZP" },
  });
  vi.mocked(vectorSearch).mockResolvedValue({
    ...complete,
    mode: "keyword",
    requestedMode: "hybrid",
    warning: "semantic_unavailable",
    searchableMessages: 0,
  });
  fireEvent.click(screen.getByRole("button", { name: "Search locally" }));
  await screen.findByText(
    /Semantic retrieval unavailable; showing keyword-only results/,
  );
  expect(vectorSearch).toHaveBeenLastCalledWith(
    expect.objectContaining({ mode: "keyword" }),
  );
  expect(screen.getByText(/Semantic index is partial/)).toBeVisible();
  expect(screen.getByText(/Keyword coverage: 7 of 8/)).toBeVisible();
});

const allCategories = Array.from({ length: 12 }, (_, index) => ({
  id: `category-${index}`,
  name: `Category ${index + 1}`,
  description: "Synthetic category",
  enabled: index !== 11,
  created_at: 0,
  updated_at: 0,
}));

it("loads every category page, including disabled categories that may still have assignments", async () => {
  vi.mocked(manageCategories).mockImplementation(async (request) => ({
    accountId: idle.accountId,
    rows: allCategories.slice(request.offset ?? 0, (request.offset ?? 0) + 5),
    hasMore: (request.offset ?? 0) + 5 < allCategories.length,
  }));
  render(<SemanticSearch onOpen={vi.fn()} onEvidence={vi.fn()} />);
  await screen.findByRole("option", { name: "Category 12 (disabled)" });
  expect(
    screen.getByLabelText("Assigned categories").querySelectorAll("option"),
  ).toHaveLength(13);
  expect(
    vi.mocked(manageCategories).mock.calls.map(([request]) => request.offset),
  ).toEqual([0, 5, 10]);
  expect(vectorSearch).toHaveBeenCalledExactlyOnceWith(searchControl("status"));
  fireEvent.change(screen.getByLabelText("Assigned categories"), {
    target: { value: "category-11" },
  });
  fireEvent.change(screen.getByLabelText("Search"), {
    target: { value: "invoice" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Search locally" }));
  await waitFor(() =>
    expect(vectorSearch).toHaveBeenCalledWith(
      expect.objectContaining({
        operation: "start",
        filter: expect.objectContaining({ categoryId: "category-11" }),
      }),
    ),
  );
});

it("discards partial category menus on a later-page error and lets the user retry", async () => {
  vi.mocked(manageCategories).mockImplementation(async (request) => {
    if (request.offset === 5) throw new Error("Synthetic storage failure");
    return {
      accountId: idle.accountId,
      rows: allCategories.slice(0, 5),
      hasMore: true,
    };
  });
  render(<SemanticSearch onOpen={vi.fn()} onEvidence={vi.fn()} />);
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Could not load all categories",
  );
  expect(screen.getByLabelText("Assigned categories")).toBeDisabled();
  expect(screen.queryByRole("option", { name: "Category 1" })).toBeNull();
  vi.mocked(manageCategories).mockResolvedValue({
    accountId: idle.accountId,
    rows: allCategories.slice(0, 1),
    hasMore: false,
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Refresh category options" }),
  );
  expect(
    await screen.findByRole("option", { name: "Category 1" }),
  ).toBeInTheDocument();
  expect(screen.getByLabelText("Assigned categories")).toBeEnabled();
});

it("ignores category results from a revoked session", async () => {
  vi.useFakeTimers();
  let finish!: (value: Awaited<ReturnType<typeof manageCategories>>) => void;
  vi.mocked(manageCategories).mockReturnValue(
    new Promise((resolve) => {
      finish = resolve;
    }),
  );
  render(<SemanticSearch onOpen={vi.fn()} onEvidence={vi.fn()} />);
  await act(async () => {});
  vi.mocked(vectorSearch).mockResolvedValue({
    ...idle,
    accountId: "",
    code: "unauthorized",
  });
  await act(async () => {
    await vi.advanceTimersByTimeAsync(5000);
  });
  await act(async () => {
    finish({
      accountId: idle.accountId,
      rows: allCategories.slice(0, 5),
      hasMore: true,
    });
  });
  expect(screen.queryByRole("option", { name: "Category 1" })).toBeNull();
  expect(manageCategories).toHaveBeenCalledTimes(1);
});

it("refreshes categories on return from category editing while preserving the search input", async () => {
  const view = render(
    <SemanticSearch onOpen={vi.fn()} onEvidence={vi.fn()} active />,
  );
  await screen.findByRole("option", { name: "Finance" });
  fireEvent.change(screen.getByLabelText("Search"), {
    target: { value: "saved search" },
  });
  view.rerender(
    <SemanticSearch onOpen={vi.fn()} onEvidence={vi.fn()} active={false} />,
  );
  vi.mocked(manageCategories).mockResolvedValue({
    accountId: idle.accountId,
    rows: allCategories.slice(0, 1),
    hasMore: false,
  });
  view.rerender(
    <SemanticSearch onOpen={vi.fn()} onEvidence={vi.fn()} active />,
  );
  expect(
    await screen.findByRole("option", { name: "Category 1" }),
  ).toBeInTheDocument();
  expect(screen.queryByRole("option", { name: "Finance" })).toBeNull();
  expect(screen.getByLabelText("Search")).toHaveValue("saved search");
  expect(vectorSearch).toHaveBeenCalledExactlyOnceWith(searchControl("status"));
});
