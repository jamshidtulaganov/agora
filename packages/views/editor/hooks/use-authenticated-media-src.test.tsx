import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  isAuthGatedAttachmentDownloadURL,
  useAuthenticatedMediaSrc,
} from "./use-authenticated-media-src";

const { getAttachmentDownloadBlobMock, getBaseUrlMock } = vi.hoisted(() => ({
  getAttachmentDownloadBlobMock: vi.fn(),
  getBaseUrlMock: vi.fn(() => ""),
}));

vi.mock("@agora/core/api", () => ({
  api: {
    getAttachmentDownloadBlob: getAttachmentDownloadBlobMock,
    getBaseUrl: getBaseUrlMock,
  },
}));

const ATT_ID = "11111111-2222-3333-4444-555555555555";
const BLOB_URL = "blob:https://test.local/media";

function wrapper({ children }: { children: ReactNode }) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
}

beforeEach(() => {
  vi.clearAllMocks();
  getBaseUrlMock.mockReturnValue("");
  getAttachmentDownloadBlobMock.mockResolvedValue(
    new Blob(["x"], { type: "image/png" }),
  );
  vi.spyOn(URL, "createObjectURL").mockReturnValue(BLOB_URL);
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("isAuthGatedAttachmentDownloadURL", () => {
  it("recognizes absolute and relative download paths with a UUID", () => {
    expect(
      isAuthGatedAttachmentDownloadURL(`/api/attachments/${ATT_ID}/download`),
    ).toBe(true);
    expect(
      isAuthGatedAttachmentDownloadURL(
        `https://api.example.test/api/attachments/${ATT_ID}/download`,
      ),
    ).toBe(true);
  });

  it("recognizes a non-UUID id when the caller already knows the attachment id", () => {
    expect(
      isAuthGatedAttachmentDownloadURL(
        "/api/attachments/att-1/download",
        "att-1",
      ),
    ).toBe(true);
  });

  it("rejects signed CDN URLs and unrelated paths", () => {
    expect(
      isAuthGatedAttachmentDownloadURL(
        "https://cdn.example.test/x.png?Signature=s",
      ),
    ).toBe(false);
    expect(isAuthGatedAttachmentDownloadURL("/uploads/x.png")).toBe(false);
  });
});

describe("useAuthenticatedMediaSrc", () => {
  it("passes through on web (empty apiBaseUrl)", () => {
    const src = `/api/attachments/${ATT_ID}/download`;
    const { result } = renderHook(
      () => useAuthenticatedMediaSrc(src, ATT_ID),
      { wrapper },
    );
    expect(result.current).toBe(src);
    expect(getAttachmentDownloadBlobMock).not.toHaveBeenCalled();
  });

  it("resolves a blob: URL in token mode for auth-gated download URLs", async () => {
    getBaseUrlMock.mockReturnValue("https://api.example.test");
    const src = `https://api.example.test/api/attachments/${ATT_ID}/download`;
    const { result } = renderHook(
      () => useAuthenticatedMediaSrc(src, ATT_ID),
      { wrapper },
    );

    await waitFor(() => {
      expect(result.current).toBe(BLOB_URL);
    });
    expect(getAttachmentDownloadBlobMock).toHaveBeenCalledWith(ATT_ID);
  });

  it("extracts the attachment id from the URL when not passed explicitly", async () => {
    getBaseUrlMock.mockReturnValue("https://api.example.test");
    const src = `/api/attachments/${ATT_ID}/download`;
    const { result } = renderHook(() => useAuthenticatedMediaSrc(src), {
      wrapper,
    });

    await waitFor(() => {
      expect(result.current).toBe(BLOB_URL);
    });
    expect(getAttachmentDownloadBlobMock).toHaveBeenCalledWith(ATT_ID);
  });

  it("skips the fetch when enabled is false", () => {
    getBaseUrlMock.mockReturnValue("https://api.example.test");
    const src = `/api/attachments/${ATT_ID}/download`;
    const { result } = renderHook(
      () => useAuthenticatedMediaSrc(src, ATT_ID, false),
      { wrapper },
    );
    expect(result.current).toBe(src);
    expect(getAttachmentDownloadBlobMock).not.toHaveBeenCalled();
  });
});
