import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { render as rtlRender, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@agora/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enSettings from "../../locales/en/settings.json";

const TEST_RESOURCES = { en: { common: enCommon, settings: enSettings } };

function render(ui: React.ReactElement) {
  return rtlRender(ui, {
    wrapper: ({ children }: { children: ReactNode }) => (
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        {children}
      </I18nProvider>
    ),
  });
}

vi.mock("@agora/ui/components/ui/dialog", () => ({
  Dialog: ({ children, open }: { children: ReactNode; open: boolean }) =>
    open ? <div>{children}</div> : null,
  DialogContent: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  DialogHeader: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  DialogTitle: ({ children }: { children: ReactNode }) => <h1>{children}</h1>,
  DialogDescription: ({ children }: { children: ReactNode }) => <p>{children}</p>,
  DialogFooter: ({ children }: { children: ReactNode }) => <div>{children}</div>,
}));

import { DeleteAccountDialog } from "./delete-account-dialog";

describe("DeleteAccountDialog", () => {
  it("requires the exact account email before deletion", async () => {
    const user = userEvent.setup();
    const onConfirm = vi.fn();
    render(
      <DeleteAccountDialog
        email="owner@example.com"
        open
        onOpenChange={vi.fn()}
        onConfirm={onConfirm}
      />,
    );

    const input = screen.getByRole("textbox");
    const button = screen.getByRole("button", { name: "Delete account" });
    expect(button).toBeDisabled();

    await user.type(input, "Owner@example.com");
    expect(button).toBeDisabled();

    await user.clear(input);
    await user.type(input, "owner@example.com");
    expect(button).toBeEnabled();
    await user.click(button);
    expect(onConfirm).toHaveBeenCalledTimes(1);
  });

  it("submits with Enter only after the email matches", async () => {
    const user = userEvent.setup();
    const onConfirm = vi.fn();
    render(
      <DeleteAccountDialog
        email="owner@example.com"
        open
        onOpenChange={vi.fn()}
        onConfirm={onConfirm}
      />,
    );

    const input = screen.getByRole("textbox");
    await user.type(input, "owner@example.co{Enter}");
    expect(onConfirm).not.toHaveBeenCalled();
    await user.type(input, "m{Enter}");
    expect(onConfirm).toHaveBeenCalledTimes(1);
  });

  it("disables dismissal and confirmation while deletion is pending", () => {
    render(
      <DeleteAccountDialog
        email="owner@example.com"
        loading
        open
        onOpenChange={vi.fn()}
        onConfirm={vi.fn()}
      />,
    );

    expect(screen.getByRole("button", { name: "Deleting..." })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Cancel" })).toBeDisabled();
  });

  it("clears stale confirmation text after close and reopen", async () => {
    const user = userEvent.setup();
    const props = {
      email: "owner@example.com",
      onOpenChange: vi.fn(),
      onConfirm: vi.fn(),
    };
    const { rerender } = render(<DeleteAccountDialog {...props} open />);
    await user.type(screen.getByRole("textbox"), "owner@example.com");
    expect(screen.getByRole("textbox")).toHaveValue("owner@example.com");

    rerender(<DeleteAccountDialog {...props} open={false} />);
    rerender(<DeleteAccountDialog {...props} open />);
    expect(screen.getByRole("textbox")).toHaveValue("");
  });
});
