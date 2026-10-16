import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "./App";
import {
  manageReplies,
  organize,
  reviewClassifications,
  manageCategories,
  gmailCommand,
  getHealth,
  syncCommand,
  getMailbox,
} from "../lib/ipc/client";

vi.mock("../lib/ipc/client", () => ({
  manageReplies: vi.fn(),
  organize: vi.fn(),
  reviewClassifications: vi.fn(),
  manageCategories: vi.fn(),
  getHealth: vi.fn(),
  gmailCommand: vi.fn(),
  syncCommand: vi.fn(),
  getMailbox: vi.fn(),
}));
describe("desktop status", () => {
  beforeEach(() => {
    vi.resetAllMocks();
    vi.mocked(manageReplies).mockResolvedValue({
      accountId: "",
      state: "idle",
      configured: false,
      provider: "",
      model: "",
      external: false,
      jobId: "",
      previewId: "",
      reviewId: "",
      expiresAt: 0,
      context: "",
      instruction: "",
      policy: "",
      to: "",
      subject: "",
      body: "",
      sourceCount: 0,
      truncated: false,
      actionId: "",
      draftId: "",
      reconciled: false,
      undoAvailable: false,
      code: "unauthorized",
    });
    vi.mocked(organize).mockResolvedValue({
      accountId: "",
      state: "idle",
      code: "unauthorized",
      id: "",
      digest: "",
      kind: "",
      expiresAt: 0,
      dryRun: false,
      hasMore: false,
      rows: [],
      steps: [],
    });
    vi.mocked(reviewClassifications).mockResolvedValue({
      accountId: "",
      rows: [],
      hasMore: false,
      processed: 0,
      errors: 0,
      code: "unauthorized",
    });
    vi.mocked(manageCategories).mockResolvedValue({
      accountId: "",
      rows: [],
      hasMore: false,
      code: "unauthorized",
    });
    vi.mocked(syncCommand).mockResolvedValue({
      state: "idle",
      accountId: "",
      limit: 500,
      discovered: 0,
      imported: 0,
      updated: 0,
      errors: 0,
      capped: false,
    });
    vi.mocked(getMailbox).mockResolvedValue({
      accountId: "",
      rows: [],
      hasMore: false,
    });
    vi.mocked(gmailCommand).mockResolvedValue({ state: "disconnected" });
  });
  it("shows confirmed backend and database status", async () => {
    vi.mocked(getHealth).mockResolvedValue({
      name: "MailPilot",
      version: "0.1.0",
      protocolVersion: 1,
      backend: "connected",
      database: "connected",
      schemaVersion: 1,
    });
    render(<App />);
    expect(
      screen.getByRole("heading", { name: "MailPilot" }),
    ).toBeInTheDocument();
    expect(await screen.findByText("Backend connected")).toBeInTheDocument();
    expect(screen.getByText("Database connected")).toBeInTheDocument();
  });
  it("does not expose retired navigation, prompts or creation controls", async () => {
    vi.mocked(getHealth).mockRejectedValue(new Error("offline"));
    render(<App />);
    await screen.findByText("Backend disconnected");
    for (const name of [
      /Ask MailPilot/i,
      /Search your mailbox/i,
      /Draft a reply/i,
      /semantic/i,
      /Index cached mail/i,
      /Generate proposed reply/i,
      /Create Gmail draft/i,
      /Preview AI context/i,
    ]) {
      expect(screen.queryByRole("heading", { name })).toBeNull();
      expect(screen.queryByRole("button", { name })).toBeNull();
    }
    expect(screen.queryByLabelText(/Optional instruction/i)).toBeNull();
    expect(screen.queryByLabelText(/Include earlier cached/i)).toBeNull();
    expect(
      screen.getByRole("heading", { name: "Classification review" }),
    ).toBeInTheDocument();
  });
  it("does not claim connectivity after a bridge failure", async () => {
    vi.mocked(getHealth).mockRejectedValue(new Error("offline"));
    render(<App />);
    expect(await screen.findByText("Backend disconnected")).toBeInTheDocument();
    expect(screen.getByText("Database disconnected")).toBeInTheDocument();
  });
});
