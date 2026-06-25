import { describe, expect, it } from "vitest";
import { fullDateLabel, monthYearLabel } from "./changelog-page-client";

describe("changelog date labels", () => {
  it("formats month labels for each landing locale", () => {
    expect(monthYearLabel(2026, 1, "en")).toBe("January 2026");
    expect(monthYearLabel(2026, 1, "zh-Hans")).toBe("2026年1月");
    // Intl output for ru/uz varies by ICU version — assert it localizes the
    // year rather than pinning a brittle exact string.
    expect(monthYearLabel(2026, 1, "ru")).toContain("2026");
    expect(monthYearLabel(2026, 1, "uz")).toContain("2026");
  });

  it("formats full dates for each landing locale", () => {
    expect(fullDateLabel("2026-01-15", "en")).toBe("January 15, 2026");
    expect(fullDateLabel("2026-01-15", "zh-Hans")).toBe("2026年1月15日");
    expect(fullDateLabel("2026-01-15", "ru")).toContain("2026");
    expect(fullDateLabel("2026-01-15", "uz")).toContain("2026");
  });

  it("keeps invalid release dates unchanged", () => {
    expect(fullDateLabel("not-a-date", "ru")).toBe("not-a-date");
  });
});
