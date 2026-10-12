import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, it, expect, vi } from "vitest";
import { SemanticSearch } from "./SemanticSearch";
import {
  semanticSearch,
  manageCategories,
  type SemanticSearchResponse,
} from "../../lib/ipc/client";
import fixture from "../../../../contracts/search-semantic-v1.json";
vi.mock("../../lib/ipc/client", () => ({
  semanticSearch: vi.fn(),
  manageCategories: vi.fn(),
}));
const idle: SemanticSearchResponse = {
  accountId: "fixture-account",
  jobId: "",
  operation: "",
  state: "idle",
  counts: { cached: 4, eligible: 3, indexed: 3 },
  processed: 0,
  errors: 0,
  scanned: 0,
  partial: false,
  capped: false,
  excerptFallback: false,
  rows: [],
};
beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(semanticSearch).mockResolvedValue(idle);
  vi.mocked(manageCategories).mockResolvedValue({
    accountId: "fixture-account",
    rows: [
      {
        id: "fixture-school",
        name: "School",
        description: "Coursework",
        enabled: true,
        created_at: 1,
        updated_at: 1,
      },
    ],
    hasMore: false,
  });
});
it("submits a local semantic query with filters and renders sender/date/score/excerpt", async () => {
  render(<SemanticSearch />);
  await screen.findByText(
    "3 indexed of 3 complete cached messages (4 cached total).",
  );
  fireEvent.change(screen.getByLabelText("Meaning to search for"), {
    target: { value: "technical interview scheduling" },
  });
  fireEvent.change(screen.getByLabelText("Sender contains"), {
    target: { value: "recruiter" },
  });
  fireEvent.change(screen.getByLabelText("Category"), {
    target: { value: "fixture-school" },
  });
  fireEvent.change(screen.getByLabelText("From date (UTC)"), {
    target: { value: "2026-10-10" },
  });
  fireEvent.change(screen.getByLabelText("Through date (UTC)"), {
    target: { value: "2026-10-11" },
  });
  fireEvent.change(screen.getByLabelText("Maximum results"), {
    target: { value: "2" },
  });
  vi.mocked(semanticSearch).mockResolvedValue(
    fixture.response as SemanticSearchResponse,
  );
  fireEvent.click(screen.getByRole("button", { name: "Search locally" }));
  expect(
    await screen.findByText("Meet the engineering team"),
  ).toBeInTheDocument();
  expect(screen.getByText("Similarity: 0.820")).toBeInTheDocument();
  expect(
    screen.getByText(
      "Choose a time for your technical interview with the hiring team",
    ),
  ).toBeInTheDocument();
  expect(
    screen.getByText(/Recruiter <recruiter@example.test>/),
  ).toBeInTheDocument();
  expect(semanticSearch).toHaveBeenCalledWith({
    operation: "query",
    accountId: "fixture-account",
    jobId: "",
    query: "technical interview scheduling",
    topK: 2,
    sender: "recruiter",
    categoryId: "fixture-school",
    from: Date.parse("2026-10-10T00:00:00Z"),
    until: Date.parse("2026-10-12T00:00:00Z"),
  });
});
it("starts indexing explicitly and supports cancellation without blocking the UI", async () => {
  render(<SemanticSearch />);
  await screen.findByText(/3 indexed of/);
  const indexing: SemanticSearchResponse = {
    ...idle,
    operation: "index",
    state: "indexing",
    jobId: "job",
    processed: 1,
  };
  vi.mocked(semanticSearch).mockResolvedValue(indexing);
  fireEvent.click(screen.getByRole("button", { name: "Index cached mail" }));
  expect(
    await screen.findByText("Indexing locally… 1 saved; 0 errors."),
  ).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Search locally" })).toBeDisabled();
  vi.mocked(semanticSearch).mockResolvedValue({
    ...indexing,
    state: "cancelled",
    code: "cancelled",
    partial: true,
  });
  fireEvent.click(screen.getByRole("button", { name: "Cancel local job" }));
  expect(
    await screen.findByText(
      "Stopped. Saved local embeddings remain available.",
    ),
  ).toBeInTheDocument();
  expect(semanticSearch).toHaveBeenCalledWith({
    operation: "cancel",
    accountId: "fixture-account",
    jobId: "job",
  });
});
it("explains model/index coverage and renders partial results as escaped text", async () => {
  const row = fixture.response.rows[0]!;
  vi.mocked(semanticSearch).mockResolvedValue({
    ...fixture.response,
    state: "completed",
    operation: "query",
    partial: true,
    errors: 1,
    excerptFallback: true,
    rows: [
      {
        ...row,
        subject: "<script>synthetic</script>",
        excerpt: '<img src="https://example.test/pixel">',
      },
    ],
  } as SemanticSearchResponse);
  const { container } = render(<SemanticSearch />);
  expect(
    await screen.findByText("<script>synthetic</script>"),
  ).toBeInTheDocument();
  expect(container.querySelector("script")).toBeNull();
  expect(container.querySelector("img")).toBeNull();
  expect(screen.getByText(/Partial results: 1 errors/)).toBeInTheDocument();
  expect(
    screen.getByText(/Passage ranking was unavailable/),
  ).toBeInTheDocument();
});
it("disables semantic actions when no model is configured", async () => {
  vi.mocked(semanticSearch).mockResolvedValue({
    ...idle,
    code: "embedding_unavailable",
  });
  render(<SemanticSearch />);
  expect(
    await screen.findByText(/Install and configure the local MiniLM/),
  ).toBeInTheDocument();
  expect(
    screen.getByRole("button", { name: "Index cached mail" }),
  ).toBeDisabled();
});
it("clears results and ignores late replies after disconnect/remount", async () => {
  vi.mocked(semanticSearch).mockResolvedValue(
    fixture.response as SemanticSearchResponse,
  );
  const first = render(<SemanticSearch />);
  await screen.findByText("Meet the engineering team");
  let finish: (response: SemanticSearchResponse) => void = () => {};
  vi.mocked(semanticSearch).mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        finish = resolve;
      }),
  );
  fireEvent.change(screen.getByLabelText("Meaning to search for"), {
    target: { value: "query" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Search locally" }));
  first.unmount();
  vi.mocked(semanticSearch).mockResolvedValue({
    ...idle,
    accountId: "",
    code: "unauthorized",
  });
  render(<SemanticSearch />);
  finish(fixture.response as SemanticSearchResponse);
  await screen.findByText(/Connect Gmail to search/);
  await waitFor(() =>
    expect(
      screen.queryByText("Meet the engineering team"),
    ).not.toBeInTheDocument(),
  );
});
