import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { UpdateNotification } from "./update-notification";

type DownloadedHandler = (info: { version: string; releaseNotes?: string }) => void;

const ctx = vi.hoisted(() => ({
  handlers: [] as DownloadedHandler[],
  installUpdate: vi.fn(),
  openExternal: vi.fn(),
}));

function emitUpdateDownloaded(version: string) {
  for (const handler of ctx.handlers) handler({ version });
}

describe("UpdateNotification", () => {
  beforeEach(() => {
    ctx.handlers = [];
    ctx.installUpdate.mockReset();
    ctx.openExternal.mockReset();

    Object.defineProperty(window, "updater", {
      configurable: true,
      value: {
        onUpdateDownloaded: (handler: DownloadedHandler) => {
          ctx.handlers.push(handler);
          return () => {
            ctx.handlers = ctx.handlers.filter((entry) => entry !== handler);
          };
        },
        installUpdate: ctx.installUpdate,
      },
    });
    Object.defineProperty(window, "desktopAPI", {
      configurable: true,
      value: { openExternal: ctx.openExternal },
    });
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("renders nothing until an update has been downloaded", () => {
    const { container } = render(<UpdateNotification />);
    expect(container).toBeEmptyDOMElement();
  });

  it("offers a restart once the update is downloaded", async () => {
    render(<UpdateNotification />);
    emitUpdateDownloaded("0.3.50");

    expect(await screen.findByText("Update ready")).toBeInTheDocument();
    expect(screen.getByText(/v0\.3\.50 will be applied/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Restart now" })).toBeEnabled();
  });

  it("surfaces the failure instead of leaving the button dead", async () => {
    // The 0.3.49→0.3.50 bug: quitAndInstall silently no-ops on an ad-hoc
    // build, so the click must end somewhere the user can see.
    const user = userEvent.setup();
    ctx.installUpdate.mockResolvedValue({
      ok: false,
      error: "Couldn't apply the update automatically: code signature rejected",
    });

    render(<UpdateNotification />);
    emitUpdateDownloaded("0.3.50");
    await user.click(await screen.findByRole("button", { name: "Restart now" }));

    expect(await screen.findByText("Update failed")).toBeInTheDocument();
    expect(screen.getByText(/code signature rejected/)).toBeInTheDocument();
  });

  it("offers a manual download once the install has failed", async () => {
    const user = userEvent.setup();
    ctx.installUpdate.mockResolvedValue({ ok: false, error: "nope" });

    render(<UpdateNotification />);
    emitUpdateDownloaded("0.3.50");
    await user.click(await screen.findByRole("button", { name: "Restart now" }));
    await user.click(
      await screen.findByRole("button", { name: "Download manually" }),
    );

    expect(ctx.openExternal).toHaveBeenCalledWith(
      "https://github.com/jamshidtulaganov/agora-cli/releases/latest",
    );
  });

  it("shows progress and blocks double-clicks while installing", async () => {
    const user = userEvent.setup();
    ctx.installUpdate.mockReturnValue(new Promise(() => {}));

    render(<UpdateNotification />);
    emitUpdateDownloaded("0.3.50");
    await user.click(await screen.findByRole("button", { name: "Restart now" }));

    const button = await screen.findByRole("button", { name: /Restarting/ });
    expect(button).toBeDisabled();
    await waitFor(() => expect(ctx.installUpdate).toHaveBeenCalledTimes(1));
  });

  it("can be dismissed", async () => {
    const user = userEvent.setup();
    render(<UpdateNotification />);
    emitUpdateDownloaded("0.3.50");

    await user.click(await screen.findByRole("button", { name: "Later" }));
    expect(screen.queryByText("Update ready")).not.toBeInTheDocument();
  });
});
