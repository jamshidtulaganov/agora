import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient, ApiError } from "./client";

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("Bitrix import API", () => {
  it("uses the self-scoped endpoint for the caller's own tasks", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ accepted: 4, errors: [] }), {
        status: 202,
        headers: { "Content-Type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);

    const client = new ApiClient("https://api.example.test");
    const response = await client.importMyBitrixTasks();

    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(fetchMock.mock.calls[0]?.[0]).toBe(
      "https://api.example.test/api/bitrix/import/mine",
    );
    expect(fetchMock.mock.calls[0]?.[1]?.method).toBe("POST");
    expect(response).toMatchObject({ accepted: 4, errors: [] });
  });
});

describe("ApiClient", () => {
  it("sends the rendered orchestration question identity with an answer", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response("null", {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);

    const client = new ApiClient("https://api.example.test");
    await client.respondToOrchestrationStep("issue-1", "step-1", {
      question_id: "question-2",
      message: "Use the v2 contract.",
    });

    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(fetchMock.mock.calls[0]?.[0]).toBe(
      "https://api.example.test/api/issues/issue-1/orchestration/steps/step-1/respond",
    );
    expect(JSON.parse(fetchMock.mock.calls[0]?.[1]?.body as string)).toEqual({
      question_id: "question-2",
      message: "Use the v2 contract.",
    });
  });

  it("preserves HTTP status on failed requests", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ error: "workspace slug already exists" }), {
          status: 409,
          statusText: "Conflict",
          headers: { "Content-Type": "application/json" },
        }),
      ),
    );

    const client = new ApiClient("https://api.example.test");

    try {
      await client.createWorkspace({ name: "Test", slug: "test" });
      throw new Error("expected createWorkspace to fail");
    } catch (error) {
      expect(error).toBeInstanceOf(ApiError);
      expect(error).toMatchObject({
        message: "workspace slug already exists",
        status: 409,
        statusText: "Conflict",
      });
    }
  });

  it("uses the expected HTTP contract for autopilot endpoints", async () => {
    const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(
      new Response(JSON.stringify({ autopilots: [], runs: [], total: 0 }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    ));
    vi.stubGlobal("fetch", fetchMock);

    const client = new ApiClient("https://api.example.test");

    await client.listAutopilots({ status: "active" });
    await client.getAutopilot("ap-1");
    await client.createAutopilot({
      title: "Daily triage",
      project_id: "project-1",
      assignee_id: "agent-1",
      execution_mode: "create_issue",
    });
    await client.updateAutopilot("ap-1", { status: "paused", project_id: null });
    await client.deleteAutopilot("ap-1");
    await client.triggerAutopilot("ap-1");
    await client.listAutopilotRuns("ap-1", { limit: 10, offset: 20 });
    await client.createAutopilotTrigger("ap-1", {
      kind: "schedule",
      cron_expression: "0 9 * * *",
      timezone: "UTC",
    });
    await client.updateAutopilotTrigger("ap-1", "tr-1", { enabled: false });
    await client.deleteAutopilotTrigger("ap-1", "tr-1");
    await client.rotateAutopilotTriggerWebhookToken("ap-1", "tr-1");

    const calls = fetchMock.mock.calls.map(([url, init]) => ({
      url,
      method: init?.method ?? "GET",
      body: init?.body,
    }));

    expect(calls).toMatchObject([
      { url: "https://api.example.test/api/autopilots?status=active", method: "GET" },
      { url: "https://api.example.test/api/autopilots/ap-1", method: "GET" },
      {
        url: "https://api.example.test/api/autopilots",
        method: "POST",
        body: JSON.stringify({
          title: "Daily triage",
          project_id: "project-1",
          assignee_id: "agent-1",
          execution_mode: "create_issue",
        }),
      },
      {
        url: "https://api.example.test/api/autopilots/ap-1",
        method: "PATCH",
        body: JSON.stringify({ status: "paused", project_id: null }),
      },
      { url: "https://api.example.test/api/autopilots/ap-1", method: "DELETE" },
      { url: "https://api.example.test/api/autopilots/ap-1/trigger", method: "POST" },
      { url: "https://api.example.test/api/autopilots/ap-1/runs?limit=10&offset=20", method: "GET" },
      {
        url: "https://api.example.test/api/autopilots/ap-1/triggers",
        method: "POST",
        body: JSON.stringify({
          kind: "schedule",
          cron_expression: "0 9 * * *",
          timezone: "UTC",
        }),
      },
      {
        url: "https://api.example.test/api/autopilots/ap-1/triggers/tr-1",
        method: "PATCH",
        body: JSON.stringify({ enabled: false }),
      },
      { url: "https://api.example.test/api/autopilots/ap-1/triggers/tr-1", method: "DELETE" },
      {
        url: "https://api.example.test/api/autopilots/ap-1/triggers/tr-1/rotate-webhook-token",
        method: "POST",
      },
    ]);
  });

  it("emits X-Client-* headers when identity is configured", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify([]), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);

    const client = new ApiClient("https://api.example.test", {
      identity: { platform: "desktop", version: "1.2.3", os: "macos" },
    });
    await client.listWorkspaces();

    const headers = fetchMock.mock.calls[0]![1]!.headers as Record<string, string>;
    expect(headers["X-Client-Platform"]).toBe("desktop");
    expect(headers["X-Client-Version"]).toBe("1.2.3");
    expect(headers["X-Client-OS"]).toBe("macos");
  });

  it("omits X-Client-* headers when identity is not configured", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify([]), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);

    const client = new ApiClient("https://api.example.test");
    await client.listWorkspaces();

    const headers = fetchMock.mock.calls[0]![1]!.headers as Record<string, string>;
    expect(headers["X-Client-Platform"]).toBeUndefined();
    expect(headers["X-Client-Version"]).toBeUndefined();
    expect(headers["X-Client-OS"]).toBeUndefined();
  });

  it("uses the expected HTTP contract for comment trigger preview and suppress", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ agents: [] }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify({
          id: "comment-1",
          issue_id: "issue-1",
          author_type: "member",
          author_id: "user-1",
          content: "hello",
          type: "comment",
          parent_id: null,
          reactions: [],
          attachments: [],
          created_at: "2026-06-05T00:00:00Z",
          updated_at: "2026-06-05T00:00:00Z",
        }), {
          status: 201,
          headers: { "Content-Type": "application/json" },
        }),
      );
    vi.stubGlobal("fetch", fetchMock);

    const client = new ApiClient("https://api.example.test");
    await client.previewCommentTriggers("issue-1", "hello", "parent-1");
    await client.createComment(
      "issue-1",
      "hello",
      "comment",
      "parent-1",
      ["attachment-1"],
      ["agent-1"],
      "plan",
    );

    expect(fetchMock.mock.calls.map(([url, init]) => ({
      url,
      method: init?.method,
      body: init?.body,
    }))).toMatchObject([
      {
        url: "https://api.example.test/api/issues/issue-1/comments/trigger-preview",
        method: "POST",
        body: JSON.stringify({ content: "hello", parent_id: "parent-1" }),
      },
      {
        url: "https://api.example.test/api/issues/issue-1/comments",
        method: "POST",
        body: JSON.stringify({
          content: "hello",
          type: "comment",
          parent_id: "parent-1",
          attachment_ids: ["attachment-1"],
          suppress_agent_ids: ["agent-1"],
          run_mode: "plan",
        }),
      },
    ]);
  });

  it("uses the Cloud Runtime node API contract", async () => {
    const node = {
      id: "node-1",
      owner_id: "user-1",
      instance_id: "i-0123456789abcdef0",
      region: "us-west-2",
      instance_type: "g5.xlarge",
      image_id: "ami-1",
      subnet_id: "subnet-1",
      name: "gpu-dev-01",
      status: "launching",
      tags: {},
      metadata: {},
      created_at: "2026-05-21T08:30:00Z",
      updated_at: "2026-05-21T08:30:00Z",
    };
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify([]), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify(node), {
          status: 201,
          headers: { "Content-Type": "application/json" },
        }),
      );
    vi.stubGlobal("fetch", fetchMock);

    const client = new ApiClient("https://api.example.test");
    await client.listCloudRuntimeNodes({ limit: 20, offset: 5 });
    await client.createCloudRuntimeNode(
      { instance_type: "g5.xlarge", name: "gpu-dev-01" },
    );

    const listCall = fetchMock.mock.calls[0]!;
    const createCall = fetchMock.mock.calls[1]!;
    expect(listCall[0]).toBe(
      "https://api.example.test/api/cloud-runtime/nodes?limit=20&offset=5",
    );
    expect(createCall[0]).toBe(
      "https://api.example.test/api/cloud-runtime/nodes",
    );
    expect(createCall[1]).toMatchObject({
      method: "POST",
      body: JSON.stringify({
        instance_type: "g5.xlarge",
        name: "gpu-dev-01",
      }),
    });
  });

  it("falls back when Cloud Runtime node responses drift", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify([{ id: 123 }]), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ id: 123 }), {
          status: 201,
          headers: { "Content-Type": "application/json" },
        }),
      );
    vi.stubGlobal("fetch", fetchMock);

    const client = new ApiClient("https://api.example.test");

    await expect(client.listCloudRuntimeNodes()).resolves.toEqual([]);
    await expect(
      client.createCloudRuntimeNode({ instance_type: "g5.xlarge" }),
    ).resolves.toMatchObject({ id: "", status: "" });
  });

  it("deleteCloudRuntimeNode sends DELETE with JSON body containing instance id", async () => {
    const fetchMock = vi.fn().mockResolvedValueOnce(
      new Response(null, { status: 204 }),
    );
    vi.stubGlobal("fetch", fetchMock);

    const client = new ApiClient("https://api.example.test");
    await client.deleteCloudRuntimeNode("i-0123456789abcdef0");

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, opts] = fetchMock.mock.calls[0]!;
    expect(url).toBe("https://api.example.test/api/cloud-runtime/nodes");
    expect(opts).toMatchObject({
      method: "DELETE",
      body: JSON.stringify({ instance_id: "i-0123456789abcdef0" }),
    });
    expect((opts.headers as Record<string, string>)["Content-Type"]).toBe(
      "application/json",
    );
  });

  describe("getAttachment", () => {
    it("returns the parsed attachment for a well-formed response", async () => {
      vi.stubGlobal(
        "fetch",
        vi.fn().mockResolvedValue(
          new Response(
            JSON.stringify({
              id: "att-1",
              workspace_id: "ws-1",
              issue_id: null,
              comment_id: null,
              uploader_type: "member",
              uploader_id: "u-1",
              filename: "report.md",
              url: "https://static.example.test/ws/att-1.md",
              download_url:
                "https://static.example.test/ws/att-1.md?Policy=p&Signature=s&Key-Pair-Id=k",
              content_type: "text/markdown",
              size_bytes: 123,
              created_at: "2026-05-11T00:00:00Z",
            }),
            { status: 200, headers: { "Content-Type": "application/json" } },
          ),
        ),
      );

      const client = new ApiClient("https://api.example.test");
      const att = await client.getAttachment("att-1");

      expect(att.id).toBe("att-1");
      expect(att.download_url).toContain("Policy=");
    });

    it("falls back to an empty attachment when the response is missing download_url", async () => {
      vi.stubGlobal(
        "fetch",
        vi.fn().mockResolvedValue(
          new Response(JSON.stringify({ id: "att-1" }), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          }),
        ),
      );

      const client = new ApiClient("https://api.example.test");
      const att = await client.getAttachment("att-1");

      // parseWithFallback returns the EMPTY_ATTACHMENT record so callers can
      // safely read `download_url` without crashing — they'll see "" and
      // surface a user-facing error instead of opening `undefined`.
      expect(att.id).toBe("");
      expect(att.download_url).toBe("");
    });
  });

  describe("getAttachmentDownloadBlob", () => {
    it("returns the response body as a Blob from the download endpoint", async () => {
      const body = new Uint8Array([137, 80, 78, 71]);
      vi.stubGlobal(
        "fetch",
        vi.fn().mockResolvedValue(
          new Response(body, {
            status: 200,
            headers: { "Content-Type": "image/png" },
          }),
        ),
      );

      const client = new ApiClient("https://api.example.test");
      client.setToken("jwt-test");
      const blob = await client.getAttachmentDownloadBlob("att-1");

      expect(blob).toBeInstanceOf(Blob);
      expect(blob.type).toBe("image/png");
      expect(fetch).toHaveBeenCalledWith(
        "https://api.example.test/api/attachments/att-1/download",
        expect.objectContaining({
          headers: expect.objectContaining({
            Authorization: "Bearer jwt-test",
          }),
        }),
      );
    });
  });

  describe("getAttachmentTextContent", () => {
    it("returns body text and the original content type from the X-* header", async () => {
      vi.stubGlobal(
        "fetch",
        vi.fn().mockResolvedValue(
          new Response("# heading\n\nbody\n", {
            status: 200,
            headers: {
              "Content-Type": "text/plain; charset=utf-8",
              "X-Original-Content-Type": "text/markdown",
            },
          }),
        ),
      );

      const client = new ApiClient("https://api.example.test");
      const { text, originalContentType } =
        await client.getAttachmentTextContent("att-1");

      expect(text).toBe("# heading\n\nbody\n");
      expect(originalContentType).toBe("text/markdown");
    });

    it("throws PreviewTooLargeError on 413", async () => {
      const { PreviewTooLargeError } = await import("./client");
      vi.stubGlobal(
        "fetch",
        vi.fn().mockResolvedValue(
          new Response("", { status: 413, statusText: "Payload Too Large" }),
        ),
      );

      const client = new ApiClient("https://api.example.test");
      await expect(client.getAttachmentTextContent("att-1")).rejects.toBeInstanceOf(
        PreviewTooLargeError,
      );
    });

    it("throws PreviewUnsupportedError on 415", async () => {
      const { PreviewUnsupportedError } = await import("./client");
      vi.stubGlobal(
        "fetch",
        vi.fn().mockResolvedValue(
          new Response("", { status: 415, statusText: "Unsupported Media Type" }),
        ),
      );

      const client = new ApiClient("https://api.example.test");
      await expect(client.getAttachmentTextContent("att-1")).rejects.toBeInstanceOf(
        PreviewUnsupportedError,
      );
    });
  });

  describe("listChatMessagesPage deployment-order fallback", () => {
    const jsonResponse = (body: unknown, status: number, statusText = "") =>
      new Response(JSON.stringify(body), {
        status,
        statusText,
        headers: { "Content-Type": "application/json" },
      });

    it("falls back to the legacy full-list endpoint when the paged route 404s", async () => {
      const legacy = [
        { id: "m1", role: "user", content: "hi", created_at: "2026-06-01T00:00:00Z" },
        { id: "m2", role: "assistant", content: "yo", created_at: "2026-06-01T00:00:01Z" },
      ];
      const fetchMock = vi
        .fn()
        .mockResolvedValueOnce(jsonResponse({ error: "not found" }, 404, "Not Found"))
        .mockResolvedValueOnce(jsonResponse(legacy, 200));
      vi.stubGlobal("fetch", fetchMock);

      const client = new ApiClient("https://api.example.test");
      const page = await client.listChatMessagesPage("session-1", { limit: 50 });

      expect(fetchMock).toHaveBeenCalledTimes(2);
      expect(fetchMock.mock.calls[0]![0]).toBe(
        "https://api.example.test/api/chat/sessions/session-1/messages/page?limit=50",
      );
      expect(fetchMock.mock.calls[1]![0]).toBe(
        "https://api.example.test/api/chat/sessions/session-1/messages",
      );
      expect(page).toEqual({ messages: legacy, limit: 50, has_more: false, next_cursor: null });
    });

    it("does NOT fall back on a cursor request — a 404 there propagates", async () => {
      const fetchMock = vi
        .fn()
        .mockResolvedValue(jsonResponse({ error: "not found" }, 404, "Not Found"));
      vi.stubGlobal("fetch", fetchMock);

      const client = new ApiClient("https://api.example.test");
      await expect(
        client.listChatMessagesPage("session-1", {
          before: { created_at: "2026-06-01T00:00:00Z", id: "m1" },
        }),
      ).rejects.toBeInstanceOf(ApiError);
      // Only the paged request fires; no legacy full-list call that would duplicate messages.
      expect(fetchMock).toHaveBeenCalledTimes(1);
    });

    it("propagates non-404 errors instead of masking them with the legacy list", async () => {
      const fetchMock = vi
        .fn()
        .mockResolvedValue(jsonResponse({ error: "boom" }, 500, "Internal Server Error"));
      vi.stubGlobal("fetch", fetchMock);

      const client = new ApiClient("https://api.example.test");
      await expect(client.listChatMessagesPage("session-1")).rejects.toMatchObject({
        status: 500,
      });
      expect(fetchMock).toHaveBeenCalledTimes(1);
    });
  });

  describe("cancelTaskById response parsing", () => {
    const taskResponse = {
      id: "task-1",
      agent_id: "agent-1",
      runtime_id: "runtime-1",
      issue_id: "",
      status: "cancelled",
      priority: 0,
      dispatched_at: null,
      started_at: null,
      completed_at: "2026-06-12T06:40:00Z",
      result: null,
      error: null,
      created_at: "2026-06-12T06:39:00Z",
    };

    it("parses the cancelled chat message payload", async () => {
      const fetchMock = vi.fn().mockResolvedValue(
        new Response(JSON.stringify({
          ...taskResponse,
          cancelled_chat_message: {
            chat_session_id: "session-1",
            message_id: "message-1",
            content: "restore me",
            restore_to_input: true,
          },
        }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      );
      vi.stubGlobal("fetch", fetchMock);

      const client = new ApiClient("https://api.example.test");
      const result = await client.cancelTaskById("task-1");

      expect(fetchMock.mock.calls[0]).toMatchObject([
        "https://api.example.test/api/tasks/task-1/cancel",
        { method: "POST" },
      ]);
      expect(result.cancelled_chat_message).toEqual({
        chat_session_id: "session-1",
        message_id: "message-1",
        content: "restore me",
        restore_to_input: true,
      });
    });

    it("treats a null cancelled chat message as absent", async () => {
      vi.stubGlobal(
        "fetch",
        vi.fn().mockResolvedValue(
          new Response(JSON.stringify({
            ...taskResponse,
            cancelled_chat_message: null,
          }), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          }),
        ),
      );

      const client = new ApiClient("https://api.example.test");
      const result = await client.cancelTaskById("task-1");

      expect(result.id).toBe("task-1");
      expect(result.cancelled_chat_message).toBeUndefined();
    });

    it.each([
      ["a missing task id", { ...taskResponse, id: undefined }],
      [
        "a malformed cancelled chat message",
        {
          ...taskResponse,
          cancelled_chat_message: {
            chat_session_id: "session-1",
            message_id: "message-1",
            content: "restore me",
            restore_to_input: "true",
          },
        },
      ],
      ["a null body", null],
    ])("falls back for %s", async (_label, body) => {
      vi.stubGlobal(
        "fetch",
        vi.fn().mockResolvedValue(
          new Response(JSON.stringify(body), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          }),
        ),
      );

      const client = new ApiClient("https://api.example.test");
      const result = await client.cancelTaskById("task-1");

      expect(result.id).toBe("");
      expect(result.cancelled_chat_message).toBeUndefined();
    });
  });

  describe("chat attachment wiring", () => {
    it("uploadFile includes chat_session_id in the FormData body", async () => {
      const fetchMock = vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ id: "att-1", url: "https://cdn/x" }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      );
      vi.stubGlobal("fetch", fetchMock);

      const client = new ApiClient("https://api.example.test");
      const file = new File(["hi"], "hi.png", { type: "image/png" });
      await client.uploadFile(file, { chatSessionId: "session-123" });

      expect(fetchMock).toHaveBeenCalledTimes(1);
      const [url, init] = fetchMock.mock.calls[0]!;
      expect(url).toBe("https://api.example.test/api/upload-file");
      expect(init?.method).toBe("POST");
      const body = init?.body as FormData;
      expect(body).toBeInstanceOf(FormData);
      expect(body.get("chat_session_id")).toBe("session-123");
      expect(body.get("issue_id")).toBeNull();
      expect(body.get("comment_id")).toBeNull();
    });

    it("uploadFile honors a selected workspace over the current slug", async () => {
      const fetchMock = vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ id: "att-1", url: "https://cdn/x" }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      );
      vi.stubGlobal("fetch", fetchMock);
      const client = new ApiClient("https://api.example.test");
      await client.uploadFile(new File(["hi"], "hi.md", { type: "text/markdown" }), { workspaceId: "selected-ws" });
      const [, init] = fetchMock.mock.calls[0]!;
      expect(init?.headers["X-Workspace-ID"]).toBe("selected-ws");
      expect(init?.headers["X-Workspace-Slug"]).toBeUndefined();
    });

    it("sendChatMessage serialises attachment_ids onto the JSON body when present", async () => {
      const fetchMock = vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ message_id: "m1", task_id: "t1", created_at: "" }), {
          status: 201,
          headers: { "Content-Type": "application/json" },
        }),
      );
      vi.stubGlobal("fetch", fetchMock);

      const client = new ApiClient("https://api.example.test");
      await client.sendChatMessage("session-1", "hello", ["att-1", "att-2"]);

      const [, init] = fetchMock.mock.calls[0]!;
      expect(JSON.parse(init?.body as string)).toEqual({
        content: "hello",
        attachment_ids: ["att-1", "att-2"],
      });
    });

    it("sendChatMessage omits attachment_ids when the list is empty or undefined", async () => {
      const fetchMock = vi.fn().mockImplementation(() =>
        Promise.resolve(
          new Response(JSON.stringify({ message_id: "m1", task_id: "t1", created_at: "" }), {
            status: 201,
            headers: { "Content-Type": "application/json" },
          }),
        ),
      );
      vi.stubGlobal("fetch", fetchMock);

      const client = new ApiClient("https://api.example.test");
      await client.sendChatMessage("session-1", "hello");
      await client.sendChatMessage("session-1", "again", []);

      expect(JSON.parse(fetchMock.mock.calls[0]![1]?.body as string)).toEqual({ content: "hello" });
      expect(JSON.parse(fetchMock.mock.calls[1]![1]?.body as string)).toEqual({ content: "again" });
    });
  });

  describe("project test cases (regression suite)", () => {
    it("falls back to an empty list when the base-suite response is malformed", async () => {
      // The server returns garbage (a bare array instead of { test_cases: [...] },
      // or a non-array test_cases) — parseWithFallback must degrade to an empty
      // list so the Suite tab renders instead of white-screening.
      vi.stubGlobal(
        "fetch",
        vi.fn().mockResolvedValue(
          new Response(JSON.stringify({ test_cases: "not-an-array" }), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          }),
        ),
      );

      const client = new ApiClient("https://api.example.test");
      const res = await client.listProjectTestCases("project-1");

      expect(res.test_cases).toEqual([]);
    });

    it("parses a well-formed base-suite list", async () => {
      vi.stubGlobal(
        "fetch",
        vi.fn().mockResolvedValue(
          new Response(
            JSON.stringify({
              test_cases: [
                { id: "tc-1", title: "[e2e] Checkout", kind: "automated", category: "positive" },
              ],
            }),
            { status: 200, headers: { "Content-Type": "application/json" } },
          ),
        ),
      );

      const client = new ApiClient("https://api.example.test");
      const res = await client.listProjectTestCases("project-1");

      expect(res.test_cases).toHaveLength(1);
      expect(res.test_cases[0]).toMatchObject({ id: "tc-1", title: "[e2e] Checkout", kind: "automated" });
      // A base row omits issue_id in the schema default — the fallback keeps "".
      expect(res.test_cases[0]!.issue_id).toBe("");
    });

    it("uses the expected HTTP contract for the project test-case + build endpoints", async () => {
      const fetchMock = vi.fn().mockImplementation(() =>
        Promise.resolve(new Response(JSON.stringify({}), { status: 200, headers: { "Content-Type": "application/json" } })),
      );
      vi.stubGlobal("fetch", fetchMock);

      const client = new ApiClient("https://api.example.test");
      await client.listProjectTestCases("p1");
      await client.createProjectTestCase("p1", { title: "case", kind: "automated", category: "positive" });
      await client.buildProjectBaseSuite("p1");

      const calls = fetchMock.mock.calls.map(([url, init]) => ({ url, method: init?.method ?? "GET" }));
      expect(calls).toMatchObject([
        { url: "https://api.example.test/api/projects/p1/test-cases", method: "GET" },
        { url: "https://api.example.test/api/projects/p1/test-cases", method: "POST" },
        { url: "https://api.example.test/api/projects/p1/base-suite/build", method: "POST" },
      ]);
    });

    it("degrades a malformed build response to empty status/issue_id", async () => {
      vi.stubGlobal(
        "fetch",
        vi.fn().mockResolvedValue(
          new Response(JSON.stringify({ status: 123 }), {
            status: 202,
            headers: { "Content-Type": "application/json" },
          }),
        ),
      );

      const client = new ApiClient("https://api.example.test");
      const res = await client.buildProjectBaseSuite("p1");

      expect(res).toEqual({ status: "", issue_id: "" });
    });
  });
});

// Confirmation binding — docs/agora-assistant-final-plan.md ("Pinned wire
// contract"). The endpoints ship after this client, so the 404 case below is
// a real deployment state rather than a hypothetical.
describe("Assistant operation confirm / reject", () => {
  it("posts the confirmation to the bound operation id", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          operation: { id: "op-1", tool_name: "delete_issue", status: "confirmed" },
          message: { id: "msg-9", role: "tool", tool_name: "delete_issue" },
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );
    vi.stubGlobal("fetch", fetchMock);

    const client = new ApiClient("https://api.example.test");
    const decision = await client.confirmAssistantOperation("op-1");

    expect(fetchMock.mock.calls[0]?.[0]).toBe(
      "https://api.example.test/api/assistant/operations/op-1/confirm",
    );
    expect(fetchMock.mock.calls[0]?.[1]?.method).toBe("POST");
    expect(decision).toEqual({ status: "confirmed", operation_id: "op-1", message_id: "msg-9" });
  });

  it("flattens a decision body that lost its fields rather than failing the click", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ operation: {}, message: {} }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      ),
    );

    const client = new ApiClient("https://api.example.test");
    await expect(client.confirmAssistantOperation("op-1")).resolves.toEqual({
      status: "",
      operation_id: "",
      message_id: "",
    });
  });

  it("treats the specified 204 reject as a success, not a drifted body", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(new Response(null, { status: 204, statusText: "No Content" }));
    vi.stubGlobal("fetch", fetchMock);

    const client = new ApiClient("https://api.example.test");
    const decision = await client.rejectAssistantOperation("op-1");

    expect(fetchMock.mock.calls[0]?.[0]).toBe(
      "https://api.example.test/api/assistant/operations/op-1/reject",
    );
    expect(decision).toEqual({ status: "", operation_id: "", message_id: "" });
  });

  it("propagates a 409 so the card can flip to expired", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ error: "operation changed" }), {
          status: 409,
          statusText: "Conflict",
          headers: { "Content-Type": "application/json" },
        }),
      ),
    );

    const client = new ApiClient("https://api.example.test");
    await expect(client.confirmAssistantOperation("op-1")).rejects.toMatchObject({
      name: "ApiError",
      status: 409,
    });
  });

  it("propagates a 404 from a runtime without confirmation binding", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ error: "not found" }), {
          status: 404,
          statusText: "Not Found",
          headers: { "Content-Type": "application/json" },
        }),
      ),
    );

    const client = new ApiClient("https://api.example.test");
    await expect(client.confirmAssistantOperation("op-1")).rejects.toBeInstanceOf(ApiError);
  });
});

describe("Assistant artifact library pagination", () => {
  it("accepts an actual empty page as the end of pagination", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(
      new Response("[]", { status: 200, headers: { "Content-Type": "application/json" } }),
    ));

    const client = new ApiClient("https://api.example.test");
    await expect(client.listMyAssistantArtifacts(40)).resolves.toEqual([]);
  });

  it("rejects a malformed page so the library can offer retry instead of ending pagination", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(
      new Response(JSON.stringify([{ id: "artifact-1", session_id: "session-1", kind: "markdown" }, { title: "missing identity" }]), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    ));

    const client = new ApiClient("https://api.example.test");
    await expect(client.listMyAssistantArtifacts(20)).rejects.toThrow(
      "Could not load the artifact library page. Try again.",
    );
  });
});

describe("Assistant artifact revisions", () => {
  const revision = {
    id: "rev-1",
    artifact_id: "art-1",
    version: 2,
    title: "Sprint report",
    created_at: "2026-09-16T10:00:00Z",
  };

  it("reads the history and one version from the documented paths", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify([revision]), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);

    const client = new ApiClient("https://api.example.test");
    await expect(client.listAssistantArtifactRevisions("art-1")).resolves.toEqual([revision]);
    expect(fetchMock.mock.calls[0]?.[0]).toBe(
      "https://api.example.test/api/assistant/artifacts/art-1/revisions",
    );

    fetchMock.mockResolvedValue(
      new Response(JSON.stringify({ ...revision, content: "# body" }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    await expect(client.getAssistantArtifactRevision("art-1", 2)).resolves.toMatchObject({
      version: 2,
      content: "# body",
    });
    expect(fetchMock.mock.calls[1]?.[0]).toBe(
      "https://api.example.test/api/assistant/artifacts/art-1/revisions/2",
    );
  });

  it("propagates a 404 so the pane can collapse to a plain version badge", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ error: "not found" }), {
          status: 404,
          statusText: "Not Found",
          headers: { "Content-Type": "application/json" },
        }),
      ),
    );

    const client = new ApiClient("https://api.example.test");
    await expect(client.listAssistantArtifactRevisions("art-1")).rejects.toBeInstanceOf(ApiError);
    await expect(client.getAssistantArtifactRevision("art-1", 9)).rejects.toBeInstanceOf(ApiError);
  });

  it("degrades a drifted history body to an empty list instead of throwing into the UI", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ revisions: [revision] }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    ));

    const client = new ApiClient("https://api.example.test");
    await expect(client.listAssistantArtifactRevisions("art-1")).resolves.toEqual([]);
  });
});

// Pinned reports — docs/assistant-domain-plan.md Phase 2a.
describe("pinned reports API", () => {
  function jsonFetch(body: unknown, status = 200) {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(status === 204 ? null : JSON.stringify(body), {
        status,
        headers: { "Content-Type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);
    return fetchMock;
  }

  it("posts the project and normalizes an `id`-spelled pin", async () => {
    const fetchMock = jsonFetch(
      { id: "pin-1", artifact_id: "art-1", project_id: "proj-1", created_at: "now" },
      201,
    );
    const client = new ApiClient("https://api.example.test");
    const pin = await client.pinArtifact("art-1", "proj-1");

    expect(fetchMock.mock.calls[0]?.[0]).toBe(
      "https://api.example.test/api/assistant/artifacts/art-1/pins",
    );
    expect(JSON.parse(fetchMock.mock.calls[0]?.[1]?.body as string)).toEqual({
      project_id: "proj-1",
    });
    expect(pin).toMatchObject({ id: "pin-1", project_id: "proj-1" });
  });

  it("normalizes a `pin_id`-spelled pin (the list endpoint's spelling)", async () => {
    jsonFetch({ pin_id: "pin-2" });
    const client = new ApiClient("https://api.example.test");
    const pin = await client.pinArtifact("art-1", "proj-1");
    // Ids the body omitted are filled from the request, so the Unpin action
    // has everything it needs without a second read.
    expect(pin).toMatchObject({ id: "pin-2", artifact_id: "art-1", project_id: "proj-1" });
  });

  it("returns the empty pin when the create response has no usable id", async () => {
    jsonFetch({ id: 7 });
    const client = new ApiClient("https://api.example.test");
    expect((await client.pinArtifact("art-1", "proj-1")).id).toBe("");
  });

  it("unwraps the reports envelope and degrades a malformed one to an empty list", async () => {
    jsonFetch({
      reports: [
        {
          pin_id: "pin-1",
          artifact_id: "art-1",
          title: "Sprint report",
          kind: "markdown",
          version: 2,
          updated_at: "now",
          created_at: "then",
          pinned_by: { id: "u-1", name: "Jamshid" },
          owner: { id: "u-1", name: "Jamshid" },
        },
      ],
    });
    const client = new ApiClient("https://api.example.test");
    expect(await client.listProjectReports("proj-1")).toHaveLength(1);

    jsonFetch({ reports: "nope" });
    expect(await client.listProjectReports("proj-1")).toEqual([]);
  });

  it("degrades an unreadable report body to the empty report rather than throwing", async () => {
    jsonFetch({ pin_id: "pin-1", content: { body: "x" } });
    const client = new ApiClient("https://api.example.test");
    expect((await client.getReport("pin-1")).pin_id).toBe("");
  });

  it("deletes a pin through the artifact-scoped route", async () => {
    const fetchMock = jsonFetch(null, 204);
    const client = new ApiClient("https://api.example.test");
    await client.unpinArtifact("art-1", "pin-1");
    expect(fetchMock.mock.calls[0]?.[0]).toBe(
      "https://api.example.test/api/assistant/artifacts/art-1/pins/pin-1",
    );
    expect(fetchMock.mock.calls[0]?.[1]?.method).toBe("DELETE");
  });
});

// Scheduled refresh — docs/assistant-domain-plan.md Phase 2b.
describe("report schedule API", () => {
  function jsonFetch(body: unknown, status = 200) {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(status === 204 ? null : JSON.stringify(body), {
        status,
        headers: { "Content-Type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);
    return fetchMock;
  }

  const schedule = {
    frequency: "weekly",
    time: "09:00",
    weekday: 1,
    timezone: "Asia/Tashkent",
    enabled: true,
    last_run_at: null,
    last_status: "",
    next_run_at: "2026-09-21T04:00:00Z",
  };

  it("PUTs the schedule on the pin-scoped route and returns the saved cadence", async () => {
    const fetchMock = jsonFetch({ schedule });
    const client = new ApiClient("https://api.example.test");
    const saved = await client.setReportSchedule("art-1", "pin-1", {
      frequency: "weekly",
      time: "09:00",
      weekday: 1,
      timezone: "Asia/Tashkent",
    });

    expect(fetchMock.mock.calls[0]?.[0]).toBe(
      "https://api.example.test/api/assistant/artifacts/art-1/pins/pin-1/schedule",
    );
    expect(fetchMock.mock.calls[0]?.[1]?.method).toBe("PUT");
    expect(JSON.parse(fetchMock.mock.calls[0]?.[1]?.body as string)).toEqual({
      frequency: "weekly",
      time: "09:00",
      weekday: 1,
      timezone: "Asia/Tashkent",
    });
    expect(saved).toMatchObject({ frequency: "weekly", time: "09:00", weekday: 1 });
  });

  it("returns null instead of throwing when the echo is unreadable", async () => {
    // A 200 whose schedule drifted (unknown preset). The save happened; only
    // the echo is unusable, and the invalidated reports query is the authority.
    jsonFetch({ schedule: { frequency: "fortnightly" } });
    const client = new ApiClient("https://api.example.test");
    await expect(
      client.setReportSchedule("art-1", "pin-1", {
        frequency: "daily",
        time: "09:00",
        timezone: "UTC",
      }),
    ).resolves.toBeNull();

    // Same for an envelope that never arrived at all.
    jsonFetch(null);
    await expect(
      client.setReportSchedule("art-1", "pin-1", {
        frequency: "daily",
        time: "09:00",
        timezone: "UTC",
      }),
    ).resolves.toBeNull();
  });

  it("omits weekday from the body for the non-weekly presets", async () => {
    const fetchMock = jsonFetch({ schedule: { ...schedule, frequency: "weekdays", weekday: null } });
    const client = new ApiClient("https://api.example.test");
    await client.setReportSchedule("art-1", "pin-1", {
      frequency: "weekdays",
      time: "18:30",
      timezone: "Europe/Berlin",
    });
    expect(JSON.parse(fetchMock.mock.calls[0]?.[1]?.body as string)).toEqual({
      frequency: "weekdays",
      time: "18:30",
      timezone: "Europe/Berlin",
    });
  });

  it("deletes a schedule through the same route", async () => {
    const fetchMock = jsonFetch(null, 204);
    const client = new ApiClient("https://api.example.test");
    await client.deleteReportSchedule("art-1", "pin-1");
    expect(fetchMock.mock.calls[0]?.[0]).toBe(
      "https://api.example.test/api/assistant/artifacts/art-1/pins/pin-1/schedule",
    );
    expect(fetchMock.mock.calls[0]?.[1]?.method).toBe("DELETE");
  });

  it("carries a row's schedule through the reports list, and survives a drifted one", async () => {
    const row = {
      pin_id: "pin-1",
      artifact_id: "art-1",
      title: "Sprint report",
      kind: "markdown",
      version: 2,
      updated_at: "now",
      created_at: "then",
      pinned_by: { id: "u-1", name: "Jamshid" },
      owner: { id: "u-1", name: "Jamshid" },
    };
    jsonFetch({ reports: [{ ...row, schedule }] });
    const client = new ApiClient("https://api.example.test");
    expect((await client.listProjectReports("proj-1"))[0]?.schedule).toMatchObject({
      frequency: "weekly",
    });

    // An older backend omits the key entirely — the row still lists.
    jsonFetch({ reports: [row] });
    const older = await client.listProjectReports("proj-1");
    expect(older).toHaveLength(1);
    expect(older[0]?.schedule).toBeUndefined();

    // A drifted schedule costs the badge, not the row.
    jsonFetch({ reports: [{ ...row, schedule: { frequency: "hourly" } }] });
    const drifted = await client.listProjectReports("proj-1");
    expect(drifted).toHaveLength(1);
    expect(drifted[0]?.schedule).toBeNull();
  });
});
