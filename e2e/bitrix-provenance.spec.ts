import { test, expect } from "@playwright/test";

/**
 * Bitrix provenance UI: the workgroup ("Проект") an imported task came from must
 * be visible on the issue and usable as a filter.
 *
 * Neither is expressible with existing Agora fields — routing files every Bitrix
 * workgroup under ONE project, and only sprint-NAMED groups get a sprint — so the
 * data lives in issue metadata (bitrix_group_id / bitrix_group_name) and both
 * surfaces read it from there.
 *
 * This spec runs against the LOCAL stack and needs a workspace that already holds
 * a Bitrix-linked issue carrying that metadata. Set E2E_BITRIX_TOKEN (a token for
 * a member of it) and optionally E2E_BITRIX_SLUG / E2E_BITRIX_GROUP_NAME; the spec
 * skips when they're absent so CI and the shared suite stay unaffected.
 */

const TOKEN = process.env.E2E_BITRIX_TOKEN ?? "";
const SLUG = process.env.E2E_BITRIX_SLUG ?? "sd-main";
const GROUP_NAME = process.env.E2E_BITRIX_GROUP_NAME ?? "Спринт 12";

test.describe("Bitrix provenance", () => {
  test.skip(!TOKEN, "set E2E_BITRIX_TOKEN to run the Bitrix provenance checks");

  test.beforeEach(async ({ page }) => {
    await page.addInitScript((t) => {
      localStorage.setItem("agora_token", t);
      localStorage.setItem("agora:chat:isOpen", "false");
    }, TOKEN);
  });

  test("filter menu offers the Bitrix project and filtering keeps its issues", async ({
    page,
  }) => {
    await page.goto(`/${SLUG}/issues`);
    await page.waitForURL(`**/${SLUG}/issues`, { timeout: 30000 });

    await page.getByRole("button", { name: /filter/i }).first().click();

    // The section renders only when loaded issues carry bitrix_group_id, which is
    // what keeps the menu unchanged for workspaces that never imported Bitrix.
    const section = page.getByText("Bitrix project", { exact: true });
    await expect(section).toBeVisible({ timeout: 15000 });

    await section.click();
    const option = page.getByText(GROUP_NAME, { exact: false }).first();
    await expect(option).toBeVisible({ timeout: 10000 });
    await option.click();

    // Applying the filter must leave at least the fixture issue on the board and
    // must not empty the view (the failure mode when metadata reads are wrong).
    await page.keyboard.press("Escape");
    await expect(page.getByText(/RELINK FIXTURE/i).first()).toBeVisible({
      timeout: 15000,
    });
  });

  test("issue detail names the source Bitrix project", async ({ page }) => {
    await page.goto(`/${SLUG}/issues`);
    await page.waitForURL(`**/${SLUG}/issues`, { timeout: 30000 });

    await page.getByText(/RELINK FIXTURE/i).first().click();

    // Chip text is the workgroup name; it sits beside the stage chip.
    await expect(page.getByText(GROUP_NAME, { exact: false }).first()).toBeVisible({
      timeout: 15000,
    });
  });

  test("imported portal links are absolute, not app routes", async ({ page }) => {
    // A root-relative "/workgroups/group/105/" href is the bug that produced a
    // 404 route error in the desktop app: the router tried to resolve it.
    await page.goto(`/${SLUG}/issues`);
    await page.waitForURL(`**/${SLUG}/issues`, { timeout: 30000 });
    await page.getByText(/RELINK FIXTURE/i).first().click();

    const portalLink = page.locator('a[href*="/workgroups/group/"]').first();
    await expect(portalLink).toBeVisible({ timeout: 15000 });
    const href = await portalLink.getAttribute("href");
    expect(href, "portal links must carry an origin").toMatch(/^https?:\/\//);
  });
});
