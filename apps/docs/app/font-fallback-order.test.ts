import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

const chineseFonts = ["PingFang SC", "Microsoft YaHei", "Noto Sans CJK SC"];

// Docs ship en + zh, so the global stack needs Latin (Inter) and
// Simplified-Chinese coverage. Guard that the Chinese CJK chain stays present
// so zh docs readers never lose glyph coverage.
function expectChineseFontsPresent(source: string) {
  const chineseIndexes = chineseFonts.map((font) => source.indexOf(font));
  expect(chineseIndexes).not.toContain(-1);
}

describe("CJK font fallback order", () => {
  it("keeps Chinese CJK fonts in the docs global stack", () => {
    const cssSource = readFileSync(
      resolve(process.cwd(), "app/global.css"),
      "utf8",
    );

    expectChineseFontsPresent(cssSource);
  });
});
