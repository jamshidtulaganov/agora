import { test, expect } from "@playwright/test";
import { execFileSync } from "node:child_process";
import "./env";

const apiBase = process.env.NEXT_PUBLIC_API_URL || `http://localhost:${process.env.PORT || "8080"}`;
const frontendBase = process.env.PLAYWRIGHT_BASE_URL || "http://127.0.0.1:3000";

async function createDockerSession() {
  const email = `qa-report-${Date.now()}@agora.dev`;
  const code = process.env.AGORA_DEV_VERIFICATION_CODE;
  if (!code) throw new Error("AGORA_DEV_VERIFICATION_CODE is required for this local Docker test");

  const sendResponse = await fetch(`${apiBase}/auth/send-code`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ email }),
  });
  expect(sendResponse.ok).toBe(true);

  const verifyResponse = await fetch(`${apiBase}/auth/verify-code`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ email, code }),
  });
  expect(verifyResponse.ok).toBe(true);
  const login = (await verifyResponse.json()) as { token: string };

  const request = async (path: string, init?: RequestInit, workspaceSlug?: string) => {
    const response = await fetch(`${apiBase}${path}`, {
      ...init,
      headers: {
        "Content-Type": "application/json",
        "X-Client-Platform": "e2e",
        "X-Client-Version": "playwright",
        Authorization: `Bearer ${login.token}`,
        ...(workspaceSlug ? { "X-Workspace-Slug": workspaceSlug } : {}),
        ...((init?.headers as Record<string, string>) ?? {}),
      },
    });
    if (!response.ok) throw new Error(`${init?.method ?? "GET"} ${path} failed: ${response.status}`);
    return response;
  };

  const workspaceResponse = await request("/api/workspaces");
  const workspaces = (await workspaceResponse.json()) as Array<{ id: string; slug: string }>;
  let workspace = workspaces[0];
  if (!workspace) {
    const created = await request("/api/workspaces", {
      method: "POST",
      body: JSON.stringify({ name: "QA Report E2E", slug: `qa-report-${Date.now()}` }),
    });
    workspace = (await created.json()) as { id: string; slug: string };
  }

  await request("/api/me/onboarding/complete", {
    method: "POST",
    body: JSON.stringify({ completion_path: "skip_existing", workspace_id: workspace.id }),
  });

  return { token: login.token, workspace, request };
}

test("QA report explains failed behavior before technical evidence", async ({ page }) => {
  const session = await createDockerSession();
  const issueResponse = await session.request("/api/issues", {
    method: "POST",
    body: JSON.stringify({
      title: `QA report UI demo ${Date.now()}`,
      status: "in_review",
      description: "Create the required smoke documentation and preserve existing behavior.",
    }),
  }, session.workspace.slug);
  const issue = (await issueResponse.json()) as { id: string; workspace_id: string };

  await page.addInitScript((token) => {
    localStorage.setItem("agora_token", token);
  }, session.token);

  try {
    const result = {
      verdict: "fail",
      summary: "docs/orchestration-worktree-smoke.md not found on any branch",
      commands: [
        { cmd: "node /tmp/case-11df7aa2.mjs", baseline_exit: null, branch_exit: 0, kind: "pass", error: "" },
        { cmd: "node /tmp/case-12cf7cc8.mjs", baseline_exit: null, branch_exit: 0, kind: "pass", error: "" },
        {
          cmd: "check: docs/orchestration-worktree-smoke.md exists with exact content",
          baseline_exit: null,
          branch_exit: 1,
          kind: "new_failure",
          error: "file not found on any branch — implementation not pushed",
        },
      ],
      screenshots: [],
    };
    const encodedResult = Buffer.from(JSON.stringify(result)).toString("base64");
    const encodedSummary = Buffer.from(result.summary).toString("base64");
    execFileSync(
      "docker",
      ["compose", "exec", "-T", "postgres", "psql", "-U", "agora", "-d", "agora"],
      {
        cwd: process.cwd(),
        input: `INSERT INTO qa_evidence (workspace_id, issue_id, verdict, summary, result_json, source)
          VALUES ('${issue.workspace_id}', '${issue.id}', 'fail',
            convert_from(decode('${encodedSummary}', 'base64'), 'UTF8'),
            convert_from(decode('${encodedResult}', 'base64'), 'UTF8')::jsonb, 'agent');`,
      },
    );

    const createCase = async (body: Record<string, unknown>) => {
      const response = await session.request(
        `/api/issues/${issue.id}/test-cases`,
        { method: "POST", body: JSON.stringify(body) },
        session.workspace.slug,
      );
      return (await response.json()) as { id: string };
    };
    const failedCase = await createCase({
      title: "Required smoke documentation is created",
      steps: "Inspect the delivered documentation artifact after implementation.",
      expected: "The requested file exists and contains the exact approved statement.",
      kind: "automated",
      category: "positive",
      preconditions: "The implementation branch is available for verification.",
      priority: "p1",
      modality: "unit",
      criterion_ref: "AC1",
    });
    await session.request(
      `/api/test-cases/${failedCase.id}/runs`,
      {
        method: "POST",
        body: JSON.stringify({
          status: "fail",
          output: "The required documentation was missing from the tested result.",
        }),
      },
      session.workspace.slug,
    );

    const passedCase = await createCase({
      title: "Existing application behavior remains unchanged",
      steps: "Run the existing regression suite.",
      expected: "Every existing regression check passes.",
      kind: "automated",
      category: "positive",
      priority: "p2",
      modality: "unit",
      criterion_ref: "AC2",
    });
    await session.request(
      `/api/test-cases/${passedCase.id}/runs`,
      { method: "POST", body: JSON.stringify({ status: "pass", output: "" }) },
      session.workspace.slug,
    );

    // Enter through the workspace route once so the client API installs the
    // route-derived workspace header before IssueDetail starts its queries.
    await page.goto(`${frontendBase}/${session.workspace.slug}/issues`);
    await page.waitForURL("**/issues", { timeout: 10_000 });
    await page.goto(`${frontendBase}/${session.workspace.slug}/issues/${issue.id}`);

    const report = page.locator("section", { hasText: "QA report" }).last();
    await expect(report.getByText("At least one expected behavior did not match the tested result.")).toBeVisible();
    await expect(report.getByText("What QA checked")).toBeVisible();
    await expect(report.getByText("Required smoke documentation is created")).toBeVisible();
    await expect(report.getByText("The requested file exists and contains the exact approved statement.")).toBeVisible();
    await expect(report.getByText("The required documentation was missing from the tested result.")).toBeVisible();
    await expect(report.getByText("Required file has the expected content")).toBeHidden();
    await expect(report.getByText("The required file was not present in the tested result.")).toBeHidden();
    await expect(report.getByText("check: docs/orchestration-worktree-smoke.md exists with exact content")).toBeHidden();

    await report.screenshot({ path: "/tmp/agora-qa-report.png" });
  } finally {
    await session.request(`/api/issues/${issue.id}`, { method: "DELETE" }, session.workspace.slug);
  }
});
