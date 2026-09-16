import { describe, expect, it } from "vitest";
import { toggleHiddenNavKey } from "./hidden-nav";

describe("toggleHiddenNavKey", () => {
  it("appends a key when hiding", () => {
    expect(toggleHiddenNavKey(["usage"], "mcp", true)).toEqual(["usage", "mcp"]);
  });

  it("removes a key when restoring", () => {
    expect(toggleHiddenNavKey(["usage", "mcp"], "usage", false)).toEqual(["mcp"]);
  });

  // Callers skip the network round-trip on reference equality, so a no-op
  // must return the very same array, not an equal copy.
  it("returns the input array unchanged when already in the target state", () => {
    const hidden = ["usage"];
    expect(toggleHiddenNavKey(hidden, "usage", true)).toBe(hidden);
    expect(toggleHiddenNavKey(hidden, "mcp", false)).toBe(hidden);
  });

  it("does not mutate the input", () => {
    const hidden = ["usage"];
    toggleHiddenNavKey(hidden, "mcp", true);
    expect(hidden).toEqual(["usage"]);
  });
});
