import { expect, test, type Page } from "@playwright/test";
import { loginAsDefault } from "./helpers";

const sessionId = "11111111-1111-4111-8111-111111111111";
const messageId = "22222222-2222-4222-8222-222222222222";
const runId = "33333333-3333-4333-8333-333333333333";
const previousWorkspaceId = "44444444-4444-4444-8444-444444444444";
const timestamp = "2026-09-16T08:00:00Z";

interface Submission {
  content: string;
  request_id: string;
  context: { workspace_id: string | null; timezone?: string };
}

// Exercise the real page, cache, and persisted drafts without contacting a
// model or allowing assistant tools to mutate the shared E2E workspace.
async function mockAssistant(page: Page, initialFailure: boolean) {
  const submissions: Submission[] = [];
  let hasRun = initialFailure;
  let status = initialFailure ? "failed" : "completed";
  let messages = initialFailure
    ? [{ id: messageId, session_id: sessionId, role: "user", content: "Summarize my tasks", created_at: timestamp }]
    : [];
  const run = () => ({
    id: runId,
    session_id: sessionId,
    message_id: messageId,
    status,
    active_tool: null,
    error: status === "failed" ? "The model could not be reached." : null,
    created_at: timestamp,
    updated_at: timestamp,
    finished_at: timestamp,
    version: 2,
    context: { workspace_id: previousWorkspaceId, timezone: "UTC" },
  });
  const session = () => ({
    id: sessionId,
    title: "Recovery fixture",
    focus_workspace_id: previousWorkspaceId,
    created_at: timestamp,
    updated_at: timestamp,
    latest_run: hasRun ? run() : null,
  });

  await page.route("**/api/assistant/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    const method = route.request().method();
    const fulfill = (json: unknown, code = 200) => route.fulfill({ status: code, json });
    if (path.endsWith("/availability")) return fulfill({ enabled: true, model_label: "Test model" });
    if (path.endsWith("/sessions")) return fulfill([session()]);
    if (path.endsWith(`/sessions/${sessionId}`)) return fulfill(session());
    if (path.endsWith("/runs")) return fulfill(hasRun ? [run()] : []);
    if (path.endsWith(`/runs/${runId}`)) return fulfill(run());
    if (path.endsWith("/artifacts")) return fulfill([]);
    if (path.endsWith("/messages") && method === "GET") return fulfill(messages);
    if (path.endsWith("/messages") && method === "POST") {
      const submission = route.request().postDataJSON() as Submission;
      submissions.push(submission);
      if (submissions.length === 1) return fulfill({ error: "Temporary connection failure" }, 503);
      messages = [{ id: messageId, session_id: sessionId, role: "user", content: submission.content, created_at: timestamp }];
      hasRun = true;
      status = "completed";
      return fulfill({ message_id: messageId, run_id: runId, created_at: timestamp }, 202);
    }
    return fulfill({ error: "Unexpected assistant test request" }, 404);
  });
  return submissions;
}

test("assistant restores a failed run after reload", async ({ page }) => {
  await mockAssistant(page, true);
  const slug = await loginAsDefault(page);
  await page.goto(`/${slug}/assistant`);
  await expect(page.getByText("Summarize my tasks", { exact: true })).toBeVisible();
  await expect(page.getByRole("status").filter({ hasText: "could not finish this reply" })).toBeVisible();
  await page.reload();
  await expect(page.getByRole("status").filter({ hasText: "could not finish this reply" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Stop", exact: true })).toHaveCount(0);
  await expect(page.getByRole("textbox")).toBeEnabled();
});

test("assistant retains a failed draft and retries its request in the current workspace", async ({ page }) => {
  const submissions = await mockAssistant(page, false);
  const slug = await loginAsDefault(page);
  await page.goto(`/${slug}/assistant`);
  const composer = page.getByRole("textbox");
  await composer.fill("Create a task for the pricing copy");
  await page.getByRole("button", { name: "Send", exact: true }).click();
  await expect.poll(() => submissions.length).toBe(1);
  await expect(composer).toHaveValue("Create a task for the pricing copy");

  // A reload must preserve both text and request identity after a lost send.
  await page.reload();
  await expect(composer).toHaveValue("Create a task for the pricing copy");
  await page.getByRole("button", { name: "Send", exact: true }).click();
  await expect.poll(() => submissions.length).toBe(2);
  expect(submissions[1]?.request_id).toBe(submissions[0]?.request_id);
  expect(submissions[0]?.request_id).toMatch(/^[0-9a-f-]{36}$/i);
  expect(submissions[1]?.context).toEqual(submissions[0]?.context);
  expect(submissions[0]?.context.workspace_id).toBeTruthy();
  expect(submissions[0]?.context.workspace_id).not.toBe(previousWorkspaceId);
  expect(submissions[0]?.context.timezone).toBeTruthy();
  await expect(composer).toHaveValue("");
  await expect(page.getByRole("button", { name: "Stop", exact: true })).toHaveCount(0);
});
