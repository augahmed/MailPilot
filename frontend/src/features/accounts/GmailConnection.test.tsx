import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { GmailConnection } from "./GmailConnection";
import { gmailCommand, type GmailStatus } from "../../lib/ipc/client";
vi.mock("../../lib/ipc/client", () => ({ gmailCommand: vi.fn() }));
afterEach(() => {
  vi.useRealTimers();
});
describe("Gmail connection", () => {
  beforeEach(() => {
    vi.resetAllMocks();
  });
  it("starts browser sign-in without claiming it succeeded", async () => {
    vi.mocked(gmailCommand)
      .mockResolvedValueOnce({ state: "disconnected" })
      .mockResolvedValue({ state: "connecting" });
    render(<GmailConnection />);
    await screen.findByText("Gmail disconnected");
    fireEvent.click(screen.getByRole("button", { name: "Connect Gmail" }));
    expect(
      await screen.findByText("Waiting for Google sign-in in your browser…"),
    ).toBeInTheDocument();
    expect(screen.queryByText("Gmail connected")).not.toBeInTheDocument();
    expect(gmailCommand).toHaveBeenCalledWith("gmail_connect");
  });
  it("shows verified address and clears it immediately on disconnect", async () => {
    vi.mocked(gmailCommand)
      .mockResolvedValueOnce({
        state: "connected",
        email: "reader@example.test",
      })
      .mockResolvedValue({ state: "disconnecting" });
    render(<GmailConnection />);
    await screen.findByText("reader@example.test");
    fireEvent.click(screen.getByRole("button", { name: "Disconnect" }));
    expect(screen.queryByText("reader@example.test")).not.toBeInTheDocument();
    expect(await screen.findByText("Disconnecting Gmail…")).toBeInTheDocument();
    expect(gmailCommand).toHaveBeenCalledWith("gmail_disconnect");
  });
  it("explains denied consent and permits retry", async () => {
    vi.mocked(gmailCommand).mockResolvedValue({
      state: "error",
      code: "consent_denied",
    });
    render(<GmailConnection />);
    expect(
      await screen.findByText(/Google consent was denied/),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Connect Gmail" })).toBeEnabled();
  });
  it("shows setup instructions without fabricating an account", async () => {
    vi.mocked(gmailCommand).mockResolvedValue({
      state: "unconfigured",
      code: "oauth_not_configured",
    });
    render(<GmailConnection />);
    expect(await screen.findByText("Gmail setup required")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Connect Gmail" }),
    ).toBeDisabled();
  });
  it("does not show connected after bridge failure", async () => {
    vi.mocked(gmailCommand).mockRejectedValue(
      new Error("synthetic-private-error"),
    );
    render(<GmailConnection />);
    expect(
      await screen.findByText("Gmail status unavailable"),
    ).toBeInTheDocument();
    expect(screen.queryByText("Gmail connected")).not.toBeInTheDocument();
    expect(
      screen.queryByText("synthetic-private-error"),
    ).not.toBeInTheDocument();
  });
});

it("ignores an old status response after disconnect", async () => {
  vi.useFakeTimers();
  let resolvePoll!: (status: GmailStatus) => void;
  const stalePoll = new Promise<GmailStatus>((resolve) => {
    resolvePoll = resolve;
  });
  vi.mocked(gmailCommand).mockReset();
  vi.mocked(gmailCommand)
    .mockResolvedValueOnce({ state: "connected", email: "reader@example.test" })
    .mockReturnValueOnce(stalePoll)
    .mockResolvedValue({ state: "disconnected" });
  const view = render(<GmailConnection />);
  await act(async () => {});
  await act(async () => {
    await vi.advanceTimersByTimeAsync(1500);
  });
  fireEvent.click(screen.getByRole("button", { name: "Disconnect" }));
  await act(async () => {});
  expect(screen.getByText("Gmail disconnected")).toBeInTheDocument();
  await act(async () => {
    resolvePoll({ state: "connected", email: "reader@example.test" });
  });
  expect(screen.queryByText("reader@example.test")).not.toBeInTheDocument();
  expect(screen.getByText("Gmail disconnected")).toBeInTheDocument();
  view.unmount();
  vi.useRealTimers();
});
