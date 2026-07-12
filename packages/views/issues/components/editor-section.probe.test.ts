// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";
import { probeEditorReachable } from "./editor-workbench";

// Contract for the cloud-editor reachability probe: a dead code-server proxy
// (network reject or a 5xx from the ReverseProxy) is unreachable → the caller
// shows the empty state instead of iframing a URL that renders a raw browser
// net-error. Any 2xx/3xx/4xx means code-server answered → reachable.
function mockFetch(impl: () => Promise<Response> | never) {
  vi.stubGlobal("fetch", vi.fn(impl));
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("probeEditorReachable", () => {
  it("treats a 200 as reachable", async () => {
    mockFetch(async () => new Response("ok", { status: 200 }));
    expect(await probeEditorReachable("/editor/proxy/tok/?folder=x")).toBe(true);
  });

  it("treats an opaqueredirect (3xx under redirect:manual) as reachable", async () => {
    // jsdom can't synthesize a real opaqueredirect Response, so fake the shape
    // probeEditorReachable checks: type === "opaqueredirect".
    mockFetch(
      async () =>
        ({ type: "opaqueredirect", status: 0 }) as unknown as Response,
    );
    expect(await probeEditorReachable("/editor/proxy/tok/")).toBe(true);
  });

  it("treats a 401/403 as reachable (auth is the iframe's problem, not a dead proxy)", async () => {
    mockFetch(async () => new Response("", { status: 403 }));
    expect(await probeEditorReachable("/editor/proxy/tok/")).toBe(true);
  });

  it("treats a 502 (ReverseProxy can't dial the daemon's dead code-server) as unreachable", async () => {
    mockFetch(async () => new Response("bad gateway", { status: 502 }));
    expect(await probeEditorReachable("/editor/proxy/tok/")).toBe(false);
  });

  it("treats a 503/504 as unreachable", async () => {
    mockFetch(async () => new Response("", { status: 504 }));
    expect(await probeEditorReachable("/editor/proxy/tok/")).toBe(false);
  });

  it("treats a network rejection (connection reset) as unreachable", async () => {
    mockFetch(async () => {
      throw new TypeError("NetworkError when attempting to fetch resource.");
    });
    expect(await probeEditorReachable("/editor/proxy/tok/")).toBe(false);
  });
});
