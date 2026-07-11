import { describe, expect, it } from "vitest";
import { matchLocale, pickLocale } from "./pick-locale";
import type { LocaleAdapter } from "./types";

function makeAdapter(
  overrides: Partial<LocaleAdapter> = {},
): LocaleAdapter {
  return {
    getUserChoice: () => null,
    getSystemPreferences: () => [],
    persist: () => {},
    ...overrides,
  };
}

describe("matchLocale", () => {
  it("returns DEFAULT_LOCALE when given an empty list", () => {
    expect(matchLocale([])).toBe("en");
  });

  it("matches a clean supported tag", () => {
    expect(matchLocale(["uz"])).toBe("uz");
    expect(matchLocale(["ru"])).toBe("ru");
    expect(matchLocale(["en"])).toBe("en");
  });

  it("collapses region-tagged BCP-47 to the supported base", () => {
    expect(matchLocale(["en-US"])).toBe("en");
    expect(matchLocale(["uz-UZ"])).toBe("uz");
    expect(matchLocale(["ru-RU"])).toBe("ru");
  });

  it("falls back to DEFAULT_LOCALE when no candidate matches", () => {
    expect(matchLocale(["fr", "de"])).toBe("en");
  });

  // zh-Hans is a valid SupportedLocale value but is currently NOT in
  // SUPPORTED_LOCALES (disabled as a selectable locale — product decision,
  // see types.ts). Chinese candidates must fall back to English instead of
  // resolving to the disabled locale. When zh-Hans is re-enabled, these
  // expectations flip back to "zh-Hans".
  it("disabled zh-Hans: Chinese candidates fall back to DEFAULT_LOCALE", () => {
    expect(matchLocale(["zh-Hans"])).toBe("en");
    expect(matchLocale(["zh-Hans-CN"])).toBe("en");
    expect(matchLocale(["zh-Hant"])).toBe("en");
  });

  it("uses the first supported candidate when multiple appear", () => {
    expect(matchLocale(["fr", "uz-UZ", "en"])).toBe("uz");
    expect(matchLocale(["fr", "ru-RU", "en"])).toBe("ru");
  });

  it("skips a disabled candidate and honors the next supported one", () => {
    expect(matchLocale(["zh-Hans", "ru"])).toBe("ru");
  });

  it("returns DEFAULT_LOCALE for malformed BCP-47 tags rather than throwing", () => {
    expect(matchLocale(["----"])).toBe("en");
    expect(matchLocale(["x-private-only"])).toBe("en");
  });
});

describe("pickLocale", () => {
  it("prefers explicit user choice over system signal", () => {
    const adapter = makeAdapter({
      getUserChoice: () => "ru",
      getSystemPreferences: () => ["en-US"],
    });
    expect(pickLocale(adapter)).toBe("ru");
  });

  it("falls back to system preferences when no user choice", () => {
    const adapter = makeAdapter({
      getSystemPreferences: () => ["uz-UZ", "en-US"],
    });
    expect(pickLocale(adapter)).toBe("uz");
  });

  // A user whose stored choice is the disabled zh-Hans must degrade to the
  // default locale, not crash or leak an unsupported locale into the UI —
  // stored user.language="zh-Hans" stays valid data (see types.ts).
  it("stored zh-Hans choice degrades to DEFAULT_LOCALE while disabled", () => {
    const adapter = makeAdapter({
      getUserChoice: () => "zh-Hans",
      getSystemPreferences: () => ["ru"],
    });
    expect(pickLocale(adapter)).toBe("en");
  });

  it("returns DEFAULT_LOCALE when neither choice nor preference yields a match", () => {
    const adapter = makeAdapter({
      getUserChoice: () => null,
      getSystemPreferences: () => ["fr", "de"],
    });
    expect(pickLocale(adapter)).toBe("en");
  });

  it("ignores empty-string user choice and falls through to system", () => {
    const adapter = makeAdapter({
      getUserChoice: () => "",
      getSystemPreferences: () => ["ru"],
    });
    expect(pickLocale(adapter)).toBe("ru");
  });
});
