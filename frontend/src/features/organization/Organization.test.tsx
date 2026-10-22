import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
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
it("explains multiple suggestions and requires a primary before label preview", async () => {
  vi.mocked(organize).mockResolvedValue({
    ...page,
    rows: [
      {
        ...page.rows[0]!,
        change: "multiple_suggestions",
        proposedLabel: "",
        confidence: 0,
      },
    ],
  });
  render(<Organization />);
  await screen.findByText(/Multiple suggestions — choose a primary category/);
  expect(
    screen.queryByRole("checkbox", { name: "Select Your receipt" }),
  ).not.toBeInTheDocument();
});
it("requires selection, defaults to dry run, and never applies a dry run", async () => {
  render(<Organization />);
  await screen.findByText("Your receipt");
  await waitFor(() =>
    expect(
      screen.getByRole("button", { name: "Preview label changes (Dry Run)" }),
    ).toBeDisabled(),
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
  fireEvent.click(
    screen.getByRole("button", { name: "Preview label changes (Dry Run)" }),
  );
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
    screen.getByRole("button", { name: "Preview label changes (Dry Run)" }),
  ).toBeEnabled();
});
it("shows exact operations and requires explicit confirmation for application", async () => {
  render(<Organization />);
  await screen.findByText("Your receipt");
  fireEvent.click(
    screen.getByRole("checkbox", { name: "Select Your receipt" }),
  );
  vi.mocked(organize).mockResolvedValue({
    ...preview,
    expiresAt: Date.now() + 300000,
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Prepare apply preview" }),
  );
  const apply = await screen.findByRole("button", {
    name: "Apply selected labels",
  });
  expect(apply).toBeDisabled();
  expect(
    screen.getByText(/Create label: MailPilot\/Receipts/),
  ).toBeInTheDocument();
  expect(screen.getByRole("cell", { name: "INBOX, Keep" })).toBeInTheDocument();
  expect(
    screen.getByRole("cell", { name: "Evidence score 0.600" }),
  ).toBeInTheDocument();
  fireEvent.click(
    screen.getByRole("checkbox", {
      name: "I confirm these Gmail label changes.",
    }),
  );
  expect(apply).toBeEnabled();
  vi.mocked(organize).mockResolvedValue({ ...preview, state: "applied" });
  fireEvent.click(apply);
  expect(organize).not.toHaveBeenCalledWith(
    expect.objectContaining({ operation: "apply" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Confirm and apply" }));
  await screen.findByText("Changes applied");
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

it("ignores an older initial status after the user reviews a newer preview", async () => {
  let resolveOldStatus!: (value: OrganizationResponse) => void;
  const oldStatus = new Promise<OrganizationResponse>((resolve) => {
    resolveOldStatus = resolve;
  });
  const reviewed = {
    ...preview,
    id: "reviewed-action",
    expiresAt: Date.now() + 300000,
    rows: preview.rows.map((row) => ({
      ...row,
      subject: "New reviewed preview",
    })),
  };
  vi.mocked(organize).mockImplementation(async (request) => {
    if (request.operation === "list") return page;
    if (request.operation === "status") return oldStatus;
    return reviewed;
  });
  render(<Organization />);
  await screen.findByText("Your receipt");
  fireEvent.click(
    screen.getByRole("checkbox", { name: "Select Your receipt" }),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Prepare apply preview" }),
  );
  await screen.findByText("New reviewed preview");
  fireEvent.click(
    screen.getByRole("checkbox", {
      name: "I confirm these Gmail label changes.",
    }),
  );
  await act(async () => {
    resolveOldStatus({ ...reviewed, id: "old-action", rows: preview.rows });
  });
  expect(screen.getByText("New reviewed preview")).toBeInTheDocument();
  expect(screen.queryByText("Your receipt")).not.toBeInTheDocument();
  fireEvent.click(
    screen.getByRole("button", { name: "Apply selected labels" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Confirm and apply" }));
  await waitFor(() =>
    expect(organize).toHaveBeenCalledWith(
      expect.objectContaining({
        operation: "apply",
        actionId: "reviewed-action",
      }),
    ),
  );
});

it("reports cancellation transport failures instead of rejecting silently", async () => {
  let finishStatus!: (value: OrganizationResponse) => void;
  const working = new Promise<OrganizationResponse>((resolve) => {
    finishStatus = resolve;
  });
  let previewRequested = false;
  vi.mocked(organize).mockImplementation(async (request) => {
    if (request.operation === "list") return page;
    if (request.operation === "cancel")
      throw new Error("synthetic transport failure");
    if (request.operation === "dry_run") {
      previewRequested = true;
      return { ...idle, state: "running" };
    }
    return previewRequested ? working : idle;
  });
  const { unmount } = render(<Organization />);
  await screen.findByText("Your receipt");
  fireEvent.click(
    screen.getByRole("checkbox", { name: "Select Your receipt" }),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Preview label changes (Dry Run)" }),
  );
  fireEvent.click(
    await screen.findByRole("button", { name: "Stop organization" }),
  );
  await screen.findByText(
    "Could not stop organization. Refresh batch status to check recorded outcomes.",
  );
  unmount();
  await act(async () => {
    finishStatus(idle);
  });
});

it("does not overlap slow background status polls", async () => {
  vi.useFakeTimers();
  try {
    await act(async () => {
      render(<Organization />);
    });
    let resolveStatus!: (value: OrganizationResponse) => void;
    vi.mocked(organize).mockClear();
    vi.mocked(organize).mockReturnValue(
      new Promise((resolve) => {
        resolveStatus = resolve;
      }),
    );
    await act(async () => {
      await vi.advanceTimersByTimeAsync(15000);
    });
    expect(organize).toHaveBeenCalledTimes(1);
    await act(async () => {
      resolveStatus(idle);
    });
    vi.mocked(organize).mockResolvedValue(idle);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });
    expect(organize).toHaveBeenCalledTimes(2);
  } finally {
    vi.useRealTimers();
  }
});

import riskFixture from "../../../../contracts/organization-phishing-v1.json";
it("reviews phishing scores before a confirmed label-only action and defaults to Dry Run", async () => {
  const riskPreview = {
    ...riskFixture.response,
    expiresAt: Date.now() + 300000,
  } as OrganizationResponse;
  vi.mocked(organize).mockImplementation(async (request) => {
    if (request.operation === "list")
      return request.source === "phishing"
        ? {
            ...idle,
            rows: riskPreview.rows.map((row) => ({
              ...row,
              change: "candidate" as const,
            })),
          }
        : page;
    if (request.operation === "preview") return riskPreview;
    if (request.operation === "apply")
      return { ...riskPreview, state: "applied" };
    return idle;
  });
  render(<Organization />);
  await screen.findByText("Your receipt");
  fireEvent.change(
    screen.getByRole("combobox", { name: "Review suggestions" }),
    { target: { value: "phishing" } },
  );
  await screen.findByText("80 HIGH");
  expect(screen.getByText("HIGH threshold: 60")).toBeInTheDocument();
  expect(
    screen.getByText("+25 Reply-To and sender use different sites"),
  ).toBeInTheDocument();
  expect(
    screen.getByRole("button", { name: "Preview label changes (Dry Run)" }),
  ).toBeDisabled();
  fireEvent.click(
    screen.getByRole("checkbox", { name: "Select Your receipt" }),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Prepare apply preview" }),
  );
  const apply = await screen.findByRole("button", {
    name: "Apply selected labels",
  });
  expect(apply).toBeDisabled();
  expect(organize).toHaveBeenLastCalledWith(
    expect.objectContaining({ operation: "preview", source: "phishing" }),
  );
  expect(
    screen.getByText(/Create label: Potential Phishing/),
  ).toBeInTheDocument();
  expect(
    screen.getByRole("list", { name: "Phishing risk reasons" }),
  ).toHaveTextContent("Credential-related language");
  fireEvent.click(
    screen.getByRole("checkbox", {
      name: "I confirm these Gmail label changes.",
    }),
  );
  fireEvent.click(apply);
  expect(organize).not.toHaveBeenCalledWith(
    expect.objectContaining({ operation: "apply" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Confirm and apply" }));
  await screen.findByText("Changes applied");
  expect(organize).toHaveBeenLastCalledWith(
    expect.objectContaining({
      operation: "apply",
      source: undefined,
      digest: riskPreview.digest,
    }),
  );
});

it("moves from selection through read-only preview, fresh approval, results, and a separately confirmed undo", async () => {
  const fresh = { ...preview, expiresAt: Date.now() + 300000 };
  const undo = {
    ...fresh,
    id: "undo-action",
    digest: "f".repeat(64),
    kind: "undo" as const,
    steps: [{ ...preview.steps[1]!, kind: "remove" as const }],
    rows: preview.rows.map((row) => ({ ...row, change: "remove" as const })),
  };
  vi.mocked(organize).mockImplementation(async (request) => {
    if (request.operation === "list") return page;
    if (request.operation === "dry_run")
      return { ...fresh, state: "dry_run", id: "", digest: "", dryRun: true };
    if (request.operation === "preview") return fresh;
    if (request.operation === "undo_preview") return undo;
    if (request.operation === "apply")
      return {
        ...(request.actionId === undo.id ? undo : fresh),
        state: "applied",
      };
    return idle;
  });
  render(<Organization />);
  await screen.findByText("Your receipt");
  const progress = screen.getByRole("list", { name: "Organization progress" });
  const activeStep = (name: string) =>
    expect(within(progress).getByText(name).closest("li")).toHaveAttribute(
      "aria-current",
      "step",
    );
  activeStep("Select emails");
  fireEvent.click(
    screen.getByRole("checkbox", { name: "Select Your receipt" }),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Preview label changes (Dry Run)" }),
  );
  await screen.findByText(/Dry Run complete/);
  activeStep("Preview labels");
  expect(
    screen.queryByRole("button", { name: "Apply selected labels" }),
  ).toBeNull();
  fireEvent.click(
    screen.getByRole("button", { name: "Prepare apply preview" }),
  );
  await screen.findByRole("button", { name: "Apply selected labels" });
  activeStep("Confirm changes");
  fireEvent.click(
    screen.getByRole("checkbox", {
      name: "I confirm these Gmail label changes.",
    }),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Apply selected labels" }),
  );
  const dialog = screen.getByRole("dialog", {
    name: "Apply these Gmail label changes?",
  });
  expect(within(dialog).getByRole("button", { name: "Cancel" })).toHaveFocus();
  fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
  expect(organize).not.toHaveBeenCalledWith(
    expect.objectContaining({ operation: "apply" }),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Apply selected labels" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Confirm and apply" }));
  await screen.findByText("Changes applied");
  activeStep("Results & undo");
  expect(screen.getByRole("list", { name: "Recorded outcomes" })).toBeVisible();
  fireEvent.click(
    screen.getByRole("button", { name: "Preview undo last batch" }),
  );
  await screen.findByRole("button", { name: "Confirm undo" });
  fireEvent.click(
    screen.getByRole("checkbox", { name: "I confirm these undo changes." }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Confirm undo" }));
  expect(
    screen.getByRole("dialog", { name: "Undo these Gmail label additions?" }),
  ).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "Confirm and undo" }));
  await waitFor(() =>
    expect(organize).toHaveBeenLastCalledWith(
      expect.objectContaining({
        operation: "apply",
        actionId: "undo-action",
        digest: undo.digest,
        emailIds: [],
      }),
    ),
  );
});

it("closes a confirmation dialog when its preview expires without dispatching changes", async () => {
  vi.useFakeTimers();
  try {
    const fresh = { ...preview, expiresAt: Date.now() + 1500 };
    vi.mocked(organize).mockImplementation(async (request) =>
      request.operation === "list"
        ? page
        : request.operation === "preview"
          ? fresh
          : idle,
    );
    render(<Organization />);
    await act(async () => {});
    fireEvent.click(
      screen.getByRole("checkbox", { name: "Select Your receipt" }),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Prepare apply preview" }),
    );
    await act(async () => {});
    fireEvent.click(
      screen.getByRole("checkbox", {
        name: "I confirm these Gmail label changes.",
      }),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Apply selected labels" }),
    );
    expect(screen.getByRole("dialog")).toBeVisible();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000);
    });
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(
      screen.getByRole("button", { name: "Apply selected labels" }),
    ).toBeDisabled();
    expect(screen.getByText(/This preview has expired/)).toBeVisible();
    expect(organize).not.toHaveBeenCalledWith(
      expect.objectContaining({ operation: "apply" }),
    );
  } finally {
    vi.useRealTimers();
  }
});

it("keeps uncertain outcomes visible and prevents selection resets from hiding them", async () => {
  vi.mocked(organize).mockImplementation(async (request) =>
    request.operation === "list"
      ? page
      : {
          ...preview,
          state: "partial",
          steps: preview.steps.map((operation) => ({
            ...operation,
            state: "uncertain",
          })),
        },
  );
  render(<Organization />);
  await screen.findByRole("heading", {
    name: "Check interrupted changes before continuing",
  });
  expect(screen.getByRole("list", { name: "Recorded outcomes" })).toBeVisible();
  expect(
    screen.getByRole("button", { name: "Refresh organization" }),
  ).toBeDisabled();
  expect(
    screen.getByRole("combobox", { name: "Review suggestions" }),
  ).toBeDisabled();
  expect(
    screen.queryByRole("button", { name: "Back to selection" }),
  ).toBeNull();
  expect(
    screen.queryByRole("button", { name: "Start another selection" }),
  ).toBeNull();
  fireEvent.click(
    screen.getByRole("button", { name: "Reconcile interrupted batch" }),
  );
  await waitFor(() =>
    expect(organize).toHaveBeenLastCalledWith(
      expect.objectContaining({ operation: "reconcile" }),
    ),
  );
  expect(organize).not.toHaveBeenCalledWith(
    expect.objectContaining({ operation: "apply" }),
  );
});

it("revokes an open confirmation when outcomes become uncertain and requires opening a new dialog after recovery", async () => {
  vi.useFakeTimers();
  try {
    const fresh = { ...preview, expiresAt: Date.now() + 300000 };
    vi.mocked(organize).mockImplementation(async (request) =>
      request.operation === "list"
        ? page
        : request.operation === "preview"
          ? fresh
          : idle,
    );
    render(<Organization />);
    await act(async () => {});
    fireEvent.click(
      screen.getByRole("checkbox", { name: "Select Your receipt" }),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Prepare apply preview" }),
    );
    await act(async () => {});
    fireEvent.click(
      screen.getByRole("checkbox", {
        name: "I confirm these Gmail label changes.",
      }),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Apply selected labels" }),
    );
    expect(screen.getByRole("dialog")).toBeVisible();
    vi.mocked(organize).mockResolvedValue({ ...fresh, state: "uncertain" });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(
      screen.getByRole("heading", {
        name: "Check interrupted changes before continuing",
      }),
    ).toBeVisible();
    vi.mocked(organize).mockResolvedValue(fresh);
    fireEvent.click(
      screen.getByRole("button", { name: "Refresh batch status" }),
    );
    await act(async () => {});
    fireEvent.click(
      screen.getByRole("checkbox", {
        name: "I confirm these Gmail label changes.",
      }),
    );
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(organize).not.toHaveBeenCalledWith(
      expect.objectContaining({ operation: "apply" }),
    );
  } finally {
    vi.useRealTimers();
  }
});
