import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { render, screen, cleanup, waitFor } from "@testing-library/react";
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

// The card is self-contained (it owns its two mutations) so the transcript
// needs no confirmation plumbing of its own — which is exactly what these
// mocks stand in for.
vi.mock("@agora/core/assistant", () => ({
  useConfirmAssistantOperation: (sessionId: string) => ({
    mutate: (operationId: string, opts?: { onSuccess?: () => void; onError?: (e: unknown) => void }) =>
      mockConfirm(sessionId, operationId, opts),
    isPending: false,
  }),
  useRejectAssistantOperation: (sessionId: string) => ({
    mutate: (operationId: string, opts?: { onSuccess?: () => void; onError?: (e: unknown) => void }) =>
      mockReject(sessionId, operationId, opts),
    isPending: false,
  }),
}));

import { ApiError } from "@agora/core/api";
import { MessageList } from "./message-list";

const mockPush = vi.hoisted(() => vi.fn());

function renderList(messages: AssistantMessage[]) {
  const nav: NavigationAdapter = {
    push: mockPush,
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
    tool_name: "delete_issue",
    tool_result: result,
    created_at: "2026-09-17T10:00:00Z",
  };
}

const PENDING = {
  status: "needs_confirmation",
  operation: {
    id: "op-1",
    tool_name: "delete_issue",
    summary: "Delete MUL-123",
    workspace_slug: "acme",
    target: { type: "issue", identifier: "MUL-123", title: "Fix the login redirect" },
  },
};

beforeEach(() => {
  vi.clearAllMocks();
});

afterEach(() => {
  cleanup();
});

describe("ConfirmCard — pending", () => {
  it("renders the summary, the resolved scope and both buttons", () => {
    renderList([toolMessage("m1", PENDING)]);

    expect(screen.getByText("Delete MUL-123")).toBeInTheDocument();
    // Workspace slug + target identifier + title: the scope is never implicit.
    expect(screen.getByText("acme · MUL-123 · Fix the login redirect")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Confirm" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Cancel" })).toBeInTheDocument();
  });

  it("falls back to a generic summary when the server sent none", () => {
    renderList([toolMessage("m1", { status: "needs_confirmation", operation: { id: "op-1" } })]);

    expect(screen.getByText("Confirm this action")).toBeInTheDocument();
  });

  it("degrades to the plain tool chip when the operation has no id to bind to", () => {
    renderList([toolMessage("m1", { status: "needs_confirmation", operation: { summary: "Delete it" } })]);

    expect(screen.queryByRole("button", { name: "Confirm" })).not.toBeInTheDocument();
    expect(screen.getByText("delete issue")).toBeInTheDocument();
  });
});

describe("ConfirmCard — confirm and reject", () => {
  it("confirms the exact operation id and flips to confirmed", async () => {
    mockConfirm.mockImplementation((_s, _id, opts) => opts?.onSuccess?.());
    renderList([toolMessage("m1", PENDING)]);

    await userEvent.click(screen.getByRole("button", { name: "Confirm" }));

    expect(mockConfirm).toHaveBeenCalledWith("session-1", "op-1", expect.anything());
    expect(await screen.findByText("Confirmed")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Confirm" })).not.toBeInTheDocument();
  });

  it("rejects the exact operation id and flips to cancelled", async () => {
    mockReject.mockImplementation((_s, _id, opts) => opts?.onSuccess?.());
    renderList([toolMessage("m1", PENDING)]);

    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));

    expect(mockReject).toHaveBeenCalledWith("session-1", "op-1", expect.anything());
    expect(await screen.findByText("Cancelled")).toBeInTheDocument();
  });

  it("flips to expired and explains itself on a 409", async () => {
    mockConfirm.mockImplementation((_s, _id, opts) =>
      opts?.onError?.(new ApiError("conflict", 409, "Conflict")),
    );
    renderList([toolMessage("m1", PENDING)]);

    await userEvent.click(screen.getByRole("button", { name: "Confirm" }));

    expect(await screen.findByText("This action changed — ask again.")).toBeInTheDocument();
    expect(toastError).toHaveBeenCalledWith("This action changed — ask again.");
    expect(screen.queryByRole("button", { name: "Confirm" })).not.toBeInTheDocument();
  });

  it("stays pending and toasts while the runtime has no confirmation endpoints (404)", async () => {
    mockConfirm.mockImplementation((_s, _id, opts) =>
      opts?.onError?.(new ApiError("not found", 404, "Not Found")),
    );
    renderList([toolMessage("m1", PENDING)]);

    await userEvent.click(screen.getByRole("button", { name: "Confirm" }));

    expect(toastError).toHaveBeenCalledWith("Failed to send message");
    // Retryable: a deploy can fix this, so the buttons stay.
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Confirm" })).toBeInTheDocument(),
    );
    expect(screen.queryByText("This action changed — ask again.")).not.toBeInTheDocument();
  });
});

describe("ConfirmCard — outcome already in the transcript", () => {
  it("reads a later receipt row as confirmed, without a click", () => {
    renderList([
      toolMessage("m1", PENDING),
      toolMessage("m2", { operation_id: "op-1", receipt: { action: "Deleted issue" } }),
    ]);

    expect(screen.getByText("Confirmed")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Confirm" })).not.toBeInTheDocument();
  });

  it("reads a later cancelled row as rejected", () => {
    renderList([
      toolMessage("m1", PENDING),
      toolMessage("m2", { operation_id: "op-1", status: "cancelled" }),
    ]);

    expect(screen.getByText("Cancelled")).toBeInTheDocument();
  });
});

describe("ReceiptChip", () => {
  it("renders the action, target, links and a collapsed effects line", async () => {
    renderList([
      toolMessage("m1", {
        receipt: {
          action: "assign_issue",
          target: { type: "issue", identifier: "MUL-123", title: "Fix the login redirect" },
          links: [{ label: "MUL-123", url: "/acme/issues/MUL-123" }],
          effects: ["Notified 2 subscribers", "Moved to In Progress"],
        },
      }),
    ]);

    // The server sends the tool name as the action; the chip humanizes it.
    expect(screen.getByText("assign issue")).toBeInTheDocument();
    expect(screen.getByText("MUL-123 · Fix the login redirect")).toBeInTheDocument();

    // Only the first effect shows until expanded.
    expect(screen.getByText("Notified 2 subscribers")).toBeInTheDocument();
    expect(screen.getByText("+1 more")).toBeInTheDocument();
    expect(screen.queryByText("Moved to In Progress")).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Show what else changed" }));
    expect(screen.getByText("Moved to In Progress")).toBeInTheDocument();
  });

  it("navigates in-app through AppLink rather than a raw anchor", async () => {
    renderList([
      toolMessage("m1", {
        receipt: { action: "create_issue", links: ["/acme/issues/MUL-9"] },
      }),
    ]);

    await userEvent.click(screen.getByRole("link", { name: "MUL-9" }));
    expect(mockPush).toHaveBeenCalledWith("/acme/issues/MUL-9");
  });

  it("drops an off-site link instead of pushing it into the router", () => {
    renderList([
      toolMessage("m1", {
        receipt: {
          action: "open_pull_request",
          links: [{ label: "GitHub", url: "https://github.com/agora/agora/pull/1" }],
        },
      }),
    ]);

    expect(screen.getByText("open pull request")).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "GitHub" })).not.toBeInTheDocument();
  });

  it("degrades to the plain tool chip for a result with no receipt", () => {
    renderList([toolMessage("m1", { id: "issue-1", title: "Fix the login redirect" })]);

    expect(screen.getByText("delete issue")).toBeInTheDocument();
    expect(screen.getByText("Fix the login redirect")).toBeInTheDocument();
  });
});

describe("UncertainChip", () => {
  it("renders the inspect hint in its own tone", () => {
    renderList([
      toolMessage("m1", { status: "uncertain", inspect: "Check MUL-123 before trying again." }),
    ]);

    const chip = screen.getByRole("status");
    expect(chip).toHaveTextContent("Outcome unclear.");
    expect(chip).toHaveTextContent("Check MUL-123 before trying again.");
    expect(chip.className).toContain("warning");
  });

  it("has a generic hint when the server sent none", () => {
    renderList([toolMessage("m1", { status: "uncertain" })]);

    expect(screen.getByRole("status")).toHaveTextContent(
      "Check the affected items before trying again.",
    );
  });
});
