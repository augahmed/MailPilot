import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useState } from "react";
import { App } from "./App";
import {
  gmailCommand,
  getHealth,
  syncCommand,
  getMailbox,
} from "../lib/ipc/client";
import { features } from "./navigation";
import type { OpenMail } from "../lib/ipc/reader";

vi.mock("../features/indexing/LocalEmailIndex", () => ({
  LocalEmailIndex: () => <section>Existing index controls</section>,
}));
vi.mock("../features/categories/Categories", () => ({
  Categories: () => <section>Existing category editor</section>,
}));
vi.mock("../features/classification/ClassificationReview", () => ({
  ClassificationReview: () => <section>Existing classification review</section>,
}));
vi.mock("../features/organization/Organization", () => ({
  Organization: () => <section>Existing organization controls</section>,
}));
vi.mock("../features/ai/AISettings", () => ({
  AISettings: () => <section>Existing AI settings</section>,
}));
vi.mock("../features/replies/AIMaintenance", () => ({
  AIMaintenance: () => <section>Existing historical recovery</section>,
}));
vi.mock("../features/search/SemanticSearch", () => ({
  SemanticSearch: () => {
    const [query, setQuery] = useState("");
    return (
      <label>
        Existing search
        <input
          value={query}
          onChange={(event) => setQuery(event.target.value)}
        />
      </label>
    );
  },
}));
vi.mock("../features/ai/EmailQuestion", () => ({
  EmailQuestion: ({ onOpen }: { onOpen: OpenMail }) => (
    <button onClick={(event) => onOpen("a", "1", event.currentTarget)}>
      Existing evidence questions
    </button>
  ),
}));
vi.mock("../features/reader/MailReader", () => ({
  MailReader: ({ onBack }: { onBack: () => void }) => (
    <button onClick={onBack}>Back to mailbox</button>
  ),
}));
vi.mock("../lib/ipc/client", () => ({
  getHealth: vi.fn(),
  gmailCommand: vi.fn(),
  syncCommand: vi.fn(),
  getMailbox: vi.fn(),
}));

beforeEach(() => {
  vi.resetAllMocks();
  vi.spyOn(window, "scrollTo").mockImplementation(() => {});
  vi.mocked(getHealth).mockResolvedValue({
    name: "MailPilot",
    version: "0.1.0",
    protocolVersion: 1,
    backend: "connected",
    database: "connected",
    schemaVersion: 20,
  });
  vi.mocked(gmailCommand).mockResolvedValue({ state: "disconnected" });
  vi.mocked(syncCommand).mockResolvedValue({
    state: "completed",
    accountId: "a",
    limit: 500,
    discovered: 1,
    imported: 1,
    updated: 0,
    errors: 0,
    capped: false,
  });
  vi.mocked(getMailbox).mockResolvedValue({
    accountId: "a",
    rows: [
      {
        id: "1",
        sender: "Sender",
        subject: "Cached message",
        receivedAt: 1700000000000,
        bodyState: "complete",
        phishing: { score: 18, risk: "LOW", version: "v1", reasons: [] },
      },
    ],
    hasMore: true,
  });
});
afterEach(() => {
  vi.useRealTimers();
});
const connected = () =>
  vi
    .mocked(gmailCommand)
    .mockResolvedValue({ state: "connected", email: "reader@example.test" });
const account = () => screen.getByRole("button", { name: /Gmail account/ });

describe("authentication and dashboard", () => {
  it("gates the dashboard and all feature requests during disconnected launch", async () => {
    render(<App />);
    expect(await screen.findByText("Gmail disconnected")).toBeVisible();
    expect(screen.getByRole("button", { name: /Connect Gmail/ })).toBeEnabled();
    expect(screen.queryByRole("navigation")).toBeNull();
    expect(screen.queryByText("Existing index controls")).toBeNull();
    expect(getMailbox).not.toHaveBeenCalled();
    expect(syncCommand).not.toHaveBeenCalled();
  });
  it("waits for verified browser authentication before opening the dashboard", async () => {
    vi.useFakeTimers();
    render(<App />);
    await act(async () => {});
    vi.mocked(gmailCommand).mockResolvedValue({ state: "connecting" });
    fireEvent.click(screen.getByRole("button", { name: /Connect Gmail/ }));
    await act(async () => {});
    expect(screen.getByText(/Waiting for Google sign-in/)).toBeVisible();
    expect(getMailbox).not.toHaveBeenCalled();
    connected();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1500);
    });
    expect(screen.getByRole("heading", { name: "Your Emails" })).toBeVisible();
    expect(screen.queryByRole("button", { name: /Connect Gmail/ })).toBeNull();
    expect(gmailCommand).toHaveBeenCalledWith("gmail_connect");
  });
  it("restores an existing verified session without starting a new OAuth flow", async () => {
    connected();
    render(<App />);
    expect(
      await screen.findByRole("heading", { name: "Your Emails" }),
    ).toBeVisible();
    expect(
      screen.getByRole("button", { name: /reader@example.test/ }),
    ).toBeVisible();
    expect(gmailCommand).not.toHaveBeenCalledWith("gmail_connect");
    await waitFor(() => expect(getMailbox).toHaveBeenCalledTimes(1));
    expect(screen.queryByText("Existing evidence questions")).toBeNull();
  });
  it("retains authorized cached access through checks and transient failures, but clears revoked sessions", async () => {
    vi.useFakeTimers();
    connected();
    render(<App />);
    await act(async () => {});
    fireEvent.click(account());
    vi.mocked(gmailCommand).mockResolvedValue({ state: "checking" });
    fireEvent.click(screen.getByRole("button", { name: "Check Connection" }));
    await act(async () => {});
    expect(screen.getByRole("heading", { name: "Your Emails" })).toBeVisible();
    vi.mocked(gmailCommand).mockResolvedValue({
      state: "error",
      code: "authentication_failed",
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1500);
    });
    expect(
      screen.getByText(/authorized local cache remains available/),
    ).toBeVisible();
    expect(screen.getByText("Cached message")).toBeVisible();
    vi.mocked(gmailCommand).mockResolvedValue({
      state: "reauth_required",
      code: "reauth_required",
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1500);
    });
    expect(screen.queryByText("Cached message")).toBeNull();
    expect(screen.getByRole("button", { name: /Connect Gmail/ })).toBeEnabled();
  });
  it("does not unlock cached access after an unverified cold-start network failure", async () => {
    vi.mocked(gmailCommand).mockResolvedValue({
      state: "error",
      code: "authentication_failed",
    });
    render(<App />);
    expect(
      await screen.findByRole("button", { name: "Retry connection" }),
    ).toBeVisible();
    expect(getMailbox).not.toHaveBeenCalled();
  });
  it("keeps bridge errors readable without claiming connectivity or exposing private errors", async () => {
    vi.mocked(getHealth).mockRejectedValue(new Error("private error"));
    vi.mocked(gmailCommand).mockRejectedValue(new Error("private error"));
    render(<App />);
    expect(
      await screen.findByText(/Desktop backend unavailable/),
    ).toBeVisible();
    expect(screen.getByText("Gmail status unavailable")).toBeVisible();
    expect(screen.queryByText("private error")).toBeNull();
  });
});

it("opens all five existing workflows and returns focus to the originating card", async () => {
  connected();
  render(<App />);
  await screen.findByRole("heading", { name: "Your Emails" });
  const content = {
    index: "Existing index controls",
    search: "Existing search",
    ask: "Existing evidence questions",
    categories: "Existing category editor",
    organization: "Existing organization controls",
  };
  for (const feature of features) {
    const trigger = screen.getByRole("button", {
      name: new RegExp(feature.title),
    });
    fireEvent.click(trigger);
    expect(screen.getByRole("heading", { name: feature.title })).toBeVisible();
    expect(screen.getByText(content[feature.id])).toBeVisible();
    expect(screen.queryByRole("heading", { name: "Your Emails" })).toBeNull();
    if (feature.id === "categories")
      expect(screen.getByText("Existing classification review")).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: /Back to dashboard/ }));
    await waitFor(() => expect(trigger).toHaveFocus());
  }
});
it("preserves search input, mailbox pagination and reader focus across navigation", async () => {
  connected();
  render(<App />);
  await screen.findByText("Cached message");
  fireEvent.click(screen.getByRole("button", { name: "Next" }));
  await screen.findByText("Page 2");
  const message = await screen.findByRole("button", { name: "Cached message" });
  fireEvent.click(message);
  fireEvent.click(screen.getByRole("button", { name: "Back to mailbox" }));
  await waitFor(() => expect(message).toHaveFocus());
  fireEvent.click(screen.getByRole("button", { name: /Email Search/ }));
  fireEvent.change(screen.getByLabelText("Existing search"), {
    target: { value: "invoice" },
  });
  fireEvent.click(screen.getByRole("button", { name: /Back to dashboard/ }));
  expect(screen.getByText("Page 2")).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: /Email Search/ }));
  expect(screen.getByLabelText("Existing search")).toHaveValue("invoice");
});
it("syncs and displays the backend phishing score without changing it", async () => {
  connected();
  render(<App />);
  expect(await screen.findByText("Low · 18/100")).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "Sync Gmail" }));
  await waitFor(() => expect(syncCommand).toHaveBeenCalledWith(true));
});
it("confirms disconnect, clears feature state, and returns to the landing page", async () => {
  connected();
  render(<App />);
  await screen.findByRole("heading", { name: "Your Emails" });
  fireEvent.click(account());
  fireEvent.click(screen.getByRole("button", { name: "Disconnect" }));
  expect(gmailCommand).not.toHaveBeenCalledWith("gmail_disconnect");
  expect(screen.getByRole("button", { name: "Cancel" })).toHaveFocus();
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  fireEvent.click(screen.getByRole("button", { name: "Disconnect" }));
  vi.mocked(gmailCommand).mockResolvedValue({ state: "disconnected" });
  fireEvent.click(screen.getByRole("button", { name: "Confirm disconnect" }));
  expect(await screen.findByText("Gmail disconnected")).toBeVisible();
  expect(screen.queryByText("Cached message")).toBeNull();
  expect(screen.queryByText("reader@example.test")).toBeNull();
});
it("keeps settings and historical recovery accessible and closes the dropdown with Escape", async () => {
  connected();
  render(<App />);
  await screen.findByRole("heading", { name: "Your Emails" });
  const trigger = account();
  fireEvent.click(trigger);
  fireEvent.keyDown(screen.getByRole("button", { name: "Check Connection" }), {
    key: "Escape",
  });
  expect(trigger).toHaveFocus();
  expect(trigger).toHaveAttribute("aria-expanded", "false");
  for (const [name, content] of [
    ["Optional AI settings", "Existing AI settings"],
    ["Historical recovery", "Existing historical recovery"],
  ] as const) {
    fireEvent.click(trigger);
    fireEvent.click(screen.getByRole("button", { name }));
    expect(screen.getByText(content)).toBeVisible();
    expect(
      screen.getByRole("button", { name: /Back to dashboard/ }),
    ).toBeVisible();
  }
});

it.each([
  "credential_store_unavailable",
  "required_scope_missing",
  "consent_denied",
] as const)("clears cached UI after definitive %s rejection", async (code) => {
  vi.useFakeTimers();
  connected();
  render(<App />);
  await act(async () => {});
  expect(screen.getByRole("heading", { name: "Your Emails" })).toBeVisible();
  vi.mocked(gmailCommand).mockResolvedValue({ state: "error", code });
  await act(async () => {
    await vi.advanceTimersByTimeAsync(1500);
  });
  expect(screen.queryByRole("heading", { name: "Your Emails" })).toBeNull();
  expect(screen.queryByText("Cached message")).toBeNull();
  expect(screen.getByRole("button", { name: /Connect Gmail/ })).toBeEnabled();
});
