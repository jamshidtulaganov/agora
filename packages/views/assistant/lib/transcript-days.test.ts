import { describe, expect, it } from "vitest";
import { dayKey, isDayBoundary, relativeDay } from "./transcript-days";

// Built from local parts so the assertions hold in any TZ the suite runs in —
// the divider is about the READER's day, not UTC's.
function localIso(year: number, month: number, day: number, hour = 12): string {
  return new Date(year, month - 1, day, hour).toISOString();
}

describe("isDayBoundary", () => {
  it("is false between two messages on the same local day", () => {
    expect(isDayBoundary(localIso(2026, 9, 16, 1), localIso(2026, 9, 16, 23))).toBe(false);
  });

  it("is true the first time the local day changes", () => {
    expect(isDayBoundary(localIso(2026, 9, 16, 23), localIso(2026, 9, 17, 0))).toBe(true);
  });

  it("is true across a month and a year boundary", () => {
    expect(isDayBoundary(localIso(2026, 9, 30), localIso(2026, 10, 1))).toBe(true);
    expect(isDayBoundary(localIso(2026, 12, 31), localIso(2027, 1, 1))).toBe(true);
  });

  it("never puts a divider above the first message", () => {
    expect(isDayBoundary(null, localIso(2026, 9, 17))).toBe(false);
    expect(isDayBoundary(undefined, localIso(2026, 9, 17))).toBe(false);
  });

  it("degrades quietly on an unusable timestamp instead of splitting the day", () => {
    expect(isDayBoundary(localIso(2026, 9, 16), "not-a-date")).toBe(false);
    expect(isDayBoundary("", localIso(2026, 9, 17))).toBe(false);
    expect(dayKey("not-a-date")).toBeNull();
  });
});

describe("relativeDay", () => {
  const now = new Date(2026, 8, 17, 10); // 17 Sep 2026, local

  it("labels today and yesterday", () => {
    expect(relativeDay(localIso(2026, 9, 17, 8), now)).toBe("today");
    expect(relativeDay(localIso(2026, 9, 16, 23), now)).toBe("yesterday");
  });

  it("falls back to a formatted date for anything older", () => {
    expect(relativeDay(localIso(2026, 9, 15), now)).toBe("older");
  });

  it("handles crossing a month boundary backwards", () => {
    expect(relativeDay(localIso(2026, 8, 31, 22), new Date(2026, 8, 1, 9))).toBe("yesterday");
  });
});
