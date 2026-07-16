import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { BrowserWindow, WebContents } from "electron";

type Handler = (...args: unknown[]) => void;

const ctx = vi.hoisted(() => ({
  handlers: new Map<string, Handler[]>(),
  ipcHandle: vi.fn(),
  checkForUpdates: vi.fn(async () => ({
    updateInfo: { version: "0.3.18" },
    isUpdateAvailable: false,
  })),
  downloadUpdate: vi.fn(),
  quitAndInstall: vi.fn(),
  getVersion: vi.fn(() => "0.3.17"),
}));

vi.mock("electron-updater", () => {
  const autoUpdater = {
    autoDownload: false,
    autoInstallOnAppQuit: false,
    channel: undefined as string | undefined,
    logger: undefined as unknown,
    on: vi.fn((event: string, handler: Handler) => {
      const handlers = ctx.handlers.get(event) ?? [];
      handlers.push(handler);
      ctx.handlers.set(event, handlers);
      return autoUpdater;
    }),
    checkForUpdates: ctx.checkForUpdates,
    downloadUpdate: ctx.downloadUpdate,
    quitAndInstall: ctx.quitAndInstall,
  };
  return { autoUpdater };
});

vi.mock("electron-log", () => ({
  default: {
    error: vi.fn(),
    warn: vi.fn(),
    info: vi.fn(),
    transports: { file: { level: "info" } },
  },
}));

vi.mock("electron", () => ({
  app: {
    getVersion: ctx.getVersion,
  },
  BrowserWindow: class BrowserWindow {},
  ipcMain: {
    handle: ctx.ipcHandle,
  },
}));

import { setupAutoUpdater } from "./updater";

function emitUpdater(event: string, ...args: unknown[]) {
  for (const handler of ctx.handlers.get(event) ?? []) {
    handler(...args);
  }
}

function makeWindow() {
  const send = vi.fn();
  return {
    win: {
      isDestroyed: () => false,
      webContents: {
        isDestroyed: () => false,
        send,
      },
    } as unknown as BrowserWindow,
    send,
  };
}

function makeDestroyedWindow() {
  return {
    isDestroyed: () => true,
    get webContents(): WebContents {
      throw new TypeError("Object has been destroyed");
    },
  } as unknown as BrowserWindow;
}

function makeWindowWithDestroyedWebContents() {
  const send = vi.fn(() => {
    throw new TypeError("Object has been destroyed");
  });
  return {
    win: {
      isDestroyed: () => false,
      webContents: {
        isDestroyed: () => true,
        send,
      },
    } as unknown as BrowserWindow,
    send,
  };
}

function makeWindowWithThrowingSend(error: Error) {
  const send = vi.fn(() => {
    throw error;
  });
  return {
    win: {
      isDestroyed: () => false,
      webContents: {
        isDestroyed: () => false,
        send,
      },
    } as unknown as BrowserWindow,
    send,
  };
}

describe("setupAutoUpdater", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    ctx.handlers.clear();
    ctx.ipcHandle.mockClear();
    ctx.checkForUpdates.mockClear();
    ctx.downloadUpdate.mockClear();
    ctx.quitAndInstall.mockClear();
    ctx.getVersion.mockClear();
  });

  afterEach(() => {
    vi.clearAllTimers();
    vi.useRealTimers();
  });

  it("forwards update progress to a live renderer", () => {
    const { win, send } = makeWindow();
    setupAutoUpdater(() => win);

    emitUpdater("download-progress", { percent: 42 });

    expect(send).toHaveBeenCalledWith("updater:download-progress", {
      percent: 42,
    });
  });

  it("skips update progress when the BrowserWindow has already been destroyed", () => {
    setupAutoUpdater(() => makeDestroyedWindow());

    expect(() => emitUpdater("download-progress", { percent: 42 })).not.toThrow();
  });

  it("skips update progress when the BrowserWindow webContents has already been destroyed", () => {
    const { win, send } = makeWindowWithDestroyedWebContents();
    setupAutoUpdater(() => win);

    expect(() => emitUpdater("download-progress", { percent: 42 })).not.toThrow();
    expect(send).not.toHaveBeenCalled();
  });

  it("skips update progress when webContents.send loses a destroy race", () => {
    const { win, send } = makeWindowWithThrowingSend(
      new TypeError("Object has been destroyed"),
    );
    setupAutoUpdater(() => win);

    expect(() => emitUpdater("download-progress", { percent: 42 })).not.toThrow();
    expect(send).toHaveBeenCalledWith("updater:download-progress", {
      percent: 42,
    });
  });

  it("rethrows non-destroy errors from webContents.send", () => {
    const { win } = makeWindowWithThrowingSend(new Error("boom"));
    setupAutoUpdater(() => win);

    expect(() => emitUpdater("download-progress", { percent: 42 })).toThrow(
      "boom",
    );
  });
});

describe("updater:install", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    ctx.handlers.clear();
    ctx.ipcHandle.mockClear();
    ctx.quitAndInstall.mockClear();
  });

  afterEach(() => {
    vi.clearAllTimers();
    vi.useRealTimers();
  });

  function installHandler() {
    const { win } = makeWindow();
    setupAutoUpdater(() => win);
    const entry = ctx.ipcHandle.mock.calls.find(
      ([channel]) => channel === "updater:install",
    );
    if (!entry) throw new Error("updater:install was never registered");
    return entry[1] as () => Promise<{ ok: false; error: string }>;
  }

  it("reports failure when quitAndInstall silently fails to restart the app", async () => {
    // Squirrel.Mac rejecting an ad-hoc build looks exactly like this:
    // quitAndInstall() returns normally, no error is thrown, and the app
    // just keeps running. Without the grace timer the promise never settles
    // and the button appears dead.
    const handler = installHandler();
    const pending = handler();

    await vi.advanceTimersByTimeAsync(10_000);

    await expect(pending).resolves.toEqual({
      ok: false,
      error: expect.stringContaining("Couldn't apply the update automatically"),
    });
    expect(ctx.quitAndInstall).toHaveBeenCalledWith(false, true);
  });

  it("explains the failure using the last updater error", async () => {
    const handler = installHandler();
    emitUpdater("error", new Error("Could not get code signature"));

    const pending = handler();
    await vi.advanceTimersByTimeAsync(10_000);

    await expect(pending).resolves.toEqual({
      ok: false,
      error: expect.stringContaining("Could not get code signature"),
    });
  });

  it("reports a synchronous quitAndInstall throw without waiting", async () => {
    ctx.quitAndInstall.mockImplementationOnce(() => {
      throw new Error("installer missing");
    });
    const handler = installHandler();

    await expect(handler()).resolves.toEqual({
      ok: false,
      error: "installer missing",
    });
  });

  it("does not settle before the grace period elapses", async () => {
    const handler = installHandler();
    const settled = vi.fn();
    void handler().then(settled);

    await vi.advanceTimersByTimeAsync(9_000);
    expect(settled).not.toHaveBeenCalled();
  });
});
