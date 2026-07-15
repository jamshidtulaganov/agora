import { test, expect } from "@playwright/test";
import { createTestApi, loginAsDefault } from "./helpers";
import type { TestApiClient } from "./fixtures";

test.describe("Comments", () => {
  let api: TestApiClient;
  let slug: string;
  let issueId: string;

  test.beforeEach(async ({ page }) => {
    api = await createTestApi();
    const issue = await api.createIssue("E2E Comment Test " + Date.now());
    issueId = issue.id;
    slug = await loginAsDefault(page);
  });

  test.afterEach(async () => {
    await api.cleanup();
  });

  test("can add a comment on an issue", async ({ page }) => {
    // Go straight to the fixture issue — clicking the board's first link
    // races other parallel workers deleting THEIR issues.
    await page.goto(`/${slug}/issues/${issueId}`);

    // Wait for issue detail to load
    await expect(page.locator("text=Properties")).toBeVisible();

    // The comment composer is a rich-text editor (contenteditable), not an
    // <input>. It is the last editable region on the detail page (title and
    // description editors come first).
    const commentText = "E2E comment " + Date.now();
    // Target the comment composer via its Tiptap placeholder — the detail
    // page mounts several editors (title, description, chat). The
    // placeholder node disappears on first keystroke, so use it only to
    // click, then type through the keyboard (real keystrokes are required:
    // fill() bypasses ProseMirror and leaves the send button disabled).
    const composer = page.locator(
      '[contenteditable="true"]:has([data-placeholder="Leave a comment..."])',
    );
    await composer.click();
    await expect(composer).toBeFocused();
    await page.keyboard.type(commentText);

    // Submit via the composer's send button. Filter to the visible one —
    // the chat dock mounts its own Send later in the DOM.
    await page
      .getByRole("button", { name: "Send" })
      .locator("visible=true")
      .first()
      .click();

    // Comment should appear in the activity section
    await expect(page.locator(`text=${commentText}`)).toBeVisible({
      timeout: 5000,
    });
  });

  test("comment submit button is disabled when empty", async ({ page }) => {
    await page.goto(`/${slug}/issues/${issueId}`);

    await expect(page.locator("text=Properties")).toBeVisible();

    // Send button should be disabled while the composer is empty
    const submitBtn = page
      .getByRole("button", { name: "Send" })
      .locator("visible=true")
      .first();
    await expect(submitBtn).toBeDisabled();
  });
});
