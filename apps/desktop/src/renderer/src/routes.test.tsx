import { describe, it, expect } from "vitest";
import { matchRoutes } from "react-router-dom";
import { paths } from "@agora/core/paths";
import { SIDEBAR_WORKSPACE_NAV_KEYS } from "@agora/views/layout";
import { appRoutes } from "./routes";

// The sidebar is SHARED between web and desktop, but desktop's router is
// hand-maintained. `ai-accounts`, `plugins` and `mcp` all shipped as clickable
// sidebar links that resolved to the 404 route-error page on desktop, because
// adding a nav entry does not add a desktop route. Nothing caught it — the
// paths consistency test only checks that URLs are spelled right, not that an
// app can serve them.
//
// This closes that gap from the desktop side: every workspace nav key the
// sidebar renders must match a real route here.
describe("desktop router covers the shared sidebar", () => {
  const ws = paths.workspace("acme");

  it("resolves every workspace nav link the sidebar renders", () => {
    const missing: string[] = [];

    for (const key of SIDEBAR_WORKSPACE_NAV_KEYS) {
      const build = (ws as unknown as Record<string, () => string>)[key];
      expect(build, `paths.workspace() has no builder for nav key "${key}"`).toBeTypeOf(
        "function",
      );
      const url = build();
      const matches = matchRoutes(appRoutes, url);
      // A route-error/catch-all match is not real coverage: require a match
      // whose leaf actually renders an element for this path.
      const leaf = matches?.[matches.length - 1];
      if (!matches || matches.length === 0 || !leaf?.route.element) {
        missing.push(`${key} → ${url}`);
      }
    }

    expect(
      missing,
      `desktop router is missing routes for sidebar links:\n  ${missing.join("\n  ")}`,
    ).toEqual([]);
  });

  // Guards specific regressions: these shared navigation destinations were
  // linked from desktop UI before the hand-maintained router served them.
  it.each(["aiAccounts", "plugins", "mcp", "bitrix"])("serves /%s", (key) => {
    const url = (ws as unknown as Record<string, () => string>)[key]!();
    const matches = matchRoutes(appRoutes, url);
    expect(matches, `no route matched ${url}`).not.toBeNull();
    expect(matches![matches!.length - 1]!.route.element).toBeTruthy();
  });
});
