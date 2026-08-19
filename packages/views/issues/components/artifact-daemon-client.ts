import { absoluteDaemonBase, daemonProxyHeaders } from "../../platform/daemon-proxy-fetch";

/**
 * The reason a daemon artifact surface reports when the working copy no longer
 * exists on disk — a finished run's worktree was torn down, the daemon was
 * restarted, or a local folder moved. Not a malfunction, so the UI renders it as
 * an explanation rather than a load failure. The same value comes back from the
 * BACKEND when a run has no recorded work dir, so one check covers both layers.
 */
export const ARTIFACT_RUNTIME_GONE = "artifact_runtime_gone";

/** A failed daemon artifact call, carrying the machine-readable reason when the
 * daemon sent one. `reason` is undefined for plain-text failures. */
export class ArtifactDaemonError extends Error {
  readonly status: number;
  readonly reason?: string;

  constructor(message: string, status: number, reason?: string) {
    super(message);
    this.name = "ArtifactDaemonError";
    this.status = status;
    this.reason = reason;
  }
}

/** True when this error means the working copy is gone, from either layer. */
export function isArtifactRuntimeGone(error: unknown): boolean {
  if (error instanceof ArtifactDaemonError && error.reason === ARTIFACT_RUNTIME_GONE) {
    return true;
  }
  // Message fallback: the backend's own 410 body reaches the UI as plain text
  // through the shared API client, which has no place to carry a reason field.
  return error instanceof Error && error.message.includes(ARTIFACT_RUNTIME_GONE);
}

export async function artifactDaemonPost(
  daemonUrl: string,
  path: string,
  body: Record<string, string>,
): Promise<unknown> {
  const response = await fetch(`${absoluteDaemonBase(daemonUrl)}${path}`, {
    method: "POST",
    headers: daemonProxyHeaders(daemonUrl),
    body: JSON.stringify(body),
  });
  if (!response.ok) {
    const raw = (await response.text()).trim();
    // A structured body carries { reason, error }; a plain-text body carries the
    // message only. Parse defensively — an older daemon sends plain text, and a
    // proxy may return HTML.
    let reason: string | undefined;
    let message = raw;
    if (raw.startsWith("{")) {
      try {
        const parsed = JSON.parse(raw) as { reason?: unknown; error?: unknown };
        if (typeof parsed.reason === "string") reason = parsed.reason;
        if (typeof parsed.error === "string" && parsed.error.trim() !== "") message = parsed.error;
      } catch {
        // Not JSON after all — keep the raw text as the message.
      }
    }
    throw new ArtifactDaemonError(message || response.statusText, response.status, reason);
  }
  return response.json() as Promise<unknown>;
}

export function artifactPreviewURL(
  daemonUrl: string,
  response: { url?: string; proxy_path?: string },
): string {
  if (response.proxy_path) {
    return `${absoluteDaemonBase(daemonUrl)}${response.proxy_path}`;
  }
  return response.url?.replace("://127.0.0.1:", "://localhost:") ?? "";
}
