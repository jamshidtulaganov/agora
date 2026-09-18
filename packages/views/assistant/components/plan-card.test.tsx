import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { render, screen, cleanup } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@agora/core/i18n/react";
import type { AssistantMessage } from "@agora/core/types";
import { NavigationProvider } from "../../navigation";
import type { NavigationAdapter } from "../../navigation";
import { RESOURCES } from "../../locales";

const toastError = vi.hoisted(() => vi.fn());
vi.mock("sonner", () => ({ toast: { error: toastError, success: vi.fn() } }));

const mockConfirm = vi.hoisted(() => vi.fn());
const mockReject = vi.hoisted(() => vi.fn());
const mockOperation = vi.hoisted(() => ({
  data: { id: "op-1", status: "pending", outcome: null, kind: "plan", items: [] } as {
    id: string;
    status: string;
    outcome: string | null;
    kind: string;
    items: unknown[];
  },
  isPending: false,
  isError: false,
  isFetching: false,
  refetch: vi.fn(),
}));

// Same mocks as confirm-card.test.tsx: the card owns its mutations, so the
// transcript needs no plan plumbing of its own.
vi.mock("@agora/core/assistant", () => ({
  useAssistantOperation: () => mockOperation,
  useConfirmAssistantOperation: (sessionId: string) => ({
    mutate: (input: unknown, opts?: { onSuccess?: () => void; onError?: (e: unknown) => void }) =>
      mockConfirm(sessionId, input, opts),
    isPending: false,
  }),
  useRejectAssistantOperation: (sessionId: string) => ({
    mutate: (operationId: string, opts?: { onSuccess?: () => void; onError?: (e: unknown) => void }) =>
      mockReject(sessionId, operationId, opts),
    isPending: false,
  }),
}));

import { MessageList } from "./message-list";

function renderList(messages: AssistantMessage[]) {
  const nav: NavigationAdapter = {
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    pathname: "/acme/assistant",
    searchParams: new URLSearchParams(),
    getShareableUrl: (p) => p,
  };
  return render(
    <I18nProvider locale="en" resources={RESOURCES}>
      <NavigationProvider value={nav}>
        <MessageList messages={messages} />
      </NavigationProvider>
    </I18nProvider>,
  );
}

function toolMessage(id: string, result: unknown): AssistantMessage {
  return {
    id,
    session_id: "session-1",
    role: "tool",
    content: "{}",
    tool_name: "propose_plan",
    tool_result: result,
    created_at: "2026-09-19T10:00:00Z",
  };
}

const PLAN = {
  status: "needs_confirmation",
  operation: {
    id: "op-1",
    kind: "plan",
    tool_name: "propose_plan",
    summary: "Plan sprint 12",
    workspace_slug: "acme",
    expires_at: "2026-09-19T18:00:00Z",
    items: [
      { index: 0, tool: "create_sprint", summary: "Create sprint 12" },
      { index: 1, tool: "move_issue_to_sprint", summary: "Move MUL-1 into sprint 12" },
      { index: 2, tool: "move_issue_to_sprint", summary: "Move MUL-2 into sprint 12" },
    ],
  },
};

function receiptMessage(items: unknown) {
  return toolMessage("m2", {
    operation_id: "op-1",
    receipt: { action: "propose_plan", items },
  });
}

beforeEach(() => {
  vi.clearAllMocks();
  mockOperation.data = { id: "op-1", status: "pending", outcome: null, kind: "plan", items: [] };
  mockOperation.isPending = false;
  mockOperation.isError = false;
  mockOperation.isFetching = false;
  mockOperation.refetch.mockResolvedValue({ data: mockOperation.data, isError: false });
});

afterEach(() => {
  cleanup();
});

describe("PlanCard — pending", () => {
  it("renders the title, the scope, a checked row per item and the count", () => {
    renderList([toolMessage("m1", PLAN)]);

    expect(screen.getByText("Plan sprint 12")).toBeInTheDocument();
    expect(screen.getByText("acme")).toBeInTheDocument();
    expect(screen.getByText("Move MUL-1 into sprint 12")).toBeInTheDocument();
    // The tool behind each row stays visible but muted.
    expect(screen.getAllByText("move issue to sprint")).toHaveLength(2);

    const boxes = screen.getAllByRole("checkbox");
    expect(boxes).toHaveLength(3);
    boxes.forEach((box) => expect(box).toHaveAttribute("aria-checked", "true"));
    expect(screen.getByText("3 of 3 selected")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Confirm" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Cancel" })).toBeInTheDocument();
  });

  it("falls back to the single-operation card when the rows didn't decode", () => {
    renderList([
      toolMessage("m1", {
        status: "needs_confirmation",
        operation: { id: "op-1", kind: "plan", summary: "Plan sprint 12", items: null },
      }),
    ]);

    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
    expect(screen.getByText("Plan sprint 12")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Confirm" })).toBeInTheDocument();
  });

  it("leaves a single operation on the ConfirmCard — an absent kind is not a plan", () => {
    renderList([
      toolMessage("m1", {
        status: "needs_confirmation",
        operation: { id: "op-1", summary: "Delete MUL-123" },
      }),
    ]);

    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Confirm" })).toBeInTheDocument();
  });
});

describe("PlanCard — confirming", () => {
  it("confirms with the bare operation id while every row is checked", async () => {
    mockConfirm.mockImplementation((_s, _input, opts) => opts?.onSuccess?.());
    renderList([toolMessage("m1", PLAN)]);

    await userEvent.click(screen.getByRole("button", { name: "Confirm" }));

    // Byte-for-byte the request a single-operation confirm makes today.
    expect(mockConfirm).toHaveBeenCalledWith("session-1", "op-1", expect.anything());
  });

  it("sends skipped_items for the rows the user unchecked", async () => {
    mockConfirm.mockImplementation((_s, _input, opts) => opts?.onSuccess?.());
    renderList([toolMessage("m1", PLAN)]);

    await userEvent.click(screen.getByRole("checkbox", { name: /Move MUL-1 into sprint 12/ }));
    expect(screen.getByText("2 of 3 selected")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Confirm" }));

    expect(mockConfirm).toHaveBeenCalledWith(
      "session-1",
      { operationId: "op-1", skippedItems: [1] },
      expect.anything(),
    );
  });

  it("blocks Confirm — but not Cancel — once every row is unchecked", async () => {
    renderList([toolMessage("m1", PLAN)]);

    for (const box of screen.getAllByRole("checkbox")) await userEvent.click(box);

    expect(screen.getByText("0 of 3 selected")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Confirm" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Cancel" })).toBeEnabled();
  });

  it("rejects the whole plan with the operation id", async () => {
    mockReject.mockImplementation((_s, _id, opts) => opts?.onSuccess?.());
    renderList([toolMessage("m1", PLAN)]);

    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));

    expect(mockReject).toHaveBeenCalledWith("session-1", "op-1", expect.anything());
    expect(await screen.findByText("Checking the result…")).toBeInTheDocument();
  });
});

describe("PlanCard — executed receipt", () => {
  it("replaces the checkboxes with per-row outcomes after reload", () => {
    mockOperation.data = { id: "op-1", status: "confirmed", outcome: "succeeded", kind: "plan", items: [] };
    renderList([
      toolMessage("m1", PLAN),
      receiptMessage([
        { index: 0, outcome: "ok", identifier: "Sprint 12" },
        { index: 1, outcome: "failed", error: "Issue MUL-1 is archived" },
        { index: 2, outcome: "not_run" },
      ]),
    ]);

    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
    expect(screen.getByText("Sprint 12")).toBeInTheDocument();
    expect(screen.getByText("Issue MUL-1 is archived")).toBeInTheDocument();
    // The stop is announced once for the plan, never per row.
    expect(
      screen.getAllByText("Stopped after a failure — the remaining steps didn't run."),
    ).toHaveLength(1);
    expect(screen.getByText("Confirmed")).toBeInTheDocument();
    expect(screen.queryByText("3 of 3 selected")).not.toBeInTheDocument();
  });

  it("renders an outcome this build doesn't know without crashing", () => {
    mockOperation.data = { id: "op-1", status: "confirmed", outcome: "succeeded", kind: "plan", items: [] };
    renderList([
      toolMessage("m1", PLAN),
      receiptMessage([{ index: 0, outcome: "deferred_to_agent" }, { index: 1, outcome: "ok" }]),
    ]);

    expect(screen.getByText("Create sprint 12")).toBeInTheDocument();
    expect(screen.getByText("Confirmed")).toBeInTheDocument();
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
  });

  it("re-serves per-row outcomes the operation read carries when no receipt row exists", () => {
    mockOperation.data = {
      id: "op-1",
      status: "confirmed",
      outcome: "succeeded",
      kind: "plan",
      items: [{ index: 0, outcome: "ok", identifier: "Sprint 12" }],
    };
    renderList([toolMessage("m1", PLAN)]);

    expect(screen.getByText("Sprint 12")).toBeInTheDocument();
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
  });

  it("shows a rejected plan as cancelled, with no glyphs invented for its rows", () => {
    mockOperation.data = { id: "op-1", status: "rejected", outcome: null, kind: "plan", items: [] };
    renderList([toolMessage("m1", PLAN)]);

    expect(screen.getByText("Cancelled")).toBeInTheDocument();
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
    expect(screen.getByText("Create sprint 12")).toBeInTheDocument();
  });

  it("shows an expired plan with the same copy the single-operation card uses", () => {
    mockOperation.data = { id: "op-1", status: "expired", outcome: null, kind: "plan", items: [] };
    renderList([toolMessage("m1", PLAN)]);

    expect(screen.getByText("This action changed — ask again.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Confirm" })).not.toBeInTheDocument();
  });
});
