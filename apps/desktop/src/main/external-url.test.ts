import { describe, expect, it, vi, beforeEach } from "vitest";

vi.mock("electron", () => ({
  app: { getPath: vi.fn(() => "/Users/test/Downloads") },
  shell: { openExternal: vi.fn().mockResolvedValue(undefined) },
}));

import { shell } from "electron";
import {
  downloadURLSafely,
  isSafeExternalHttpUrl,
  openExternalSafely,
} from "./external-url";

describe("isSafeExternalHttpUrl", () => {
  it("allows http and https URLs", () => {
    expect(isSafeExternalHttpUrl("https://agora.dev")).toBe(true);
    expect(isSafeExternalHttpUrl("http://localhost:3000/auth")).toBe(true);
  });

  it("allows https URLs with embedded credentials", () => {
    // WHATWG URL parses these as https; OS-level handling is the shell's concern.
    expect(isSafeExternalHttpUrl("https://user:pass@example.com")).toBe(true);
  });

  it("normalizes scheme casing so uppercase variants can't bypass", () => {
    expect(isSafeExternalHttpUrl("HTTPS://example.com")).toBe(true);
    expect(isSafeExternalHttpUrl("FILE:///etc/passwd")).toBe(false);
  });

  it("rejects dangerous pseudo-schemes", () => {
    expect(isSafeExternalHttpUrl("javascript:alert(1)")).toBe(false);
    expect(
      isSafeExternalHttpUrl("data:text/html,<script>alert(1)</script>"),
    ).toBe(false);
  });

  it("rejects filesystem and network transport schemes", () => {
    expect(isSafeExternalHttpUrl("file:///etc/passwd")).toBe(false);
    expect(isSafeExternalHttpUrl("ftp://example.com/x")).toBe(false);
    expect(isSafeExternalHttpUrl("smb://share/x")).toBe(false);
  });

  it("rejects local-handler schemes used in past RCE chains", () => {
    expect(isSafeExternalHttpUrl("vscode://file/test")).toBe(false);
    expect(isSafeExternalHttpUrl("ms-msdt:/id%20PCWDiagnostic")).toBe(false);
  });

  it("rejects mailto and other non-web schemes", () => {
    expect(isSafeExternalHttpUrl("mailto:test@example.com")).toBe(false);
    expect(isSafeExternalHttpUrl("tel:+15551234567")).toBe(false);
  });

  it("rejects empty, whitespace, and malformed input", () => {
    expect(isSafeExternalHttpUrl("")).toBe(false);
    expect(isSafeExternalHttpUrl(" ")).toBe(false);
    expect(isSafeExternalHttpUrl("not a url")).toBe(false);
    expect(isSafeExternalHttpUrl("http://")).toBe(false);
  });
});

describe("openExternalSafely", () => {
  beforeEach(() => {
    vi.mocked(shell.openExternal).mockClear();
  });

  it("forwards http/https URLs to shell.openExternal", () => {
    openExternalSafely("https://agora.dev");
    expect(shell.openExternal).toHaveBeenCalledWith("https://agora.dev");
  });

  it("does not call shell.openExternal for rejected schemes", () => {
    openExternalSafely("file:///etc/passwd");
    openExternalSafely("javascript:alert(1)");
    openExternalSafely("not a url");
    expect(shell.openExternal).not.toHaveBeenCalled();
  });
});

describe("downloadURLSafely", () => {
  it("uses the attachment filename in Electron's native save dialog", () => {
    const setSaveDialogOptions = vi.fn();
    let willDownload: ((event: unknown, item: unknown) => void) | undefined;
    const downloadURL = vi.fn(() => {
      willDownload?.({}, { setSaveDialogOptions });
    });
    const win = {
      webContents: {
        downloadURL,
        session: {
          once: vi.fn((_event, listener) => {
            willDownload = listener;
          }),
        },
      },
    };

    downloadURLSafely(
      win as never,
      "https://api.example.test/api/attachments/att-1/download",
      "invoice-status.png",
    );

    expect(downloadURL).toHaveBeenCalledOnce();
    expect(setSaveDialogOptions).toHaveBeenCalledWith({
      defaultPath: "/Users/test/Downloads/invoice-status.png",
    });
  });

  it("removes path traversal from renderer-provided filenames", () => {
    const setSaveDialogOptions = vi.fn();
    let willDownload: ((event: unknown, item: unknown) => void) | undefined;
    const win = {
      webContents: {
        downloadURL: vi.fn(() => {
          willDownload?.({}, { setSaveDialogOptions });
        }),
        session: {
          once: vi.fn((_event, listener) => {
            willDownload = listener;
          }),
        },
      },
    };

    downloadURLSafely(
      win as never,
      "https://cdn.example.test/image",
      "../../private\\invoice.png",
    );

    expect(setSaveDialogOptions).toHaveBeenCalledWith({
      defaultPath: "/Users/test/Downloads/invoice.png",
    });
  });
});
