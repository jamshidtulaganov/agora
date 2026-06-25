import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

const repoRoot = resolve(process.cwd(), "../..");
const chineseFonts = ["PingFang SC", "Microsoft YaHei", "Noto Sans CJK SC"];

// The supported locales (en / zh-Hans / uz / ru) only need Latin, Cyrillic,
// and Simplified-Chinese coverage. Latin + Cyrillic render with Inter; CJK
// chars fall through to the platform Chinese fonts. Guard that the Chinese
// CJK chain stays in the global stack so zh users never lose glyph coverage.
function expectChineseFontsPresent(source: string) {
  const chineseIndexes = chineseFonts.map((font) => source.indexOf(font));
  expect(chineseIndexes).not.toContain(-1);
}

describe("CJK font fallback order", () => {
  it("keeps Chinese CJK fonts in the web global stack", () => {
    const cssSource = readFileSync(
      resolve(repoRoot, "apps/web/app/globals.css"),
      "utf8",
    );

    expectChineseFontsPresent(cssSource);
  });

  it("keeps Chinese CJK fonts in the desktop global stack", () => {
    const desktopCss = readFileSync(
      resolve(repoRoot, "apps/desktop/src/renderer/src/globals.css"),
      "utf8",
    );

    expectChineseFontsPresent(desktopCss);
  });
});
