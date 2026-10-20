import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import fixture from "../../../../contracts/local-index-v1.json";
import { LocalEmailIndex } from "./LocalEmailIndex";
import { manageIndex, type IndexResponse } from "../../lib/ipc/indexing";
vi.mock("../../lib/ipc/indexing", () => ({ manageIndex: vi.fn() }));
const status = {
  ...fixture.response,
  status: "idle",
  code: "",
} as IndexResponse;
describe("Local Email Index", () => {
  beforeEach(() => {
    vi.mocked(manageIndex).mockReset();
    vi.mocked(manageIndex).mockResolvedValue(status);
  });
  afterEach(() => {
    vi.useRealTimers();
  });
  it("only polls status until the user explicitly starts", async () => {
    render(<LocalEmailIndex />);
    const start = await screen.findByRole("button", { name: "Start indexing" });
    await waitFor(() => expect(start).toBeEnabled());
    expect(manageIndex).toHaveBeenCalledExactlyOnceWith("status");
    expect(screen.getByText("Eligible messages")).toBeInTheDocument();
    expect(screen.getByText("Embeddings generated")).toBeInTheDocument();
    fireEvent.click(start);
    await waitFor(() =>
      expect(manageIndex).toHaveBeenCalledWith("start", "account-a"),
    );
  });
  it("shows pause/resume and retains partial coverage", async () => {
    vi.mocked(manageIndex).mockResolvedValue({ ...status, status: "running" });
    render(<LocalEmailIndex />);
    const pause = await screen.findByRole("button", { name: "Pause indexing" });
    await waitFor(() => expect(pause).toBeEnabled());
    vi.mocked(manageIndex).mockResolvedValue({ ...status, status: "paused" });
    fireEvent.click(pause);
    const resume = await screen.findByRole("button", {
      name: "Resume indexing",
    });
    await waitFor(() => expect(resume).toBeEnabled());
    expect(
      screen.getByText(
        /18 complete · 3 partial · 1 failed · 76 pending · 2 unavailable/,
      ),
    ).toBeInTheDocument();
    fireEvent.click(resume);
    await waitFor(() =>
      expect(manageIndex).toHaveBeenCalledWith("resume", "account-a"),
    );
  });
  it("does not restart interrupted work automatically", async () => {
    vi.mocked(manageIndex).mockResolvedValue({
      ...status,
      status: "interrupted",
      code: "interrupted",
    });
    render(<LocalEmailIndex />);
    expect(
      await screen.findByText("Interrupted — resume when ready"),
    ).toBeInTheDocument();
    expect(manageIndex).toHaveBeenCalledExactlyOnceWith("status");
  });
  it("rejects a stale poll that arrives after a pause", async () => {
    vi.useFakeTimers();
    vi.mocked(manageIndex).mockResolvedValue({ ...status, status: "running" });
    render(<LocalEmailIndex />);
    await act(async () => {});
    let resolvePoll!: (v: IndexResponse) => void;
    vi.mocked(manageIndex).mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolvePoll = resolve;
        }),
    );
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1500);
    });
    vi.mocked(manageIndex).mockResolvedValue({ ...status, status: "paused" });
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Pause indexing" }));
    });
    await act(async () => {
      resolvePoll({ ...status, status: "running" });
    });
    expect(screen.getByText("Paused")).toBeInTheDocument();
  });
});

it("requests explicit resume when email text preparation changes", async () => {
  vi.mocked(manageIndex).mockResolvedValue({
    ...status,
    status: "interrupted",
    code: "preprocessing_changed",
  });
  render(<LocalEmailIndex />);
  expect(
    await screen.findByText(/Email text cleanup changed/),
  ).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Resume indexing" })).toBeEnabled();
  expect(manageIndex).toHaveBeenCalledExactlyOnceWith("status");
});
