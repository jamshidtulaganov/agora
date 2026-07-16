import { parseWithFallback } from "@agora/core/api";
import { EMPTY_FS_LIST, FsListResponseSchema } from "@agora/core/api/schemas";
import type { FsListResponse } from "@agora/core/types";
import { absoluteDaemonBase, daemonProxyHeaders } from "./daemon-proxy-fetch";

/**
 * Lists the immediate sub-directories of `path` on a daemon's machine, for the
 * web folder picker. `daemonBase` comes from
 * `api.getDaemonBrowseTarget(daemonId)`: an absolute http://127.0.0.1:<port>
 * (self-host, dialed directly) or a same-origin /browser/proxy/<token> base
 * (cloud, where the backend re-checks workspace membership per request).
 *
 * An empty `path` asks the daemon for its default root (the machine's home
 * directory) — the picker's landing view.
 *
 * The daemon replies 403/404 for a path outside its browsable roots or one
 * that vanished; the caller renders that message in place rather than closing
 * the picker.
 */
export async function daemonListDir(
  daemonBase: string,
  path: string,
): Promise<FsListResponse> {
  const query = path ? `?path=${encodeURIComponent(path)}` : "";
  const response = await fetch(
    `${absoluteDaemonBase(daemonBase)}/editor/fs/list${query}`,
    { method: "GET", headers: daemonProxyHeaders(daemonBase) },
  );
  if (!response.ok) {
    const message = await response.text();
    throw new Error(message.trim() || response.statusText);
  }
  const raw: unknown = await response.json();
  return parseWithFallback(raw, FsListResponseSchema, EMPTY_FS_LIST, {
    endpoint: "GET /editor/fs/list",
  });
}
