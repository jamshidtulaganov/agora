import { test, expect, type Page } from "@playwright/test";
import { TestApiClient } from "./fixtures";

// Smoke test for the current onboarding flow: welcome → workspace →
// platform fork. The former per-question steps (source / role / use_case)
// were removed from the product. Uses a unique email per run so the user
// is always a fresh, un-onboarded user landing on /onboarding.

const SHOTS_DIR = "/tmp/onboarding-v2-shots";

test.use({ viewport: { width: 1440, height: 900 } });

async function loginFreshUser(page: Page, email: string, name: string) {
  const api = new TestApiClient();
  await api.login(email, name);
  const token = api.getToken();
  // Seed the token before ANY page load — visiting /login while
  // authenticated triggers an app redirect that races test navigation.
  await page.addInitScript((t) => {
    localStorage.setItem("agora_token", t);
  }, token);
}

test("onboarding — welcome → workspace step", async ({ page }) => {
  await loginFreshUser(page, `onboarding-v2-${Date.now()}@localhost`, "OBv2 Tester");
  await page.goto("/onboarding");

  // 1. Welcome screen
  const continueButton = page.getByRole("button", { name: "Continue on web" });
  await expect(continueButton).toBeVisible({ timeout: 15000 });
  await page.screenshot({ path: `${SHOTS_DIR}/01-welcome.png`, fullPage: false });

  await continueButton.click();

  // 2. Workspace step
  await expect(
    page.getByRole("heading", { name: /Name your workspace/i }),
  ).toBeVisible({ timeout: 10000 });
  await page.screenshot({ path: `${SHOTS_DIR}/02-workspace.png` });
});

// NOTE: the former zh-Hans smoke test was removed on purpose: zh-Hans is
// currently a disabled locale (product decision — pick-locale falls back to
// en). Reinstate a locale smoke test here when zh-Hans is re-enabled.
