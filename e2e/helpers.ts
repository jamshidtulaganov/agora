import { readFileSync } from "node:fs";
import { type Page } from "@playwright/test";
import { TestApiClient } from "./fixtures";
import { AUTH_STATE_PATH } from "./global-setup";

export const DEFAULT_E2E_WORKSPACE_NAME = "E2E Workspace";

/** Token + workspace minted once by global-setup. One shared login keeps
 *  parallel workers off the backend's per-email send-code cooldown. */
function sharedAuth(): { token: string; slug: string } {
  return JSON.parse(readFileSync(AUTH_STATE_PATH, "utf8"));
}

/**
 * Authenticate the page as the default E2E user. The token is seeded through
 * addInitScript BEFORE any page load — visiting /login with a token and
 * navigating away raced the app's own logged-in redirect and hung
 * intermittently — so the first navigation goes straight to the issues page.
 *
 * Returns the E2E workspace slug so callers can build workspace-scoped URLs.
 */
export async function loginAsDefault(
  page: Page,
  targetSlug?: string,
): Promise<string> {
  const { token, slug } = sharedAuth();
  const workspace = { slug: targetSlug ?? slug };
  await page.addInitScript((t) => {
    localStorage.setItem("agora_token", t);
    // Keep the floating chat dock closed: it renders at z-50 over
    // bottom-anchored controls (Save/Send/Create) and intercepts their
    // clicks. The store reads this raw "false" as an explicit user choice.
    localStorage.setItem("agora:chat:isOpen", "false");
  }, token);
  await page.goto(`/${workspace.slug}/issues`);
  await page.waitForURL(`**/${workspace.slug}/issues`, { timeout: 15000 });
  return workspace.slug;
}

/**
 * Create a TestApiClient authenticated as the default E2E user (token from
 * global-setup — no per-test login).
 * Call api.cleanup() in afterEach to remove test data created during the test.
 */
export async function createTestApi(): Promise<TestApiClient> {
  const { token } = sharedAuth();
  const api = new TestApiClient();
  api.useToken(token);
  await api.ensureWorkspace(DEFAULT_E2E_WORKSPACE_NAME, "e2e-workspace");
  return api;
}

/**
 * Open the workspace dropdown menu. The sidebar renders plain lists (no
 * <aside>/<nav> landmarks), so target the trigger by its visible workspace
 * name and wait for the Base UI menu popup.
 */
export async function openWorkspaceMenu(
  page: Page,
  workspaceName: string = DEFAULT_E2E_WORKSPACE_NAME,
) {
  await page
    .getByRole("button", { name: new RegExp(workspaceName) })
    .first()
    .click();
  await page.getByRole("menu").waitFor({ state: "visible" });
}
