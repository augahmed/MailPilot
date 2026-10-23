import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { AccountDropdown } from "./AccountDropdown";
import type { GmailConnectionState } from "./useGmailConnection";

function fixture() {
  const connection: GmailConnectionState = {
    email: "reader@example.test",
    status: { state: "connected", email: "reader@example.test" },
    error: false,
    busy: false,
    act: vi.fn().mockResolvedValue(undefined),
  };
  render(
    <>
      <AccountDropdown connection={connection} navigate={vi.fn()} />
      <button>Outside dropdown</button>
    </>,
  );
  const trigger = screen.getByRole("button", { name: /Gmail account/ });
  trigger.focus();
  fireEvent.click(trigger);
  return { connection, trigger };
}

describe("account dropdown disconnect", () => {
  it("keeps confirmation open when a focus transfer reports no blur destination", () => {
    const { connection } = fixture();
    const disconnect = screen.getByRole("button", { name: "Disconnect" });
    disconnect.focus();
    fireEvent.click(disconnect);
    expect(
      screen.getByRole("dialog", { name: "Disconnect Gmail?" }),
    ).toHaveAttribute("open");
    const cancel = screen.getByRole("button", { name: "Cancel" });
    expect(cancel).toHaveFocus();
    // A transient blur without a destination must not dismiss the modal.
    fireEvent.blur(cancel, { relatedTarget: null });
    expect(
      screen.getByRole("heading", { name: "Disconnect Gmail?" }),
    ).toBeVisible();
    expect(connection.act).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Confirm disconnect" }));
    expect(connection.act).toHaveBeenCalledExactlyOnceWith("gmail_disconnect");
  });

  it("cancels without disconnecting and restores focus to Disconnect", async () => {
    const { connection } = fixture();
    fireEvent.click(screen.getByRole("button", { name: "Disconnect" }));
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Disconnect" })).toHaveFocus(),
    );
    expect(connection.act).not.toHaveBeenCalled();
  });

  it("cancels the modal with Escape and restores focus without disconnecting", async () => {
    const { connection } = fixture();
    fireEvent.click(screen.getByRole("button", { name: "Disconnect" }));
    fireEvent(
      screen.getByRole("dialog", { name: "Disconnect Gmail?" }),
      new Event("cancel", { cancelable: true }),
    );
    expect(screen.queryByRole("dialog")).toBeNull();
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Disconnect" })).toHaveFocus(),
    );
    expect(connection.act).not.toHaveBeenCalled();
  });

  it("still closes on an actual keyboard focus move outside", () => {
    const { trigger } = fixture();
    screen.getByRole("button", { name: "Disconnect" }).focus();
    act(() => screen.getByRole("button", { name: "Outside dropdown" }).focus());
    expect(trigger).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByRole("button", { name: "Disconnect" })).toBeNull();
  });

  it("closes confirmation on an outside pointer press without disconnecting", () => {
    const { connection, trigger } = fixture();
    fireEvent.click(screen.getByRole("button", { name: "Disconnect" }));
    fireEvent.pointerDown(
      screen.getByRole("button", { name: "Outside dropdown" }),
    );
    expect(trigger).toHaveAttribute("aria-expanded", "false");
    expect(connection.act).not.toHaveBeenCalled();
  });
});
