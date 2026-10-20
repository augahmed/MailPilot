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
  fireEvent.change(screen.getByLabelText("Search query"), {
    target: { value: "meeting paraphrase" },
  });
  fireEvent.change(screen.getByLabelText("Sender contains"), {
    target: { value: "Alex" },
  });
  await screen.findByRole("option", { name: "Finance" });
  fireEvent.change(screen.getByLabelText("Assigned category"), {
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
