"use client";

/**
 * Resolves an auth-gated attachment download URL into a URL that a native
 * <img>/<video> can load on token-mode clients (Electron desktop, mobile
 * webview).
 *
 * Background: markdown persists a stable `/api/attachments/{id}/download`
 * (or AGORA_PUBLIC_URL-absolute form). Native browser media fetches cannot
 * attach `Authorization: Bearer`, and packaged desktop runs from `file://`
 * so SameSite cookies never reach the API host. Web works because cookie
 * auth + the Next.js `/api` rewrite make the same path a same-origin
 * authenticated request.
 *
 * When the renderer has a non-empty API base URL (token mode) and the media
 * src is that auth-gated download endpoint, we fetch the bytes through the
 * ApiClient (which sends Bearer) and expose a blob: URL. Public CDN URLs,
 * CloudFront-signed URLs, blob:/data: previews, and web's empty apiBaseUrl
 * all pass through unchanged.
 */

import { useQuery } from "@tanstack/react-query";
import { api } from "@agora/core/api";
import { attachmentIdFromDownloadURL } from "@agora/core/types";

function isTokenModeClient(): boolean {
  return Boolean((api.getBaseUrl?.() ?? "").replace(/\/+$/, ""));
}

function resolveAttachmentId(
  src: string,
  attachmentId?: string,
): string | undefined {
  if (attachmentId) return attachmentId;
  return attachmentIdFromDownloadURL(src);
}

function pathnameOf(src: string): string {
  if (!src) return "";
  if (/^https?:\/\//i.test(src)) {
    try {
      return new URL(src).pathname;
    } catch {
      return "";
    }
  }
  return src.split(/[?#]/, 1)[0] ?? "";
}

/** True when `src` is the stable auth-gated attachment download endpoint. */
export function isAuthGatedAttachmentDownloadURL(
  src: string,
  attachmentId?: string,
): boolean {
  if (attachmentIdFromDownloadURL(src) !== undefined) return true;
  if (!attachmentId) return false;
  return pathnameOf(src) === `/api/attachments/${attachmentId}/download`;
}

/**
 * Returns a loadable media `src` for token-mode clients.
 *
 * While the authenticated blob is in flight, returns `""` so the <img>
 * stays blank instead of flashing a broken-image icon against the
 * unauthenticated API URL.
 *
 * Pass `enabled: false` for non-media surfaces (file cards) so we don't
 * download the whole object just because the href is auth-gated.
 */
export function useAuthenticatedMediaSrc(
  src: string,
  attachmentId?: string,
  enabled = true,
): string {
  return useAuthenticatedMediaSrcResult(src, attachmentId, enabled).src;
}

export interface AuthenticatedMediaSrcResult {
  src: string;
  isLoading: boolean;
  isError: boolean;
}

/**
 * Status-aware variant for modal renderers. Native media elements can stay
 * blank while the authenticated blob is loading, but an iframe with src=""
 * would recursively load the Agora shell. The modal uses these flags to show
 * an explicit loading/error state instead.
 */
export function useAuthenticatedMediaSrcResult(
  src: string,
  attachmentId?: string,
  enabled = true,
): AuthenticatedMediaSrcResult {
  const id = resolveAttachmentId(src, attachmentId);
  const needsAuthFetch =
    enabled &&
    !!src &&
    !!id &&
    isTokenModeClient() &&
    isAuthGatedAttachmentDownloadURL(src, id);

  const query = useQuery({
    queryKey: ["attachment-download-blob", id ?? ""] as const,
    queryFn: async () => {
      const blob = await api.getAttachmentDownloadBlob(id as string);
      return URL.createObjectURL(blob);
    },
    enabled: needsAuthFetch,
    // Bytes don't change for a given attachment id; keep the blob URL for
    // the session so scrolling a long description doesn't refetch.
    staleTime: Infinity,
    gcTime: 30 * 60_000,
    retry: false,
  });

  if (!needsAuthFetch) {
    return { src, isLoading: false, isError: false };
  }
  return {
    src: query.data ?? "",
    isLoading: query.isPending,
    isError: query.isError,
  };
}
