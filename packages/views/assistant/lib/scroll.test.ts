import { describe, expect, it } from "vitest";
import { STICK_TO_BOTTOM_THRESHOLD, distanceFromBottom, isNearBottom } from "./scroll";

describe("transcript stick-to-bottom", () => {
  it("counts a transcript pinned to the bottom as near-bottom", () => {
    expect(isNearBottom({ scrollTop: 900, scrollHeight: 1400, clientHeight: 500 })).toBe(true);
  });

  it("counts a short transcript that does not scroll as near-bottom", () => {
    expect(isNearBottom({ scrollTop: 0, scrollHeight: 300, clientHeight: 500 })).toBe(true);
  });

  it("stays near-bottom inside the threshold and leaves it one pixel past", () => {
    const atThreshold = {
      scrollTop: 900 - STICK_TO_BOTTOM_THRESHOLD,
      scrollHeight: 1400,
      clientHeight: 500,
    };
    expect(isNearBottom(atThreshold)).toBe(true);
    expect(isNearBottom({ ...atThreshold, scrollTop: atThreshold.scrollTop - 1 })).toBe(false);
  });

  it("is detached once the reader scrolls up to re-read", () => {
    expect(isNearBottom({ scrollTop: 100, scrollHeight: 4000, clientHeight: 500 })).toBe(false);
  });

  it("treats elastic over-scroll past the end as zero distance", () => {
    expect(distanceFromBottom({ scrollTop: 1000, scrollHeight: 1400, clientHeight: 500 })).toBe(0);
  });

  it("honours a custom threshold", () => {
    const metrics = { scrollTop: 860, scrollHeight: 1400, clientHeight: 500 };
    expect(isNearBottom(metrics, 40)).toBe(true);
    expect(isNearBottom(metrics, 20)).toBe(false);
  });
});
