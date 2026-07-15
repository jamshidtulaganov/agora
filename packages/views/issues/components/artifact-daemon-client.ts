import { absoluteDaemonBase, daemonProxyHeaders } from "./daemon-proxy-fetch";

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
    const message = await response.text();
    throw new Error(message.trim() || response.statusText);
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
