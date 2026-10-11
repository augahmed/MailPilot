import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { Organization } from "./Organization";
import { organize, type OrganizationResponse } from "../../lib/ipc/client";
import fixture from "../../../../contracts/organization-v1.json";
vi.mock("../../lib/ipc/client", () => ({ organize: vi.fn() }));
const preview = fixture.response as OrganizationResponse;
const idle: OrganizationResponse = {
  ...preview,
  state: "idle",
  id: "",
  digest: "",
  kind: "",
  expiresAt: 0,
  rows: [],
  steps: [],
};
const page: OrganizationResponse = {
  ...idle,
  rows: preview.rows.map((row) => ({ ...row, change: "candidate" })),
};
beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(organize).mockImplementation(async (request) =>
    request.operation === "list" ? page : idle,
  );
});
it("requires selection, defaults to dry run, and never applies a dry run", async () => {
  render(<Organization />);
  await screen.findByText("Your receipt");
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Run Dry Run" })).toBeDisabled(),
  );
  fireEvent.click(
    screen.getByRole("checkbox", { name: "Select Your receipt" }),
  );
  vi.mocked(organize).mockResolvedValue({
    ...preview,
    id: "",
    digest: "",
    state: "dry_run",
    dryRun: true,
  });
  fireEvent.click(screen.getByRole("button", { name: "Run Dry Run" }));
  await screen.findByText(/Dry Run complete/);
  expect(
    screen.queryByRole("button", { name: "Apply selected labels" }),
  ).not.toBeInTheDocument();
  expect(organize).toHaveBeenLastCalledWith(
    expect.objectContaining({
      operation: "dry_run",
      emailIds: ["fixture-email"],
    }),
  );
  expect(
    vi
      .mocked(organize)
      .mock.calls.some(([request]) => request.operation === "apply"),
  ).toBe(false);
  fireEvent.click(screen.getByRole("button", { name: "Back to selection" }));
  expect(
    screen.getByRole("checkbox", { name: "Dry Run (read only)" }),
  ).toBeChecked();
});
it("shows exact operations and requires explicit confirmation for application", async () => {
  render(<Organization />);
  await screen.findByText("Your receipt");
  fireEvent.click(
    screen.getByRole("checkbox", { name: "Select Your receipt" }),
  );
  fireEvent.click(
    screen.getByRole("checkbox", { name: "Dry Run (read only)" }),
  );
  vi.mocked(organize).mockResolvedValue({
    ...preview,
    expiresAt: Date.now() + 300000,
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Generate apply preview" }),
  );
  const apply = await screen.findByRole("button", {
    name: "Apply selected labels",
  });
  expect(apply).toBeDisabled();
  expect(
    screen.getByText(/Create label: MailPilot\/Receipts/),
  ).toBeInTheDocument();
  expect(screen.getByRole("cell", { name: "INBOX, Keep" })).toBeInTheDocument();
  expect(screen.getByRole("cell", { name: "60%" })).toBeInTheDocument();
  fireEvent.click(
    screen.getByRole("checkbox", {
      name: "I confirm these Gmail label changes.",
    }),
  );
  expect(apply).toBeEnabled();
  vi.mocked(organize).mockResolvedValue({ ...preview, state: "applied" });
  fireEvent.click(apply);
  await screen.findByText("Batch: applied");
  expect(organize).toHaveBeenLastCalledWith(
    expect.objectContaining({
      operation: "apply",
      actionId: "fixture-action",
      digest: preview.digest,
      emailIds: [],
    }),
  );
});
it("requires a separate undo preview and confirmation", async () => {
  render(<Organization />);
  await screen.findByText("Your receipt");
  vi.mocked(organize).mockResolvedValue({
    ...preview,
    kind: "undo",
    expiresAt: Date.now() + 300000,
    rows: preview.rows.map((row) => ({ ...row, change: "remove" })),
    steps: [{ ...preview.steps[1]!, kind: "remove" }],
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Preview undo last batch" }),
  );
  const undo = await screen.findByRole("button", { name: "Confirm undo" });
  expect(undo).toBeDisabled();
  expect(organize).toHaveBeenLastCalledWith(
    expect.objectContaining({ operation: "undo_preview" }),
  );
  fireEvent.click(
    screen.getByRole("checkbox", { name: "I confirm these undo changes." }),
  );
  expect(undo).toBeEnabled();
});
it("shows interrupted work and reconciles without requesting an apply", async () => {
  vi.mocked(organize).mockImplementation(async (request) =>
    request.operation === "list"
      ? page
      : {
          ...preview,
          state: "uncertain",
          steps: preview.steps.map((step) => ({ ...step, state: "uncertain" })),
        },
  );
  render(<Organization />);
  const reconcile = await screen.findByRole("button", {
    name: "Reconcile interrupted batch",
  });
  fireEvent.click(reconcile);
  await waitFor(() =>
    expect(organize).toHaveBeenLastCalledWith(
      expect.objectContaining({ operation: "reconcile" }),
    ),
  );
  expect(
    screen.queryByRole("button", { name: "Apply selected labels" }),
  ).not.toBeInTheDocument();
});
it("renders hostile mail as escaped text and ignores late responses after disconnect", async () => {
  vi.mocked(organize).mockResolvedValue({
    ...page,
    rows: page.rows.map((row) => ({
      ...row,
      subject: '<img src="https://example.test/tracker">',
    })),
  });
  const { container, unmount } = render(<Organization />);
  await screen.findByText('<img src="https://example.test/tracker">');
  expect(container.querySelector("img")).toBeNull();
  let resolve: (response: OrganizationResponse) => void = () => {};
  vi.mocked(organize).mockReturnValue(
    new Promise((done) => {
      resolve = done;
    }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Refresh organization" }));
  unmount();
  resolve(preview);
  await Promise.resolve();
  expect(screen.queryByText("Your receipt")).not.toBeInTheDocument();
});

it("clears cached mail when the session becomes unauthorized", async () => {
  render(<Organization />);
  await screen.findByText("Your receipt");
  vi.mocked(organize).mockResolvedValue({
    ...idle,
    accountId: "",
    code: "unauthorized",
  });
  fireEvent.click(screen.getByRole("button", { name: "Refresh batch status" }));
  await screen.findByRole("alert");
  expect(screen.queryByText("Your receipt")).not.toBeInTheDocument();
  expect(
    screen.queryByRole("checkbox", { name: "Select Your receipt" }),
  ).not.toBeInTheDocument();
});
