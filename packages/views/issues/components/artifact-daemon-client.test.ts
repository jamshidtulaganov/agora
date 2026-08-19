import { afterEach, describe, expect, it, vi } from "vitest";
import {
  ARTIFACT_RUNTIME_GONE,
  ArtifactDaemonError,
  artifactDaemonPost,
  isArtifactRuntimeGone,
} from "./artifact-daemon-client";

function mockFetch(status: number, body: string, contentType = "application/json") {
  const fetchMock = vi.fn().mockResolvedValue({
    ok: status >= 200 && status < 300,
    status,
    statusText: status === 410 ? "Gone" : "Error",
    headers: new Headers({ "content-type": contentType }),
    text: async () => body,
    json: async () => JSON.parse(body) as unknown,
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("artifactDaemonPost error shaping", () => {
  it("carries the reason from a structured daemon 410", async () => {
    mockFetch(410, JSON.stringify({ reason: ARTIFACT_RUNTIME_GONE, error: "artifact repository is unavailable" }));
    const error = await artifactDaemonPost("/browser/proxy/x", "/artifact/changes", {}).catch((e: unknown) => e);
    expect(error).toBeInstanceOf(ArtifactDaemonError);
    const typed = error as ArtifactDaemonError;
    expect(typed.reason).toBe(ARTIFACT_RUNTIME_GONE);
    expect(typed.status).toBe(410);
    expect(typed.message).toBe("artifact repository is unavailable");
    expect(isArtifactRuntimeGone(typed)).toBe(true);
  });

  it("keeps a plain-text failure readable and unclassified", async () => {
    // An older daemon (or a proxy) answers with plain text and no reason field:
    // the message must survive and the error must NOT be treated as "gone".
    mockFetch(404, "invalid repository name", "text/plain");
    const error = await artifactDaemonPost("/browser/proxy/x", "/artifact/file", {}).catch((e: unknown) => e);
    const typed = error as ArtifactDaemonError;
    expect(typed.message).toBe("invalid repository name");
    expect(typed.reason).toBeUndefined();
    expect(isArtifactRuntimeGone(typed)).toBe(false);
  });

  it("survives a non-JSON body that starts like JSON", async () => {
    mockFetch(502, "{gateway exploded", "text/html");
    const error = await artifactDaemonPost("/browser/proxy/x", "/artifact/changes", {}).catch((e: unknown) => e);
    const typed = error as ArtifactDaemonError;
    expect(typed.message).toBe("{gateway exploded");
    expect(isArtifactRuntimeGone(typed)).toBe(false);
  });

  it("falls back to the status text when the body is empty", async () => {
    mockFetch(410, "   ");
    const error = await artifactDaemonPost("/browser/proxy/x", "/artifact/changes", {}).catch((e: unknown) => e);
    expect((error as ArtifactDaemonError).message).toBe("Gone");
  });
});

describe("isArtifactRuntimeGone", () => {
  it("recognises the backend's own reason arriving as a plain message", () => {
    // The backend returns { reason: "artifact_runtime_gone" } for a run with no
    // recorded work dir; that reaches the UI through the shared API client as
    // message text, with no field to carry a reason.
    expect(isArtifactRuntimeGone(new Error(`{"reason":"${ARTIFACT_RUNTIME_GONE}"}`))).toBe(true);
  });

  it("does not classify unrelated failures", () => {
    expect(isArtifactRuntimeGone(new Error("network error"))).toBe(false);
    expect(isArtifactRuntimeGone(undefined)).toBe(false);
    expect(isArtifactRuntimeGone(null)).toBe(false);
    expect(isArtifactRuntimeGone("artifact_runtime_gone")).toBe(false);
  });
});
