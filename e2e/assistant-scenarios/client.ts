/**
 * AgoraClient — raw HTTP client for the assistant release scenario suite.
 *
 * Deliberately independent of e2e/fixtures.ts: that client is workspace-pinned
 * and single-user, while these scenarios need several users, several
 * workspaces, and the user-scoped (header-less) /api/assistant/* surface at the
 * same time. It is also plain fetch so the runner has no build step — it runs
 * under `node` type-stripping, not Playwright.
 *
 * Auth uses the dev fixed verification code (AGORA_DEV_VERIFICATION_CODE),
 * which is why this suite is local/dev only.
 */
import "../env.ts";

// `||` (not `??`) so an empty NEXT_PUBLIC_API_URL= in .env still falls back,
// matching e2e/fixtures.ts.
export const API_BASE =
  process.env.NEXT_PUBLIC_API_URL || `http://localhost:${process.env.PORT || "8080"}`;

const DEV_CODE = process.env.AGORA_DEV_VERIFICATION_CODE || "888888";

export interface ApiResponse<T> {
  status: number;
  body: T;
}

/** Thrown for transport/plumbing failures (not product assertions). */
export class HarnessError extends Error {}

export interface RequestOptions {
  body?: unknown;
  workspaceId?: string;
  timeoutMs?: number;
  headers?: Record<string, string>;
}

export interface Workspace {
  id: string;
  name: string;
  slug: string;
}

export interface Issue {
  id: string;
  number: number;
  identifier: string;
  title: string;
  description: string | null;
  status: string;
  priority: string;
  assignee_type: string | null;
  assignee_id: string | null;
  project_id: string | null;
  workspace_id: string;
  labels?: Array<{ id: string; name: string }>;
  metadata?: Record<string, unknown>;
}

export interface Project {
  id: string;
  title: string;
  description: string | null;
  status?: string;
}

export interface AssistantToolCall {
  id?: string;
  name: string;
  arguments: string;
}

export interface AssistantMessage {
  id: string;
  session_id: string;
  role: string;
  content: string;
  tool_calls?: AssistantToolCall[];
  tool_call_id?: string;
  tool_name?: string;
  tool_result?: unknown;
  created_at: string;
}

export interface AssistantRun {
  id: string;
  session_id: string;
  message_id: string;
  status: string;
  active_tool: string | null;
  error: string | null;
}

export interface SendResult {
  message_id: string;
  run_id: string;
  created_at: string;
}

/**
 * The `operation` object of a needs_confirmation tool result, and the body of
 * the confirm/reject/read endpoints. Mirrors
 * server/internal/handler/assistant_operations.go (assistantOperationPayload)
 * and the plan's pinned wire contract.
 */
export interface AssistantOperation {
  id: string;
  tool_name: string;
  summary: string;
  workspace_slug: string;
  target: { type: string; identifier: string; title: string };
  status?: string;
  created_at?: string;
  expires_at?: string;
}

export interface AssistantConfirmResult {
  operation: AssistantOperation;
  message: AssistantMessage;
}

export class AgoraClient {
  token: string | null = null;
  email = "";
  userId = "";
  name = "";

  async request<T = unknown>(
    method: string,
    path: string,
    opts: RequestOptions = {},
  ): Promise<ApiResponse<T>> {
    const headers: Record<string, string> = {
      "Content-Type": "application/json",
      // Same dev marker e2e/fixtures.ts sends: keeps fixture rows out of any
      // configured Telegram report room while still exercising WS invalidation.
      "X-Client-Platform": "e2e",
      "X-Client-Version": "assistant-scenarios",
      ...(opts.headers ?? {}),
    };
    if (this.token) headers["Authorization"] = `Bearer ${this.token}`;
    if (opts.workspaceId) headers["X-Workspace-ID"] = opts.workspaceId;

    let res: Response;
    try {
      res = await fetch(`${API_BASE}${path}`, {
        method,
        headers,
        body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
        signal: AbortSignal.timeout(opts.timeoutMs ?? 30000),
      });
    } catch (err) {
      throw new HarnessError(`${method} ${path} failed: ${(err as Error).message}`);
    }
    const text = await res.text();
    let body: unknown = null;
    if (text.trim()) {
      try {
        body = JSON.parse(text);
      } catch {
        body = { raw: text.slice(0, 400) };
      }
    }
    return { status: res.status, body: body as T };
  }

  /**
   * Dev login.
   *
   * verify-code accepts the dev code only when a live verification_code row
   * exists, so a failed send-code (wrong intent for the account, throttling)
   * surfaces as "invalid or expired code" one call later. Each intent is
   * therefore gated on its own send-code succeeding, and the whole pair is
   * retried once — that turns a transient send into a slow login rather than a
   * scenario error.
   */
  async login(email: string, intents: string[] = ["signup", "login"]): Promise<void> {
    const failures: string[] = [];
    for (let round = 0; round < 2; round++) {
      for (const intent of intents) {
        const sent = await this.request<{ error?: string }>("POST", "/auth/send-code", {
          body: { email, intent },
        });
        if (sent.status !== 200) {
          failures.push(`send-code(${intent})=${sent.status}`);
          continue;
        }
        const res = await this.request<{ token: string; user: { id: string; name: string } }>(
          "POST",
          "/auth/verify-code",
          { body: { email, code: DEV_CODE, intent } },
        );
        if (res.status === 200 && res.body?.token) {
          this.token = res.body.token;
          this.email = email;
          this.userId = res.body.user?.id ?? "";
          this.name = res.body.user?.name ?? "";
          return;
        }
        failures.push(`verify(${intent})=${res.status}`);
      }
      await new Promise((r) => setTimeout(r, 1500));
    }
    throw new HarnessError(
      `dev login failed for ${email} [${failures.join(", ")}] — is AGORA_DEV_VERIFICATION_CODE set on the server?`,
    );
  }

  // --- workspaces ----------------------------------------------------------

  async listWorkspaces(): Promise<Workspace[]> {
    const res = await this.request<Workspace[]>("GET", "/api/workspaces");
    return Array.isArray(res.body) ? res.body : [];
  }

  async createWorkspace(name: string, slug: string): Promise<Workspace> {
    const res = await this.request<Workspace>("POST", "/api/workspaces", { body: { name, slug } });
    if (res.status !== 200 && res.status !== 201) {
      throw new HarnessError(`create workspace ${slug}: ${res.status} ${JSON.stringify(res.body)}`);
    }
    return res.body;
  }

  async deleteWorkspace(id: string): Promise<number> {
    const res = await this.request("DELETE", `/api/workspaces/${id}`);
    return res.status;
  }

  async listMembers(workspaceId: string): Promise<Array<{ id: string; user_id: string; email: string; role: string }>> {
    const res = await this.request<Array<{ id: string; user_id: string; email: string; role: string }>>(
      "GET",
      `/api/workspaces/${workspaceId}/members`,
    );
    return Array.isArray(res.body) ? res.body : [];
  }

  async invite(workspaceId: string, email: string, role = "member"): Promise<{ id: string }> {
    const res = await this.request<{ id: string }>("POST", `/api/workspaces/${workspaceId}/members`, {
      body: { email, role },
    });
    if (res.status !== 200 && res.status !== 201) {
      throw new HarnessError(`invite ${email}: ${res.status} ${JSON.stringify(res.body)}`);
    }
    return res.body;
  }

  async listMyInvitations(): Promise<Array<{ id: string; workspace_id?: string }>> {
    const res = await this.request<Array<{ id: string; workspace_id?: string }>>("GET", "/api/invitations");
    return Array.isArray(res.body) ? res.body : [];
  }

  async acceptInvitation(id: string): Promise<number> {
    const res = await this.request("POST", `/api/invitations/${id}/accept`);
    return res.status;
  }

  async removeMember(workspaceId: string, memberId: string): Promise<number> {
    const res = await this.request("DELETE", `/api/workspaces/${workspaceId}/members/${memberId}`);
    return res.status;
  }

  // --- issues / projects ---------------------------------------------------

  async createIssue(workspaceId: string, body: Record<string, unknown>): Promise<Issue> {
    const res = await this.request<Issue>("POST", "/api/issues", { body, workspaceId });
    if (res.status !== 200 && res.status !== 201) {
      throw new HarnessError(`create issue: ${res.status} ${JSON.stringify(res.body)}`);
    }
    return res.body;
  }

  async listIssues(workspaceId: string, query = "limit=100"): Promise<Issue[]> {
    const res = await this.request<{ issues?: Issue[] } | Issue[]>("GET", `/api/issues?${query}`, { workspaceId });
    const body = res.body as { issues?: Issue[] } | Issue[];
    if (Array.isArray(body)) return body;
    return body?.issues ?? [];
  }

  async getIssue(workspaceId: string, id: string): Promise<ApiResponse<Issue>> {
    return this.request<Issue>("GET", `/api/issues/${id}`, { workspaceId });
  }

  async deleteIssue(workspaceId: string, id: string): Promise<number> {
    const res = await this.request("DELETE", `/api/issues/${id}`, { workspaceId });
    return res.status;
  }

  async listComments(workspaceId: string, issueId: string): Promise<Array<{ id: string; content: string }>> {
    const res = await this.request<Array<{ id: string; content: string }> | { comments?: Array<{ id: string; content: string }> }>(
      "GET",
      `/api/issues/${issueId}/comments`,
      { workspaceId },
    );
    const body = res.body as Array<{ id: string; content: string }> | { comments?: Array<{ id: string; content: string }> };
    if (Array.isArray(body)) return body;
    return body?.comments ?? [];
  }

  async createComment(workspaceId: string, issueId: string, content: string): Promise<number> {
    const res = await this.request("POST", `/api/issues/${issueId}/comments`, { body: { content }, workspaceId });
    return res.status;
  }

  async createProject(workspaceId: string, body: Record<string, unknown>): Promise<Project> {
    const res = await this.request<Project>("POST", "/api/projects", { body, workspaceId });
    if (res.status !== 200 && res.status !== 201) {
      throw new HarnessError(`create project: ${res.status} ${JSON.stringify(res.body)}`);
    }
    return res.body;
  }

  async listProjects(workspaceId: string): Promise<Project[]> {
    const res = await this.request<Project[] | { projects?: Project[] }>("GET", "/api/projects", { workspaceId });
    const body = res.body as Project[] | { projects?: Project[] };
    if (Array.isArray(body)) return body;
    return body?.projects ?? [];
  }

  async listAgents(workspaceId: string): Promise<Array<{ id: string; name: string }>> {
    const res = await this.request<Array<{ id: string; name: string }> | { agents?: Array<{ id: string; name: string }> }>(
      "GET",
      "/api/agents",
      { workspaceId },
    );
    const body = res.body as Array<{ id: string; name: string }> | { agents?: Array<{ id: string; name: string }> };
    if (Array.isArray(body)) return body;
    return body?.agents ?? [];
  }


  async listSprints(workspaceId: string): Promise<Array<{ id: string; name: string; project_id?: string }>> {
    const res = await this.request<Array<{ id: string; name: string }> | { sprints?: Array<{ id: string; name: string }> }>(
      "GET",
      "/api/sprints",
      { workspaceId },
    );
    const body = res.body as Array<{ id: string; name: string }> | { sprints?: Array<{ id: string; name: string }> };
    if (Array.isArray(body)) return body;
    return body?.sprints ?? [];
  }

  async listLabels(workspaceId: string): Promise<Array<{ id: string; name: string }>> {
    const res = await this.request<Array<{ id: string; name: string }> | { labels?: Array<{ id: string; name: string }> }>(
      "GET",
      "/api/labels",
      { workspaceId },
    );
    const body = res.body as Array<{ id: string; name: string }> | { labels?: Array<{ id: string; name: string }> };
    if (Array.isArray(body)) return body;
    return body?.labels ?? [];
  }

  // --- assistant -----------------------------------------------------------

  async createSession(focusWorkspaceId?: string): Promise<string> {
    const res = await this.request<{ id: string }>("POST", "/api/assistant/sessions", {
      body: focusWorkspaceId ? { focus_workspace_id: focusWorkspaceId } : {},
    });
    if (res.status !== 201 && res.status !== 200) {
      throw new HarnessError(`create session: ${res.status} ${JSON.stringify(res.body)}`);
    }
    return res.body.id;
  }

  async getSession(id: string): Promise<ApiResponse<unknown>> {
    return this.request("GET", `/api/assistant/sessions/${id}`);
  }

  async sendMessage(
    sessionId: string,
    content: string,
    context: { workspace_id: string | null; timezone?: string },
    requestId?: string,
  ): Promise<ApiResponse<SendResult>> {
    const body: Record<string, unknown> = { content, context };
    if (requestId) body.request_id = requestId;
    return this.request<SendResult>("POST", `/api/assistant/sessions/${sessionId}/messages`, { body });
  }

  async transcript(sessionId: string): Promise<AssistantMessage[]> {
    const res = await this.request<AssistantMessage[]>("GET", `/api/assistant/sessions/${sessionId}/messages`);
    return Array.isArray(res.body) ? res.body : [];
  }

  async getRun(runId: string): Promise<ApiResponse<AssistantRun>> {
    return this.request<AssistantRun>("GET", `/api/assistant/runs/${runId}`);
  }

  async cancelRun(runId: string): Promise<number> {
    const res = await this.request("POST", `/api/assistant/runs/${runId}/cancel`);
    return res.status;
  }

  // --- confirmation binding -------------------------------------------------
  //
  // The out-of-band human gesture. A model can write `confirm: true` into an
  // argument; it cannot make an authenticated HTTP request as the session's
  // owner, which is the whole point of these three endpoints.

  async listOperations(sessionId: string): Promise<AssistantOperation[]> {
    const res = await this.request<AssistantOperation[]>("GET", `/api/assistant/sessions/${sessionId}/operations`);
    return Array.isArray(res.body) ? res.body : [];
  }

  async getOperation(id: string): Promise<ApiResponse<AssistantOperation>> {
    return this.request<AssistantOperation>("GET", `/api/assistant/operations/${id}`);
  }

  /** Press Confirm. 200 + receipt, or 409 on a spent/expired/changed operation. */
  async confirmOperation(id: string): Promise<ApiResponse<AssistantConfirmResult & { error?: string }>> {
    return this.request<AssistantConfirmResult & { error?: string }>(
      "POST",
      `/api/assistant/operations/${id}/confirm`,
      { timeoutMs: 60000 },
    );
  }

  /** Press Cancel. 204 and a "cancelled" tool message on the transcript. */
  async rejectOperation(id: string): Promise<ApiResponse<{ error?: string }>> {
    return this.request<{ error?: string }>("POST", `/api/assistant/operations/${id}/reject`);
  }

  async getArtifact(id: string): Promise<ApiResponse<{ id: string; kind: string; version: number; content: string; title: string }>> {
    return this.request("GET", `/api/assistant/artifacts/${id}`);
  }

  async sessionArtifacts(sessionId: string): Promise<Array<{ id: string; kind: string; version: number; title: string }>> {
    const res = await this.request<Array<{ id: string; kind: string; version: number; title: string }>>(
      "GET",
      `/api/assistant/sessions/${sessionId}/artifacts`,
    );
    return Array.isArray(res.body) ? res.body : [];
  }
}
