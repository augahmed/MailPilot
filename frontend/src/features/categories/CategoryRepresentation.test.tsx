import {
  fireEvent,
  render,
  screen,
  waitFor,
  act,
} from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import fixture from "../../../../contracts/category-vectors-v1.json";
import reader from "../../../../contracts/mail-reader-v1.json";
import { CategoryRepresentation } from "./CategoryRepresentation";
import {
  manageCategoryVectors,
  type CategoryVectorResponse,
} from "../../lib/ipc/category-vectors";
import { getMailbox } from "../../lib/ipc/client";
import { readCachedMail, type ReadMailResponse } from "../../lib/ipc/reader";
vi.mock("../../lib/ipc/category-vectors", () => ({
  manageCategoryVectors: vi.fn(),
}));
vi.mock("../../lib/ipc/client", () => ({ getMailbox: vi.fn() }));
vi.mock("../../lib/ipc/reader", () => ({ readCachedMail: vi.fn() }));
const category = {
  id: "jobs",
  name: "Jobs",
  description: "Applications and interviews.",
  enabled: true,
  created_at: 1,
  updated_at: 1,
};
const representation = {
  ...fixture.response,
  examples: [],
} as CategoryVectorResponse;
beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(manageCategoryVectors).mockResolvedValue(representation);
  vi.mocked(getMailbox).mockResolvedValue({
    accountId: "account-a",
    rows: [
      {
        id: "email-a",
        subject: "Interview invitation",
        sender: "Hiring",
        receivedAt: 1,
        bodyState: "complete",
      },
    ],
    hasMore: false,
  });
  vi.mocked(readCachedMail).mockResolvedValue({
    ...reader,
    accountId: "account-a",
    emailId: "email-a",
  } as ReadMailResponse);
});
it("shows definition inputs without inference and requires preview then explicit confirmation", async () => {
  render(<CategoryRepresentation accountId="account-a" category={category} />);
  await screen.findByText(
    "No confirmed examples. The definition alone is sufficient.",
  );
  expect(manageCategoryVectors).toHaveBeenCalledExactlyOnceWith({
    operation: "inspect",
    accountId: "account-a",
    categoryId: "jobs",
    emailId: "",
    revision: "",
    approved: false,
  });
  expect(getMailbox).not.toHaveBeenCalled();
  expect(
    screen.getByText(representation.definitionInputs[0]!),
  ).toBeInTheDocument();
  fireEvent.click(
    screen.getByRole("button", { name: "Choose a confirmed example" }),
  );
  fireEvent.click(
    await screen.findByRole("button", {
      name: "Preview example: Interview invitation",
    }),
  );
  const confirm = await screen.findByRole("button", {
    name: "Confirm this email as a Jobs example",
  });
  expect(readCachedMail).toHaveBeenCalledWith("account-a", "email-a");
  expect(manageCategoryVectors).toHaveBeenCalledTimes(1);
  fireEvent.click(confirm);
  await waitFor(() =>
    expect(manageCategoryVectors).toHaveBeenLastCalledWith({
      operation: "confirm_example",
      accountId: "account-a",
      categoryId: "jobs",
      emailId: "email-a",
      revision: reader.revision,
      approved: true,
    }),
  );
  await screen.findByText(/Examples saved locally/);
  expect(
    screen.queryByRole("group", { name: "Confirm category example" }),
  ).not.toBeInTheDocument();
});
it("clears failed or stale confirmations and never accepts another account's mailbox", async () => {
  vi.mocked(getMailbox).mockResolvedValue({
    accountId: "account-b",
    rows: [],
    hasMore: false,
  });
  render(<CategoryRepresentation accountId="account-a" category={category} />);
  fireEvent.click(
    await screen.findByRole("button", { name: "Choose a confirmed example" }),
  );
  await screen.findByText(/Could not complete the local operation/);
  expect(
    screen.queryByRole("group", { name: "Choose example email" }),
  ).not.toBeInTheDocument();
  expect(readCachedMail).not.toHaveBeenCalled();
});
it("discards late example previews after the category panel closes", async () => {
  let resolve!: (v: ReadMailResponse) => void;
  vi.mocked(readCachedMail).mockImplementation(
    () =>
      new Promise((r) => {
        resolve = r;
      }),
  );
  const view = render(
    <CategoryRepresentation accountId="account-a" category={category} />,
  );
  fireEvent.click(
    await screen.findByRole("button", { name: "Choose a confirmed example" }),
  );
  fireEvent.click(
    await screen.findByRole("button", {
      name: "Preview example: Interview invitation",
    }),
  );
  await waitFor(() => expect(readCachedMail).toHaveBeenCalled());
  view.unmount();
  await act(async () => resolve(reader as ReadMailResponse));
  expect(manageCategoryVectors).toHaveBeenCalledTimes(1);
});
it("removes an example only on explicit action", async () => {
  vi.mocked(manageCategoryVectors).mockResolvedValue(
    fixture.response as CategoryVectorResponse,
  );
  render(<CategoryRepresentation accountId="account-a" category={category} />);
  fireEvent.click(
    await screen.findByRole("button", {
      name: "Remove example: Interview invitation",
    }),
  );
  await waitFor(() =>
    expect(manageCategoryVectors).toHaveBeenLastCalledWith({
      operation: "remove_example",
      accountId: "account-a",
      categoryId: "jobs",
      emailId: "email-a",
      revision: "",
      approved: false,
    }),
  );
});
