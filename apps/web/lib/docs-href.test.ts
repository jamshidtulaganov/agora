import { describe, expect, it } from "vitest";
import { docsHrefForLocale } from "./docs-href";

describe("docsHrefForLocale", () => {
  it("routes each supported locale to the matching docs entry", () => {
    expect(docsHrefForLocale("en")).toBe("/docs");
    expect(docsHrefForLocale("zh-Hans")).toBe("/docs/zh");
    // uz/ru have no dedicated docs subsite yet — they fall back to /docs.
    expect(docsHrefForLocale("uz")).toBe("/docs");
    expect(docsHrefForLocale("ru")).toBe("/docs");
  });
});
