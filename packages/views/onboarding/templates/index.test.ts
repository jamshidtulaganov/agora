import { describe, expect, it } from "vitest";
import { pickContentLang } from "./index";

describe("pickContentLang", () => {
  it("uses the shared locale matcher before selecting persisted content", () => {
    expect(pickContentLang("en-US")).toBe("en");
    expect(pickContentLang("uz-UZ")).toBe("uz");
    expect(pickContentLang("ru-RU")).toBe("ru");
  });

  // zh-Hans is disabled in SUPPORTED_LOCALES (product decision, see
  // packages/core/i18n/types.ts), so Chinese languages resolve through the
  // matcher to English content. Flips back to "zh" when re-enabled.
  it("serves English content while zh-Hans is a disabled locale", () => {
    expect(pickContentLang("zh-Hant")).toBe("en");
    expect(pickContentLang("zh-Hans")).toBe("en");
  });

  it("falls back to English for unsupported or missing languages", () => {
    expect(pickContentLang("fr-FR")).toBe("en");
    expect(pickContentLang(null)).toBe("en");
    expect(pickContentLang(undefined)).toBe("en");
  });
});
