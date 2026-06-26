import { describe, it, expect } from "vitest";
import { decodeStartParam } from "./start-param";

// Mirror the backend's telegram.MiniAppStartParam encoding: base64url (no pad)
// of "i:<wsSlug>:<issueID>" (or legacy "i:<issueID>"). Cross-language contract —
// if the Go encoder changes, this test must change with it.
function enc(s: string): string {
  return btoa(s)
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/, "");
}

const id = "550e8400-e29b-41d4-a716-446655440000";

describe("decodeStartParam", () => {
  it("decodes legacy i:<id> (no workspace)", () => {
    expect(decodeStartParam(enc("i:" + id))).toEqual({ wsSlug: null, issueId: id });
  });

  it("decodes i:<wsSlug>:<id>", () => {
    expect(decodeStartParam(enc("i:sd-main:" + id))).toEqual({
      wsSlug: "sd-main",
      issueId: id,
    });
  });

  it("returns nulls for null or empty input", () => {
    expect(decodeStartParam(null)).toEqual({ wsSlug: null, issueId: null });
    expect(decodeStartParam("")).toEqual({ wsSlug: null, issueId: null });
  });

  it("returns null issueId for a non-issue prefix", () => {
    expect(decodeStartParam(enc("x:something")).issueId).toBeNull();
  });

  it("returns null issueId for malformed base64", () => {
    expect(decodeStartParam("!!! not base64 !!!").issueId).toBeNull();
  });

  it("returns null issueId for an empty id", () => {
    expect(decodeStartParam(enc("i:")).issueId).toBeNull();
  });
});
