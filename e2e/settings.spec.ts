import { test, expect } from "@playwright/test";
import { createTestApi, loginAsDefault } from "./helpers";

test.describe("Settings", () => {
  test("updating workspace name reflects in sidebar immediately", async ({
    page,
  }) => {
    // This test RENAMES a workspace. Use a dedicated one — renaming the
    // shared E2E workspace breaks parallel tests that look the sidebar
    // trigger up by name.
    const api = await createTestApi();
    const wsName = "E2E Settings " + Date.now();
    const wsSlug = "e2e-settings-" + Date.now();
    await api.ensureWorkspace(wsName, wsSlug);
    await loginAsDefault(page, wsSlug);

    // The sidebar workspace trigger carries the current workspace name.
    await expect(
      page.getByRole("button", { name: new RegExp(wsName) }).first(),
    ).toBeVisible();

    // Settings is a direct sidebar link; the workspace name lives in the
    // General tab.
    await page.getByRole("link", { name: "Settings", exact: true }).click();
    await page.waitForURL("**/settings");
    await page.getByRole("tab", { name: "General" }).click();

    // The name field is the first visible text input of the General panel
    // (its Label is not programmatically associated, so no getByLabel).
    const nameInput = page.locator('main input[type="text"]:visible').first();
    await expect(nameInput).toHaveValue(wsName);

    // Filter to the visible Save — inactive tab panels keep hidden buttons
    // mounted in the DOM.
    const saveButton = page
      .getByRole("button", { name: "Save" })
      .locator("visible=true")
      .first();

    const newName = "Renamed WS " + Date.now();
    await nameInput.fill(newName);
    await saveButton.click();
    await expect(page.getByText("Workspace settings saved")).toBeVisible({
      timeout: 5000,
    });

    // Sidebar should reflect the new name WITHOUT page refresh
    await expect(
      page.getByRole("button", { name: new RegExp(newName) }).first(),
    ).toBeVisible();
  });
});
