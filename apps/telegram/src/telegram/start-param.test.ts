import { describe, it, expect } from "vitest";
import { decodeStartParam } from "./start-param";

// Mirror the backend's telegram.MiniAppStartParam encoding: base64url (no pad)
// of "i:" + issueID. This is the cross-language deep-link contract — if the Go
// encoder changes, this test must change with it.
function encodeLikeBackend(id: string): string {
  return btoa("i:" + id)
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/, "");
}

describe("decodeStartParam", () => {
  it("decodes a backend-encoded issue id", () => {
    const id = "550e8400-e29b-41d4-a716-446655440000";
    expect(decodeStartParam(encodeLikeBackend(id))).toBe(id);
  });

  it("returns null for null or empty input", () => {
    expect(decodeStartParam(null)).toBeNull();
    expect(decodeStartParam("")).toBeNull();
  });

  it("returns null for a non-issue payload prefix", () => {
    const other = btoa("x:something")
      .replace(/\+/g, "-")
      .replace(/\//g, "_")
      .replace(/=+$/, "");
    expect(decodeStartParam(other)).toBeNull();
  });

  it("returns null for malformed base64", () => {
    expect(decodeStartParam("!!! not base64 !!!")).toBeNull();
  });

  it("returns null for an empty issue id", () => {
    const emptyId = btoa("i:").replace(/=+$/, "");
    expect(decodeStartParam(emptyId)).toBeNull();
  });
});
