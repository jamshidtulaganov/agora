import { describe, expect, it } from "vitest";
import type { TFunction } from "i18next";
import { createI18n } from "@agora/core/i18n/react";
import enAutopilots from "../../locales/en/autopilots.json";
import zhAutopilots from "../../locales/zh-Hans/autopilots.json";
import { formatSchedulePartialFailureToast } from "./autopilot-dialog-toast";

// Contract test for the autopilot-dialog partial-success toast formatting.
//
// The dialog routes its partial-success branches through
// `formatSchedulePartialFailureToast`, so this test drives that exact
// helper rather than calling `t(...)` independently. That means a regression
// in either side — the JSON template (e.g. `{reason}` instead of `{{reason}}`)
// or the call-site variable name (e.g. `{ msg: ... }` instead of
// `{ reason: ... }`) — fails this test with the substring assertion.

describe("autopilot dialog partial-success toast", () => {
  const reason = "schedule conflict: 09:00 overlaps existing trigger";

  describe("en", () => {
    const i18n = createI18n("en", { en: { autopilots: enAutopilots } });
    const t = i18n.getFixedT("en", "autopilots") as TFunction<"autopilots">;

    it("renders create partial-success with the server reason verbatim", () => {
      const rendered = formatSchedulePartialFailureToast(t, "create", reason);
      expect(rendered).toContain(reason);
      expect(rendered).not.toContain("{{");
      expect(rendered).not.toContain("{reason}");
    });

    it("renders update partial-success with the server reason verbatim", () => {
      const rendered = formatSchedulePartialFailureToast(t, "update", reason);
      expect(rendered).toContain(reason);
      expect(rendered).not.toContain("{{");
      expect(rendered).not.toContain("{reason}");
    });

    it("falls back to the no-reason create string when reason is null", () => {
      expect(formatSchedulePartialFailureToast(t, "create", null)).toBe(
        "Autopilot created, but schedule failed to save",
      );
    });

    it("falls back to the no-reason update string when reason is null", () => {
      expect(formatSchedulePartialFailureToast(t, "update", null)).toBe(
        "Autopilot updated, but schedule failed to save",
      );
    });
  });

  describe("zh-Hans", () => {
    const i18n = createI18n("zh-Hans", {
      "zh-Hans": { autopilots: zhAutopilots },
      en: { autopilots: enAutopilots },
    });
    const t = i18n.getFixedT("zh-Hans", "autopilots") as TFunction<"autopilots">;

    it("renders create partial-success with the server reason verbatim", () => {
      const rendered = formatSchedulePartialFailureToast(t, "create", reason);
      expect(rendered).toContain(reason);
      expect(rendered).not.toContain("{{");
      expect(rendered).not.toContain("{reason}");
    });

    it("renders update partial-success with the server reason verbatim", () => {
      const rendered = formatSchedulePartialFailureToast(t, "update", reason);
      expect(rendered).toContain(reason);
      expect(rendered).not.toContain("{{");
      expect(rendered).not.toContain("{reason}");
    });
  });
});

// The Telegram delivery line is pure interpolation, and interpolation is where
// these strings break: a template written `{bot}` instead of `{{bot}}`, or a
// call site passing `botName` where the template says `bot`, both render a
// destination with a hole in it — and the whole point of the line is telling
// the reader exactly where the report lands.
describe("telegram delivery strings", () => {
  const locales = [
    ["en", enAutopilots],
    ["zh-Hans", zhAutopilots],
  ] as const;

  for (const [locale, resource] of locales) {
    it(`interpolates bot and chat in ${locale}`, () => {
      const i18n = createI18n(locale, { [locale]: { autopilots: resource } });
      const t = i18n.getFixedT(locale, "autopilots") as TFunction<"autopilots">;

      const withChat = t(($) => $.dialog.telegram_destination, {
        bot: "sd_pm_agent_bot",
        chat: "-1004336001519",
      });
      expect(withChat).toContain("sd_pm_agent_bot");
      expect(withChat).toContain("-1004336001519");
      expect(withChat).not.toContain("{{");

      const noChat = t(($) => $.dialog.telegram_no_chat, { bot: "sd_pm_agent_bot" });
      expect(noChat).toContain("sd_pm_agent_bot");
      expect(noChat).not.toContain("{{");

      // The no-bot line takes no variables; a stray placeholder there would
      // reach the reader verbatim.
      expect(t(($) => $.dialog.telegram_no_bot)).not.toContain("{{");
    });
  }
});
