import React from "react";
import { describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithI18n } from "../test/i18n";

// Stable mutation spy, hoisted so the vi.mock factory can close over it.
const { mutateAsync } = vi.hoisted(() => ({ mutateAsync: vi.fn().mockResolvedValue({}) }));

const sprint = {
  id: "s1",
  workspace_id: "ws-1",
  project_id: "p1",
  name: "Sprint 9",
  goal: "Ship it",
  status: "active",
  // full RFC3339 (SQL-created) — the modal must reduce to date-only for the API.
  start_date: "2026-06-30T00:00:00Z",
  end_date: "2026-07-14T00:00:00Z",
  branch: "billing",
  created_at: "",
  updated_at: "",
};

vi.mock("@tanstack/react-query", () => ({
  useQuery: () => ({ data: sprint }),
}));

vi.mock("@agora/core", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("@agora/core/sprints", () => ({
  sprintDetailOptions: () => ({ queryKey: ["sprint", "s1"], queryFn: vi.fn() }),
}));
vi.mock("@agora/core/sprints/mutations", () => ({ useUpdateSprint: () => ({ mutateAsync }) }));
vi.mock("@agora/core/sprints/config", () => ({
  SPRINT_STATUS_CONFIG: {
    planned: { dotColor: "" },
    active: { dotColor: "" },
    completed: { dotColor: "" },
  },
  SPRINT_STATUS_ORDER: ["planned", "active", "completed"],
}));
vi.mock("@agora/core/paths", () => ({
  useCurrentWorkspace: () => ({ id: "ws-1", name: "Test WS", slug: "test-ws" }),
}));
vi.mock("../projects/components/sprint-labels", () => ({
  useSprintStatusLabels: () => ({ planned: "Planned", active: "Active", completed: "Completed" }),
}));

vi.mock("@agora/ui/components/ui/dialog", () => ({
  Dialog: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DialogContent: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DialogTitle: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
}));
vi.mock("@agora/ui/components/ui/dropdown-menu", () => ({
  DropdownMenu: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  DropdownMenuTrigger: ({ render }: { render: React.ReactNode }) => <>{render}</>,
  DropdownMenuContent: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  DropdownMenuItem: ({ children }: { children: React.ReactNode }) => <button type="button">{children}</button>,
}));
vi.mock("@agora/ui/components/ui/popover", () => ({
  Popover: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  PopoverTrigger: ({ render }: { render: React.ReactNode }) => <>{render}</>,
  PopoverContent: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
}));
vi.mock("@agora/ui/components/ui/tooltip", () => ({
  Tooltip: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  TooltipTrigger: ({ render }: { render: React.ReactNode }) => <>{render}</>,
  TooltipContent: ({ children }: { children: React.ReactNode }) => <div role="tooltip">{children}</div>,
}));
vi.mock("@agora/ui/components/ui/calendar", () => ({ Calendar: () => <div data-testid="calendar" /> }));
vi.mock("@agora/ui/components/ui/button", () => ({
  Button: ({
    children,
    disabled,
    onClick,
  }: {
    children: React.ReactNode;
    disabled?: boolean;
    onClick?: () => void;
  }) => (
    <button type="button" disabled={disabled} onClick={onClick}>
      {children}
    </button>
  ),
}));
vi.mock("@agora/ui/lib/utils", () => ({
  cn: (...v: Array<string | false | null | undefined>) => v.filter(Boolean).join(" "),
}));
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

import { EditSprintModal } from "./edit-sprint";

describe("EditSprintModal", () => {
  it("loads the sprint and saves edited fields as date-only", async () => {
    const user = userEvent.setup();
    renderWithI18n(<EditSprintModal onClose={vi.fn()} data={{ sprint_id: "s1" }} />);

    // Pre-filled from the loaded sprint.
    const nameInput = screen.getByDisplayValue("Sprint 9");
    expect(nameInput).toBeInTheDocument();
    expect(screen.getByDisplayValue("Ship it")).toBeInTheDocument();

    // Edit the name, then save.
    await user.clear(nameInput);
    await user.type(nameInput, "Sprint 9 — renamed");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(mutateAsync).toHaveBeenCalledWith({
      id: "s1",
      name: "Sprint 9 — renamed",
      goal: "Ship it",
      status: "active",
      // full RFC3339 reduced to the date part (matches the create-sprint contract)
      start_date: "2026-06-30",
      end_date: "2026-07-14",
      branch: "billing",
    });
  });
});
