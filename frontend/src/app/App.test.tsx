import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "./App";
import { getHealth } from "../lib/ipc/client";

vi.mock("../lib/ipc/client", () => ({ getHealth: vi.fn() }));
describe("desktop status", () => {
  beforeEach(() => {
    vi.resetAllMocks();
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
  it("does not claim connectivity after a bridge failure", async () => {
    vi.mocked(getHealth).mockRejectedValue(new Error("offline"));
    render(<App />);
    expect(await screen.findByText("Backend disconnected")).toBeInTheDocument();
    expect(screen.getByText("Database disconnected")).toBeInTheDocument();
  });
});
