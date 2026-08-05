import { test, expect } from "@playwright/test";
import { loginAsDefault } from "./helpers";

test.describe("Navigation", () => {
  test.beforeEach(async ({ page }) => {
    await loginAsDefault(page);
  });

  test("sidebar navigation works", async ({ page }) => {
    // The sidebar renders plain lists (no <nav> landmark) — target links by
    // their accessible name. "Issues" must be exact so it doesn't collide
    // with "My Issues".
    await page.getByRole("link", { name: "Inbox", exact: true }).click();
    await page.waitForURL("**/inbox");
    await expect(page).toHaveURL(/\/inbox/);

    await page.getByRole("link", { name: "Agents", exact: true }).click();
    await page.waitForURL("**/agents");
    await expect(page).toHaveURL(/\/agents/);

    await page.getByRole("link", { name: "Issues", exact: true }).click();
    await page.waitForURL("**/issues");
    await expect(page).toHaveURL(/\/issues/);
  });

  test("settings page loads via sidebar", async ({ page }) => {
    // Settings is a direct sidebar link now (the workspace menu only carries
    // account/workspace switching actions).
    await page.getByRole("link", { name: "Settings", exact: true }).click();
    await page.waitForURL("**/settings");

    await expect(page.getByRole("heading", { name: "Settings" })).toBeVisible();
    await expect(page.getByRole("tab", { name: "Members", exact: true })).toBeVisible();
  });

  test("agents page shows agent list", async ({ page }) => {
    await page.getByRole("link", { name: "Agents", exact: true }).click();
    await page.waitForURL("**/agents");

    // exact:true — the empty state ("No agents yet") and the chat panel
    // ("Chat with your agents") also expose headings containing "Agents".
    await expect(
      page.getByRole("heading", { name: "Agents", exact: true }),
    ).toBeVisible();
  });
});
