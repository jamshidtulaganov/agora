// @vitest-environment jsdom

import { cleanup, fireEvent, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Agent, Issue } from "@agora/core/types";
import { renderWithI18n } from "../../test/i18n";

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

vi.mock("@agora/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

const mockAuthUser = { id: "user-1", email: "u@test.com", name: "User" };
vi.mock("@agora/core/auth", () => ({
  useAuthStore: (selector?: (s: unknown) => unknown) => {
    const state = { user: mockAuthUser };
    return selector ? selector(state) : state;
  },
}));

// sliceAction lives in the shared api client (added by the orchestrator via
// clientEdits); the mock stands in for it at test time so this file does not
// depend on the integrated method existing.
const mockSliceAction = vi.hoisted(() => vi.fn().mockResolvedValue({}));
vi.mock("@agora/core/api", () => ({
  api: { sliceAction: mockSliceAction },
}));

const mockToast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));
vi.mock("sonner", () => ({ toast: mockToast }));

// Drive both useQuery calls (issue detail + agent list) off the queryKey.
const queryState = vi.hoisted(() => ({
  issue: null as Issue | null,
  agents: [] as Agent[],
}));
const mockInvalidate = vi.hoisted(() => vi.fn());
vi.mock("@tanstack/react-query", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-query")>();
  return {
    ...actual,
    useQuery: ({ queryKey }: { queryKey: unknown[] }) => {
      if (queryKey.includes("agents")) return { data: queryState.agents };
      if (queryKey.includes("detail")) return { data: queryState.issue };
      return { data: undefined };
    },
    useQueryClient: () => ({ invalidateQueries: mockInvalidate }),
  };
});

import { SliceActionsSection } from "./slice-actions-section";

function makeIssue(overrides: Partial<Issue> = {}): Issue {
  return {
    id: "issue-1",
    assignee_type: "agent",
    assignee_id: "agent-1",
    ...overrides,
  } as Issue;
}

function makeAgent(overrides: Partial<Agent> = {}): Agent {
  return {
    id: "agent-9",
    owner_id: "user-1",
    archived_at: null,
    status: "online",
    ...overrides,
  } as Agent;
}

beforeEach(() => {
  cleanup();
  vi.clearAllMocks();
  queryState.issue = makeIssue();
  queryState.agents = [];
});

afterEach(() => cleanup());

describe("SliceActionsSection", () => {
  it("renders the four action buttons when the issue has an agent assignee", () => {
    renderWithI18n(<SliceActionsSection issueId="issue-1" />);
    // The section is collapsed by default (rail simplicity) — expand it first.
    fireEvent.click(screen.getByRole("button", { name: /AI actions/i }));

    expect(screen.getByRole("button", { name: "Draft code" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Write docs" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Write tests" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Review a part" })).toBeInTheDocument();
  });

  it("shows the no-agent hint when there is no agent assignee and no caller-owned ready agent", () => {
    queryState.issue = makeIssue({ assignee_type: "member", assignee_id: "user-2" });
    queryState.agents = [];
    renderWithI18n(<SliceActionsSection issueId="issue-1" />);
    // The section is collapsed by default (rail simplicity) — expand it first.
    fireEvent.click(screen.getByRole("button", { name: /AI actions/i }));

    expect(screen.getByText(/Assignee picker/i)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Draft code" })).not.toBeInTheDocument();
  });

  it("treats a caller-owned ready agent as agent-reachable even without an agent assignee", () => {
    queryState.issue = makeIssue({ assignee_type: "member", assignee_id: "user-2" });
    queryState.agents = [makeAgent()];
    renderWithI18n(<SliceActionsSection issueId="issue-1" />);
    // The section is collapsed by default (rail simplicity) — expand it first.
    fireEvent.click(screen.getByRole("button", { name: /AI actions/i }));

    expect(screen.getByRole("button", { name: "Draft code" })).toBeInTheDocument();
    expect(screen.queryByText(/Assignee picker/i)).not.toBeInTheDocument();
  });

  it("does not count an offline or archived owned agent as ready", () => {
    queryState.issue = makeIssue({ assignee_type: "member", assignee_id: "user-2" });
    queryState.agents = [
      makeAgent({ status: "offline" }),
      makeAgent({ id: "agent-x", archived_at: "2026-01-01T00:00:00Z" }),
      makeAgent({ id: "agent-y", owner_id: "someone-else" }),
    ];
    renderWithI18n(<SliceActionsSection issueId="issue-1" />);
    // The section is collapsed by default (rail simplicity) — expand it first.
    fireEvent.click(screen.getByRole("button", { name: /AI actions/i }));

    expect(screen.getByText(/Assignee picker/i)).toBeInTheDocument();
  });

  it("fires api.sliceAction with the kind and trimmed scope, then toasts success", async () => {
    renderWithI18n(<SliceActionsSection issueId="issue-1" />);
    // The section is collapsed by default (rail simplicity) — expand it first.
    fireEvent.click(screen.getByRole("button", { name: /AI actions/i }));

    const scopeInput = screen.getByPlaceholderText(/Scope \(optional\)/i);
    fireEvent.change(scopeInput, { target: { value: "  the login form  " } });

    fireEvent.click(screen.getByRole("button", { name: "Write tests" }));

    await waitFor(() =>
      expect(mockSliceAction).toHaveBeenCalledWith("issue-1", {
        kind: "write_tests",
        scope: "the login form",
      }),
    );
    await waitFor(() => expect(mockToast.success).toHaveBeenCalled());
  });

  it("omits scope from the body when the scope input is empty", async () => {
    renderWithI18n(<SliceActionsSection issueId="issue-1" />);
    // The section is collapsed by default (rail simplicity) — expand it first.
    fireEvent.click(screen.getByRole("button", { name: /AI actions/i }));

    fireEvent.click(screen.getByRole("button", { name: "Draft code" }));

    await waitFor(() =>
      expect(mockSliceAction).toHaveBeenCalledWith("issue-1", { kind: "draft_code" }),
    );
  });

  it("toasts an error when the action fails", async () => {
    mockSliceAction.mockRejectedValueOnce(new Error("boom"));
    renderWithI18n(<SliceActionsSection issueId="issue-1" />);
    // The section is collapsed by default (rail simplicity) — expand it first.
    fireEvent.click(screen.getByRole("button", { name: /AI actions/i }));

    fireEvent.click(screen.getByRole("button", { name: "Write docs" }));

    await waitFor(() => expect(mockToast.error).toHaveBeenCalledWith("boom"));
  });
});
